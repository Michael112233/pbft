#!/usr/bin/env bash
set -euo pipefail

SESSION="${1:-pbft}"

if (( $# > 1 )); then
    echo "Usage: $0 [tmux-session]" >&2
    exit 2
fi

if ! command -v tmux >/dev/null 2>&1; then
    echo "Error: tmux is required to trigger latency-summary exports." >&2
    exit 1
fi

if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Error: tmux session '$SESSION' does not exist." >&2
    exit 1
fi

triggered=0

while IFS='|' read -r window_name pane_id pane_pid pane_command; do
    if [[ ! "$window_name" =~ ^node[0-9]+$ ]]; then
        continue
    fi

    if [[ "$pane_command" != "pbft_main" ]] &&
       ! pgrep -P "$pane_pid" -x pbft_main >/dev/null; then
        echo "Skipping $window_name: no running pbft_main process found." >&2
        continue
    fi

    if tmux send-keys -t "$pane_id" -l "x" &&
       tmux send-keys -t "$pane_id" Enter; then
        echo "Triggered latency-summary export on $window_name (pane $pane_id)."
        ((triggered += 1))
    fi
done < <(
    tmux list-panes -s -t "$SESSION" \
        -F '#{window_name}|#{pane_id}|#{pane_pid}|#{pane_current_command}'
)

if (( triggered == 0 )); then
    echo "Error: no running pbft_main panes were found in node windows in session '$SESSION'." >&2
    exit 1
fi

echo "Requested latency-summary exports from $triggered node(s)."
echo "Latency summary files are written to logs/node_<id>_latencylog.json."
