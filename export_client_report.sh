#!/usr/bin/env bash
set -euo pipefail

SESSION="${1:-pbft}"
REPORT="${2:-report.json}"

if (( $# > 2 )); then
    echo "Usage: $0 [tmux-session] [report-file]" >&2
    exit 2
fi

if ! command -v tmux >/dev/null 2>&1; then
    echo "Error: tmux is required to trigger the client export." >&2
    exit 1
fi

if ! tmux has-session -t "$SESSION" 2>/dev/null; then
    echo "Error: tmux session '$SESSION' does not exist." >&2
    exit 1
fi

found=0
triggered=0
missed=()

while IFS='|' read -r window_name pane_id pane_pid pane_command; do
    # "client" for the single client, client1..clientN with client_num > 1
    if ! [[ "$window_name" =~ ^client[0-9]*$ ]]; then
        continue
    fi
    ((found += 1))

    if [[ "$pane_command" != "pbft_main" ]] &&
       ! pgrep -P "$pane_pid" -x pbft_main >/dev/null; then
        echo "Skipping $window_name: no running pbft_main process found." >&2
        missed+=("$window_name")
        continue
    fi

    # Same as typing "export <file>" in the client window (controller.go runClient).
    if tmux send-keys -t "$pane_id" -l "export $REPORT" &&
       tmux send-keys -t "$pane_id" Enter; then
        echo "Triggered client export on $window_name (pane $pane_id)."
        ((triggered += 1))
    else
        echo "Failed to send the export command to $window_name (pane $pane_id)." >&2
        missed+=("$window_name")
    fi
done < <(
    tmux list-panes -s -t "$SESSION" \
        -F '#{window_name}|#{pane_id}|#{pane_pid}|#{pane_current_command}'
)

if (( triggered == 0 )); then
    echo "Error: no running client pane found in session '$SESSION'." >&2
    exit 1
fi

if (( found == 1 )); then
    echo "Client TPS series is written to logs/$REPORT"
    echo "Client latency summary is written to logs/latency$REPORT"
else
    echo "Each client i writes logs/client<i>_$REPORT and logs/client<i>_latency$REPORT;"
    echo "scripts/merge_client_reports.py combines them into logs/$REPORT and logs/latency$REPORT."
fi

# With several clients a partial export still leaves files behind, but their reports
# would be missing from the merge, so say which ones and fail.
if (( triggered < found )); then
    echo "Error: exported $triggered of $found clients; not exported: ${missed[*]}" >&2
    exit 1
fi
