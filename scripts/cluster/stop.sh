#!/usr/bin/env bash
# Stop a cluster run started by scripts/cluster/start.sh and collect its logs into one
# results directory. The multi-host counterpart of stop_experiment.sh.
#
#   scripts/cluster/stop.sh <hosts.json> [results-name]
#
# 1. asks every node for its latency summary and the client(s) for the report, as
#    stop_experiment.sh does, and waits until every host has written its files
# 2. kills the tmux sessions and removes every node's egress netem
# 3. copies every host's logs/ into results/<name>/ (default <config>_<timestamp>),
#    flat, so the analysis scripts read it like a single-host run. Files that every
#    host writes (others.log, netem_schedule.log, learning-agent-launcher.log,
#    clock_offsets.txt, tcp_state.txt) get the host's label: others_node3.log, clock_offsets_client1.txt.
# 4. merges the client reports when client_num > 1, and saves config.json and hosts.json
set -uo pipefail

if (( $# < 1 || $# > 2 )); then
    echo "Usage: $0 <hosts.json> [results-name]" >&2
    exit 2
fi
HOSTS_FILE="$(realpath "$1")"
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
cd "$REPO_DIR"

CONFIG_PATH=""
CLIENT_COUNT=1
if [[ -f "$STATE_DIR/running" ]]; then
    CONFIG_PATH="$(sed -n 's/^CONFIG_PATH=//p' "$STATE_DIR/running")"
    CLIENT_COUNT="$(sed -n 's/^CLIENT_COUNT=//p' "$STATE_DIR/running")"
else
    echo "Warning: no $STATE_DIR/running (start.sh not run from here?); collecting without the config." >&2
fi
NAME="${2:-$(basename "${CONFIG_PATH:-run}" .json)_$(date +%Y%m%d_%H%M%S)}"
OUT="results/$NAME"
if [[ -e "$OUT" ]]; then
    echo "Error: $OUT already exists." >&2
    exit 1
fi
REPORT="report.json"
EXPORT_WAIT_SECONDS="${EXPORT_WAIT_SECONDS:-600}"
SHARED_NAMES=(others.log netem_schedule.log learning-agent-launcher.log clock_offsets.txt tcp_state.txt)

host_active() { # kind id
    [[ "$1" == node ]] || (( $2 <= CLIENT_COUNT ))
}

# The file whose appearance means the host's export is complete (stop_experiment.sh).
export_file() { # kind id
    if [[ "$1" == node ]]; then
        echo "logs/node_$2_latencylog.json"
    elif (( CLIENT_COUNT == 1 )); then
        echo "logs/latency$REPORT"
    else
        echo "logs/client$2_latency$REPORT"
    fi
}

trigger_export() { # label host kind id
    host_active "$3" "$4" || return 0
    if [[ "$3" == node ]]; then
        rsh "$2" "touch logs/.export_marker && ./print_all_latency_summaries.sh $SESSION"
    else
        rsh "$2" "touch logs/.export_marker && ./export_client_report.sh $SESSION $REPORT"
    fi
}

wait_export() { # label host kind id
    host_active "$3" "$4" || return 0
    local f deadline=$((SECONDS + EXPORT_WAIT_SECONDS))
    f="$(export_file "$3" "$4")"
    until rsh "$2" "[[ $f -nt logs/.export_marker ]]" 2>/dev/null; do
        if (( SECONDS >= deadline )); then
            echo "Warning: $f still missing after ${EXPORT_WAIT_SECONDS}s." >&2
            return 1
        fi
        sleep 2
    done
}

shutdown_host() { # label host kind id
    local down=""
    if [[ "$3" == node ]]; then
        down="sudo -n ./scripts/netem_egress.sh down $(peer_ips "$4") || echo 'Warning: netem down failed' >&2"
    fi
    rsh "$2" "tmux kill-session -t $SESSION 2>/dev/null || echo 'no tmux session $SESSION'
$down"
}

collect() { # label host kind id
    host_active "$3" "$4" || return 0
    mkdir -p "$OUT/.hosts/$1"
    rsync_ssh -a --exclude .export_marker "$2:$RUN_DIR/logs/" "$OUT/.hosts/$1/"
}

echo "== Requesting exports"
each_host trigger_export || echo "Warning: some exports could not be triggered." >&2
echo "== Waiting for the exported files (up to ${EXPORT_WAIT_SECONDS}s)"
each_host wait_export || echo "Warning: some exports are missing; collecting anyway." >&2
sleep 1 # let the last write finish before the processes are killed

echo "== Stopping processes and removing netem"
each_host shutdown_host || echo "Warning: shutdown failed on some hosts." >&2

echo "== Collecting logs into $OUT"
mkdir -p "$OUT"
each_host collect || echo "Warning: some hosts' logs could not be copied." >&2

shopt -s nullglob
for dir in "$OUT"/.hosts/*/; do
    label="$(basename "$dir")"
    for f in "$dir"*; do
        base="$(basename "$f")"
        dest="$base"
        shared=0
        for s in "${SHARED_NAMES[@]}"; do [[ "$base" == "$s" ]] && shared=1; done
        if (( shared )) || [[ -e "$OUT/$base" ]]; then
            if [[ "$base" == *.* ]]; then
                dest="${base%.*}_$label.${base##*.}"
            else
                dest="${base}_$label"
            fi
            (( shared )) || echo "Warning: $base from $label collides with another host's; saved as $dest." >&2
        fi
        mv "$f" "$OUT/$dest"
    done
    rmdir "$dir" 2>/dev/null || echo "Warning: $dir not empty (subdirectories?); left in place." >&2
done
rmdir "$OUT/.hosts" 2>/dev/null || true

if (( CLIENT_COUNT > 1 )); then
    python3 scripts/merge_client_reports.py "$OUT" --report "$REPORT" --clients $(seq 1 "$CLIENT_COUNT") \
        || echo "Warning: merging the client reports failed." >&2
fi

[[ -n "$CONFIG_PATH" ]] && cp "$CONFIG_PATH" "$OUT/config.json"
cp "$HOSTS_FILE" "$OUT/hosts.json"
rm -f "$STATE_DIR/running"
echo "Saved results to $OUT"
