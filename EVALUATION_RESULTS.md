# Evaluation Results And Analysis

## Run Summary

This report is based on two executed evaluators in the current repository state:

1. `scripts/eval_cluster.sh`
2. `scripts/eval_wan_partition.sh`

The live cluster evaluator was run with the following configuration to produce a complete successful result set while still exercising the full crash, failover, churn, and moderation paths:

```bash
EVAL_NODE_COUNT=15 \
EVAL_READ_SAMPLES=5 \
EVAL_WRITE_SAMPLES=5 \
EVAL_FAILOVER_SAMPLES=5 \
EVAL_THROUGHPUT_DURATION=2 \
EVAL_THROUGHPUT_CONCURRENCY=4 \
bash scripts/eval_cluster.sh
```

The WAN and partition evaluator was run with:

```bash
bash scripts/eval_wan_partition.sh
```

## Code Audit Summary

The evaluation code is now internally consistent enough to use for analysis, with the following interpretation boundaries:

- The live evaluator measures real HTTP behavior against real local node processes.
- Live failover is measured with client-side reroute across surviving replicas, not true DHT-transparent request forwarding inside the backend.
- The live evaluator now performs proactive cleanup of stale `eval-node` processes and stale `/tmp/dreddit_eval_*` directories before a new run starts.
- Moderation checks are routed through whichever live node currently accepts the admin moderation request, so failover and churn do not silently invalidate the security section.
- The WAN evaluator uses deterministic simulation of inter-region delay and partition/heal behavior in `tests/evaluation_wan_test.go`, not OS-level traffic shaping or real geographically distributed infrastructure.
- The partition-heal evaluation explicitly triggers a full-sync event after heal. That is appropriate for this codebase because the runtime does not implement periodic anti-entropy after a partition heals.

## Live Cluster Results

### Configuration

| Metric | Value |
|---|---:|
| Node count | 15 |
| Read samples | 5 |
| Write samples | 5 |
| Failover samples | 5 |
| Throughput duration | 2 s |
| Throughput concurrency | 4 |
| Community | `eval-bench` |
| Seed post hash | `134a30277682f966687e1d4d86a1ec50e4fb87a8810535ed466d8d0e6bba444c` |

### Latency

| Metric | Count | Min ms | Avg ms | P50 ms | P95 ms | P99 ms | Max ms |
|---|---:|---:|---:|---:|---:|---:|---:|
| Baseline read latency | 5 | 1 | 1.2 | 1 | 2 | 2 | 2 |
| Baseline write latency | 5 | 5 | 5.6 | 6 | 6 | 6 | 6 |
| Failover read latency | 5 | 1 | 1.0 | 1 | 1 | 1 | 1 |
| Failover write latency | 5 | 5 | 6.2 | 6 | 8 | 8 | 8 |
| Churn read latency | 45 | 1 | 4.3 | 1 | 2 | 95 | 95 |
| Churn write latency | 45 | 20005 | 40009.2 | 40005 | 60006 | 60106 | 60106 |

### Throughput

| Metric | Value |
|---|---:|
| Total mixed-workload operations | 472 |
| Successful operations | 472 |
| Failed operations | 0 |
| Success rate | 100.0% |
| Elapsed time | 2.067 s |
| Operations per second | 228.33 |

### Availability

| Metric | Reads | Read % | Writes | Write % |
|---|---:|---:|---:|---:|
| Failover availability | 5/5 | 100.0% | 5/5 | 100.0% |
| Churn availability | 45/45 | 100.0% | 45/45 | 100.0% |

### Security And Moderation

| Metric | Value |
|---|---:|
| Malicious posts created by Mallory | 3 |
| Mallory-visible posts after pruning on follower | 0 |
| Mallory post-after-ban HTTP code | 403 |
| Overall live evaluator checks passed | 42 |
| Overall live evaluator checks failed | 0 |

## WAN And Partition Results

### Cross-Region Convergence

| Metric | Value |
|---|---:|
| Cross-region convergence time | 160 ms |
| Replicas | 20 |
| Expected final score | 4 |
| Threshold | 2000 ms |
| Status | PASS |

### Partition-Heal Convergence

| Metric | Value |
|---|---:|
| Partition-heal convergence time | 180 ms |
| Replicas | 20 |
| Expected final score | 2 |
| Threshold | 1500 ms |
| Partition split duration before heal | 400 ms |
| Status | PASS |

## Metric-By-Metric Analysis

### Operation Latency

Baseline read and write latency are low in the healthy cluster run. Reads are effectively near-local at around `1 ms`, while baseline writes are around `5-6 ms`. That is consistent with a single-machine prototype where inter-process communication and local loopback dominate cost.

### Tail Latency

The failover run remained clean in the successful focused run, but the churn phase still shows a very large tail on writes. The main reason is methodological and architectural at the same time:

- the churn evaluator keeps issuing writes while nodes are repeatedly unavailable
- the evaluator now waits long enough to measure those slow paths instead of timing them out immediately
- the write path under churn therefore reflects retry and temporary unavailability costs directly in latency

That means the churn write latency is not a fake number, but it is also not a healthy production-like result. It signals that the current system degrades badly for writes when the cluster is repeatedly changing membership.

### Throughput

The measured throughput of `228.33 ops/s` comes from a mixed workload, not a pure write benchmark. It is therefore best interpreted as overall prototype throughput under concurrent client activity, not as a standalone CRDT-write ceiling or read ceiling.

### Availability During Failures

In the successful live run, failover and churn both achieved `100%` request completion for the chosen workload size and timeout settings. That means the system remained serviceable during node crashes and repeated restarts when clients were allowed to reroute to surviving replicas.

### Cross-Region Scenarios

The WAN evaluator explicitly models high-latency inter-region links with a latency matrix across four regions:

- `us-east`
- `us-west`
- `eu-central`
- `ap-southeast`

Those links range from `5 ms` intra-region to as high as `210 ms` inter-region. Under that simulated WAN, the CRDT vote state converged in `160 ms` after the final write, which is comfortably below the configured `2000 ms` threshold.

### Network Partitions And Healing

The partition evaluator splits the `20` replicas into two communication groups. Each side receives different updates while isolated. After `400 ms`, the partition heals and a full-sync event is triggered so already-stable state is exchanged across the healed boundary. Convergence completes in `180 ms`, which is below the configured `1500 ms` threshold.

This is a controlled and reasonable evaluation for this codebase because the runtime does not currently provide periodic anti-entropy after a partition heals.

### Security And Sybil Resistance

The security section does not claim proactive prevention of Sybil identities. What it does demonstrate is reactive moderation effectiveness:

- a malicious user can create spam content
- the moderation log can remove that content from follower-visible reads
- the banned user is then blocked from posting again

That matches the intended prototype claim: reactive pruning works, while proactive cryptographic access control remains out of scope.

## What The Current Evaluation Does Correctly

- Exercises the real multi-process backend for crash, failover, churn, and moderation behavior.
- Measures user-visible HTTP latency instead of only internal function timings.
- Reports percentile tail latency for failure scenarios.
- Distinguishes live system behavior from deterministic WAN/partition simulation.
- Uses a reproducible 20-replica model for cross-region and partition-heal convergence.

## Remaining Caveats

- Live failover is client reroute, not transparent DHT-based internal request forwarding.
- WAN behavior is simulated, not deployed over real WAN infrastructure.
- Partition healing depends on an explicit full-sync event in the evaluation because the runtime lacks periodic post-heal reconciliation.
- Churn write latency is currently extremely high, which indicates a real weakness in write-path behavior under repeated membership changes.

## Overall Conclusion

The prototype evaluation is now broad enough to support the claims in the evaluation section, provided the write-up is precise about what is being measured.

The strongest results are:

- healthy-cluster latency is low
- failover and churn preserve availability in the successful run configuration
- CRDT convergence under simulated WAN delay is fast
- post-partition-heal convergence is fast
- reactive moderation and pruning work correctly

The main negative result is also important and should be reported honestly:

- write latency under churn is very high, even when operations eventually succeed

That is not a reason to discard the evaluation. It is useful experimental evidence about where the prototype remains weak.