# Evaluation Harness

The repository already had functional cluster checks in `scripts/test_cluster.sh`, `scripts/test_cluster_full.sh`, and in `tests/integration_test.go`. Those cover replication, failover, moderation, and restart behavior, but they do not provide a full evaluation protocol for the prototype scope described in the paper section.

This repository now includes two explicit evaluation harnesses:

- `scripts/eval_cluster.sh`
- `scripts/eval_wan_partition.sh`
- `scripts/eval_all.sh`
- `tests/evaluation_wan_test.go`

## What Each Harness Covers

`scripts/eval_cluster.sh` is the live system evaluator.

It starts a local cluster with `15` nodes by default and measures:

- baseline read latency
- baseline write latency
- mixed-workload throughput
- availability during node failover with reroute attempts
- p95 and p99 read/write latency during failover
- resilience under restart churn
- reactive moderation-based pruning of malicious content

`tests/evaluation_wan_test.go` is the deterministic distributed-state evaluator.

It simulates `20` replicas across four regions and measures:

- CRDT convergence over high-latency cross-region links
- convergence after a partition heals across two replica groups

`scripts/eval_wan_partition.sh` is the executable wrapper for that deterministic evaluator.

It runs the targeted Go tests and prints a terminal summary for:

- cross-region convergence time
- partition-heal convergence time
- replica counts
- pass/fail status against the configured thresholds

`scripts/eval_all.sh` runs the complete methodology end to end:

- the live 15-node cluster evaluation
- the WAN/partition evaluation
- a final coverage summary showing which requested dimensions are covered

## Why WAN And Partition Evaluation Is Separate

The current runtime exposes no built-in traffic shaping, no link-delay injection, and no partition control API. Because of that, a shell script alone cannot honestly claim to test cross-region WAN latency or healed network partitions end to end without relying on external firewall or proxy tooling.

The deterministic Go evaluation keeps those scenarios explicit and reproducible while still using the repository's real CRDT and storage logic.

## Running The Live Evaluation

From the repository root:

```bash
bash scripts/eval_cluster.sh
```

This live script does not, by itself, cover cross-region WAN scenarios or healed network partitions.
Those are covered by the dedicated WAN/partition script below.

Useful environment overrides:

```bash
EVAL_NODE_COUNT=15
EVAL_READ_SAMPLES=60
EVAL_WRITE_SAMPLES=60
EVAL_FAILOVER_SAMPLES=50
EVAL_THROUGHPUT_DURATION=8
EVAL_THROUGHPUT_CONCURRENCY=12
```

For a quicker smoke run while keeping the 15-node topology:

```bash
EVAL_NODE_COUNT=15 \
EVAL_READ_SAMPLES=5 \
EVAL_WRITE_SAMPLES=5 \
EVAL_FAILOVER_SAMPLES=5 \
EVAL_THROUGHPUT_DURATION=2 \
EVAL_THROUGHPUT_CONCURRENCY=4 \
bash scripts/eval_cluster.sh
```

## Running The WAN And Partition Evaluation

```bash
bash scripts/eval_wan_partition.sh
```

## Running The Full Evaluation Suite

```bash
bash scripts/eval_all.sh
```

## Coverage Against The Requested Evaluation Scope

- Node crashes: covered by `scripts/eval_cluster.sh`
- Churn: covered by `scripts/eval_cluster.sh`
- Network partitions: covered by `scripts/eval_wan_partition.sh`
- Cross-region convergence: covered by `scripts/eval_wan_partition.sh`
- Operation latency: covered by `scripts/eval_cluster.sh`
- Tail latency (`p95`, `p99`): covered by `scripts/eval_cluster.sh`
- Throughput: covered by `scripts/eval_cluster.sh`
- Availability during failures: covered by `scripts/eval_cluster.sh`
- Convergence time after healing: covered by `scripts/eval_wan_partition.sh`
- Security and Sybil-resistance note: covered by the pruning and ban scenario in `scripts/eval_cluster.sh`

## Important Limitation

The live evaluation script measures failover with client-side reroute across surviving replicas. The current implementation does not automatically route requests through the DHT, so the reported failover latency should be interpreted as service-level recovery with rerouting rather than transparent request redirection inside the application.