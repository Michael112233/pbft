#!/usr/bin/env bash
set -uo pipefail

SESSION="${1:-pbft}"
REPORT="${2:-report.json}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if (( $# > 2 )); then
    echo "Usage: $0 [tmux-session] [report-file]" >&2
    exit 2
fi

# Files written after this marker are this run's exports, not leftovers.
MARKER="$(mktemp)"
trap 'rm -f "$MARKER"' EXIT
NODE_IDS="$(tmux list-windows -t "$SESSION" -F '#{window_name}' 2>/dev/null | sed -n 's/^node\([0-9][0-9]*\)$/\1/p')"

# Export failures must not stop the session from being killed.
"$DIR/print_all_latency_summaries.sh" "$SESSION" || echo "Warning: node latency export failed." >&2

"$DIR/export_client_report.sh" "$SESSION" "$REPORT" || echo "Warning: client report export failed." >&2

# The exports run inside the processes and the two scripts above return at once, so wait
# for the files instead of a fixed sleep. The client latency summary sorts one sample per
# committed tx (~150M after a 7.5 h run, ~30 s); killing it mid-sort loses the file.
# The client writes the TPS series before the latency summary, so the latter appearing
# means both are complete.
EXPORT_WAIT_SECONDS="${EXPORT_WAIT_SECONDS:-600}"
expected_files=("$DIR/logs/latency$REPORT")
for id in $NODE_IDS; do
    expected_files+=("$DIR/logs/node_${id}_latencylog.json")
done
deadline=$((SECONDS + EXPORT_WAIT_SECONDS))
while :; do
    missing=()
    for f in "${expected_files[@]}"; do
        [[ "$f" -nt "$MARKER" ]] || missing+=("$f")
    done
    (( ${#missing[@]} == 0 )) && break
    if (( SECONDS >= deadline )); then
        echo "Warning: exports still missing after ${EXPORT_WAIT_SECONDS}s: ${missing[*]}" >&2
        break
    fi
    sleep 1
done
sleep 1 # let the last write finish before the processes are killed

if tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Killing tmux session '$SESSION'..."
    tmux kill-session -t "$SESSION"
else
    echo "tmux session '$SESSION' not found."
fi



# Usage examples:
#./stop_experiment.sh                 # session pbft, report.json
#./stop_experiment.sh pbft myrun.json # custom session and report name

# Remove the netem qdisc set up by alt_run_project.sh (if any).
NETEM_INTERFACE="lo"
if command -v tc >/dev/null 2>&1 && tc qdisc show dev "$NETEM_INTERFACE" 2>/dev/null | grep -Eq 'prio|netem'; then
    echo "Removing netem qdisc on $NETEM_INTERFACE..."
    sudo -n tc qdisc del dev "$NETEM_INTERFACE" root 2>/dev/null \
        || echo "Warning: could not remove qdisc (needs passwordless sudo); run: sudo tc qdisc del dev $NETEM_INTERFACE root" >&2
fi
