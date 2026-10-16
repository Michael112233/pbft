# Shared helpers for scripts/cluster/*.sh. Source it after setting HOSTS_FILE:
#
#   HOSTS_FILE=scripts/cluster/hosts.cloudlab.json; source scripts/cluster/common.sh
#
# The hosts file is the one pbft_main -m remote --hosts reads (config.LoadHosts):
#   {"run_dir": "pbft-run",
#    "nodes":   {"1": {"ip": "10.10.1.1", "ssh": "node1"}, ...},
#    "clients": {"1": {"ip": "10.10.1.8", "ssh": "client"}}}
# ip is the experiment-network address the process binds; ssh is the name this script
# logs in with. run_dir is relative to $HOME on every host and holds only the run bundle
# (binary, keys, config, scripts, agent) plus logs/; the scripts recreate it each run.

REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SESSION="pbft"
# Local state shared by start.sh and stop.sh (which config and hosts file is running).
STATE_DIR="$REPO_DIR/.cluster"
# Name of the hosts file inside run_dir, passed to pbft_main and the agent as --hosts.
REMOTE_HOSTS_FILE="cluster_hosts.json"
AGENT_PORT=29000
NODE_BASE_PORT=28000
NODE_PORT_STEP=100

SSH_OPTS=(-o BatchMode=yes -o StrictHostKeyChecking=accept-new -o ConnectTimeout=10 -o ServerAliveInterval=30)

if [[ -z "${HOSTS_FILE:-}" || ! -f "$HOSTS_FILE" ]]; then
    echo "Error: hosts file '${HOSTS_FILE:-}' not found." >&2
    exit 1
fi

declare -A NODE_IP=() NODE_SSH=() CLIENT_IP=() CLIENT_SSH=()
eval "$(python3 - "$HOSTS_FILE" <<'EOF'
import json, shlex, sys
h = json.load(open(sys.argv[1]))
q = shlex.quote
print(f"RUN_DIR={q(h.get('run_dir') or 'pbft-run')}")
for kind, key in (("NODE", "nodes"), ("CLIENT", "clients")):
    ids = sorted(int(i) for i in h.get(key, {}))
    print(f"{kind}_IDS=({' '.join(map(str, ids))})")
    for i in ids:
        e = h[key][str(i)]
        print(f"{kind}_IP[{i}]={q(e['ip'])}")
        print(f"{kind}_SSH[{i}]={q(e.get('ssh') or e['ip'])}")
EOF
)"

# rsh <ssh name> <bash commands>: run the commands with bash on that host, from
# $HOME/$RUN_DIR when it exists. Feeding bash on stdin keeps this independent of
# the remote login shell (CloudLab accounts may default to tcsh). The commands are
# expanded locally first: write \$var for a variable of the remote shell.
rsh() {
    local host="$1"
    shift
    printf '%s\n' 'set -euo pipefail' "cd \"\$HOME/$RUN_DIR\" 2>/dev/null || cd \"\$HOME\"" "$*" |
        ssh "${SSH_OPTS[@]}" "$host" bash -s
}

# tmux_start <ssh name> <window> <command>: run command in a window of the host's
# pbft tmux session (created on first use), from run_dir. The window stays open
# with the exit status when the command ends, as in alt_run_project.sh.
tmux_start() {
    local host="$1" window="$2" cmd="$3" q
    q="$(printf '%q' "$cmd; status=\$?; echo; echo \"$window exited with status \$status\"; exec bash")"
    rsh "$host" "if tmux has-session -t $SESSION 2>/dev/null; then tmux new-window -t $SESSION -n $window -c \"\$HOME/$RUN_DIR\" $q; else tmux new-session -d -s $SESSION -n $window -c \"\$HOME/$RUN_DIR\" $q; fi"
}

# run_dir is wiped with rm -rf on every start, so it must be a plain relative path.
if [[ -z "$RUN_DIR" || "$RUN_DIR" == /* || "$RUN_DIR" == "." || "$RUN_DIR" == *..* ]]; then
    echo "Error: run_dir '$RUN_DIR' in $HOSTS_FILE must be a relative path below \$HOME." >&2
    exit 1
fi

# rsync_ssh <rsync args...>: rsync with the same ssh options.
rsync_ssh() {
    rsync -e "ssh ${SSH_OPTS[*]}" "$@"
}

# peer_ips <node id>: every other node's IP, for netem_egress.sh.
peer_ips() {
    local self="$1" id out=()
    for id in "${NODE_IDS[@]}"; do
        [[ "$id" == "$self" ]] || out+=("${NODE_IP[$id]}")
    done
    echo "${out[*]}"
}

node_port() {
    echo $((NODE_BASE_PORT + $1 * NODE_PORT_STEP))
}

# probe_ip <kind> <id>: a node IP other than the host's own, so `ip route get` on that
# host names its experiment device rather than lo.
probe_ip() {
    if [[ "$1" == node && "$2" == 1 ]]; then echo "${NODE_IP[2]}"; else echo "${NODE_IP[1]}"; fi
}

# tcp_counters <ssh name> <probe ip> <file>: save the host's TCP and NIC counters to
# logs/<file>. start.sh and stop.sh take one each around a run, and
# scripts/tcp_counters_summary.py diffs them. The counters are host-wide, so they also
# count the harness's own ssh traffic, which is small.
tcp_counters() {
    rsh "$1" "dev=\$(ip -o route get $2 | awk '{for (i = 1; i < NF; i++) if (\$i == \"dev\" && d == \"\") d = \$(i + 1)} END {print d}')
{ echo \"== link dev=\$dev speed_mbps=\$(cat /sys/class/net/\$dev/speed 2>/dev/null || echo unknown) taken=\$(date +%s.%N)\"
echo '== nstat'
nstat -asz
echo '== ip -s link'
ip -s link show dev \"\$dev\"
} > logs/$3"
}

# wait_port <ip> <port> <deadline seconds> <label>: wait until a TCP connect succeeds.
wait_port() {
    local ip="$1" port="$2" limit="$3" label="$4"
    local deadline=$((SECONDS + limit))
    until timeout 1 bash -c "exec 3<>/dev/tcp/$ip/$port" 2>/dev/null; do
        if (( SECONDS >= deadline )); then
            echo "Error: $label ($ip:$port) not reachable after ${limit}s." >&2
            return 1
        fi
        sleep 0.5
    done
}

# each_host <fn>: run fn "<label>" "<ssh name>" "<kind>" "<id>" for every node and
# client host in parallel; fails if any call failed. Output lines are prefixed.
each_host() {
    local fn="$1" id pids=() labels=() failed=0 i
    # A subshell per host, so wait sees fn's status through the pipe, not sed's.
    for id in "${NODE_IDS[@]}"; do
        (set -o pipefail; "$fn" "node$id" "${NODE_SSH[$id]}" node "$id" 2>&1 | sed "s/^/[node$id] /") &
        pids+=($!)
        labels+=("node$id")
    done
    for id in "${CLIENT_IDS[@]}"; do
        (set -o pipefail; "$fn" "client$id" "${CLIENT_SSH[$id]}" client "$id" 2>&1 | sed "s/^/[client$id] /") &
        pids+=($!)
        labels+=("client$id")
    done
    for i in "${!pids[@]}"; do
        if ! wait "${pids[$i]}"; then
            echo "Error: ${labels[$i]} failed." >&2
            failed=1
        fi
    done
    return "$failed"
}

# cfg_get <config> <python expression on c>: read a value from the experiment config.
cfg_get() {
    python3 -c 'import json, sys; c = json.load(open(sys.argv[1])); print(eval(sys.argv[2]))' "$1" "$2"
}
