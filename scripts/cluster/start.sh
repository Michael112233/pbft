#!/usr/bin/env bash
# Start a run on a cluster, one node per machine (-m remote). The multi-host
# counterpart of alt_run_project.sh; run it from the repo on the control machine.
#
#   scripts/cluster/start.sh <hosts.json> <config.json>
#
# 0. refuses to start (exit 3) unless every host is on the performance governor
# 1. stops leftovers and recreates $HOME/<run_dir> on every host
# 2. builds pbft_main and generates the keys once, here (keys are random, so every
#    host must get the same copy)
# 3. pushes the bundle: pbft_main, keys/, the config at the same relative path, the
#    hosts file, the agent, and the scripts the nodes and stop.sh run
# 4. starts each node host's learning agent, waits for it, starts the node, and waits
#    until every node port accepts a connection (instead of alt_run_project.sh's sleep 5),
#    then until every node has logged PEERS READY (streams open to all its peers)
# 5. starts the client(s); with netem_delay, delays every node's egress 1 s later
# 6. records every host's clock sync state in logs/clock_offsets.txt and its TCP
#    settings in logs/tcp_state.txt
#
# Stop with scripts/cluster/stop.sh, which collects the logs. NODE_GOMAXPROCS and
# CLIENT_GOMAXPROCS are passed through as in alt_run_project.sh. See docs/multi-host.md.
set -euo pipefail

if (( $# != 2 )); then
    echo "Usage: $0 <hosts.json> <config.json>" >&2
    exit 2
fi
HOSTS_FILE="$(realpath "$1")"
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
cd "$REPO_DIR"

# The config must sit in the repo: hosts get it at the same relative path, so the
# agent's default-path logic and the analysis scripts see the same name.
CONFIG_PATH="$(realpath --relative-to="$REPO_DIR" "$2")"
if [[ ! -f "$CONFIG_PATH" || "$CONFIG_PATH" == ../* ]]; then
    echo "Error: config '$2' must be a file inside $REPO_DIR." >&2
    exit 1
fi

NODE_ENV_PREFIX=""
CLIENT_ENV_PREFIX=""
[[ -n "${NODE_GOMAXPROCS:-}" ]] && NODE_ENV_PREFIX="GOMAXPROCS=$NODE_GOMAXPROCS "
[[ -n "${CLIENT_GOMAXPROCS:-}" ]] && CLIENT_ENV_PREFIX="GOMAXPROCS=$CLIENT_GOMAXPROCS "

NODE_COUNT="$(cfg_get "$CONFIG_PATH" 'c["node_num"]')"
CLIENT_COUNT="$(cfg_get "$CONFIG_PATH" 'c.get("client_num", 1) or 1')"
NETEM_DELAY="$(cfg_get "$CONFIG_PATH" '1 if c.get("netem_delay", False) is True else 0')"
SCENARIO_MODE="$(cfg_get "$CONFIG_PATH" '1 if c.get("scenario_mode", False) is True else 0')"
FAR_NODE_NETEM="$(cfg_get "$CONFIG_PATH" '1 if c.get("far_node_netem", False) is True else 0')"
# Same value as start_netem_schedule in alt_run_project.sh.
NETEM_DELAY_MS=170

if [[ "${NODE_IDS[*]}" != "$(seq -s ' ' 1 "$NODE_COUNT")" ]]; then
    echo "Error: $HOSTS_FILE lists nodes ${NODE_IDS[*]}; node_num in $CONFIG_PATH is $NODE_COUNT." >&2
    exit 1
fi
for id in $(seq 1 "$CLIENT_COUNT"); do
    if [[ -z "${CLIENT_SSH[$id]:-}" ]]; then
        echo "Error: client_num is $CLIENT_COUNT but $HOSTS_FILE has no client $id." >&2
        exit 1
    fi
done
if [[ "$NETEM_DELAY" == 1 && "$SCENARIO_MODE" == 1 ]]; then
    echo "Error: netem_delay and scenario_mode cannot both be true in $CONFIG_PATH." >&2
    exit 1
fi
if [[ "$FAR_NODE_NETEM" == 1 ]]; then
    echo "Error: far_node_netem is lo-only (alt_run_project.sh); not supported on a cluster yet." >&2
    exit 1
fi

# Clients that run in this config; hosts listed beyond client_num are left idle.
RUN_CLIENT_IDS=($(seq 1 "$CLIENT_COUNT"))

host_active() { # kind id
    [[ "$1" == node ]] || (( $2 <= CLIENT_COUNT ))
}

stop_leftovers() { # label host kind id
    local node_only=""
    if [[ "$3" == node ]]; then
        node_only="pkill -f 'learningagent[.](launcher|server)' || true
if [[ -x scripts/netem_egress.sh ]]; then sudo -n ./scripts/netem_egress.sh down $(peer_ips "$4") || true; fi"
    fi
    # The control machine may be a client host, so kill by exact process name only,
    # and never wipe a directory that is a git checkout (run_dir pointing at the repo).
    rsh "$2" "if [[ -e \"\$HOME/$RUN_DIR/.git\" ]]; then echo \"refusing to wipe \$HOME/$RUN_DIR: it is a git checkout\" >&2; exit 1; fi
tmux kill-session -t $SESSION 2>/dev/null || true
pkill -x pbft_main || true
$node_only
cd \"\$HOME\"
rm -rf \"\$HOME/$RUN_DIR\"
mkdir -p \"\$HOME/$RUN_DIR/logs\""
}

push_bundle() { # label host kind id
    host_active "$3" "$4" || return 0
    rsync_ssh -aR --exclude __pycache__ \
        pbft_main keys "$CONFIG_PATH" learningagent requirements.txt \
        scripts/netem_egress.sh print_all_latency_summaries.sh export_client_report.sh \
        "$2:$RUN_DIR/"
    rsync_ssh -a "$HOSTS_FILE" "$2:$RUN_DIR/$REMOTE_HOSTS_FILE"
}

record_clock() { # label host kind id
    host_active "$3" "$4" || return 0
    rsh "$2" "{ echo \"$1 \$(hostname) \$(date --iso-8601=ns)\"
timedatectl show -p NTPSynchronized 2>/dev/null || true
chronyc tracking 2>/dev/null || ntpq -pn 2>/dev/null || echo 'no chronyc or ntpq'
} > logs/clock_offsets.txt"
}

# The TCP settings the run used (setup.sh --tune-tcp-lan / --tune-tcp-wan or neither),
# so a results directory says which mode it ran under.
record_tcp() { # label host kind id
    host_active "$3" "$4" || return 0
    local probe="${NODE_IP[1]}"
    [[ "$3" == node && "$4" == 1 ]] && probe="${NODE_IP[2]}"
    rsh "$2" "dev=\$(ip -o route get $probe | awk '{for (i = 1; i < NF; i++) if (\$i == \"dev\" && d == \"\") d = \$(i + 1)} END {print d}')
{ echo \"$1 \$(hostname) dev \$dev mtu \$(cat /sys/class/net/\$dev/mtu)\"
echo \"route: \$(ip route show dev \"\$dev\" proto kernel scope link | sed -n 1p)\"
sysctl net.ipv4.tcp_congestion_control net.ipv4.tcp_slow_start_after_idle net.ipv4.tcp_rmem net.ipv4.tcp_wmem \
    net.core.rmem_max net.core.wmem_max net.core.default_qdisc
tc qdisc show dev \"\$dev\" | sed -n 1p
} > logs/tcp_state.txt"
}

# TCP and NIC counters before anything runs; stop.sh takes the same snapshot at the end.
record_counters_start() { # label host kind id
    host_active "$3" "$4" || return 0
    tcp_counters "$2" "$(probe_ip "$3" "$4")" tcp_counters_start.txt
}

# Runs only happen under the performance governor (set by setup.sh, lost on reboot).
# Checked before anything is touched; exit 3 tells run_experiments.sh to abort the batch.
check_governor() { # label host kind id
    host_active "$3" "$4" || return 0
    rsh "$2" "g=\$(cat /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor 2>/dev/null | sort -u | paste -sd,)
if [[ \"\$g\" != performance ]]; then
    echo \"FAIL: governor is '\${g:-none}', not performance; rerun scripts/cluster/setup.sh\" >&2
    exit 1
fi"
}

echo "== Checking the CPU governor on every host"
if ! each_host check_governor; then
    echo "Error: not every host is on the performance governor; nothing was started." >&2
    exit 3
fi

echo "== Stopping leftovers and recreating ~/$RUN_DIR on every host"
each_host stop_leftovers

echo "== Building keys and pbft_main"
rm -f keys/*.pem
go build -o crypto_main setup_crypto/crypto_main.go
./crypto_main --config "$CONFIG_PATH"
rm -f pbft_main
go build -o pbft_main main.go

echo "== Pushing the bundle"
each_host push_bundle

echo "== Recording TCP and NIC counters"
each_host record_counters_start || echo "Warning: TCP counters could not be recorded on some hosts." >&2

echo "== Starting learning agents"
for id in "${NODE_IDS[@]}"; do
    tmux_start "${NODE_SSH[$id]}" learning-agents \
        "python3 -m learningagent.launcher --node-count $NODE_COUNT --node-ids $id --mode remote --hosts $REMOTE_HOSTS_FILE --log-dir logs --config $CONFIG_PATH" &
done
wait
for id in "${NODE_IDS[@]}"; do
    wait_port "${NODE_IP[$id]}" "$AGENT_PORT" 60 "learning agent $id"
done

echo "== Starting nodes"
for id in "${NODE_IDS[@]}"; do
    tmux_start "${NODE_SSH[$id]}" "node$id" \
        "${NODE_ENV_PREFIX}./pbft_main --config $CONFIG_PATH -r node -m remote --hosts $REMOTE_HOSTS_FILE -n $id" &
done
wait
for id in "${NODE_IDS[@]}"; do
    wait_port "${NODE_IP[$id]}" "$(node_port "$id")" 60 "node $id"
done
echo "All $NODE_COUNT nodes are listening."

# Every node dials all its peers at startup (node/peerdial.go) and logs PEERS READY
# once each stream is open. Wait for that on every node, so the client's first
# requests don't race connection setup (slow under a 170 ms delay scenario).
echo "== Waiting for every node to connect to its $((NODE_COUNT - 1)) peers"
deadline=$((SECONDS + 60))
pending=("${NODE_IDS[@]}")
while (( ${#pending[@]} > 0 )); do
    still=()
    for id in "${pending[@]}"; do
        rsh "${NODE_SSH[$id]}" "grep -q 'PEERS READY' logs/node_$id.log" 2>/dev/null || still+=("$id")
    done
    pending=("${still[@]+"${still[@]}"}")
    (( ${#pending[@]} == 0 )) && break
    if (( SECONDS >= deadline )); then
        for id in "${pending[@]}"; do
            n="$(rsh "${NODE_SSH[$id]}" "grep -c 'PEER READY' logs/node_$id.log || true" 2>/dev/null)"
            echo "Error: node $id connected to ${n:-0} of $((NODE_COUNT - 1)) peers after 60s; see 'PEER READY' in its logs/node_$id.log." >&2
        done
        exit 1
    fi
    sleep 0.5
done
echo "Every node is connected to every peer."

echo "== Starting $CLIENT_COUNT client(s)"
for id in "${RUN_CLIENT_IDS[@]}"; do
    if (( CLIENT_COUNT == 1 )); then
        tmux_start "${CLIENT_SSH[$id]}" client \
            "${CLIENT_ENV_PREFIX}./pbft_main --config $CONFIG_PATH -r client -m remote --hosts $REMOTE_HOSTS_FILE"
    else
        tmux_start "${CLIENT_SSH[$id]}" "client$id" \
            "${CLIENT_ENV_PREFIX}./pbft_main --config $CONFIG_PATH -r client -m remote --hosts $REMOTE_HOSTS_FILE -n $id"
    fi
done

if [[ "$NETEM_DELAY" == 1 ]]; then
    sleep 1
    echo "== Delaying every node's egress to its peers by ${NETEM_DELAY_MS}ms"
    for id in "${NODE_IDS[@]}"; do
        rsh "${NODE_SSH[$id]}" "sudo -n ./scripts/netem_egress.sh up $NETEM_DELAY_MS $(peer_ips "$id")" &
    done
    wait
fi

each_host record_clock
each_host record_tcp

mkdir -p "$STATE_DIR"
{
    echo "CONFIG_PATH=$CONFIG_PATH"
    echo "HOSTS_FILE=$HOSTS_FILE"
    echo "CLIENT_COUNT=$CLIENT_COUNT"
    echo "STARTED=$(date --iso-8601=seconds)"
} > "$STATE_DIR/running"
echo "Run started: $CONFIG_PATH on $NODE_COUNT nodes. Stop with: scripts/cluster/stop.sh $1"
