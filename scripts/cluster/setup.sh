#!/usr/bin/env bash
# One-time setup of a fresh cluster for scripts/cluster/start.sh. Run it from the repo on
# the control machine after the experiment is up (cloudlab/profile.py), and again only
# after the machines are reimaged or rebooted (neither the governor nor the TCP tuning
# survives a reboot).
#
#   scripts/cluster/setup.sh <hosts.json> [--tune-tcp-lan | --tune-tcp-wan]
#
# On every host, in parallel:
#   - ssh works, sudo -n works, the hosts-file IP is on this machine, NTP is synced
#   - tmux, rsync and pip are installed (apt-get if missing)
#   - every host: the netem/prio/flower kernel modules netem_egress.sh needs (nodes for
#     NetworkDelay, every host for an emulated WAN)
#   - node hosts: the agent's Python packages (requirements.txt), and RTT to every peer (ping);
#     client hosts: RTT to every node
#   - the performance governor on every CPU, required (cpu_check.sh --fix; the previous one is saved
#     to /tmp/prev_governor), then the CPU baseline (scripts/cpu_check.sh, see the
#     cpu-baseline skill)
#   - TCP, at most one of (docs/multi-host.md, "TCP tuning"):
#     --tune-tcp-lan  make the LAN behave like the single host's lo, to reproduce lo runs:
#                     initcwnd/initrwnd on the experiment route sized so the first flight is
#                     lo's 10 x 65483 B (~640 KiB; 453 segments at MTU 1500), and every other
#                     TCP setting at the single host's values (cubic, slow-start restart on,
#                     default buffers, fq_codel)
#     --tune-tcp-wan  for long fat links: no slow-start restart after idle, initcwnd/initrwnd
#                     1500, 128 MB buffers, BBR with fq
#     The original values are saved once to ~/sysctl_backup.txt on each host.
#
# Every host's output is saved to .cluster/setup/<label>.txt. Record the CPU results in
# notes/cpu_note.txt.
set -euo pipefail

if (( $# < 1 || $# > 2 )); then
    echo "Usage: $0 <hosts.json> [--tune-tcp-lan | --tune-tcp-wan]" >&2
    exit 2
fi
HOSTS_FILE="$(realpath "$1")"
TUNE_TCP=""
if (( $# == 2 )); then
    case "$2" in
        --tune-tcp-lan) TUNE_TCP=lan ;;
        --tune-tcp-wan) TUNE_TCP=wan ;;
        --tune-tcp) echo "--tune-tcp is now --tune-tcp-wan; for reproducing lo runs use --tune-tcp-lan" >&2; exit 2 ;;
        *) echo "unknown option $2" >&2; exit 2 ;;
    esac
fi

# --tune-tcp-lan targets: stock Ubuntu 22.04 / kernel 5.15 values, read on the development
# host (clnode113) on 2026-10-15. If the single-host runs used another machine, check that
# its sysctls match. LO_ADVMSS is lo's MSS (ss advmss, MTU 65536); the LAN's initcwnd is
# chosen so 10 lo segments fit in the first flight.
LO_ADVMSS=65483
LO_INITCWND=10
SINGLE_HOST_SYSCTLS="net.ipv4.tcp_congestion_control=cubic net.ipv4.tcp_slow_start_after_idle=1 \
net.core.rmem_max=212992 net.core.wmem_max=212992 \
net.ipv4.tcp_rmem='4096 131072 6291456' net.ipv4.tcp_wmem='4096 16384 4194304' \
net.core.default_qdisc=fq_codel"
source "$(dirname "${BASH_SOURCE[0]}")/common.sh"
cd "$REPO_DIR"
mkdir -p "$STATE_DIR/setup"

setup_host() { # label host kind id
    local label="$1" host="$2" kind="$3" id="$4" ip probe peers=""
    if [[ "$kind" == node ]]; then
        ip="${NODE_IP[$id]}"
        peers="$(peer_ips "$id")"
        probe="${peers%% *}"
    else
        ip="${CLIENT_IP[$id]}"
        probe="${NODE_IP[1]}"
    fi
    # Setup files go in ~/pbft-setup, outside run_dir, which start.sh wipes.
    rsync_ssh -a requirements.txt scripts/cpu_check.sh "$host:pbft-setup/"

    rsh "$host" "
cd \"\$HOME/pbft-setup\"
echo \"== $label \$(hostname) kernel \$(uname -r)\"
sudo -n true || { echo 'FAIL: sudo -n needs a password; netem, the governor and the TCP tuning need passwordless sudo'; exit 1; }
if ! ip -o -4 addr show | awk '{print \$4}' | cut -d/ -f1 | grep -qx '$ip'; then
    echo 'FAIL: $ip from the hosts file is not an address of this machine'; exit 1
fi
echo \"ip $ip is local; NTPSynchronized=\$(timedatectl show -p NTPSynchronized --value 2>/dev/null || echo unknown)\"
missing=''
for c in tmux rsync pip3; do command -v \$c >/dev/null || missing=\"\$missing \$c\"; done
if [[ -n \"\$missing\" ]]; then
    echo \"installing:\$missing\"
    sudo -n apt-get update -qq
    sudo -n DEBIAN_FRONTEND=noninteractive apt-get install -y -qq tmux rsync python3-pip >/dev/null
fi
# netem_egress.sh runs on every host: nodes for NetworkDelay, every host for an emulated WAN.
load_modules() { sudo -n modprobe sch_prio && sudo -n modprobe sch_netem && sudo -n modprobe cls_flower; }
if ! load_modules 2>/dev/null; then
    echo 'netem modules missing; installing linux-modules-extra'
    sudo -n DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \"linux-modules-extra-\$(uname -r)\" >/dev/null
    load_modules
fi
echo 'netem, prio and flower modules load'
"

    if [[ "$kind" == node ]]; then
        rsh "$host" "
cd \"\$HOME/pbft-setup\"
if python3 -c 'import grpc, google.protobuf, numpy, sklearn' 2>/dev/null; then
    echo 'agent Python packages present'
else
    echo 'installing the agent Python packages'
    python3 -m pip install --user -q -r requirements.txt
    python3 -c 'import grpc, google.protobuf, numpy, sklearn'
fi
for p in $peers; do
    echo \"rtt to \$p: \$(ping -c 20 -i 0.2 -q \$p | tail -1)\"
done
"
    else
        # Client to every node: the client's request latency includes this round trip.
        local nodes="" n
        for n in "${NODE_IDS[@]}"; do nodes="$nodes ${NODE_IP[$n]}"; done
        rsh "$host" "
for p in $nodes; do
    echo \"rtt to \$p: \$(ping -c 20 -i 0.2 -q \$p | tail -1)\"
done
"
    fi

    # Performance governor first, so the check below measures the clock the run gets.
    # Required: start.sh refuses to run on a host without it.
    rsh "$host" "bash \"\$HOME/pbft-setup/cpu_check.sh\" --fix || { echo 'FAIL: could not set the performance governor; runs need it on every host'; exit 1; }"
    rsh "$host" "bash \"\$HOME/pbft-setup/cpu_check.sh\""

    if [[ -n "$TUNE_TCP" ]]; then
        # Shared: back up the originals once, find the experiment device and its route.
        local find_route="
[[ -f \"\$HOME/sysctl_backup.txt\" ]] || sysctl net.core.rmem_max net.core.wmem_max net.ipv4.tcp_rmem net.ipv4.tcp_wmem \
    net.ipv4.tcp_congestion_control net.ipv4.tcp_slow_start_after_idle net.core.default_qdisc > \"\$HOME/sysctl_backup.txt\"
dev=\$(ip -o route get $probe | awk '{for (i = 1; i < NF; i++) if (\$i == \"dev\" && d == \"\") d = \$(i + 1)} END {print d}')
route=\$(ip route show dev \"\$dev\" proto kernel scope link | sed -n 1p | sed 's/ initcwnd.*//')"
        local report="
echo \"tcp tuned ($TUNE_TCP) on \$dev: \$(ip route show dev \"\$dev\" proto kernel scope link | sed -n 1p)\"
sysctl net.ipv4.tcp_congestion_control net.ipv4.tcp_slow_start_after_idle net.ipv4.tcp_rmem net.ipv4.tcp_wmem net.core.rmem_max
tc qdisc show dev \"\$dev\" | sed -n 1p"
        if [[ "$TUNE_TCP" == lan ]]; then
            # MSS = MTU - 40 (IP + TCP headers) - 12 (timestamp option), as lo's 65536 -> 65483.
            # Every other setting is put back to the single host's, which also undoes an
            # earlier --tune-tcp-wan; deleting the root qdisc lets the kernel reattach the
            # default (fq_codel) in place of a leftover fq.
            rsh "$host" "$find_route
mtu=\$(cat /sys/class/net/\"\$dev\"/mtu)
mss=\$((mtu - 52))
segs=\$(( ($LO_INITCWND * $LO_ADVMSS + mss - 1) / mss ))
echo \"MTU \$mtu, MSS \$mss: initcwnd/initrwnd \$segs = \$((segs * mss)) B, lo's first flight is $((LO_INITCWND * LO_ADVMSS)) B\"
sudo -n ip route change \$route dev \"\$dev\" initcwnd \$segs initrwnd \$segs
sudo -n sysctl -q -w $SINGLE_HOST_SYSCTLS
if tc qdisc show dev \"\$dev\" | awk 'NR == 1 && \$2 == \"fq\" { f = 1 } END { exit !f }'; then sudo -n tc qdisc del dev \"\$dev\" root; fi
$report"
        else
            rsh "$host" "$find_route
sudo -n ip route change \$route dev \"\$dev\" initcwnd 1500 initrwnd 1500
sudo -n sysctl -q -w net.ipv4.tcp_slow_start_after_idle=0 \
    net.core.rmem_max=134217728 net.core.wmem_max=134217728 \
    net.ipv4.tcp_rmem='4096 4194304 134217728' net.ipv4.tcp_wmem='4096 65536 134217728'
sudo -n modprobe tcp_bbr
sudo -n sysctl -q -w net.core.default_qdisc=fq net.ipv4.tcp_congestion_control=bbr
sudo -n tc qdisc replace dev \"\$dev\" root fq
$report"
        fi
    fi
}

save_setup() { # label host kind id
    setup_host "$@" > >(tee "$STATE_DIR/setup/$1.txt") 2>&1
}

if each_host save_setup; then
    echo "Setup OK on every host. Outputs: $STATE_DIR/setup/. Record the CPU results in notes/cpu_note.txt."
else
    echo "Setup failed on some host; see the FAIL lines above and $STATE_DIR/setup/." >&2
    exit 1
fi
