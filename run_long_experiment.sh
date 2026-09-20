#!/usr/bin/env bash
# Long unattended experiment: start alt_run_project.sh, let it run, then stop_experiment.sh
# (which exports latency/TPS), then move logs/ to results/<config>_<timestamp>/.
#
# Usage: ./run_long_experiment.sh [duration] [config]
#   duration  seconds, or with a suffix: 26400, 440m, 7h20m   (default 7h20m)
#   config    default config/run2new.json
#
# The script detaches itself (setsid), so closing the terminal or dropping SSH does not stop
# it. Progress goes to long_run.out in the repo root (not logs/, which alt_run_project.sh wipes).
#   tail -f long_run.out
# To end early but still export and save results:  kill <pid printed at launch>
set -uo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$DIR"

DURATION_ARG="${1:-7h20m}"
CONFIG="${2:-config/run2new.json}"

# "7h20m" / "440m" / "26400" -> seconds
to_seconds() {
    local s="$1" total=0
    if [[ "$s" =~ ^[0-9]+$ ]]; then echo "$s"; return; fi
    [[ "$s" =~ ^([0-9]+h)?([0-9]+m)?([0-9]+s)?$ && -n "$s" ]] || return 1
    [[ "$s" =~ ([0-9]+)h ]] && total=$((total + ${BASH_REMATCH[1]} * 3600))
    [[ "$s" =~ ([0-9]+)m ]] && total=$((total + ${BASH_REMATCH[1]} * 60))
    [[ "$s" =~ ([0-9]+)s ]] && total=$((total + ${BASH_REMATCH[1]}))
    echo "$total"
}

if ! DURATION="$(to_seconds "$DURATION_ARG")" || (( DURATION <= 0 )); then
    echo "Invalid duration '$DURATION_ARG' (use e.g. 26400, 440m, 7h20m)." >&2
    exit 2
fi
if [[ ! -f "$CONFIG" ]]; then
    echo "Config not found: $CONFIG" >&2
    exit 2
fi

# First invocation: relaunch detached and return.
if [[ -z "${LONG_RUN_DETACHED:-}" ]]; then
    LONG_RUN_DETACHED=1 setsid nohup "$0" "$DURATION_ARG" "$CONFIG" >> long_run.out 2>&1 < /dev/null &
    echo "Launched detached, pid $!  (duration ${DURATION_ARG} = ${DURATION}s, config $CONFIG)"
    echo "Progress:        tail -f long_run.out"
    echo "End early + save: kill $!"
    exit 0
fi

name="$(basename "$CONFIG" .json)_$(date +%Y%m%d_%H%M%S)"
out="results/$name"
stopped=0

sleep_pid=""

stop_and_save() {
    (( stopped )) && return
    stopped=1
    [[ -n "$sleep_pid" ]] && kill "$sleep_pid" 2>/dev/null
    echo "[$(date '+%F %T')] stopping experiment (exports can take about a minute)..."
    ./stop_experiment.sh || echo "Warning: stop_experiment.sh failed." >&2
    mkdir -p results
    mv logs "$out" && mkdir -p logs
    cp "$CONFIG" "$out/config.json"
    echo "[$(date '+%F %T')] saved results to $out"
}

# kill / Ctrl-C ends the wait early but still exports and saves.
trap 'echo "[$(date "+%F %T")] interrupted"; stop_and_save; exit 130' INT TERM

echo "[$(date '+%F %T')] starting $CONFIG for ${DURATION}s, ends about $(date -d "+${DURATION} seconds" '+%F %T')"
NO_ATTACH=1 ./alt_run_project.sh "$CONFIG" || echo "Warning: alt_run_project.sh reported a failure." >&2

# sleep in the background and wait, so the trap fires immediately instead of after the sleep.
sleep "$DURATION" &
sleep_pid=$!
wait "$sleep_pid"

stop_and_save
echo "[$(date '+%F %T')] done"
