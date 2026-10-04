package node

import (
	"context"
	"errors"
	"runtime"
	"sync"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

var errEventLoopStopped = errors.New("node event loop stopped")

func (n *Node) HandleEventMessage(ctx context.Context, msg core.EventMsg) error {
	// Gate commands are applied on the event loop before this returns, so the
	// unary Ack the client reads means "applied", not just "received". The
	// controller's slot state machine relies on that (node/proposalgate.go).
	if enable, ok := throttleGateEnableFor(msg.EventType); ok {
		// ok mean throttle event
		// wait till applied then ack tell if applied or not
		return n.ReceiveThrottleGateCmd(ctx, enable)
	}


	// below code is dead unless have event manager from client and have leader stall even which we dont
	select {
	case n.clientEventMsgChan <- msg:
		n.log.Debug("event message received: %s", msg.EventType)
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-n.eventLoopStopCh:
		return errEventLoopStopped
	}
}

func (n *Node) HandleRequestMessage(requests core.RequestMessage) {
	txs := requests.Txs
	if len(txs) == 0 {
		return
	}

	workers := runtime.NumCPU()
	if workers > len(txs) {
		workers = len(txs)
	}
	if workers < 1 {
		workers = 1
	}
	chunkSize := (len(txs) + workers - 1) / workers

	var wg sync.WaitGroup
	for start := 0; start < len(txs); start += chunkSize {
		end := start + chunkSize
		if end > len(txs) {
			end = len(txs)
		}
		wg.Add(1)
		go func(chunk []core.ClientMsgSignature) {
			defer wg.Done()
			// backpressure is ther when pending queue fill but multiple goroutin will be parked, goroutine are harmless dont block other cpu processing
			n.verifyAndForwardRequests(chunk)
		}(txs[start:end])
	}
	wg.Wait()
}

// verifyAndForwardRequests verifies each request's client signature and, for
// the ones that pass, hands them one at a time to the event loop. Called
// concurrently by HandleRequestMessage's per-chunk workers; touches no node
// state besides the read-only client public key and the request channel.
func (n *Node) verifyAndForwardRequests(reqs []core.ClientMsgSignature) {
	for _, req := range reqs {
		clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(req.Data))
		if err != nil {
			n.log.Error("Failed to marshal client message for signature verification: %v", err)
			return
		}
		verified := crypto.VerifySignatureEd25519(clientMsgBytes, req.Signature, n.encryptionKeyStore.clientKey)
		if !verified {
			n.log.Error("Failed to verify client message signature for request ID %d and client %s", req.Data.Id, req.Data.ClientName)
			continue
		}
		n.ReceiveVerifiedClientRequestCh(req)
	}
}
