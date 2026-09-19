#!/bin/bash
# Scenario-mode netem control, run by node 4 as: sudo -n scripts/netem_scenario.sh ...
#
#   netem_scenario.sh up <delay_ms> <node_count>   fresh prio+netem qdisc on lo with a
#                                                 flower filter for every node pair
#   netem_scenario.sh down                         remove it (idempotent)
#
# Same qdisc layout as setup_netem in alt_run_project.sh. Transitions are appended
# to logs/netem_schedule.log with the same timestamp format.
set -euo pipefail

NETEM_INTERFACE="lo"
NETEM_LIMIT=100000
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG="$ROOT/logs/netem_schedule.log"

log_delay() {
    mkdir -p "$(dirname "$LOG")"
    echo "$(date --iso-8601=ns) scenario delay=$1" >> "$LOG"
}

teardown() {
    tc qdisc del dev "$NETEM_INTERFACE" root 2>/dev/null || true
}

case "${1:-}" in
up)
    delay_ms="${2:?usage: $0 up <delay_ms> <node_count>}"
    node_count="${3:?usage: $0 up <delay_ms> <node_count>}"
    if (( node_count < 1 || node_count > 8 )); then
        echo "node_count must be between 1 and 8 in loopbackip mode" >&2
        exit 1
    fi

    teardown
    {
        echo "qdisc add dev $NETEM_INTERFACE root handle 1: prio bands 3 priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
        echo "qdisc add dev $NETEM_INTERFACE parent 1:3 handle 30: netem limit $NETEM_LIMIT delay ${delay_ms}ms"
        for ((src_id = 1; src_id <= node_count; src_id++)); do
            for ((dst_id = 1; dst_id <= node_count; dst_id++)); do
                echo "filter add dev $NETEM_INTERFACE parent 1: protocol ip prio 10 flower src_ip 127.0.0.$((src_id + 1))/32 dst_ip 127.0.0.$((dst_id + 1))/32 classid 1:3"
            done
        done
    } | tc -batch -
    log_delay "${delay_ms}ms"
    ;;
down)
    teardown
    log_delay "0ms"
    ;;
*)
    echo "usage: $0 up <delay_ms> <node_count> | down" >&2
    exit 2
    ;;
esac
