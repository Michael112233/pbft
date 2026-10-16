package node

import (
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
)

// peerDialRetryInterval is how long a startup dial to a peer that is not up yet
// waits before trying again. Peers start in parallel, so a few retries are normal.
const peerDialRetryInterval = 200 * time.Millisecond

// dialPeersAtStart opens this node's stream to every peer when the node starts,
// instead of on the first message to that peer, so the first protocol messages
// (and generation 1's timing) don't include connection setup. Each node sends only
// on streams it dialed itself (a peer's stream to it is receive-only), so every
// node dials all n-1 peers.
//
// It logs "PEER READY" per peer and, once all are open, one "PEERS READY" line,
// which scripts/cluster/start.sh waits for before starting the client. It runs
// off the event loop and touches only the hub, which is safe for concurrent use
// (asyncBroadCast already sends from goroutines). A dead node dials too: dead
// gates messages, not transport, and NetworkDelayFCrash can be dead from start.
func (n *Node) dialPeersAtStart() {
	start := time.Now()
	self := n.GetNodeID()
	ids := make([]int, 0, len(config.NodeAddr))
	for id := range config.NodeAddr {
		if id != self {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)

	var wg sync.WaitGroup
	var connected atomic.Int32
	for _, id := range ids {
		wg.Add(1)
		go func(id int, addr string) {
			defer wg.Done()
			for attempt := 1; ; attempt++ {
				if _, err := n.messageHub.getOrCreatePeerStream(addr); err == nil {
					n.log.Info("PEER READY: node %d (%s) after %v, %d attempt(s)", id, addr, time.Since(start).Round(time.Millisecond), attempt)
					connected.Add(1)
					return
				}
				select {
				case <-n.eventLoopStopCh:
					return
				case <-time.After(peerDialRetryInterval):
				}
			}
		}(id, config.NodeAddr[id])
	}
	wg.Wait()
	if got := int(connected.Load()); got == len(ids) {
		n.log.Info("PEERS READY: %d/%d peers connected after %v", got, len(ids), time.Since(start).Round(time.Millisecond))
	}
}
