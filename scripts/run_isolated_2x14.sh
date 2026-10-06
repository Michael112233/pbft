#!/usr/bin/env bash
# Run one of up to two long experiments side by side on this machine, isolated from each other.
#
#   scripts/run_isolated_2x14.sh <A|C> start [duration] [config]   default 7h20m, config/run2new.json
#   scripts/run_isolated_2x14.sh <A|C> status                      processes, pinning, tmux, log tail
#   scripts/run_isolated_2x14.sh <A|C> attach                      attach to that run's tmux session
#   scripts/run_isolated_2x14.sh <A|C> down                        delete the run's network namespace
#   DRY_RUN=1 scripts/run_isolated_2x14.sh A start                     print what would happen, change nothing
#
# Same as scripts/run_isolated.sh, for 2 x 14-core hosts (e.g. 2x Xeon E5-2683 v3, 56 logical),
# but one run per socket: on this 2.49 GHz host, 7 cores could not carry inject_speed 200 at
# n = 7 (ungated leaders ~104 slots/s, dead-on-arrival views); a full socket gives ~177.
#
# Run it from the main checkout. Each run R gets:
#   - a git worktree  ../<repo>-R          own logs/, keys/, pbft_main, results/, config copy
#   - a netns         pbftR                own lo: own 127.0.0.0/8 ports and own netem qdisc, so
#                                          scenario_mode's `tc` on lo cannot touch another run
#   - a CPU set       14 physical cores    a whole socket, both hyperthreads of each core, so
#                                          runtime.NumCPU() and GOMAXPROCS are 28 in every process
#   - a tmux server   .tmux/ in worktree   via TMUX_TMPDIR; the session is still called "pbft"
#
# Layout (2 sockets x 14 cores, core k = cpus k and k+28, socket = k % 2):
#   A cores 0..26 even (socket 0)   C cores 1..27 odd (socket 1)
# The two runs share no L3 and no memory node.
#
# Needs passwordless sudo (ip netns, and node 4's `sudo -n tc`) and numactl.
# The worktree is a checkout of HEAD: uncommitted code changes are NOT included. Only the
# config is copied in on every start. To move an existing worktree to another commit:
#   git -C ../<repo>-R checkout --detach <rev>
set -euo pipefail

DRY_RUN="${DRY_RUN:-0}"
SESSION="pbft"

usage() { sed -n '2,/^set -euo/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//' >&2; exit 2; }
die() { echo "Error: $*" >&2; exit 1; }
run() { if [ "$DRY_RUN" = 1 ]; then echo "+ $*"; else "$@"; fi; }

RUN="${1:-}"; CMD="${2:-start}"
case "$RUN" in A|C) ;; *) usage ;; esac

MAIN_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
[ "$(git -C "$MAIN_DIR" rev-parse --git-dir)" = "$(git -C "$MAIN_DIR" rev-parse --git-common-dir)" ] \
    || [ "$(cd "$(git -C "$MAIN_DIR" rev-parse --git-dir)" && pwd)" = "$(cd "$(git -C "$MAIN_DIR" rev-parse --git-common-dir)" && pwd)" ] \
    || die "run this from the main checkout ($(git -C "$MAIN_DIR" worktree list | head -1 | cut -d' ' -f1)), not from a worktree"

RUN_DIR="$(dirname "$MAIN_DIR")/$(basename "$MAIN_DIR")-$RUN"
NS="pbft$RUN"
TMUX_DIR="$RUN_DIR/.tmux"

case "$RUN" in
    A) CORES="$(seq 0 2 26)" ;;
    C) CORES="$(seq 1 2 27)" ;;
esac
CPUS="$( { for c in $CORES; do echo "$c"; echo "$((c + 28))"; done; } | sort -n | paste -sd, -)"

expand_cpus() {  # "0-2,5" -> "0,1,2,5"
    local IFS=, p
    for p in $1; do
        if [[ $p == *-* ]]; then seq "${p%-*}" "${p#*-}"; else echo "$p"; fi
    done | sort -n | paste -sd, -
}

# Refuse to run on a machine that is not laid out as assumed: every hyperthread pair must sit
# inside this run's set and every cpu must be on one NUMA node. Sets NODE.
verify_topology() {
    local cpu sib node="" n
    for cpu in ${CPUS//,/ }; do
        sib="$(expand_cpus "$(cat /sys/devices/system/cpu/cpu$cpu/topology/thread_siblings_list)")"
        for s in ${sib//,/ }; do
            [[ ",$CPUS," == *",$s,"* ]] || die "cpu $cpu has sibling $s outside run $RUN's set ($CPUS); this machine's topology is not the assumed one"
        done
        n="$(ls -d /sys/devices/system/cpu/cpu$cpu/node* 2>/dev/null | head -1 | sed 's/.*node//')"
        [ -n "$n" ] || die "cannot read NUMA node of cpu $cpu"
        [ -z "$node" ] || [ "$node" = "$n" ] || die "run $RUN's cpus span NUMA nodes $node and $n"
        node="$n"
    done
    NODE="$node"
}

ns_exists() { [ -e "/var/run/netns/$NS" ]; }
ns_pids() { sudo -n ip netns pids "$NS" 2>/dev/null || true; }
session_alive() { TMUX_TMPDIR="$TMUX_DIR" tmux has-session -t "$SESSION" 2>/dev/null; }

cmd_start() {
    local duration="${3:-7h20m}" config="${4:-config/run2new.json}"

    command -v numactl >/dev/null || {
        [ "$DRY_RUN" = 1 ] && echo "(warning: numactl missing; sudo apt install numactl)" >&2 \
            || die "numactl is not installed: sudo apt install numactl"
    }
    command -v tmux >/dev/null || die "tmux is not installed"
    sudo -n true 2>/dev/null || die "passwordless sudo is required"

    [ -f "$config" ] || [ -f "$MAIN_DIR/$config" ] || die "config not found: $config"
    [ -f "$config" ] && config="$(realpath --relative-to="$MAIN_DIR" "$config" 2>/dev/null || echo "$config")"
    case "$config" in /*|../*) die "config must be inside the repo (relative path, e.g. config/foo.json)" ;; esac

    verify_topology

    # alt_run_project.sh wipes logs/ and keys/ on start, so never start over a live run.
    if session_alive || [ -n "$(ns_pids)" ]; then
        die "run $RUN is already running (tmux session or processes in $NS). Attach with: $0 $RUN attach"
    fi

    echo "Run $RUN: cpus $CPUS, memory node $NODE, netns $NS, dir $RUN_DIR"

    if [ ! -d "$RUN_DIR" ]; then
        run git -C "$MAIN_DIR" worktree add --detach "$RUN_DIR" HEAD
    else
        echo "Reusing worktree $RUN_DIR at $(git -C "$RUN_DIR" rev-parse --short HEAD)"
    fi
    if [ -n "$(git -C "$MAIN_DIR" status --porcelain --untracked-files=no)" ]; then
        echo "Note: the main checkout has uncommitted changes to tracked files; the worktree has none of them." >&2
    fi

    # The config is copied on every start (most experiment configs are gitignored, so a fresh
    # worktree would not have them, and edits to a tracked one would be missing).
    run mkdir -p "$RUN_DIR/$(dirname "$config")" "$TMUX_DIR"
    run cp "$MAIN_DIR/$config" "$RUN_DIR/$config"

    if ! ns_exists; then
        run sudo -n ip netns add "$NS"
    fi
    run sudo -n ip netns exec "$NS" ip link set lo up

    # netns first, then pinning, then back to the user: everything the run spawns (tmux server,
    # nodes, client, learners, node 4's sudo tc) inherits the namespace, cpu set and memory node.
    # RUN_ISOLATED makes alt_run_project.sh kill only this run's tmux server, not every pbft_main.
    echo "Launching: ./scripts/../run_long_experiment.sh $duration $config"
    run sudo -n ip netns exec "$NS" \
        numactl "--physcpubind=$CPUS" "--membind=$NODE" \
        sudo -n -u "$USER" env "PATH=$PATH" "HOME=$HOME" \
            "TMUX_TMPDIR=$TMUX_DIR" "RUN_ISOLATED=$RUN" \
            bash -c 'cd "$1" && exec ./run_long_experiment.sh "$2" "$3"' _ "$RUN_DIR" "$duration" "$config"

    cat <<EOF
Progress:   tail -f $RUN_DIR/long_run.out
Attach:     $0 $RUN attach
Status:     $0 $RUN status
Results:    $RUN_DIR/results/<config>_<timestamp>/
End early:  kill <pid printed above>   (still exports and saves)
EOF
}

cmd_status() {
    ns_exists || { echo "Run $RUN: no namespace $NS (not started)."; return; }
    verify_topology
    echo "Run $RUN: netns $NS, expected cpus $CPUS, memory node $NODE"
    if session_alive; then echo "tmux: session '$SESSION' alive"; else echo "tmux: no session"; fi
    local pids pid aff want bad=0 n=0
    want="$(expand_cpus "$CPUS")"
    pids="$(ns_pids)"
    for pid in $pids; do
        aff="$(taskset -cp "$pid" 2>/dev/null | sed 's/.*: //')" || continue
        n=$((n + 1))
        if [ "$(expand_cpus "$aff")" != "$want" ]; then
            bad=$((bad + 1)); echo "  NOT PINNED: pid $pid $(ps -o comm= -p "$pid") affinity $aff"
        fi
    done
    echo "processes in namespace: $n, wrongly pinned: $bad"
    [ -f "$RUN_DIR/long_run.out" ] && { echo "--- long_run.out (tail)"; tail -n 5 "$RUN_DIR/long_run.out"; }
    return 0
}

cmd_attach() {
    session_alive || die "run $RUN has no tmux session"
    TMUX_TMPDIR="$TMUX_DIR" exec tmux attach -t "$SESSION"
}

cmd_down() {
    ns_exists || { echo "No namespace $NS."; return; }
    [ -z "$(ns_pids)" ] || die "processes still running in $NS; stop the run first"
    run sudo -n ip netns del "$NS"
}

case "$CMD" in
    start) cmd_start "$@" ;;
    status) cmd_status ;;
    attach) cmd_attach ;;
    down) cmd_down ;;
    *) usage ;;
esac
