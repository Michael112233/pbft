package client

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/transportpb"
)

// Per-node send queues. The pacer only enqueues request batches here; one sender
// goroutine per node does the blocking stream.Send. A node that stops reading its
// stream (e.g. a proposal-delayed leader whose event loop sleeps) then blocks only
// its own queue, not the pacer and not sends to the next leader.

// sendQueueBatches is how many request batches may wait for one node: about 190 ms
// of load at one batch per 24 ms, ~170 KB.
const sendQueueBatches = 8

type queuedRequest struct {
	env *transportpb.Envelope
	txs int
}

type nodeSender struct {
	addr string
	ch   chan queuedRequest
}

func newNodeSender(addr string) *nodeSender {
	return &nodeSender{addr: addr, ch: make(chan queuedRequest, sendQueueBatches)}
}

// EnqueueRequest queues a request batch for node to without blocking. When that
// node's queue is full the batch is dropped and counted, and false is returned.
func (hub *ClientMessageHub) EnqueueRequest(to string, req core.RequestMessage) bool {
	s := hub.senders[to]
	if s == nil {
		hub.log.Error("no send queue for target %s; dropping %d requests", to, len(req.Txs))
		hub.droppedFull.Add(int64(len(req.Txs)))
		return false
	}
	env, err := hub.buildEnvelope(core.MsgRequestMessage, req)
	if err != nil {
		hub.log.Error("build envelope failed. msgType=%s err=%v", core.MsgRequestMessage, err)
		return false
	}
	select {
	case s.ch <- queuedRequest{env: env, txs: len(req.Txs)}:
		return true // full then dropped
	default:
		hub.droppedFull.Add(int64(len(req.Txs)))
		return false
	}
}

// DiscardQueued empties the queue for addr (called when addr stops being the
// leader: a non-leader drops client requests anyway) and returns how many requests
// were discarded. A batch the sender is already writing is not affected.
func (hub *ClientMessageHub) DiscardQueued(addr string) int {
	s := hub.senders[addr]
	if s == nil {
		return 0
	}
	discarded := 0
	for {
		select {
		case item := <-s.ch: // batches
			discarded += item.txs
		default:
			hub.discardedOnLeaderChange.Add(int64(discarded))
			return discarded
		}
	}
}

// SendQueueStats returns the requests dropped because a queue was full and those
// discarded on a leader change, since the start.
func (hub *ClientMessageHub) SendQueueStats() (dropped, discarded int64) {
	return hub.droppedFull.Load(), hub.discardedOnLeaderChange.Load()
}

func (hub *ClientMessageHub) senderLoop(s *nodeSender) {
	defer hub.workersWG.Done()
	for {
		select {
		case <-hub.ctx.Done():
			return
		case item := <-s.ch:
			if err := hub.sendFn(s.addr, item.env); err != nil {
				hub.log.Error("send failed. msgType=%s target=%s err=%v", item.env.MsgType, s.addr, err)
				continue
			}
			if hub.client_ref != nil {
				hub.client_ref.recordRequestSent(item.txs)
				hub.client_ref.logFirstSendToNewLeader(s.addr)
			}
		}
	}
}
