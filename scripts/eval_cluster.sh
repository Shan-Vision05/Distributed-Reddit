#!/usr/bin/env bash

set -euo pipefail
set +m

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="${REPO_ROOT}/dreddit"
TEST_DIR="/tmp/dreddit_eval_$$"
COMMUNITY_ID="eval-bench"
NODE_COUNT="${EVAL_NODE_COUNT:-15}"
BASE_HTTP="${EVAL_HTTP_BASE:-8201}"
BASE_GOSSIP="${EVAL_GOSSIP_BASE:-12001}"
READ_SAMPLES="${EVAL_READ_SAMPLES:-60}"
WRITE_SAMPLES="${EVAL_WRITE_SAMPLES:-60}"
FAILOVER_SAMPLES="${EVAL_FAILOVER_SAMPLES:-50}"
THROUGHPUT_DURATION="${EVAL_THROUGHPUT_DURATION:-8}"
THROUGHPUT_CONCURRENCY="${EVAL_THROUGHPUT_CONCURRENCY:-12}"
FAILOVER_CONNECT_TIMEOUT="${EVAL_FAILOVER_CONNECT_TIMEOUT:-1}"
FAILOVER_MAX_TIME="${EVAL_FAILOVER_MAX_TIME:-20}"

PASS=0
FAIL=0

declare -a PIDS=()
declare -a HTTP_PORTS=()
declare -a GOSSIP_PORTS=()
declare -a NODE_DIRS=()
declare -a TOKENS=()
declare -a USERNAMES=()
declare -a ADMIN_TOKENS=()

HTTP_BODY=""
HTTP_CODE=""
HTTP_TIME_MS=""
SEED_POST_HASH=""
ADMIN_TOKEN=""
MALLORY_POST_AFTER_BAN_HTTP=""
MALLORY_POSTS_CREATED=0

pass() { echo -e "  \033[32mPASS\033[0m  $1"; PASS=$((PASS + 1)); }
fail() { echo -e "  \033[31mFAIL\033[0m  $1"; FAIL=$((FAIL + 1)); }
note() { echo "  INFO  $1"; }

cleanup_stale_eval_state() {
    pkill -f 'dreddit -id eval-node' 2>/dev/null || true
    find /tmp -maxdepth 1 -type d -name 'dreddit_eval_*' -prune -exec rm -rf {} + 2>/dev/null || true
}

cleanup() {
    for pid in "${PIDS[@]:-}"; do
        if [[ -n "$pid" ]]; then
            kill "$pid" 2>/dev/null || true
        fi
    done
    wait 2>/dev/null || true
    rm -rf "$TEST_DIR"
}
trap cleanup EXIT INT TERM HUP QUIT

require() {
    command -v "$1" >/dev/null 2>&1 || {
        echo "Missing required command: $1" >&2
        exit 1
    }
}

wait_for() {
    local url=$1
    local timeout_seconds=${2:-40}
    local attempts=0
    until curl --connect-timeout 1 --max-time 2 -sf "$url" >/dev/null 2>&1; do
        sleep 0.5
        attempts=$((attempts + 1))
        if (( attempts > timeout_seconds * 2 )); then
            echo "Timed out waiting for $url" >&2
            return 1
        fi
    done
}

request_json() {
    local method=$1
    local url=$2
    local body=${3:-}
    local token=${4:-}
    local output_file
    local curl_args=(-sS -o)
    output_file="$(mktemp)"

    if [[ -n "$body" ]]; then
        curl_args+=("$output_file" -w "%{http_code} %{time_total}" -X "$method" -H "Content-Type: application/json")
        if [[ -n "$token" ]]; then
            curl_args+=(-H "Authorization: Bearer ${token}")
        fi
        curl_args+=(-d "$body" "$url")
    else
        curl_args+=("$output_file" -w "%{http_code} %{time_total}" -X "$method" -H "Content-Type: application/json")
        if [[ -n "$token" ]]; then
            curl_args+=(-H "Authorization: Bearer ${token}")
        fi
        curl_args+=("$url")
    fi

    local meta
    meta="$(curl "${curl_args[@]}")"
    HTTP_BODY="$(cat "$output_file")"
    rm -f "$output_file"

    HTTP_CODE="${meta%% *}"
    HTTP_TIME_MS="$(python3 -c 'import sys; print(int(round(float(sys.argv[1]) * 1000)))' "${meta##* }")"
}

json_get() {
    python3 -c 'import json, sys; data = json.load(sys.stdin); value = data.get(sys.argv[1], ""); print(value if not isinstance(value, (dict, list)) else json.dumps(value))' "$1"
}

json_len() {
    python3 -c 'import json, sys; data = json.load(sys.stdin); print(len(data) if isinstance(data, list) else 0)'
}

json_count_author() {
    python3 -c 'import json, sys; author = sys.argv[1]; data = json.load(sys.stdin); data = data if isinstance(data, list) else []; print(sum(1 for item in data if item.get("author_id") == author))' "$1"
}

summarize_samples() {
    local label=$1
    local file=$2
    python3 - "$label" "$file" <<'PY'
import math
import pathlib
import statistics
import sys

label = sys.argv[1]
path = pathlib.Path(sys.argv[2])
values = [int(line.strip()) for line in path.read_text().splitlines() if line.strip()]
if not values:
    print(f"  {label}: no samples")
    raise SystemExit(0)
values.sort()

def percentile(p):
    if len(values) == 1:
        return values[0]
    rank = math.ceil((p / 100.0) * len(values)) - 1
    rank = min(max(rank, 0), len(values) - 1)
    return values[rank]

avg = round(statistics.mean(values), 1)
print(f"  {label}: n={len(values)} avg_ms={avg} p95_ms={percentile(95)} p99_ms={percentile(99)} max_ms={values[-1]}")
PY
}

sample_stats_json() {
    local file=$1
    python3 - "$file" <<'PY'
import json
import math
import pathlib
import statistics
import sys

path = pathlib.Path(sys.argv[1])
if not path.exists():
    print(json.dumps({}))
    raise SystemExit(0)

values = [int(line.strip()) for line in path.read_text().splitlines() if line.strip()]
if not values:
    print(json.dumps({}))
    raise SystemExit(0)

values.sort()

def percentile(p):
    if len(values) == 1:
        return values[0]
    rank = math.ceil((p / 100.0) * len(values)) - 1
    rank = min(max(rank, 0), len(values) - 1)
    return values[rank]

print(json.dumps({
    "count": len(values),
    "min_ms": values[0],
    "avg_ms": round(statistics.mean(values), 1),
    "p50_ms": percentile(50),
    "p95_ms": percentile(95),
    "p99_ms": percentile(99),
    "max_ms": values[-1],
}))
PY
}

format_ratio_pct() {
    local numerator=$1
    local denominator=$2
    python3 - "$numerator" "$denominator" <<'PY'
import sys
num = int(sys.argv[1])
den = int(sys.argv[2])
if den == 0:
    print("0.0")
else:
    print(f"{(num / den) * 100:.1f}")
PY
}

print_sample_report() {
    local title=$1
    local file=$2
    local stats_json
    stats_json="$(sample_stats_json "$file")"
    if [[ -z "$stats_json" || "$stats_json" == "{}" ]]; then
        printf "  %-28s %s\n" "$title" "no samples"
        return
    fi
    python3 - "$title" "$stats_json" <<'PY'
import json
import sys

title = sys.argv[1]
stats = json.loads(sys.argv[2])
print(
    f"  {title:<28} "
    f"count={stats['count']} "
    f"min_ms={stats['min_ms']} "
    f"avg_ms={stats['avg_ms']} "
    f"p50_ms={stats['p50_ms']} "
    f"p95_ms={stats['p95_ms']} "
    f"p99_ms={stats['p99_ms']} "
    f"max_ms={stats['max_ms']}"
)
PY
}

print_throughput_report() {
    local title=$1
    local file=$2
    python3 - "$title" "$file" <<'PY'
import json
import sys

title = sys.argv[1]
with open(sys.argv[2]) as handle:
    data = json.load(handle)

success = data.get("success", 0)
failures = data.get("failures", 0)
ops = data.get("ops", 0)
elapsed = data.get("elapsed_seconds", 0)
ops_per_second = data.get("ops_per_second", 0)
success_rate = (success / ops * 100.0) if ops else 0.0

print(
    f"  {title:<28} "
    f"ops={ops} "
    f"success={success} "
    f"failures={failures} "
    f"success_rate_pct={success_rate:.1f} "
    f"elapsed_s={elapsed} "
    f"ops_per_second={ops_per_second}"
)
PY
}

print_final_report() {
    local failover_read_pct failover_write_pct churn_read_pct churn_write_pct
    failover_read_pct="$(format_ratio_pct "$failover_read_success" "$FAILOVER_SAMPLES")"
    failover_write_pct="$(format_ratio_pct "$failover_write_success" "$FAILOVER_SAMPLES")"
    churn_read_pct="$(format_ratio_pct "$churn_read_success" 45)"
    churn_write_pct="$(format_ratio_pct "$churn_write_success" 45)"

    echo
    echo "-- [8] Final Metrics Report --"
    echo "======================================================================"
    printf "  %-28s %s\n" "node_count" "$NODE_COUNT"
    printf "  %-28s %s\n" "community_id" "$COMMUNITY_ID"
    printf "  %-28s %s\n" "seed_post_hash" "$SEED_POST_HASH"
    printf "  %-28s %s\n" "read_samples" "$READ_SAMPLES"
    printf "  %-28s %s\n" "write_samples" "$WRITE_SAMPLES"
    printf "  %-28s %s\n" "failover_samples" "$FAILOVER_SAMPLES"
    printf "  %-28s %s\n" "throughput_duration_s" "$THROUGHPUT_DURATION"
    printf "  %-28s %s\n" "throughput_concurrency" "$THROUGHPUT_CONCURRENCY"
    echo
    echo "  Latency Metrics"
    print_sample_report "baseline_read_latency" "$BASE_READ_FILE"
    print_sample_report "baseline_write_latency" "$BASE_WRITE_FILE"
    print_sample_report "failover_read_latency" "$FAILOVER_READ_FILE"
    print_sample_report "failover_write_latency" "$FAILOVER_WRITE_FILE"
    print_sample_report "churn_read_latency" "$CHURN_READ_FILE"
    print_sample_report "churn_write_latency" "$CHURN_WRITE_FILE"
    echo
    echo "  Throughput Metrics"
    print_throughput_report "baseline_mixed_throughput" "$BASE_TP_FILE"
    echo
    echo "  Availability Metrics"
    printf "  %-28s reads=%s/%s reads_pct=%s writes=%s/%s writes_pct=%s\n" \
        "failover_availability" \
        "$failover_read_success" "$FAILOVER_SAMPLES" "$failover_read_pct" \
        "$failover_write_success" "$FAILOVER_SAMPLES" "$failover_write_pct"
    printf "  %-28s reads=%s/45 reads_pct=%s writes=%s/45 writes_pct=%s\n" \
        "churn_availability" \
        "$churn_read_success" "$churn_read_pct" \
        "$churn_write_success" "$churn_write_pct"
    echo
    echo "  Security / Moderation Metrics"
    printf "  %-28s %s\n" "malicious_posts_created" "$MALLORY_POSTS_CREATED"
    printf "  %-28s %s\n" "mallory_visible_after_prune" "$mallory_visible"
    printf "  %-28s %s\n" "mallory_post_after_ban_http" "$MALLORY_POST_AFTER_BAN_HTTP"
    echo
    echo "  Overall Result"
    printf "  %-28s %s\n" "checks_passed" "$PASS"
    printf "  %-28s %s\n" "checks_failed" "$FAIL"
    printf "  %-28s %s\n" "artifacts_dir" "$TEST_DIR"
    echo "======================================================================"
}

moderate_on_cluster() {
    local body=$1
    local i
    local last_code=""

    for i in "${!HTTP_PORTS[@]}"; do
        if [[ -z "${PIDS[$i]:-}" ]]; then
            continue
        fi
        if [[ -z "${ADMIN_TOKENS[$i]:-}" ]]; then
            continue
        fi
        request_json POST "http://127.0.0.1:${HTTP_PORTS[$i]}/api/moderate" "$body" "${ADMIN_TOKENS[$i]}"
        last_code="$HTTP_CODE"
        if [[ "$HTTP_CODE" == "200" ]]; then
            return 0
        fi
    done

    HTTP_CODE="$last_code"
    return 1
}

run_throughput_probe() {
    local output_file=$1
    local mode=$2
    local duration=$3
    local concurrency=$4
    local endpoints_json tokens_json
    endpoints_json="$(printf '%s\n' "${HTTP_PORTS[@]}" | python3 -c 'import json, sys; print(json.dumps([f"http://127.0.0.1:{line.strip()}" for line in sys.stdin if line.strip()]))')"
    tokens_json="$(printf '%s\n' "${TOKENS[@]}" | python3 -c 'import json, sys; print(json.dumps([line.strip() for line in sys.stdin if line.strip()]))')"

    ENDPOINTS_JSON="$endpoints_json" TOKENS_JSON="$tokens_json" COMMUNITY_ID="$COMMUNITY_ID" SEED_POST_HASH="$SEED_POST_HASH" MODE="$mode" DURATION_SECONDS="$duration" CONCURRENCY="$concurrency" python3 > "$output_file" <<'PY'
import json
import os
import random
import threading
import time
import urllib.error
import urllib.request

endpoints = json.loads(os.environ["ENDPOINTS_JSON"])
tokens = json.loads(os.environ["TOKENS_JSON"])
community_id = os.environ["COMMUNITY_ID"]
post_hash = os.environ["SEED_POST_HASH"]
mode = os.environ["MODE"]
duration = float(os.environ["DURATION_SECONDS"])
concurrency = int(os.environ["CONCURRENCY"])
deadline = time.time() + duration

lock = threading.Lock()
counts = {"ops": 0, "success": 0, "failures": 0}

def do_request(index, iteration):
    base = endpoints[index]
    token = tokens[index]
    if mode == "read":
        req = urllib.request.Request(
            f"{base}/api/posts?community_id={community_id}",
            headers={"Authorization": f"Bearer {token}"},
            method="GET",
        )
    elif mode == "write":
        value = 1 if iteration % 2 == 0 else -1
        body = json.dumps(
            {
                "community_id": community_id,
                "vote": {"target_hash": post_hash, "value": value},
            }
        ).encode()
        req = urllib.request.Request(
            f"{base}/api/vote",
            data=body,
            headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
            method="POST",
        )
    else:
        if iteration % 2 == 0:
            req = urllib.request.Request(
                f"{base}/api/posts?community_id={community_id}",
                headers={"Authorization": f"Bearer {token}"},
                method="GET",
            )
        else:
            value = 1 if iteration % 4 == 1 else -1
            body = json.dumps(
                {
                    "community_id": community_id,
                    "vote": {"target_hash": post_hash, "value": value},
                }
            ).encode()
            req = urllib.request.Request(
                f"{base}/api/vote",
                data=body,
                headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
                method="POST",
            )
    with urllib.request.urlopen(req, timeout=2.0) as response:
        return response.status

def worker(worker_id):
    iteration = 0
    rnd = random.Random(worker_id + 17)
    while time.time() < deadline:
        idx = rnd.randrange(len(endpoints))
        try:
            status = do_request(idx, iteration)
            ok = 200 <= status < 300
        except (urllib.error.URLError, TimeoutError, OSError):
            ok = False
        with lock:
            counts["ops"] += 1
            if ok:
                counts["success"] += 1
            else:
                counts["failures"] += 1
        iteration += 1

threads = [threading.Thread(target=worker, args=(i,)) for i in range(concurrency)]
start = time.time()
for thread in threads:
    thread.start()
for thread in threads:
    thread.join()
elapsed = time.time() - start
counts["elapsed_seconds"] = round(elapsed, 3)
counts["ops_per_second"] = round(counts["success"] / elapsed, 2) if elapsed else 0.0
print(json.dumps(counts))
PY
}

wait_for_membership() {
    local expected=$1
    local timeout_seconds=${2:-70}
    local attempts=0
    while (( attempts <= timeout_seconds * 2 )); do
        local ready=1
        local i
        for i in "${!HTTP_PORTS[@]}"; do
            if [[ -z "${PIDS[$i]:-}" ]]; then
                continue
            fi
            if ! curl --connect-timeout 1 --max-time 2 -sf "http://127.0.0.1:${HTTP_PORTS[$i]}/api/status" >/dev/null 2>&1; then
                ready=0
                break
            fi
            local count
            count="$(curl --connect-timeout 1 --max-time 2 -sf "http://127.0.0.1:${HTTP_PORTS[$i]}/api/status" | json_get member_count)"
            if [[ ! "$count" =~ ^[0-9]+$ ]] || (( count < expected )); then
                ready=0
                break
            fi
        done
        if (( ready == 1 )); then
            return 0
        fi
        sleep 0.5
        attempts=$((attempts + 1))
    done
    return 1
}

wait_for_post_visibility() {
    local expected_nodes=$1
    local timeout_seconds=${2:-60}
    local attempts=0
    while (( attempts <= timeout_seconds * 2 )); do
        local visible=0
        local i
        for i in "${!HTTP_PORTS[@]}"; do
            if [[ -z "${PIDS[$i]:-}" ]]; then
                continue
            fi
            request_json GET "http://127.0.0.1:${HTTP_PORTS[$i]}/api/posts?community_id=${COMMUNITY_ID}" "" "${TOKENS[$i]}"
            if [[ "$HTTP_CODE" == "200" ]] && echo "$HTTP_BODY" | grep -q "$SEED_POST_HASH"; then
                visible=$((visible + 1))
            fi
        done
        if (( visible >= expected_nodes )); then
            return 0
        fi
        sleep 0.5
        attempts=$((attempts + 1))
    done
    return 1
}

record_read_sample() {
    local file=$1
    local index=$((RANDOM % NODE_COUNT))
    request_json GET "http://127.0.0.1:${HTTP_PORTS[$index]}/api/posts?community_id=${COMMUNITY_ID}" "" "${TOKENS[$index]}"
    if [[ "$HTTP_CODE" == "200" ]]; then
        echo "$HTTP_TIME_MS" >> "$file"
        return 0
    fi
    return 1
}

record_write_sample() {
    local file=$1
    local ordinal=$2
    local index=$((RANDOM % NODE_COUNT))
    local value=1
    if (( ordinal % 2 == 0 )); then
        value=-1
    fi
    request_json POST "http://127.0.0.1:${HTTP_PORTS[$index]}/api/vote" "{\"community_id\":\"${COMMUNITY_ID}\",\"vote\":{\"target_hash\":\"${SEED_POST_HASH}\",\"value\":${value}}}" "${TOKENS[$index]}"
    if [[ "$HTTP_CODE" == "200" ]]; then
        echo "$HTTP_TIME_MS" >> "$file"
        return 0
    fi
    return 1
}

fallback_request_sample() {
    local method=$1
    local file=$2
    local ordinal=$3
    local success=1
    local total_ms=0
    local i
    for i in "${!HTTP_PORTS[@]}"; do
        if [[ -z "${PIDS[$i]:-}" ]]; then
            continue
        fi
        local output_file meta code time_ms url body token
        output_file="$(mktemp)"
        url="http://127.0.0.1:${HTTP_PORTS[$i]}/api/posts?community_id=${COMMUNITY_ID}"
        body=""
        token="${TOKENS[$i]}"
        if [[ "$method" == "POST" ]]; then
            local value=1
            if (( ordinal % 2 == 0 )); then
                value=-1
            fi
            url="http://127.0.0.1:${HTTP_PORTS[$i]}/api/vote"
            body="{\"community_id\":\"${COMMUNITY_ID}\",\"vote\":{\"target_hash\":\"${SEED_POST_HASH}\",\"value\":${value}}}"
            meta="$(curl --connect-timeout "$FAILOVER_CONNECT_TIMEOUT" --max-time "$FAILOVER_MAX_TIME" -sS -o "$output_file" -w "%{http_code} %{time_total}" -X POST -H "Content-Type: application/json" -H "Authorization: Bearer ${token}" -d "$body" "$url" 2>/dev/null || echo "000 ${FAILOVER_MAX_TIME}")"
        else
            meta="$(curl --connect-timeout "$FAILOVER_CONNECT_TIMEOUT" --max-time "$FAILOVER_MAX_TIME" -sS -o "$output_file" -w "%{http_code} %{time_total}" -X GET -H "Content-Type: application/json" -H "Authorization: Bearer ${token}" "$url" 2>/dev/null || echo "000 ${FAILOVER_MAX_TIME}")"
        fi
        rm -f "$output_file"
        code="${meta%% *}"
        time_ms="$(python3 -c 'import sys; print(int(round(float(sys.argv[1]) * 1000)))' "${meta##* }")"
        total_ms=$((total_ms + time_ms))
        if [[ "$code" =~ ^2 ]]; then
            success=0
            break
        fi
    done
    if (( success == 0 )); then
        echo "$total_ms" >> "$file"
        return 0
    fi
    return 1
}

start_node() {
    local index=$1
    local node_id="eval-node$((index + 1))"
    local http_port=$((BASE_HTTP + index))
    local gossip_port=$((BASE_GOSSIP + index))
    local data_dir="${TEST_DIR}/node$((index + 1))"
    local log_file="${TEST_DIR}/node$((index + 1)).log"
    local peers=""

    mkdir -p "$data_dir"

    if (( index > 0 )); then
        peers="127.0.0.1:${GOSSIP_PORTS[0]}"
    fi

    if [[ -n "$peers" ]]; then
        "$BINARY" -id "$node_id" -addr ":${http_port}" -gossip-port "$gossip_port" -peers "$peers" -data-dir "$data_dir" > "$log_file" 2>&1 &
    else
        "$BINARY" -id "$node_id" -addr ":${http_port}" -gossip-port "$gossip_port" -data-dir "$data_dir" > "$log_file" 2>&1 &
    fi

    PIDS[$index]=$!
    HTTP_PORTS[$index]=$http_port
    GOSSIP_PORTS[$index]=$gossip_port
    NODE_DIRS[$index]=$data_dir

    wait_for "http://127.0.0.1:${http_port}/api/status" 50
}

restart_node() {
    local index=$1
    local node_id="eval-node$((index + 1))"
    local http_port="${HTTP_PORTS[$index]}"
    local gossip_port="${GOSSIP_PORTS[$index]}"
    local data_dir="${NODE_DIRS[$index]}"
    local log_file="${TEST_DIR}/node$((index + 1))-restart.log"
    local peers=""
    local peer_index

    for peer_index in "${!GOSSIP_PORTS[@]}"; do
        if (( peer_index == index )); then
            continue
        fi
        if [[ -z "${PIDS[$peer_index]:-}" ]]; then
            continue
        fi
        peers="127.0.0.1:${GOSSIP_PORTS[$peer_index]}"
        break
    done

    if [[ -z "$peers" ]]; then
        echo "No live peer available to restart node ${node_id}" >&2
        return 1
    fi

    "$BINARY" -id "$node_id" -addr ":${http_port}" -gossip-port "$gossip_port" -peers "$peers" -data-dir "$data_dir" > "$log_file" 2>&1 &
    PIDS[$index]=$!
    wait_for "http://127.0.0.1:${http_port}/api/status" 50
}

require curl
require python3
require go

cleanup_stale_eval_state

if (( NODE_COUNT < 15 )); then
    echo "EVAL_NODE_COUNT must be at least 15 to match the evaluation protocol." >&2
    exit 1
fi

note "Building dreddit binary"
(cd "$REPO_ROOT" && go build -o dreddit ./cmd/dreddit/)

mkdir -p "$TEST_DIR"

echo "======================================================================"
echo " DReddit Evaluation Harness"
echo " Nodes=${NODE_COUNT} Community=${COMMUNITY_ID}"
echo "======================================================================"

echo
echo "-- [1] Starting ${NODE_COUNT}-node cluster --"
for i in $(seq 0 $((NODE_COUNT - 1))); do
    start_node "$i"
    pass "node $((i + 1)) listening on :${HTTP_PORTS[$i]}"
done

if wait_for_membership "$NODE_COUNT" 80; then
    pass "all nodes converged to ${NODE_COUNT}-member gossip view"
else
    fail "cluster did not fully converge to ${NODE_COUNT} members"
fi

echo
echo "-- [2] Preparing users and community membership --"
for i in $(seq 0 $((NODE_COUNT - 1))); do
    request_json POST "http://127.0.0.1:${HTTP_PORTS[$i]}/api/signup" '{"username":"admin","password":"adminpw"}' ""
    if [[ "$HTTP_CODE" == "201" ]]; then
        ADMIN_TOKENS[$i]="$(echo "$HTTP_BODY" | json_get token)"
    else
        fail "admin signup failed on node $((i + 1)) with HTTP ${HTTP_CODE}"
    fi
done
ADMIN_TOKEN="${ADMIN_TOKENS[0]:-}"
if [[ -n "$ADMIN_TOKEN" ]]; then
    pass "admin user created on all nodes"
fi

for i in $(seq 0 $((NODE_COUNT - 1))); do
    USERNAMES[$i]="client$(printf '%02d' $((i + 1)))"
    request_json POST "http://127.0.0.1:${HTTP_PORTS[$i]}/api/signup" "{\"username\":\"${USERNAMES[$i]}\",\"password\":\"pw$((i + 1))\"}" ""
    if [[ "$HTTP_CODE" != "201" ]]; then
        fail "signup failed on node $((i + 1)) with HTTP ${HTTP_CODE}"
        continue
    fi
    TOKENS[$i]="$(echo "$HTTP_BODY" | json_get token)"

    request_json POST "http://127.0.0.1:${HTTP_PORTS[$i]}/api/join" "{\"community_id\":\"${COMMUNITY_ID}\"}" "${TOKENS[$i]}"
    if [[ "$HTTP_CODE" == "200" ]]; then
        pass "node $((i + 1)) joined ${COMMUNITY_ID}"
    else
        fail "join failed on node $((i + 1)) with HTTP ${HTTP_CODE}"
    fi
done

sleep 4

request_json POST "http://127.0.0.1:${HTTP_PORTS[0]}/api/post" "{\"community_id\":\"${COMMUNITY_ID}\",\"title\":\"seed-post\",\"body\":\"cluster benchmark anchor\"}" "${TOKENS[0]}"
if [[ "$HTTP_CODE" == "201" ]]; then
    SEED_POST_HASH="$(echo "$HTTP_BODY" | json_get hash)"
    pass "seed post created (${SEED_POST_HASH:0:12})"
else
    fail "seed post creation returned HTTP ${HTTP_CODE}"
fi

if wait_for_post_visibility "$NODE_COUNT" 70; then
    pass "seed post replicated to all live nodes"
else
    fail "seed post did not replicate to every node"
fi

echo
echo "-- [3] Baseline latency and throughput --"
BASE_READ_FILE="${TEST_DIR}/baseline_reads.txt"
BASE_WRITE_FILE="${TEST_DIR}/baseline_writes.txt"
for sample in $(seq 1 "$READ_SAMPLES"); do
    record_read_sample "$BASE_READ_FILE" || fail "baseline read sample ${sample} failed"
done
for sample in $(seq 1 "$WRITE_SAMPLES"); do
    record_write_sample "$BASE_WRITE_FILE" "$sample" || fail "baseline write sample ${sample} failed"
done
summarize_samples "baseline read latency" "$BASE_READ_FILE"
summarize_samples "baseline write latency" "$BASE_WRITE_FILE"
pass "captured baseline latency samples"

BASE_TP_FILE="${TEST_DIR}/baseline_throughput.json"
run_throughput_probe "$BASE_TP_FILE" mixed "$THROUGHPUT_DURATION" "$THROUGHPUT_CONCURRENCY"
python3 - "$BASE_TP_FILE" <<'PY'
import json
import sys
data = json.load(open(sys.argv[1]))
print(f"  baseline throughput: success={data['success']} failures={data['failures']} ops_per_second={data['ops_per_second']}")
PY
pass "captured mixed-workload throughput sample"

echo
echo "-- [4] Failover latency and availability --"
kill "${PIDS[0]}" 2>/dev/null || true
wait "${PIDS[0]}" 2>/dev/null || true
PIDS[0]=""
pass "leader node stopped for failover experiment"

FAILOVER_READ_FILE="${TEST_DIR}/failover_reads.txt"
FAILOVER_WRITE_FILE="${TEST_DIR}/failover_writes.txt"
failover_read_success=0
failover_write_success=0
for sample in $(seq 1 "$FAILOVER_SAMPLES"); do
    if fallback_request_sample GET "$FAILOVER_READ_FILE" "$sample"; then
        failover_read_success=$((failover_read_success + 1))
    fi
done
for sample in $(seq 1 "$FAILOVER_SAMPLES"); do
    if fallback_request_sample POST "$FAILOVER_WRITE_FILE" "$sample"; then
        failover_write_success=$((failover_write_success + 1))
    fi
done
summarize_samples "failover read latency" "$FAILOVER_READ_FILE"
summarize_samples "failover write latency" "$FAILOVER_WRITE_FILE"
echo "  failover availability: reads=${failover_read_success}/${FAILOVER_SAMPLES} writes=${failover_write_success}/${FAILOVER_SAMPLES}"
if (( failover_read_success == FAILOVER_SAMPLES && failover_write_success == FAILOVER_SAMPLES )); then
    pass "all failover operations completed via reroute"
else
    fail "some failover operations did not succeed"
fi

if restart_node 0; then
    if wait_for_membership "$NODE_COUNT" 80; then
        pass "cluster recovered to full membership after leader restart"
    else
        fail "cluster did not recover to full membership after leader restart"
    fi
else
    fail "leader restart failed after failover experiment"
fi

echo
echo "-- [5] Churn resilience --"
CHURN_READ_FILE="${TEST_DIR}/churn_reads.txt"
CHURN_WRITE_FILE="${TEST_DIR}/churn_writes.txt"
churn_read_success=0
churn_write_success=0
for index in 1 2 3; do
    kill "${PIDS[$index]}" 2>/dev/null || true
    wait "${PIDS[$index]}" 2>/dev/null || true
    PIDS[$index]=""
    note "node $((index + 1)) stopped during churn window"
    for sample in $(seq 1 15); do
        if fallback_request_sample GET "$CHURN_READ_FILE" "$sample"; then
            churn_read_success=$((churn_read_success + 1))
        fi
        if fallback_request_sample POST "$CHURN_WRITE_FILE" "$sample"; then
            churn_write_success=$((churn_write_success + 1))
        fi
    done
    if ! restart_node "$index"; then
        fail "node $((index + 1)) failed to restart during churn"
    fi
done
summarize_samples "churn read latency" "$CHURN_READ_FILE"
summarize_samples "churn write latency" "$CHURN_WRITE_FILE"
echo "  churn availability: reads=${churn_read_success}/45 writes=${churn_write_success}/45"
if wait_for_membership "$NODE_COUNT" 100; then
    pass "cluster re-converged after repeated restart churn"
else
    fail "cluster did not re-converge after restart churn"
fi

echo
echo "-- [6] Reactive pruning / Sybil-resistance note --"
request_json POST "http://127.0.0.1:${HTTP_PORTS[1]}/api/signup" '{"username":"mallory","password":"pw-mal"}' ""
MALLORY_TOKEN="$(echo "$HTTP_BODY" | json_get token)"
request_json POST "http://127.0.0.1:${HTTP_PORTS[1]}/api/join" "{\"community_id\":\"${COMMUNITY_ID}\"}" "$MALLORY_TOKEN"
malicious_hashes=()
for sample in 1 2 3; do
    request_json POST "http://127.0.0.1:${HTTP_PORTS[1]}/api/post" "{\"community_id\":\"${COMMUNITY_ID}\",\"title\":\"malicious-${sample}\",\"body\":\"sybil-spam-${sample}\"}" "$MALLORY_TOKEN"
    if [[ "$HTTP_CODE" == "201" ]]; then
        malicious_hashes+=("$(echo "$HTTP_BODY" | json_get hash)")
    fi
done
MALLORY_POSTS_CREATED="${#malicious_hashes[@]}"
sleep 2

for hash in "${malicious_hashes[@]}"; do
    if ! moderate_on_cluster "{\"community_id\":\"${COMMUNITY_ID}\",\"action_type\":\"DELETE_POST\",\"target\":\"${hash}\"}"; then
        fail "delete post moderation failed with HTTP ${HTTP_CODE}"
    fi
done
if ! moderate_on_cluster "{\"community_id\":\"${COMMUNITY_ID}\",\"action_type\":\"BAN_USER\",\"target\":\"mallory\"}"; then
    fail "ban user moderation failed with HTTP ${HTTP_CODE}"
fi
sleep 2

request_json POST "http://127.0.0.1:${HTTP_PORTS[1]}/api/post" "{\"community_id\":\"${COMMUNITY_ID}\",\"title\":\"blocked-post\",\"body\":\"should fail\"}" "$MALLORY_TOKEN"
MALLORY_POST_AFTER_BAN_HTTP="$HTTP_CODE"
if [[ "$HTTP_CODE" == "403" ]]; then
    pass "banned user cannot continue posting"
else
    fail "banned user post returned HTTP ${HTTP_CODE}"
fi

request_json GET "http://127.0.0.1:${HTTP_PORTS[2]}/api/posts?community_id=${COMMUNITY_ID}" "" "${TOKENS[2]}"
mallory_visible="$(echo "$HTTP_BODY" | json_count_author mallory)"
if [[ "$mallory_visible" == "0" ]]; then
    pass "reactive pruning hides Mallory content on followers"
else
    fail "expected Mallory posts to be hidden after pruning, found ${mallory_visible}"
fi

echo
echo "-- [7] Summary --"
echo "======================================================================"
printf "  pass=%d fail=%d\n" "$PASS" "$FAIL"
echo "  artifacts=${TEST_DIR}"
echo "======================================================================"

print_final_report

if (( FAIL > 0 )); then
    exit 1
fi