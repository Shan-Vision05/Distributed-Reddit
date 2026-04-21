#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT_FILE="$(mktemp)"

cleanup() {
    rm -f "$OUT_FILE"
}
trap cleanup EXIT

echo "======================================================================"
echo " DReddit WAN / Partition Evaluation"
echo "======================================================================"

echo
echo "-- [1] Running deterministic cross-region and partition-heal tests --"
(
    cd "$REPO_ROOT"
    go test -v ./tests -run 'TestEvaluation(WANConvergenceAcrossRegions|PartitionHealConvergence20Replicas)$'
) | tee "$OUT_FILE"

echo
echo "-- [2] Final Metrics Report --"
python3 - "$OUT_FILE" <<'PY'
import pathlib
import re
import sys

path = pathlib.Path(sys.argv[1])
text = path.read_text()

patterns = {
    "cross_region": re.compile(r"METRIC cross_region_convergence_ms=(\d+) replicas=(\d+) expected_score=(\d+) threshold_ms=(\d+)"),
    "partition_heal": re.compile(r"METRIC partition_heal_convergence_ms=(\d+) replicas=(\d+) expected_score=(\d+) threshold_ms=(\d+) heal_at_ms=(\d+)"),
}

cross = patterns["cross_region"].search(text)
partition = patterns["partition_heal"].search(text)

if not cross or not partition:
    print("  Failed to extract WAN evaluation metrics from go test output.")
    raise SystemExit(1)

cross_ms, cross_replicas, cross_score, cross_threshold = cross.groups()
part_ms, part_replicas, part_score, part_threshold, heal_at_ms = partition.groups()

def status(value, threshold):
    return "PASS" if int(value) <= int(threshold) else "FAIL"

print("======================================================================")
print(f"  {'cross_region_convergence_ms':<32} {cross_ms}")
print(f"  {'cross_region_replicas':<32} {cross_replicas}")
print(f"  {'cross_region_expected_score':<32} {cross_score}")
print(f"  {'cross_region_threshold_ms':<32} {cross_threshold}")
print(f"  {'cross_region_status':<32} {status(cross_ms, cross_threshold)}")
print()
print(f"  {'partition_heal_convergence_ms':<32} {part_ms}")
print(f"  {'partition_heal_replicas':<32} {part_replicas}")
print(f"  {'partition_heal_expected_score':<32} {part_score}")
print(f"  {'partition_heal_threshold_ms':<32} {part_threshold}")
print(f"  {'partition_heal_split_duration_ms':<32} {heal_at_ms}")
print(f"  {'partition_heal_status':<32} {status(part_ms, part_threshold)}")
print("======================================================================")
PY
