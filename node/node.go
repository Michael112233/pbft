package node

import (
	"bytes"
	"fmt"

	"crypto/sha256"
	"encoding/gob"
	"encoding/json"
	"sync"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
)

type Node struct {
	NodeID int

	cfg        *config.Config
	log        *logger.Logger
	messageHub *NodeMessageHub

	////
	unverifiedClientMsgsChan  chan []core.ClientMsgSignature // ptr or no
	verifiedClientMsgsChan    chan core.ClientMsgSignature   // ptr or no
	encryptionKeyStore        *KeyStore
	preprepareSem             chan struct{}
	preprepareSeqNumber       atomic.Int64
	view                      int64
	consensusLog              *ConsensusLog
	viewChangeRunning         bool
	viewMu                    sync.RWMutex
	fNodes                    int
	verificationWorkerStarted atomic.Bool
}

func NewNode(nodeID int, cfg *config.Config) *Node {

	return &Node{
		NodeID: nodeID,

		cfg:        cfg,
		log:        logger.NewLogger(nodeID, "node"),
		messageHub: NewNodeMessageHub(),

		encryptionKeyStore:       NewKeyStore(nodeID, cfg.NodeNum),
		unverifiedClientMsgsChan: make(chan []core.ClientMsgSignature, 100), // buffer size can be tuned
		verifiedClientMsgsChan:   make(chan core.ClientMsgSignature, 100),   // buffer size can be tuned
		preprepareSem:            make(chan struct{}, 5000),
		preprepareSeqNumber:      atomic.Int64{},
		view:                     1,
		consensusLog:             NewConsensusLog(),
		viewChangeRunning:        false,
		fNodes:                   (int(cfg.NodeNum) - 1) / 3,
	}
}

func (n *Node) Start() {
	n.messageHub.Start(n, &sync.WaitGroup{})
	// n.StartGarbageCollection()
	go n.ClientSignatureVerifier()
	go n.VerifiedClientMessageHandler()
	n.log.Info("node started")
}

func (n *Node) Stop() {
	// Stop all expire timers to prevent resource leaks
	// n.StopAllExpireTimers()
	// Close network resources to stop listeners and connections
	if n.messageHub != nil {
		n.messageHub.Close()
	}
	n.log.Info("node stopped")
}

func (n *Node) GetAddr() string {
	return config.NodeAddr[int(n.NodeID)]
}

func (n *Node) GetNodeID() int {
	return n.NodeID
}
func (n *Node) PrintDetails() {
	// print sequence number
	fmt.Printf("Node ID: %d, Address: %s, Current View: %d, PrePrepare Sequence Number: %d\n", n.NodeID, n.GetAddr(), n.view, n.preprepareSeqNumber.Load())
	n.consensusLog.PrintDetails()
}

func (n *Node) ClientSignatureVerifier() {
	for {
		select {
		case clientMsgSigs := <-n.unverifiedClientMsgsChan:

			// Verify signatures
			for _, clientMsgSig := range clientMsgSigs {
				n.verifiedClientMsgsChan <- clientMsgSig
			}
			n.log.Info("Verifying client message signatures, count: %d", len(clientMsgSigs))
		}
	}
}

func (n *Node) VerifiedClientMessageHandler() {
	const (
		batchTimeout = 5000 * time.Millisecond // Adjust as needed
	)
	batch := make([]core.ClientMsgSignature, 0, n.cfg.MaxBlockSize)
	timer := time.NewTimer(batchTimeout)
	timer.Stop() // Initially stop the timer
	for {
		select {
		case clientMsgSig := <-n.verifiedClientMsgsChan: // can be block and then leftover txn can go in next batch
			batch = append(batch, clientMsgSig)
			if len(batch) == 1 {
				// Start the timer when the first message arrives
				timer.Reset(batchTimeout)
			}
			if len(batch) >= int(n.cfg.MaxBlockSize) {
				// Process full batch
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				n.processClientMessageBatch(batch) // will block on sem and put backpressure, maybe pool when block
				batch = nil

			}
		case <-timer.C:
			if len(batch) > 0 {
				// Process whatever is in the batch when the timer expires
				n.log.Info("Batch timeout reached, processing batch of size: %d", len(batch))
				n.processClientMessageBatch(batch)
				batch = nil
			}
		}
	}
}

func (n *Node) processClientMessageBatch(batch []core.ClientMsgSignature) {
	n.preprepareSem <- struct{}{} // Acquire semaphore, may add default to drop batch if full

	go func() {
		defer func() { <-n.preprepareSem }()
		n.preprepare(batch[0])
	}()
}

func ComputeBatchDigest(batch core.ClientMsg) ([32]byte, error) {
	// can use buf pool and blake 2b later for optimization
	// also can have worker for batch digest computation if it becomes bottleneck
	data, err := json.Marshal(batch)
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(data), nil
}
func matchingVotes(votes map[int][32]byte, target [32]byte) int {
	count := 0
	for _, d := range votes {
		if d == target {
			count++
		}
	}
	return count
}

func (n *Node) broadcastPrepare(view int64, seq int64, digest [32]byte) {
	msg := core.PrepareMsg{
		View:   view,
		SeqNum: seq,
		Digest: digest,
		From:   n.GetNodeID(),
	}
	// sig := n.sign(marshal(msg))
	// signed := SignedPrepare{Data: msg, Signature: sig}

	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		msg.To = othersIp
		n.messageHub.Send(core.MsgPrepareMessage, othersIp, msg, nil) // cant do go in current
	}
}

func (n *Node) broadcastCommit(view, seq int64, digest [32]byte) {
	msg := core.CommitMsg{
		View:   view,
		SeqNum: seq,
		Digest: digest,
		From:   n.GetNodeID(),
	}

	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		msg.To = othersIp
		n.messageHub.Send(core.MsgCommitMessage, othersIp, msg, nil) // cant do go in current
	}
}
func (n *Node) preprepare(batch core.ClientMsgSignature) {

	n.viewMu.RLock()
	if n.viewChangeRunning {
		n.viewMu.RUnlock()
		return
	}
	view := n.view
	n.viewMu.RUnlock()
	seqNum := n.preprepareSeqNumber.Add(1)

	digestClientMsg, err := ComputeBatchDigest(batch.Data)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}

	preprepareMsg := core.PreprepareMsg{
		View:      view,
		SeqNum:    seqNum,
		ClientMsg: batch,
		// DigestClientMsg: digestClientMsg,
		// ideally sign preprepare with digest so less costly and piggy back client messages, but for simplicity we sign whole preprepare message here
	}

	slot := n.consensusLog.getOrCreateLog(seqNum, view)
	slot.mu.Lock()
	slot.prePrepare = &preprepareMsg
	slot.digest = digestClientMsg
	slot.prepareSent = true
	slot.mu.Unlock()

	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		preprepareMsg.To = othersIp
		n.messageHub.Send(core.MsgPreprepareMessage, othersIp, preprepareMsg, nil) // cant do go in current state race
	}

}

func (n *Node) HandlePrePrepare(preprepareMsg core.PreprepareMsg) {
	n.viewMu.RLock()
	if n.viewChangeRunning {
		n.viewMu.RUnlock()
		return
	}
	view := n.view
	n.viewMu.RUnlock()

	// --- Validation ---
	if preprepareMsg.View != view {
		return
	}
	// if pp.SenderID != n.leaderID() {
	// 	return
	// }
	// if pp.SeqNum <= n.LowWaterMark || pp.SeqNum > n.HighWaterMark {
	// 	return
	// }
	// if !n.verify(pp.SenderID, marshal(pp), msg.Signature) {
	// 	return
	// }
	var buf bytes.Buffer
	gob.NewEncoder(&buf).Encode(preprepareMsg.ClientMsg.Data)
	verified := crypto.VerifySignatureEd25519(buf.Bytes(), preprepareMsg.ClientMsg.Signature, n.encryptionKeyStore.clientKey)
	if !verified {
		n.log.Error("Failed to verify client message signature in PrePrepare from %d, seqNum %d", preprepareMsg.View, preprepareMsg.SeqNum)
		return
	}

	digestClientMsg, err := ComputeBatchDigest(preprepareMsg.ClientMsg.Data)
	if err != nil {
		n.log.Error("Failed to compute batch digest: %v", err)
		return
	}
	// if !digestEqual(expectedDigest, pp.Digest) {
	// 	return
	// }

	slot := n.consensusLog.getOrCreateLog(preprepareMsg.SeqNum, view)
	slot.mu.Lock()

	// View comparison on the log entry itself
	if preprepareMsg.View < slot.view {
		// Stale PrePrepare from an older view — ignore
		slot.mu.Unlock()
		return
	}
	if preprepareMsg.View > slot.view {
		// New view for this seq — wipe old state
		slot.resetForView(preprepareMsg.View)
	}

	// Already have a PrePrepare for this (view, seq)? Reject duplicate / conflicting.
	if slot.prePrepare != nil {
		slot.mu.Unlock()
		return
	}

	slot.prePrepare = &preprepareMsg
	slot.digest = digestClientMsg

	if !slot.prepareSent {
		slot.prepareSent = true
		slot.prepares[n.GetNodeID()] = digestClientMsg
		slot.mu.Unlock()
		n.broadcastPrepare(view, preprepareMsg.SeqNum, digestClientMsg)
	} else { //redundant else ?
		slot.mu.Unlock()
	}

	// Buffered prepares may now form quorum with the PrePrepare
	n.tryAdvancePrepare(slot, view, preprepareMsg.SeqNum, digestClientMsg)
}

func (n *Node) HandlePrepare(prepareMsg core.PrepareMsg) {
	n.viewMu.RLock()
	if n.viewChangeRunning {
		n.viewMu.RUnlock()
		return
	}
	view := n.view
	n.viewMu.RUnlock()

	if prepareMsg.View != view {
		return
	}
	// if p.SeqNum <= n.LowWaterMark || p.SeqNum > n.HighWaterMark {
	// 	return
	// }
	// if !n.verify(p.SenderID, marshal(p), msg.Signature) {
	// 	return
	// }

	slot := n.consensusLog.getOrCreateLog(prepareMsg.SeqNum, view)
	slot.mu.Lock()

	// View check on the log entry
	if prepareMsg.View < slot.view {
		slot.mu.Unlock()
		return
	}
	if prepareMsg.View > slot.view {
		// Prepare arrived before its PrePrepare in a new view — reset and buffer
		slot.resetForView(prepareMsg.View)
	}

	// Digest check: if we have the PrePrepare, the digest must match.
	// If we don't have it yet, store the prepare anyway (out-of-order).
	if slot.digest != [32]byte{} && slot.digest != prepareMsg.Digest {
		slot.mu.Unlock()
		return
	}

	slot.prepares[prepareMsg.From] = prepareMsg.Digest
	slot.mu.Unlock()

	n.tryAdvancePrepare(slot, view, prepareMsg.SeqNum, prepareMsg.Digest)
}

func (n *Node) HandleCommit(commitMsg core.CommitMsg) {
	n.viewMu.RLock()
	if n.viewChangeRunning {
		n.viewMu.RUnlock()
		return
	}
	view := n.view
	n.viewMu.RUnlock()

	if commitMsg.View != view {
		return
	}
	// if c.SeqNum <= n.LowWaterMark || c.SeqNum > n.HighWaterMark {
	// 	return
	// }
	// if !n.verify(c.SenderID, marshal(c), msg.Signature) {
	// 	return
	// }

	slot := n.consensusLog.getOrCreateLog(commitMsg.SeqNum, view)
	slot.mu.Lock()

	// View check on the log entry
	if commitMsg.View < slot.view {
		slot.mu.Unlock()
		return
	}
	if commitMsg.View > slot.view {
		// Commit arrived before its PrePrepare in a new view — reset and buffer
		slot.resetForView(commitMsg.View)
	}

	if slot.digest != [32]byte{} && slot.digest != commitMsg.Digest {
		slot.mu.Unlock()
		return
	} // msybe not needed here

	slot.commits[commitMsg.From] = commitMsg.Digest
	slot.mu.Unlock()

	n.tryExecute(slot, commitMsg.SeqNum)
}

func (n *Node) tryAdvancePrepare(slot *consensusSlot, view, seq int64, digest [32]byte) {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	if slot.commitSent {
		return // already advanced past prepare
	}
	if slot.prePrepare == nil || slot.digest == [32]byte{} {
		return // can't be prepared without PrePrepare
	}
	// Need 2f prepares matching our accepted digest.
	// Leader's PrePrepare is its implicit prepare-phase vote.
	if len(slot.prepares) < 2*n.fNodes || matchingVotes(slot.prepares, slot.digest) < 2*n.fNodes {
		return
	}

	slot.commitSent = true
	// Add own commit vote with digest before releasing lock
	slot.commits[n.GetNodeID()] = slot.digest

	// Broadcast Commit (release lock first to avoid holding during I/O)
	go func() {
		n.broadcastCommit(view, seq, slot.digest)
		// After broadcasting, check if commit quorum already met
		// n.tryExecute(cl, seq)
	}()
}

func (n *Node) tryExecute(slot *consensusSlot, seq int64) {
	slot.mu.Lock()
	defer slot.mu.Unlock()

	if slot.executed {
		return
	}
	if slot.prePrepare == nil {
		return
	}
	// Must be prepared: have PrePrepare + 2f matching prepares
	// if matchingVotes(cl.prepares, cl.digest) < 2*n.f {
	// 	return
	// }
	if slot.commitSent == false {
		return
	}
	// Committed-local: 2f+1 matching commits
	if len(slot.commits) < 2*n.fNodes+1 || matchingVotes(slot.commits, slot.digest) < 2*n.fNodes+1 {
		return
	}

	slot.executed = true
	go n.sendReply(slot.prePrepare.ClientMsg.Data)
	// slot.prePrepare.ClientMsg.
	// Deliver to application layer.
	// Execute in order — hand off to an ordered executor (you'll wire this up).
	// go n.executeRequest(seq, cl.prePrepare.Data.ClientMsg)
}

func (n *Node) sendReply(clientMsg core.ClientMsg) {
	replyMsg := core.ReplyMessage{
		From:      n.GetAddr(),
		To:        config.ClientAddr,
		ClientMsg: clientMsg,
	}
	n.messageHub.Send(core.MsgReplyMessage, config.ClientAddr, replyMsg, nil)
}
