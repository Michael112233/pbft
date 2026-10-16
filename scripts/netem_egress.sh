#!/bin/bash
# Per-host netem for -m remote, run by every node as: sudo -n scripts/netem_egress.sh ...
#
#   netem_egress.sh up <delay_ms> <peer_ip>...   delay this host's packets to each peer
#   netem_egress.sh down <peer_ip>...            remove the qdisc (idempotent)
#
# The remote counterpart of netem_scenario.sh. That script runs on one node and puts a
# filter for every node pair on the shared lo. Here each host delays only its own egress,
# so with every node running it each directed node pair is delayed once (A->B at A,
# B->A at B), the same as on lo. Only traffic to a peer node's IP matches, so the client
# and the learning agent (on the host's lo) are never delayed, as on lo.
#
# Same qdisc layout as netem_scenario.sh, on whichever device routes to the peers (looked
# up with `ip route get`, so nothing is configured per cluster; peers on several devices
# get one qdisc per device). down takes the peers again to find the same devices.
# Transitions are appended to logs/netem_schedule.log in netem_scenario.sh's format.
set -euo pipefail

NETEM_LIMIT=100000
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG="$ROOT/logs/netem_schedule.log"

usage() {
    echo "usage: $0 up <delay_ms> <peer_ip>... | down <peer_ip>..." >&2
    exit 2
}

log_delay() {
    mkdir -p "$(dirname "$LOG")"
    echo "$(date --iso-8601=ns) scenario delay=$1 devs=$2" >> "$LOG"
}

# route_dev <ip>: the device the kernel sends <ip> out of.
route_dev() {
    local dev
    dev="$(ip -o route get "$1" | awk '{for (i = 1; i < NF; i++) if ($i == "dev" && d == "") d = $(i + 1)} END {print d}')"
    if [[ -z "$dev" ]]; then
        echo "no route to $1" >&2
        exit 1
    fi
    if [[ "$dev" == "lo" ]]; then
        echo "$1 is a local address; peers must be other hosts" >&2
        exit 1
    fi
    echo "$dev"
}

declare -A PEERS_BY_DEV=()

group_peers() {
    local ip dev
    for ip in "$@"; do
        dev="$(route_dev "$ip")"
        PEERS_BY_DEV[$dev]+="$ip "
    done
}

teardown() {
    local dev
    for dev in "${!PEERS_BY_DEV[@]}"; do
        tc qdisc del dev "$dev" root 2>/dev/null || true
    done
}

case "${1:-}" in
up)
    delay_ms="${2:-}"
    [[ "$delay_ms" =~ ^[0-9]+$ ]] || usage
    shift 2
    (( $# > 0 )) || usage
    group_peers "$@"
    teardown
    for dev in "${!PEERS_BY_DEV[@]}"; do
        {
            echo "qdisc add dev $dev root handle 1: prio bands 3 priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
            echo "qdisc add dev $dev parent 1:3 handle 30: netem limit $NETEM_LIMIT delay ${delay_ms}ms"
            for ip in ${PEERS_BY_DEV[$dev]}; do
                echo "filter add dev $dev parent 1: protocol ip prio 10 flower dst_ip $ip/32 classid 1:3"
            done
        } | tc -batch -
    done
    log_delay "${delay_ms}ms" "$(echo "${!PEERS_BY_DEV[@]}" | tr ' ' ',')"
    ;;
down)
    shift
    (( $# > 0 )) || usage
    group_peers "$@"
    teardown
    log_delay "0ms" "$(echo "${!PEERS_BY_DEV[@]}" | tr ' ' ',')"
    ;;
*)
    usage
    ;;
esac
