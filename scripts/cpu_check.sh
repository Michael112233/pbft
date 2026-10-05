#!/usr/bin/env bash
# Baseline CPU check for a fresh experiment host. Run it once per new machine,
# before the first experiment, and record the result.
#
#   scripts/cpu_check.sh                    topology, governor, and the clock measured under load
#   scripts/cpu_check.sh --cores 8          load 8 logical CPUs instead of 1 (all-core turbo bin)
#   scripts/cpu_check.sh --secs 5           sample for 5s instead of 3s
#   scripts/cpu_check.sh --fix              pin every CPU to the performance governor (needs sudo)
#
# Why the load test exists: with the intel_pstate/intel_cpufreq driver,
# scaling_cur_freq is not the requested P-state, it is an APERF/MPERF *average*
# that only refreshes when the scheduler runs on that CPU. An idle core returns a
# frozen leftover value, so a quiet box reads ~1 GHz under any governor, including
# `performance`, and a core that was busy a moment ago keeps reporting its old high
# value. The only honest reading comes from a core that is busy right now. Same
# reason logs/freq_trace.csv is trustworthy during a run but not before one --- see
# scripts/freq_trace_analysis.md.
set -euo pipefail

CPUDIR=/sys/devices/system/cpu
CORES=1
SECS=3
DO_FIX=0

usage() { sed -n '2,16p' "${BASH_SOURCE[0]}" | sed 's/^# \?//'; }

while [[ $# -gt 0 ]]; do
    case "$1" in
        --cores) CORES="$2"; shift 2 ;;
        --secs)  SECS="$2";  shift 2 ;;
        --fix)   DO_FIX=1;   shift ;;
        -h|--help) usage; exit 0 ;;
        *) echo "unknown argument: $1" >&2; usage; exit 1 ;;
    esac
done

read_or_na() { cat "$1" 2>/dev/null || echo "n/a"; }
khz_to_ghz() { awk -v k="$1" 'BEGIN { if (k ~ /^[0-9]+$/) printf "%.2f GHz", k/1e6; else print "n/a" }'; }

if [[ "$DO_FIX" == 1 ]]; then
    if ! grep -qw performance "$CPUDIR/cpu0/cpufreq/scaling_available_governors"; then
        echo "performance governor is not available on this host" >&2
        exit 1
    fi
    read_or_na "$CPUDIR/cpu0/cpufreq/scaling_governor" > /tmp/prev_governor
    echo "saved previous governor to /tmp/prev_governor: $(cat /tmp/prev_governor)"
    echo performance | sudo tee "$CPUDIR"/cpu*/cpufreq/scaling_governor > /dev/null
    echo "now set on all CPUs: $(cat "$CPUDIR"/cpu*/cpufreq/scaling_governor | sort -u | tr '\n' ' ')"
    exit 0
fi

ncpu=$(nproc)
if (( CORES < 1 || CORES > ncpu )); then
    echo "--cores must be between 1 and $ncpu" >&2
    exit 1
fi

echo "=== topology ==="
lscpu | grep -E '^(Model name|Socket\(s\)|Core\(s\) per socket|Thread\(s\) per core|CPU\(s\)):' | sed 's/  */ /g'

echo
echo "=== cpufreq configuration ==="
echo "driver:         $(read_or_na "$CPUDIR/cpu0/cpufreq/scaling_driver")"
echo "governor:       $(cat "$CPUDIR"/cpu*/cpufreq/scaling_governor 2>/dev/null | sort | uniq -c | tr -s ' ' | sed 's/^ //' | paste -sd', ')"
echo "scaling range:  $(khz_to_ghz "$(read_or_na "$CPUDIR/cpu0/cpufreq/scaling_min_freq")") .. $(khz_to_ghz "$(read_or_na "$CPUDIR/cpu0/cpufreq/scaling_max_freq")")"
echo "hardware range: $(khz_to_ghz "$(read_or_na "$CPUDIR/cpu0/cpufreq/cpuinfo_min_freq")") .. $(khz_to_ghz "$(read_or_na "$CPUDIR/cpu0/cpufreq/cpuinfo_max_freq")")"
if [[ -d "$CPUDIR/intel_pstate" ]]; then
    no_turbo=$(read_or_na "$CPUDIR/intel_pstate/no_turbo")
    echo "turbo:          $([[ "$no_turbo" == 0 ]] && echo enabled || echo "DISABLED (no_turbo=$no_turbo)")"
    echo "perf pct:       min $(read_or_na "$CPUDIR/intel_pstate/min_perf_pct")%, max $(read_or_na "$CPUDIR/intel_pstate/max_perf_pct")%"
fi

echo
echo "=== measured clock, $CORES core(s) busy for ${SECS}s ==="
echo "(idle readings are stale and meaningless; this is the number that counts)"

SPIN_PIDS=()
cleanup() {
    for p in ${SPIN_PIDS+"${SPIN_PIDS[@]}"}; do kill "$p" 2>/dev/null || true; done
    wait 2>/dev/null || true
}
trap cleanup EXIT

for (( c = 0; c < CORES; c++ )); do
    taskset -c "$c" bash -c 'while :; do :; done' &
    SPIN_PIDS+=("$!")
done

SAMPLES=$(mktemp)
trap 'cleanup; rm -f "$SAMPLES"' EXIT
sleep 0.3   # let the P-state request land before the first sample
end=$(( $(date +%s) + SECS ))
while [[ "$(date +%s)" -lt "$end" ]]; do
    for (( c = 0; c < CORES; c++ )); do
        read_or_na "$CPUDIR/cpu$c/cpufreq/scaling_cur_freq"
    done
    sleep 0.05
done >> "$SAMPLES"

cleanup
trap 'rm -f "$SAMPLES"' EXIT

sort -n "$SAMPLES" | awk -v hw="$(read_or_na "$CPUDIR/cpu0/cpufreq/cpuinfo_max_freq")" '
    { v[NR] = $1 }
    END {
        if (NR == 0) { print "no samples collected"; exit 1 }
        med = (NR % 2) ? v[(NR+1)/2] : (v[NR/2] + v[NR/2+1]) / 2
        spread = (v[NR] - v[1]) / med * 100
        printf "samples:  %d\n", NR
        printf "min:      %.2f GHz\n", v[1]/1e6
        printf "median:   %.2f GHz\n", med/1e6
        printf "max:      %.2f GHz\n", v[NR]/1e6
        printf "spread:   %.1f%% of median%s\n", spread, (spread < 2 ? "  (flat -- good for timing experiments)" : "  (VARIABLE -- timings will drift)")
        if (hw ~ /^[0-9]+$/ && med < hw * 0.97)
            printf "\nnote: sustained %.2f GHz is below the advertised %.2f GHz ceiling.\n      Expected when more than one or two cores are active: the advertised max is\n      the 1-2 core turbo bin, this is the all-core bin. Use the measured number.\n", med/1e6, hw/1e6
    }'

echo
govs=$(cat "$CPUDIR"/cpu*/cpufreq/scaling_governor 2>/dev/null | sort -u)
if [[ "$govs" == "performance" ]]; then
    echo "verdict: governor is performance on all $ncpu CPUs. Host is ready."
else
    echo "verdict: governor is '$(echo "$govs" | paste -sd'/')', not performance on every CPU."
    echo "         Run: scripts/cpu_check.sh --fix"
fi
