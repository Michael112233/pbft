#!/usr/bin/env bash
# Run configs one after another on a cluster: start, wait, stop, results/<config name>.
# The multi-host counterpart of run_experiments.sh.
#
#   scripts/cluster/run_experiments.sh <hosts.json> <duration> <config1.json> [config2.json ...]
#
# duration is per config: seconds, or with a suffix: 600, 10m, 1h30m, 7h20m.
#
# The script detaches itself (setsid nohup, as run_long_experiment.sh does), so closing the
# terminal or dropping ssh does not stop the batch. It returns at once; progress goes to
# cluster_run.out in the repo root:
#   tail -f cluster_run.out
# To end early: kill <pid printed at launch>. The current run is still stopped and its
# results saved; the configs after it are not started.
set -uo pipefail

if (( $# < 3 )); then
    echo "Usage: $0 <hosts.json> <duration: 600 | 10m | 7h20m> <config1.json> [config2.json ...]" >&2
    exit 2
fi

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO="$(cd "$DIR/../.." && pwd)"
OUT_FILE="$REPO/cluster_run.out"

# Check the arguments here, while the terminal still shows errors. Paths are made
# absolute because the detached copy runs from the repo root.
if [[ ! -f "$1" ]]; then
    echo "Hosts file not found: $1" >&2
    exit 2
fi
HOSTS="$(realpath "$1")"

# "7h20m" / "440m" / "90s" / "26400" -> seconds (same parser as run_long_experiment.sh)
to_seconds() {
    local s="$1" total=0
    if [[ "$s" =~ ^[0-9]+$ ]]; then echo "$s"; return; fi
    [[ "$s" =~ ^([0-9]+h)?([0-9]+m)?([0-9]+s)?$ && -n "$s" ]] || return 1
    [[ "$s" =~ ([0-9]+)h ]] && total=$((total + ${BASH_REMATCH[1]} * 3600))
    [[ "$s" =~ ([0-9]+)m ]] && total=$((total + ${BASH_REMATCH[1]} * 60))
    [[ "$s" =~ ([0-9]+)s ]] && total=$((total + ${BASH_REMATCH[1]}))
    echo "$total"
}
if ! DURATION="$(to_seconds "$2")" || (( DURATION <= 0 )); then
    echo "Invalid duration '$2' (use e.g. 600, 10m, 1h30m, 7h20m)." >&2
    exit 2
fi
shift 2
CONFIGS=()
for cfg in "$@"; do
    if [[ ! -f "$cfg" ]]; then
        echo "Config not found: $cfg" >&2
        exit 2
    fi
    CONFIGS+=("$(realpath "$cfg")")
done

# One batch at a time: two would fight over the same machines. The detached copy
# records its pid here and removes it when it ends.
PID_FILE="$REPO/.cluster/batch.pid"
if [[ -z "${CLUSTER_RUN_DETACHED:-}" && -f "$PID_FILE" ]] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
    echo "Error: a batch is already running (pid $(cat "$PID_FILE")); wait for it or kill it first." >&2
    exit 1
fi

# First invocation: relaunch detached and return.
if [[ -z "${CLUSTER_RUN_DETACHED:-}" ]]; then
    CLUSTER_RUN_DETACHED=1 setsid nohup "$0" "$HOSTS" "$DURATION" "${CONFIGS[@]}" >> "$OUT_FILE" 2>&1 < /dev/null &
    echo "Launched detached, pid $!  (${#CONFIGS[@]} config(s), ${DURATION}s each)"
    echo "Progress:              tail -f $OUT_FILE"
    echo "End early, save run:   kill $!"
    exit 0
fi

cd "$REPO"
mkdir -p "$REPO/.cluster"
echo $$ > "$PID_FILE"
trap 'rm -f "$PID_FILE"' EXIT

current=""
sleep_pid=""
# kill / Ctrl-C: stop and save the run in progress, then end the batch.
on_signal() {
    echo "[$(date '+%F %T')] interrupted"
    [[ -n "$sleep_pid" ]] && kill "$sleep_pid" 2>/dev/null
    if [[ -n "$current" ]]; then
        rm -rf "results/$current"
        "$DIR/stop.sh" "$HOSTS" "$current" || echo "Warning: stop.sh failed for $current." >&2
    fi
    echo "[$(date '+%F %T')] batch ended early"
    exit 130
}
trap on_signal INT TERM

for cfg in "${CONFIGS[@]}"; do
    name="$(basename "$cfg" .json)"
    echo "===== $(date --iso-8601=seconds) experiment '$name' ($cfg) for ${DURATION}s, ends about $(date -d "+${DURATION} seconds" '+%F %T') ====="
    current="$name"
    "$DIR/start.sh" "$HOSTS" "$cfg"
    status=$?
    if (( status == 3 )); then
        # Preflight failed (e.g. a host is not on the performance governor): nothing
        # was started, and every later config would fail the same way.
        echo "Error: start.sh preflight failed for $name; aborting the batch." >&2
        exit 1
    elif (( status != 0 )); then
        echo "Warning: start.sh failed for $name; stopping what started and moving on." >&2
    else
        # Sleep in the background and wait, so a kill is handled at once, not after the sleep.
        sleep "$DURATION" &
        sleep_pid=$!
        wait "$sleep_pid"
        sleep_pid=""
    fi
    rm -rf "results/$name"
    "$DIR/stop.sh" "$HOSTS" "$name" || echo "Warning: stop.sh failed for $name." >&2
    current=""
done

echo "[$(date '+%F %T')] All experiments finished."
