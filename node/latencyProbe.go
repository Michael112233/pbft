package node

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const rttSampleChanSize = 64

// Probe answers an RTT probe directly in the gRPC handler goroutine, without
// touching the event loop, so the caller's timing reflects network + gRPC only.
// A dead node refuses, so it looks unreachable.
// TODO(safety): the response is not authenticated (pilot only).
func (hub *NodeMessageHub) Probe(_ context.Context, _ *transportpb.ProbeReq) (*transportpb.ProbeResp, error) {
	if hub.node_ref == nil || hub.node_ref.dead.Load() {
		return nil, status.Error(codes.Unavailable, "node unavailable")
	}
	return &transportpb.ProbeResp{From: int32(hub.node_ref.GetNodeID())}, nil
}

// startLatencyProber starts the background prober when latency_probe is on.
// Call after the message hub has started.
func (n *Node) startLatencyProber() {
	if !n.cfg.LatencyProbe || !n.proberStarted.CompareAndSwap(false, true) {
		return
	}
	go n.latencyProberLoop()
}

func (n *Node) stopLatencyProber() {
	n.proberStopOnce.Do(func() { close(n.proberStop) })
	if n.proberStarted.Load() {
		<-n.proberDone
	}
}

// latencyProberLoop probes every peer each interval with a unary Probe call on
// the existing peer connection and hands the round-trip time to the event loop.
// It never writes node state itself.
func (n *Node) latencyProberLoop() {
	defer close(n.proberDone)
	interval, timeout := n.cfg.AwareProbeInterval(), n.cfg.AwareProbeTimeout()
	n.log.Info("AWARE: latency prober started interval=%s timeout=%s", interval, timeout)

	peers := make([]int, 0, len(config.NodeAddr))
	for id := range config.NodeAddr {
		if id != n.GetNodeID() {
			peers = append(peers, id)
		}
	}
	sort.Ints(peers)
	inflight := make(map[int]*atomic.Bool, len(peers))
	for _, id := range peers {
		inflight[id] = &atomic.Bool{}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if n.dead.Load() {
				continue
			}
			for _, id := range peers {
				// Skip a peer whose previous probe is still outstanding, so a dead
				// peer does not accumulate timed-out calls.
				if inflight[id].CompareAndSwap(false, true) {
					go func(peer int, busy *atomic.Bool) {
						defer busy.Store(false)
						n.probePeer(peer, timeout)
					}(id, inflight[id])
				}
			}
		case <-n.proberStop:
			return
		}
	}
}

func (n *Node) probePeer(peer int, timeout time.Duration) {
	addr, ok := config.NodeAddr[peer]
	if !ok || addr == "" {
		return
	}
	state, err := n.messageHub.getOrCreatePeerStream(addr)
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	start := time.Now()
	if _, err := transportpb.NewPBFTTransportClient(state.conn).Probe(ctx, &transportpb.ProbeReq{From: int32(n.GetNodeID())}); err != nil {
		return
	}
	now := time.Now()
	sample := rttSample{peer: peer, rttMs: float64(now.Sub(start).Microseconds()) / 1000, at: now}
	select {
	case n.rttSampleCh <- sample:
	default:
		n.log.Warn("AWARE: rtt sample channel full, dropping sample for peer %d", peer)
	}
}
