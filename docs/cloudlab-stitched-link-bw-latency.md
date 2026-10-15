# CloudLab stitched link: bandwidth and latency

Measurements of the experiment link between two CloudLab nodes joined by a stitched
(cross-site) link, taken on 2026-10-14. The link reaches near line rate (8–9 Gb/s on
one TCP flow), but only once both hosts' TCP buffers are raised. With default buffers a
flow is capped at ~0.5 Gb/s by the 48 ms RTT. One direction (node1 → node2) also
collapses under parallel cubic flows, which matters for PBFT's all-to-all traffic.

---

## 1. Setup

| | node1 | node2 |
|---|---|---|
| Hostname | `c220g2-010622.wisc.cloudlab.us` (Wisconsin) | other site |
| Experiment IP | `192.168.1.1` | `192.168.1.2` |
| Interface | `vlan3081@enp6s0f1` (VLAN 3081 on a 10 GbE NIC) | |
| `/etc/hosts` names | `node1`, `node1-link1` | `node2`, `node2-link1` |

The control interface (`enp1s0f0`, `128.105.x.x`) is a different network; every test
below goes over the `192.168.1.0/24` experiment link.

All tests ran from node1 as the iperf3 client. node2 only ran `iperf3 -s`. `-R` reverses
the direction, so node2 sends and node1 receives.

Tools: `ping`, `tracepath`, `ethtool`, `iperf3` 3.9.

---

## 2. Commands

### 2.1 Find the experiment link

```bash
hostname -f
ip -br addr                       # experiment IP is on the vlan interface
ip route
grep -v '^#' /etc/hosts           # CloudLab writes the peer names here
ip -d link show vlan3081          # VLAN id, MTU
ethtool enp6s0f1 | grep -E 'Speed|Duplex'   # parent NIC speed
```

### 2.2 Install iperf3 (both nodes)

```bash
sudo apt-get update
sudo DEBIAN_FRONTEND=noninteractive apt-get install -y iperf3
```

On node2, start the server and leave it running:

```bash
iperf3 -s
```

### 2.3 Latency and MTU (node1 only, node2 needs nothing)

```bash
sudo ping -c 300 -i 0.05 -q 192.168.1.2            # RTT distribution (sudo allows -i < 0.2)
sudo ping -c 100 -i 0.05 -s 1400 -q 192.168.1.2    # large packets
ping -c 2 -M do -s 1472 192.168.1.2                # 1472 + 28 header = 1500, DF set: should pass
ping -c 1 -M do -s 1473 192.168.1.2                # 1501: should fail if MTU is 1500
tracepath -n 192.168.1.2                           # hop count, path MTU
```

The first ping of a cold run is slower because of ARP. Ignore it, or use the 300-ping run.

### 2.4 Baseline TCP with default buffers

Record the defaults first, so they can be restored (§2.7):

```bash
sysctl net.core.rmem_max net.core.wmem_max net.ipv4.tcp_rmem net.ipv4.tcp_wmem \
       net.ipv4.tcp_congestion_control | tee ~/sysctl_backup.txt
```

Then run these from node1:

```bash
iperf3 -c 192.168.1.2 -t 10 -O 3          # 1 flow, node1 -> node2 (-O 3 omits slow start)
iperf3 -c 192.168.1.2 -t 10 -O 3 -R       # 1 flow, node2 -> node1
for P in 4 8 16; do iperf3 -c 192.168.1.2 -t 10 -O 3 -P $P | grep SUM | tail -2; done
iperf3 -c 192.168.1.2 -t 10 -O 3 -P 16 -R | grep SUM | tail -2
```

### 2.5 UDP, and where UDP loss happens

```bash
for B in 500M 1G 2G 4G; do iperf3 -c 192.168.1.2 -u -b $B -l 1400 -t 8 | grep receiver; done
for B in 1G 4G;         do iperf3 -c 192.168.1.2 -u -b $B -l 1400 -t 8 -R | grep receiver; done
```

To tell host drops from network drops, compare the receiver's UDP `RcvbufErrors`
counter before and after a run. This works for `-R`, where node1 is the receiver.
For the forward direction, run the same command on node2.

```bash
r(){ awk '/^Udp:/{getline; print "InErrors="$4, "RcvbufErrors="$6}' /proc/net/snmp; }
r; iperf3 -c 192.168.1.2 -u -b 4G -l 1400 -t 8 -R | grep receiver; r
```

If the `RcvbufErrors` delta equals the lost-datagram count, the network dropped
nothing.

### 2.6 TCP with large buffers (both nodes)

Run this on **both** nodes. It does not survive a reboot:

```bash
sudo sysctl -w net.core.rmem_max=134217728 net.core.wmem_max=134217728 \
  net.ipv4.tcp_rmem="4096 131072 134217728" net.ipv4.tcp_wmem="4096 65536 134217728"
```

Restart `iperf3 -s` on node2, then run from node1:

```bash
iperf3 -c 192.168.1.2 -t 20 -O 5                   # 1 flow fwd, cubic
iperf3 -c 192.168.1.2 -t 20 -O 5 -R                # 1 flow rev, cubic
sudo modprobe tcp_bbr; sysctl -n net.ipv4.tcp_available_congestion_control
iperf3 -c 192.168.1.2 -t 20 -O 5 -C bbr            # 1 flow fwd, bbr (sender side)
iperf3 -c 192.168.1.2 -t 15 -O 3 -P 4 | grep sender           # per-flow, cubic
iperf3 -c 192.168.1.2 -t 15 -O 3 -P 4 -C bbr | grep sender    # per-flow, bbr
```

`-C bbr` sets the congestion control on the sending socket. That is node1 in a forward
test; with `-R` it asks the server's side to use it.

### 2.7 Restore defaults (both nodes)

These are the defaults node1 had. Check `~/sysctl_backup.txt` on each node first:

```bash
sudo sysctl -w net.core.rmem_max=212992 net.core.wmem_max=212992 \
  net.ipv4.tcp_rmem="4096 131072 6291456" net.ipv4.tcp_wmem="4096 16384 4194304"
```

Or restore from the backup file:

```bash
sed 's/ = /=/; s/\t/ /g' ~/sysctl_backup.txt | sudo xargs -d '\n' -n1 sysctl -w
```

A reboot also resets them.

---

## 3. Results

### 3.1 Latency

| Test | Result |
|---|---|
| RTT, 300 × 56 B pings | min 48.10 / avg 48.20 / max 48.26 ms, mdev 0.026 ms |
| RTT, 100 × 1400 B pings | avg 48.20 ms, mdev 0.022 ms |
| Loss | 0% |
| Path MTU | 1500 (1472 B DF passes, 1473 B fails), no jumbo frames |
| Hops | 1 (layer-2 stitched VLAN) |

The RTT is flat and does not depend on packet size.

### 3.2 TCP, default buffers

Default buffers: `tcp_rmem` max 6 MB, `tcp_wmem` max 4 MB, cubic.

| Test | node1 → node2 | node2 → node1 |
|---|---|---|
| 1 flow | 504 Mb/s, 0 retr | 506 Mb/s |
| 4 flows | 1.18 Gb/s, 0 retr | |
| 8 flows | 538 Mb/s, 88 retr | |
| 16 flows | 436 Mb/s, 267 retr | 2.80 Gb/s, 2092 retr |

One flow is window-limited. Linux advertises about half of `tcp_rmem` max as the window
(~3 MB), and 3 MB × 8 / 0.048 s ≈ 500 Mb/s, which matches with zero retransmits.

### 3.3 TCP, large buffers on node1 only

| Test | node1 → node2 | node2 → node1 |
|---|---|---|
| 1 flow | 520 Mb/s | 639 Mb/s |
| 4 flows | | 2.47 Gb/s, 0 retr |

Raising one side barely helps. The window is the smaller of the sender's `wmem` and the
receiver's `rmem`, and node2 was still on its defaults.

### 3.4 TCP, large buffers on both nodes (128 MB max)

| Test | node1 → node2 | node2 → node1 |
|---|---|---|
| 1 flow, cubic | 8.35 Gb/s, 5697 retr | **9.39 Gb/s, 0 retr** |
| 1 flow, bbr | 6.72 Gb/s, 0 retr | |
| 4 flows, cubic | **457 Mb/s** (120–135 Mb/s each), 324 retr | |
| 4 flows, bbr | 7.58 Gb/s (1.87–1.91 Gb/s each), 224 retr | |

### 3.5 UDP, default buffers

| Offered rate | node1 → node2 received | loss | node2 → node1 received | loss |
|---|---|---|---|---|
| 500 Mb/s | 494 Mb/s | 0% | | |
| 1 Gb/s | 988 Mb/s | 0.08% | 992 Mb/s | 1.3% |
| 2 Gb/s | 1.65 Gb/s | 17% | | |
| 4 Gb/s | 3.25 Gb/s | 7.3% | 3.05 Gb/s | 0.55–1.1% |

On a node2 → node1 4 Gb/s run, 24,923 datagrams were lost and node1's `RcvbufErrors`
rose by exactly 24,923. So all of that loss was the receiving host's socket buffer, not
the network. The forward-direction counters on node2 were not checked.

---

## 4. Findings

1. **Latency: 48.2 ms RTT, essentially no jitter** (mdev 0.026 ms), no loss, MTU 1500,
   one hop. This is a cross-site path; ~24 ms one way is the floor for any message.
2. **Capacity is near line rate.** One TCP flow reaches 8.35 Gb/s (node1 → node2) and
   9.39 Gb/s (node2 → node1) on a 10 GbE NIC. An earlier estimate of 2.5–3 Gb/s was
   wrong: those runs were still limited by buffers (TCP) and by iperf3's
   single-threaded UDP sender plus receive-buffer drops (UDP).
3. **Default buffers cap a single flow at ~0.5 Gb/s.** The bandwidth-delay product is
   10 Gb/s × 48 ms ≈ 60 MB, but the default buffers allow only ~3 MB in flight. Both
   ends must be raised, because the effective window is min(sender `wmem`, receiver
   `rmem`).
4. **Parallel cubic flows collapse in the node1 → node2 direction.** With large buffers,
   1 flow does 8.35 Gb/s but 4 flows do 457 Mb/s total, all four equally slow. With
   default buffers too, 8 and 16 flows did worse than 4. The likely cause is a
   shallow-buffered hop in that direction that drops overlapping bursts. Cubic backs
   off on every loss and takes a long time to recover at 48 ms. BBR does not treat
   loss as congestion and gets 7.58 Gb/s with 4 flows. node2 → node1 does not show
   this.
5. **UDP loss in these tests is host-side.** All measured node2 → node1 UDP loss was
   receiver socket-buffer overflow. UDP numbers here reflect the hosts, not the link.

### Implications for running PBFT across this link

- Every protocol round costs at least ~24 ms one way, and a view change takes several
  rounds. A 150 ms (or 350 ms) Fixed/Perf timer will keep expiring before a view change
  can complete. That is the same failure mode as the `NetworkDelay` scenario (170 ms
  netem), but permanent.
- PBFT is all-to-all, so it has the many-flows pattern that collapses node1 → node2
  under cubic. Set `net.ipv4.tcp_congestion_control=bbr` on both nodes, plus the large
  buffers from §2.6.
- The gRPC transport sets its own 8 MiB flow-control window per stream
  (`node/nodeMessageHub.go`). At 48 ms that caps each peer stream at about
  8 MiB × 8 / 0.048 s ≈ 1.4 Gb/s, whatever the kernel buffers are.
- A ~600 KiB ViewChange takes about 10 ms to send on one ~0.5 Gb/s default-buffer flow.
