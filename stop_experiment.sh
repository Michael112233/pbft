#!/usr/bin/env bash
set -uo pipefail

SESSION="${1:-pbft}"
REPORT="${2:-report.json}"
DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if (( $# > 2 )); then
    echo "Usage: $0 [tmux-session] [report-file]" >&2
    exit 2
fi

# Export failures must not stop the session from being killed.
"$DIR/print_all_latency_summaries.sh" "$SESSION" || echo "Warning: node latency export failed." >&2

"$DIR/export_client_report.sh" "$SESSION" "$REPORT" || echo "Warning: client report export failed." >&2

# Give the processes time to write their files before they are killed.
sleep 3

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
