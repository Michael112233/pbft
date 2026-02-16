package nodenew

// import (
// 	"sync"
// 	"sync/atomic"
// )

// // ---------------------------------------------------------------------------
// // Message structs
// // ---------------------------------------------------------------------------

// type PrePrepareMsg struct {
// 	View      int64
// 	SeqNum    int64
// 	Digest    []byte
// 	ClientMsg ClientMsgSignature // original client request
// 	SenderID  int64
// }

// type PrepareMsg struct {
// 	View     int64
// 	SeqNum   int64
// 	Digest   []byte
// 	SenderID int64
// }

// type CommitMsg struct {
// 	View     int64
// 	SeqNum   int64
// 	Digest   []byte
// 	SenderID int64
// }

// // Signed wrappers — Signature covers the Data bytes.
// type SignedPrePrepare struct {
// 	Data      PrePrepareMsg
// 	Signature []byte
// }

// type SignedPrepare struct {
// 	Data      PrepareMsg
// 	Signature []byte
// }

// type SignedCommit struct {
// 	Data      CommitMsg
// 	Signature []byte
// }

// // ---------------------------------------------------------------------------
// // Per-sequence consensus state
// // ---------------------------------------------------------------------------

// type certLog struct {
// 	mu sync.Mutex

// 	// PrePrepare (nil until received/created)
// 	prePrepare *SignedPrePrepare
// 	digest     []byte

// 	// Vote sets — key is the sender's NodeID
// 	prepares map[int64]struct{}
// 	commits  map[int64]struct{}

// 	// One-shot flags so we broadcast exactly once per phase transition
// 	prepareSent  bool // did *this* node already broadcast Prepare
// 	commitSent   bool // did *this* node already broadcast Commit
// 	executed     bool // already delivered to application
// }

// // ---------------------------------------------------------------------------
// // Node (extended from skeleton)
// // ---------------------------------------------------------------------------

// type Node struct {
// 	NodeID            int64
// 	Peers             map[int64]string // peer id -> addr
// 	View              int64            // starts at 1; leader = View % N
// 	ViewChangeRunning bool

// 	seqCounter atomic.Int64                // leader's monotonic counter
// 	logStore   sync.Map                    // int64(seqNum) -> *certLog
// 	f          int                         // max faulty nodes: (n-1)/3

// 	// Watermarks — useful when you add checkpointing later.
// 	LowWaterMark  int64
// 	HighWaterMark int64

// 	// Guards View / ViewChangeRunning reads. Writers are view-change (future).
// 	viewMu sync.RWMutex
// }

// // Call once after Peers is populated.
// func (n *Node) InitConsensus() {
// 	totalNodes := int64(len(n.Peers)) + 1 // peers + self
// 	n.f = int((totalNodes - 1) / 3)
// 	n.LowWaterMark = 0
// 	n.HighWaterMark = n.LowWaterMark + 300 // reasonable default window
// }

// func (n *Node) quorumSize() int { return 2*n.f + 1 }

// func (n *Node) isLeader() bool { return n.leaderID() == n.NodeID }

// func (n *Node) leaderID() int64 {
// 	total := int64(len(n.Peers)) + 1
// 	return n.View % total
// }

// // getOrCreateLog returns the certLog for a sequence number, creating it lazily.
// func (n *Node) getOrCreateLog(seq int64) *certLog {
// 	if v, ok := n.logStore.Load(seq); ok {
// 		return v.(*certLog)
// 	}
// 	n.logStore.
// 	entry := &certLog{
// 		prepares: make(map[int64]struct{}),
// 		commits:  make(map[int64]struct{}),
// 	}
// 	actual, _ := n.logStore.LoadOrStore(seq, entry)
// 	return actual.(*certLog)
// }

// // ---------------------------------------------------------------------------
// // Broadcast helpers
// // ---------------------------------------------------------------------------

// func (n *Node) broadcastPrepare(cl *certLog, view, seq int64, digest []byte) {
// 	msg := PrepareMsg{
// 		View:     view,
// 		SeqNum:   seq,
// 		Digest:   digest,
// 		SenderID: n.NodeID,
// 	}
// 	sig := n.sign(marshal(msg))
// 	signed := SignedPrepare{Data: msg, Signature: sig}

// 	for pid := range n.Peers {
// 		if pid != n.NodeID {
// 			go send(pid, signed)
// 		}
// 	}
// }

// func (n *Node) broadcastCommit(view, seq int64, digest []byte) {
// 	msg := CommitMsg{
// 		View:     view,
// 		SeqNum:   seq,
// 		Digest:   digest,
// 		SenderID: n.NodeID,
// 	}
// 	sig := n.sign(marshal(msg))
// 	signed := SignedCommit{Data: msg, Signature: sig}

// 	for pid := range n.Peers {
// 		if pid != n.NodeID {
// 			go send(pid, signed)
// 		}
// 	}
// }

// // ---------------------------------------------------------------------------
// // 1. Leader: receive client request → assign seq → broadcast PrePrepare
// // ---------------------------------------------------------------------------

// func (n *Node) PrePrepare(clientMsg ClientMsgSignature) {
// 	// Basic guards
// 	if !n.isLeader() {
// 		return
// 	}
// 	n.viewMu.RLock()
// 	if n.ViewChangeRunning {
// 		n.viewMu.RUnlock()
// 		return
// 	}
// 	view := n.View
// 	n.viewMu.RUnlock()

// 	// Verify client signature
// 	if !n.verifyClient(clientMsg) {
// 		return
// 	}

// 	// Assign next sequence number
// 	seq := n.seqCounter.Add(1)

// 	// Watermark check (will matter once checkpointing is in)
// 	if seq <= n.LowWaterMark || seq > n.HighWaterMark {
// 		return
// 	}

// 	digest := computeDigest(clientMsg.Data)

// 	ppMsg := PrePrepareMsg{
// 		View:      view,
// 		SeqNum:    seq,
// 		Digest:    digest,
// 		ClientMsg: clientMsg,
// 		SenderID:  n.NodeID,
// 	}
// 	sig := n.sign(marshal(ppMsg))
// 	signed := SignedPrePrepare{Data: ppMsg, Signature: sig}

// 	// Store locally
// 	cl := n.getOrCreateLog(seq)
// 	cl.mu.Lock()
// 	cl.prePrepare = &signed
// 	cl.digest = digest
// 	cl.mu.Unlock()

// 	// Broadcast to all replicas
// 	for pid := range n.Peers {
// 		if pid != n.NodeID {
// 			go send(pid, signed)
// 		}
// 	}

// 	// Leader does NOT send Prepare; its PrePrepare counts as its vote.
// 	// But check if buffered prepares already satisfy quorum.
// 	n.tryAdvancePrepare(cl, view, seq, digest)
// }

// // ---------------------------------------------------------------------------
// // 2. Replica: receive PrePrepare → validate → broadcast Prepare
// // ---------------------------------------------------------------------------

// func (n *Node) HandlePrePrepare(msg SignedPrePrepare) {
// 	n.viewMu.RLock()
// 	if n.ViewChangeRunning {
// 		n.viewMu.RUnlock()
// 		return
// 	}
// 	view := n.View
// 	n.viewMu.RUnlock()

// 	pp := msg.Data

// 	// --- Validation ---
// 	if pp.View != view {
// 		return
// 	}
// 	if pp.SenderID != n.leaderID() {
// 		return
// 	}
// 	if pp.SeqNum <= n.LowWaterMark || pp.SeqNum > n.HighWaterMark {
// 		return
// 	}
// 	if !n.verify(pp.SenderID, marshal(pp), msg.Signature) {
// 		return
// 	}
// 	if !n.verifyClient(pp.ClientMsg) {
// 		return
// 	}

// 	expectedDigest := computeDigest(pp.ClientMsg.Data)
// 	if !digestEqual(expectedDigest, pp.Digest) {
// 		return
// 	}

// 	cl := n.getOrCreateLog(pp.SeqNum)
// 	cl.mu.Lock()

// 	// Already have a PrePrepare for this seq? Reject duplicate / conflicting.
// 	if cl.prePrepare != nil {
// 		cl.mu.Unlock()
// 		return
// 	}

// 	cl.prePrepare = &msg
// 	cl.digest = pp.Digest
// 	cl.mu.Unlock()

// 	// Broadcast Prepare (once)
// 	cl.mu.Lock()
// 	if !cl.prepareSent {
// 		cl.prepareSent = true
// 		cl.prepares[n.NodeID] = struct{}{} // count own prepare
// 		cl.mu.Unlock()
// 		n.broadcastPrepare(cl, view, pp.SeqNum, pp.Digest)
// 	} else {
// 		cl.mu.Unlock()
// 	}

// 	// Buffered prepares may now form quorum with the PrePrepare
// 	n.tryAdvancePrepare(cl, view, pp.SeqNum, pp.Digest)
// }

// // ---------------------------------------------------------------------------
// // 3. Receive Prepare → accumulate → once prepared, broadcast Commit
// // ---------------------------------------------------------------------------

// func (n *Node) HandlePrepare(msg SignedPrepare) {
// 	n.viewMu.RLock()
// 	if n.ViewChangeRunning {
// 		n.viewMu.RUnlock()
// 		return
// 	}
// 	view := n.View
// 	n.viewMu.RUnlock()

// 	p := msg.Data

// 	if p.View != view {
// 		return
// 	}
// 	if p.SeqNum <= n.LowWaterMark || p.SeqNum > n.HighWaterMark {
// 		return
// 	}
// 	if !n.verify(p.SenderID, marshal(p), msg.Signature) {
// 		return
// 	}

// 	cl := n.getOrCreateLog(p.SeqNum)
// 	cl.mu.Lock()

// 	// Digest check: if we have the PrePrepare, the digest must match.
// 	// If we don't have it yet, store the prepare anyway (out-of-order).
// 	if cl.digest != nil && !digestEqual(cl.digest, p.Digest) {
// 		cl.mu.Unlock()
// 		return
// 	}

// 	cl.prepares[p.SenderID] = struct{}{}
// 	cl.mu.Unlock()

// 	n.tryAdvancePrepare(cl, view, p.SeqNum, p.Digest)
// }

// // tryAdvancePrepare checks the "prepared" predicate and, if newly met,
// // broadcasts Commit exactly once.
// func (n *Node) tryAdvancePrepare(cl *certLog, view, seq int64, digest []byte) {
// 	cl.mu.Lock()
// 	defer cl.mu.Unlock()

// 	if cl.commitSent {
// 		return // already advanced past prepare
// 	}
// 	if cl.prePrepare == nil {
// 		return // can't be prepared without PrePrepare
// 	}
// 	// Need 2f prepares (leader's PrePrepare is its implicit vote, so
// 	// backups need 2f prepare messages; leader needs 2f from backups).
// 	if len(cl.prepares) < 2*n.f {
// 		return
// 	}

// 	cl.commitSent = true
// 	// Add own commit vote before releasing lock
// 	cl.commits[n.NodeID] = struct{}{}

// 	// Broadcast Commit (release lock first to avoid holding during I/O)
// 	go func() {
// 		n.broadcastCommit(view, seq, cl.digest)
// 		// After broadcasting, check if commit quorum already met
// 		n.tryExecute(cl, seq)
// 	}()
// }

// // ---------------------------------------------------------------------------
// // 4. Receive Commit → accumulate → once committed-local, execute
// // ---------------------------------------------------------------------------

// func (n *Node) HandleCommit(msg SignedCommit) {
// 	n.viewMu.RLock()
// 	if n.ViewChangeRunning {
// 		n.viewMu.RUnlock()
// 		return
// 	}
// 	view := n.View
// 	n.viewMu.RUnlock()

// 	c := msg.Data

// 	if c.View != view {
// 		return
// 	}
// 	if c.SeqNum <= n.LowWaterMark || c.SeqNum > n.HighWaterMark {
// 		return
// 	}
// 	if !n.verify(c.SenderID, marshal(c), msg.Signature) {
// 		return
// 	}

// 	cl := n.getOrCreateLog(c.SeqNum)
// 	cl.mu.Lock()

// 	if cl.digest != nil && !digestEqual(cl.digest, c.Digest) {
// 		cl.mu.Unlock()
// 		return
// 	}

// 	cl.commits[c.SenderID] = struct{}{}
// 	cl.mu.Unlock()

// 	n.tryExecute(cl, c.SeqNum)
// }

// // tryExecute checks committed-local predicate: prepared AND 2f+1 commits.
// func (n *Node) tryExecute(cl *certLog, seq int64) {
// 	cl.mu.Lock()
// 	defer cl.mu.Unlock()

// 	if cl.executed {
// 		return
// 	}
// 	if cl.prePrepare == nil {
// 		return
// 	}
// 	// Must be prepared: have PrePrepare + 2f prepares
// 	if len(cl.prepares) < 2*n.f {
// 		return
// 	}
// 	// Committed-local: 2f+1 commits
// 	if len(cl.commits) < n.quorumSize() {
// 		return
// 	}

// 	cl.executed = true

// 	// Deliver to application layer.
// 	// Execute in order — hand off to an ordered executor (you'll wire this up).
// 	go n.executeRequest(seq, cl.prePrepare.Data.ClientMsg)
// }

// // ---------------------------------------------------------------------------
// // Application execution (placeholder — enforce sequential delivery later)
// // ---------------------------------------------------------------------------

// func (n *Node) executeRequest(seq int64, clientMsg ClientMsgSignature) {
// 	// TODO: ensure in-order execution (seq must == lastExecuted+1).
// 	// For now, deliver to application.
// 	// n.apply(clientMsg.Data.Txn)
// 	// n.replyToClient(clientMsg)
// }

// // ---------------------------------------------------------------------------
// // Stubs — you already have these implemented
// // ---------------------------------------------------------------------------

// func (n *Node) sign(data []byte) []byte {
// 	// your signing implementation
// 	return nil
// }

// func (n *Node) verify(nodeID int64, data []byte, sig []byte) bool {
// 	// your verification implementation
// 	return true
// }

// func (n *Node) verifyClient(msg ClientMsgSignature) bool {
// 	// verify client signature on msg.Data
// 	return true
// }

// func send(peerID int64, msg interface{}) {
// 	// your network send implementation
// }

// func marshal(v interface{}) []byte {
// 	// deterministic serialization (e.g., protobuf, cbor)
// 	return nil
// }

// func computeDigest(msg ClientMsg) []byte {
// 	// SHA-256 of deterministic marshaling
// 	return nil
// }

// func digestEqual(a, b []byte) bool {
// 	if len(a) != len(b) {
// 		return false
// 	}
// 	for i := range a {
// 		if a[i] != b[i] {
// 			return false
// 		}
// 	}
// 	return true
// }
