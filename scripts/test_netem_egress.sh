#!/bin/bash
# Dry-run test of scripts/netem_egress.sh: no root, no tc. Checks the tc commands it would run
# and the delays it records, for a plain LAN run (no base) and the emulated 5+2 WAN (base, a
# NetworkDelay on top, back to base, clear).
#
#   bash scripts/test_netem_egress.sh
set -euo pipefail

SCRIPT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/netem_egress.sh"
export NETEM_DRY_RUN=1 NETEM_DEV=eth9
export NETEM_STATE_DIR
NETEM_STATE_DIR="$(mktemp -d)"
trap 'rm -rf "$NETEM_STATE_DIR"' EXIT
FAILS=0

run() { bash "$SCRIPT" "$@"; }

expect() { # <description> <output> <pattern>: the output has a line matching the pattern
    if grep -qE -- "$3" <<< "$2"; then
        echo "ok    $1"
    else
        echo "FAIL  $1: no line matching '$3' in:"; sed 's/^/        /' <<< "$2"; FAILS=$((FAILS + 1))
    fi
}

expect_not() { # <description> <output> <pattern>
    if grep -qE -- "$3" <<< "$2"; then
        echo "FAIL  $1: unexpected line matching '$3'"; FAILS=$((FAILS + 1))
    else
        echo "ok    $1"
    fi
}

delay_of() { # <ip>: its current delay in the saved state
    awk -v ip="$1" '$3 == ip {print $5}' "$NETEM_STATE_DIR/netem_egress_state.txt"
}

expect_delay() { # <ip> <ms>
    local got
    got="$(delay_of "$1")"
    if [[ "$got" == "$2" ]]; then
        echo "ok    $1 at $2 ms"
    else
        echo "FAIL  $1 at '$got' ms, want $2"; FAILS=$((FAILS + 1))
    fi
}

PEERS_N1=(10.10.1.2 10.10.1.3 10.10.1.4 10.10.1.5 10.10.1.6 10.10.1.7)

echo "== LAN: no base, NetworkDelay up then down (node 1)"
out="$(run up 170 "${PEERS_N1[@]}")"
expect "builds the tree" "$out" '^tc qdisc add dev eth9 root handle 1: prio bands 7 '
expect "a leaf per peer, at base 0" "$out" '^tc qdisc add dev eth9 parent 1:2 handle 12: netem limit 100000 delay 0ms'
expect "a filter per peer" "$out" '^tc filter add dev eth9 parent 1: protocol ip prio 10 flower dst_ip 10.10.1.7/32 classid 1:7'
expect "then raises the peers to 170" "$out" '^tc qdisc change dev eth9 parent 1:7 handle 17: netem limit 100000 delay 170ms'
expect_delay 10.10.1.2 170
out="$(run down "${PEERS_N1[@]}")"
expect "no base: down removes the qdisc" "$out" '^tc qdisc del dev eth9 root'
[[ ! -f "$NETEM_STATE_DIR/netem_egress_state.txt" ]] && echo "ok    state removed" || { echo "FAIL  state left"; FAILS=$((FAILS + 1)); }

echo "== WAN 5+2, near node 1 (far: 6, 7; client 10.10.1.8 near)"
out="$(run base 10.10.1.2=0 10.10.1.3=0 10.10.1.4=0 10.10.1.5=0 10.10.1.6=24 10.10.1.7=24 10.10.1.8=0)"
expect "base builds 7 leaves (8 bands)" "$out" 'prio bands 8 '
expect "far node at 24" "$out" 'parent 1:6 handle 16: netem limit 100000 delay 24ms'
expect "near client at 0" "$out" 'parent 1:8 handle 18: netem limit 100000 delay 0ms'
out="$(run up 100 "${PEERS_N1[@]}")"
expect_not "up does not rebuild the tree" "$out" 'qdisc (add|del)'
expect_delay 10.10.1.2 100
expect_delay 10.10.1.6 124
expect_delay 10.10.1.8 0
out="$(run down "${PEERS_N1[@]}")"
expect_not "down keeps the tree" "$out" 'qdisc del'
expect "far node back to its base" "$out" 'parent 1:6 handle 16: netem limit 100000 delay 24ms'
expect_delay 10.10.1.2 0
expect_delay 10.10.1.6 24
out="$(run clear)"
expect "clear removes the qdisc" "$out" '^tc qdisc del dev eth9 root'
[[ ! -f "$NETEM_STATE_DIR/netem_base.txt" ]] && echo "ok    base profile removed" || { echo "FAIL  base profile left"; FAILS=$((FAILS + 1)); }

echo "== WAN 5+2, far node 6 (client near)"
run base 10.10.1.1=24 10.10.1.2=24 10.10.1.3=24 10.10.1.4=24 10.10.1.5=24 10.10.1.7=0 10.10.1.8=24 > /dev/null
run up 100 10.10.1.1 10.10.1.2 10.10.1.3 10.10.1.4 10.10.1.5 10.10.1.7 > /dev/null
expect_delay 10.10.1.1 124
expect_delay 10.10.1.7 100
expect_delay 10.10.1.8 24
run clear > /dev/null

echo
if (( FAILS > 0 )); then
    echo "$FAILS check(s) failed"
    exit 1
fi
echo "all checks passed"
