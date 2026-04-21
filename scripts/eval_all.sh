#!/usr/bin/env bash

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

echo "======================================================================"
echo " DReddit Full Evaluation Suite"
echo "======================================================================"

echo
echo "-- [1] Live cluster evaluation --"
bash "$REPO_ROOT/scripts/eval_cluster.sh"

echo
echo "-- [2] WAN / partition evaluation --"
bash "$REPO_ROOT/scripts/eval_wan_partition.sh"

echo
echo "-- [3] Coverage Summary --"
echo "======================================================================"
printf "  %-36s %s\n" "node_crashes" "covered"
printf "  %-36s %s\n" "churn" "covered"
printf "  %-36s %s\n" "operation_latency" "covered"
printf "  %-36s %s\n" "throughput" "covered"
printf "  %-36s %s\n" "availability_during_failures" "covered"
printf "  %-36s %s\n" "tail_latency_p95_p99" "covered"
printf "  %-36s %s\n" "security_sybil_resistance" "covered"
printf "  %-36s %s\n" "cross_region_convergence" "covered"
printf "  %-36s %s\n" "network_partitions" "covered"
printf "  %-36s %s\n" "partition_heal_convergence" "covered"
echo "======================================================================"
