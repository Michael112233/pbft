#!/bin/bash
# Per-host netem for -m remote, run as root on every host (sudo -n scripts/netem_egress.sh ...):
#
#   netem_egress.sh base <ip>=<ms> ...   permanent per-destination delay (an emulated WAN):
#                                        start.sh, every host incl. clients, before any process
#   netem_egress.sh up <delay_ms> <peer_ip>...
#                                        scenario delay (NetworkDelay) on top of the base, for
#                                        the listed peers: the node's netem worker
#   netem_egress.sh down <peer_ip>...    the listed peers back to their base (no base: remove
#                                        the qdisc, as before there was a base)
#   netem_egress.sh clear [<ip>...]      remove the qdisc and the saved base: stop.sh
#
# Each host delays only its own egress, so with every host running it each directed pair is
# delayed once (A->B at A, B->A at B), the same as netem_scenario.sh does for every pair on lo.
# Only traffic to a listed destination IP matches; the learning agent (on the host's lo) and
# anything else is never delayed.
#
# Layout, per device that routes to the destinations (looked up with `ip route get`, so nothing
# is configured per cluster): a root prio qdisc whose band 1 takes unmatched traffic undelayed,
# and one band + netem leaf + flower dst_ip filter per destination. A destination's delay is its
# base plus the current scenario delay; a change of either is a `tc qdisc change` on its leaf,
# which keeps the packets netem is holding (deleting the qdisc would drop them). The tree is built
# once, by base (or by the first up when there is no base, e.g. on a plain LAN run).
#
# prio has at most 16 bands, so one tree takes at most 15 destinations. n = 7 nodes plus at most
# 2 clients needs 8. Past 15, use HTB classes instead of prio bands.
#
# State (which destination has which band, base and current delay) is kept in
# logs/netem_egress_state.txt, so every command after the first only changes delays. Transitions
# are appended to logs/netem_schedule.log; the base profile is also in logs/netem_base.txt.
#
# Testing without root: NETEM_DRY_RUN=1 prints the tc batch instead of running it,
# NETEM_STATE_DIR moves the state files, NETEM_DEV skips the route lookup.
set -euo pipefail

NETEM_LIMIT=100000
MAX_DESTS=15
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
STATE_DIR="${NETEM_STATE_DIR:-$ROOT/logs}"
STATE="$STATE_DIR/netem_egress_state.txt" # lines: <dev> <band> <ip> <base_ms> <delay_ms>
BASE_FILE="$STATE_DIR/netem_base.txt"
LOG="$STATE_DIR/netem_schedule.log"

usage() {
    echo "usage: $0 base <ip>=<ms>... | up <delay_ms> <peer_ip>... | down <peer_ip>... | clear [<ip>...]" >&2
    exit 2
}

log_line() {
    mkdir -p "$STATE_DIR"
    echo "$(date --iso-8601=ns) $*" >> "$LOG"
}

# route_dev <ip>: the device the kernel sends <ip> out of.
route_dev() {
    if [[ -n "${NETEM_DEV:-}" ]]; then
        echo "$NETEM_DEV"
        return
    fi
    local dev
    dev="$(ip -o route get "$1" | awk '{for (i = 1; i < NF; i++) if ($i == "dev" && d == "") d = $(i + 1)} END {print d}')"
    if [[ -z "$dev" ]]; then
        echo "no route to $1" >&2
        exit 1
    fi
    if [[ "$dev" == "lo" ]]; then
        echo "$1 is a local address; destinations must be other hosts" >&2
        exit 1
    fi
    echo "$dev"
}

tc_batch() { # reads tc batch lines on stdin
    if [[ -n "${NETEM_DRY_RUN:-}" ]]; then
        sed 's/^/tc /'
    else
        tc -batch -
    fi
}

leaf_handle() { # band -> netem leaf handle (hex), 2 -> 12, 15 -> 1f
    printf '%x' $((0x10 + $1))
}

# State, loaded into these arrays keyed by ip.
declare -A DEV=() BAND=() BASE=() DELAY=()
ORDER=()

load_state() {
    [[ -f "$STATE" ]] || return 0
    local dev band ip base delay
    while read -r dev band ip base delay; do
        [[ -n "$ip" ]] || continue
        DEV[$ip]="$dev" BAND[$ip]="$band" BASE[$ip]="$base" DELAY[$ip]="$delay"
        ORDER+=("$ip")
    done < "$STATE"
}

save_state() {
    mkdir -p "$STATE_DIR"
    local ip
    : > "$STATE"
    for ip in "${ORDER[@]}"; do
        echo "${DEV[$ip]} ${BAND[$ip]} $ip ${BASE[$ip]} ${DELAY[$ip]}" >> "$STATE"
    done
}

# build_tree <ip>=<base_ms>...: delete whatever is on the devices and build the tree from
# scratch, every destination at its base delay. Only for the first command of a run.
build_tree() {
    DEV=() BAND=() BASE=() DELAY=() ORDER=()
    local arg ip ms dev
    declare -A next_band=() devs=()
    for arg in "$@"; do
        ip="${arg%%=*}" ms="${arg#*=}"
        [[ "$ms" =~ ^[0-9]+$ && -n "$ip" && "$ip" != "$arg" ]] || usage
        dev="$(route_dev "$ip")"
        devs[$dev]=1
        next_band[$dev]=$(( ${next_band[$dev]:-1} + 1 ))
        if (( next_band[$dev] - 1 > MAX_DESTS )); then
            echo "more than $MAX_DESTS destinations on $dev: prio has 16 bands; use HTB classes" >&2
            exit 1
        fi
        DEV[$ip]="$dev" BAND[$ip]="${next_band[$dev]}" BASE[$ip]="$ms" DELAY[$ip]="$ms"
        ORDER+=("$ip")
    done
    for dev in "${!devs[@]}"; do
        if [[ -z "${NETEM_DRY_RUN:-}" ]]; then
            tc qdisc del dev "$dev" root 2>/dev/null || true
        fi
        {
            echo "qdisc add dev $dev root handle 1: prio bands ${next_band[$dev]} priomap 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0"
            for ip in "${ORDER[@]}"; do
                [[ "${DEV[$ip]}" == "$dev" ]] || continue
                echo "qdisc add dev $dev parent 1:${BAND[$ip]} handle $(leaf_handle "${BAND[$ip]}"): netem limit $NETEM_LIMIT delay ${DELAY[$ip]}ms"
                echo "filter add dev $dev parent 1: protocol ip prio 10 flower dst_ip $ip/32 classid 1:${BAND[$ip]}"
            done
        } | tc_batch
    done
    save_state
}

# set_delays <ip>=<delay_ms>...: change the listed leaves (only those whose delay differs).
# The batch is collected first: DELAY must be updated in this shell, not in a pipeline's.
set_delays() {
    local arg ip ms lines=()
    for arg in "$@"; do
        ip="${arg%%=*}" ms="${arg#*=}"
        if [[ "${DELAY[$ip]}" == "$ms" ]]; then
            continue
        fi
        DELAY[$ip]="$ms"
        lines+=("qdisc change dev ${DEV[$ip]} parent 1:${BAND[$ip]} handle $(leaf_handle "${BAND[$ip]}"): netem limit $NETEM_LIMIT delay ${ms}ms")
    done
    if (( ${#lines[@]} > 0 )); then
        printf '%s\n' "${lines[@]}" | tc_batch
    fi
    save_state
}

delays_summary() { # "ip=delay ..." of the whole tree
    local ip out=()
    for ip in "${ORDER[@]}"; do
        out+=("$ip=${DELAY[$ip]}ms")
    done
    echo "${out[*]}"
}

devs_of() { # the devices of the tree, comma-separated
    local ip
    declare -A seen=()
    for ip in "${ORDER[@]}"; do seen[${DEV[$ip]}]=1; done
    local IFS=,
    echo "${!seen[*]}"
}

case "${1:-}" in
base)
    shift
    (( $# > 0 )) || usage
    build_tree "$@"
    mkdir -p "$STATE_DIR"
    printf '%s\n' "$@" | tr '=' ' ' > "$BASE_FILE"
    log_line "base devs=$(devs_of) $(delays_summary)"
    ;;
up)
    delay_ms="${2:-}"
    [[ "$delay_ms" =~ ^[0-9]+$ ]] || usage
    shift 2
    (( $# > 0 )) || usage
    load_state
    # No tree yet (no base, e.g. a plain LAN run), or a peer the tree does not know: build it,
    # keeping any base already set.
    rebuild=0
    (( ${#ORDER[@]} > 0 )) || rebuild=1
    for ip in "$@"; do [[ -n "${BAND[$ip]:-}" ]] || rebuild=1; done
    if (( rebuild )); then
        args=()
        for ip in "${ORDER[@]}"; do args+=("$ip=${BASE[$ip]}"); done
        for ip in "$@"; do [[ -n "${BAND[$ip]:-}" ]] || args+=("$ip=0"); done
        build_tree "${args[@]}"
    fi
    declare -A peer=()
    for ip in "$@"; do peer[$ip]=1; done
    args=()
    for ip in "${ORDER[@]}"; do
        if [[ -n "${peer[$ip]:-}" ]]; then
            args+=("$ip=$(( ${BASE[$ip]} + delay_ms ))")
        else
            args+=("$ip=${BASE[$ip]}")
        fi
    done
    set_delays "${args[@]}"
    log_line "scenario delay=${delay_ms}ms devs=$(devs_of)"
    log_line "netem $(delays_summary)"
    ;;
down)
    shift
    (( $# > 0 )) || usage
    load_state
    has_base=0
    for ip in "${ORDER[@]}"; do [[ "${BASE[$ip]}" == 0 ]] || has_base=1; done
    if (( ${#ORDER[@]} == 0 || has_base == 0 )); then
        # Nothing permanent to keep: remove the qdisc, as before there was a base.
        declare -A devs=()
        for ip in "${ORDER[@]}" "$@"; do devs[$(route_dev "$ip")]=1; done
        for dev in "${!devs[@]}"; do
            if [[ -n "${NETEM_DRY_RUN:-}" ]]; then echo "tc qdisc del dev $dev root"; else tc qdisc del dev "$dev" root 2>/dev/null || true; fi
        done
        rm -f "$STATE"
        log_line "scenario delay=0ms devs=$(IFS=,; echo "${!devs[*]}")"
    else
        args=()
        for ip in "${ORDER[@]}"; do args+=("$ip=${BASE[$ip]}"); done
        set_delays "${args[@]}"
        log_line "scenario delay=0ms devs=$(devs_of)"
        log_line "netem $(delays_summary)"
    fi
    ;;
clear)
    shift
    load_state
    declare -A devs=()
    for ip in "${ORDER[@]}" "$@"; do devs[$(route_dev "$ip")]=1; done
    for dev in "${!devs[@]}"; do
        if [[ -n "${NETEM_DRY_RUN:-}" ]]; then echo "tc qdisc del dev $dev root"; else tc qdisc del dev "$dev" root 2>/dev/null || true; fi
    done
    rm -f "$STATE" "$BASE_FILE"
    log_line "clear devs=$(IFS=,; echo "${!devs[*]}")"
    ;;
*)
    usage
    ;;
esac
