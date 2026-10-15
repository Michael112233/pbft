package node

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

// ckptFetchNode builds node id of a 7-node (f=2) cluster with NewNode, in view
// (1,1), not in a view change. Its sends return at once (dead), so fetches and
// replies are driven by calling the handlers directly.
func ckptFetchNode(t *testing.T, id int, enabled bool) *Node {
	t.Helper()
	withNodeAddrs(t, 7)
	cfg := config.ReadCfg("../config/run2new.json")
	cfg.NodeNum = 7
	cfg.NodesDead = nil
	cfg.ScenarioMode = false
	cfg.EpochMode = false
	cfg.CheckpointFetch = config.CheckpointFetchConfig{Enabled: enabled, DelayMs: 20}
	// NewNode's key store looks for keys/*.pem and prints a line per missing file.
	stdout := os.Stdout
	if devnull, err := os.Open(os.DevNull); err == nil {
		os.Stdout = devnull
		defer func() { os.Stdout = stdout; devnull.Close() }()
	}
	n, err := NewNode(id, cfg)
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = stdout
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	n.encryptionKeyStore = &KeyStore{privateKey: priv, publicKeys: map[int]ed25519.PublicKey{id: pub}}
	n.dead.Store(true)
	n.messageHub.node_ref, n.messageHub.log = n, n.log
	n.viewID = core.ViewID{Generation: 1, Counter: 1}
	n.forViewID = n.viewID
	t.Cleanup(n.stopCheckpointFetchTimer)
	return n
}

// ckptState is a checkpoint state and its digest.
func ckptState() (map[string]*big.Int, [32]byte) {
	balances := map[string]*big.Int{"alice": big.NewInt(70), "bob": big.NewInt(30)}
	return balances, digestBalances(balances)
}

// giveCertificate hands n 2f+1 votes for (seq, digest) from nodes other than n.
func giveCertificate(n *Node, seq int64, digest [32]byte) {
	for from, given := 1, 0; given < n.QuorumSize(); from++ {
		if from == n.GetNodeID() {
			continue
		}
		n.HandleCheckpoint(core.CheckpointMsg{SeqNum: seq, Digest: digest, From: from}, []byte("sig"))
		given++
	}
}

// commitSlot puts a committed, unexecuted one-request batch at seq.
func commitSlot(t *testing.T, n *Node, seq int64) {
	t.Helper()
	reqs := []core.ClientMsgSignature{{Data: core.ClientMsg{Id: seq, ClientName: "client",
		Txn: core.NewTransaction("alice", "bob", big.NewInt(1))}}}
	batchDigest, digests, err := ComputeBatchDigest(reqs)
	if err != nil {
		t.Fatal(err)
	}
	slot, _ := n.consensusLog.GetorCreateEntry(seq)
	slot.view = n.viewID
	slot.preprepare = &core.PreprepareMsgMini{View: n.viewID, SeqNum: seq, DigestClientMsg: batchDigest, DigestIndividualClientMsgs: digests}
	slot.prepareSent, slot.commitSent, slot.committed = true, true, true
	n.pool.AddBatch(reqs, digests, seq, n.viewID)
}

// A node stranded below a certified checkpoint fetches after a stall window,
// installs the state, and executes the slot it already committed above it.
func TestCheckpointFetchStrandedNodeInstallsAndResumes(t *testing.T) {
	n := ckptFetchNode(t, 4, true)
	n.lastExecuted = 100 // stuck: slot 101 will never commit here
	commitSlot(t, n, CHECKPOINT_INTERVAL+1)
	balances, digest := ckptState()

	giveCertificate(n, CHECKPOINT_INTERVAL, digest)
	if n.ckptFetchTimerCh == nil {
		t.Fatal("no stall window armed with a certificate above lastExecuted")
	}
	n.handleCheckpointFetchTimeout() // a full window with no execution, same view
	if n.ckptFetchAttempts != 1 {
		t.Fatalf("fetches sent = %d, want 1", n.ckptFetchAttempts)
	}
	if n.ckptFetchTimerCh == nil {
		t.Fatal("no retry window armed after the fetch")
	}

	n.handleCheckpointState(core.CheckpointStateMsg{SeqNum: CHECKPOINT_INTERVAL, Digest: digest, From: 2, Balances: balances})
	if got := n.GetLastStableCheckpointSeq(); got != CHECKPOINT_INTERVAL {
		t.Fatalf("stable checkpoint = %d, want %d", got, CHECKPOINT_INTERVAL)
	}
	if n.lastExecuted != CHECKPOINT_INTERVAL+1 {
		t.Fatalf("lastExecuted = %d, want %d (installed, then executed the committed slot above)", n.lastExecuted, CHECKPOINT_INTERVAL+1)
	}
	if n.ckptFetchTimerCh != nil || n.ckptFetchAttempts != 0 {
		t.Fatal("fetch state not cleared after install")
	}
}

// After a fetch, the next attempt waits the retry timeout (5x the stall delay), not
// one stall window, so a reply slower than the delay does not draw a second request.
// The delay is 20 ms here, so the retry timeout is 100 ms.
func TestCheckpointFetchRetryWaitsLongerThanStallWindow(t *testing.T) {
	n := ckptFetchNode(t, 4, true)
	n.lastExecuted = 100
	_, digest := ckptState()
	giveCertificate(n, CHECKPOINT_INTERVAL, digest)
	n.handleCheckpointFetchTimeout()
	if n.ckptFetchAttempts != 1 {
		t.Fatalf("fetches sent = %d, want 1", n.ckptFetchAttempts)
	}

	select {
	case <-n.ckptFetchTimerCh:
		t.Fatal("retry timer fired within two stall windows")
	case <-time.After(2 * n.cfg.CheckpointFetchDelay()):
	}
	select {
	case <-n.ckptFetchTimerCh:
	case <-time.After(2 * n.cfg.CheckpointFetchRetry()):
		t.Fatal("retry timer never fired")
	}
	n.handleCheckpointFetchTimeout() // still stalled, no reply installed
	if n.ckptFetchAttempts != 2 {
		t.Fatalf("fetches sent = %d, want 2 after the retry timeout", n.ckptFetchAttempts)
	}
}

// A node that is behind but still executing never fetches.
func TestCheckpointFetchNotWhileExecuting(t *testing.T) {
	n := ckptFetchNode(t, 4, true)
	n.lastExecuted = 240
	_, digest := ckptState()
	giveCertificate(n, CHECKPOINT_INTERVAL, digest)

	n.lastExecuted = 245 // executed during the window
	n.handleCheckpointFetchTimeout()
	if n.ckptFetchAttempts != 0 {
		t.Fatal("fetched although the node executed during the window")
	}
	if n.ckptFetchTimerCh == nil {
		t.Fatal("no new window armed while still behind the certificate")
	}

	n.lastExecuted = CHECKPOINT_INTERVAL // caught up on its own
	n.handleCheckpointFetchTimeout()
	if n.ckptFetchAttempts != 0 || n.ckptFetchTimerCh != nil {
		t.Fatal("still watching after catching up")
	}
}

// A window that overlaps a view change is not judged: nobody executes then, and
// the NewView may carry the checkpoint.
func TestCheckpointFetchNotAcrossViewChange(t *testing.T) {
	n := ckptFetchNode(t, 4, true)
	n.lastExecuted = 100
	_, digest := ckptState()
	giveCertificate(n, CHECKPOINT_INTERVAL, digest)

	n.viewChangeRunning = true
	n.handleCheckpointFetchTimeout()
	if n.ckptFetchAttempts != 0 {
		t.Fatal("fetched during a view change")
	}

	n.viewChangeRunning = false
	n.viewID = core.ViewID{Generation: 1, Counter: 2} // installed during the window
	n.handleCheckpointFetchTimeout()
	if n.ckptFetchAttempts != 0 {
		t.Fatal("fetched after a window that started in an earlier view")
	}

	n.handleCheckpointFetchTimeout() // a full window inside (1,2)
	if n.ckptFetchAttempts != 1 {
		t.Fatalf("fetches sent = %d, want 1 after a full window in one view", n.ckptFetchAttempts)
	}
}

func TestCheckpointFetchRejectsBadReplies(t *testing.T) {
	n := ckptFetchNode(t, 4, true)
	n.lastExecuted = 100
	balances, digest := ckptState()
	giveCertificate(n, CHECKPOINT_INTERVAL, digest)

	wrong := map[string]*big.Int{"alice": big.NewInt(1)}
	n.handleCheckpointState(core.CheckpointStateMsg{SeqNum: CHECKPOINT_INTERVAL, Digest: digest, From: 2, Balances: wrong})
	if n.lastExecuted != 100 {
		t.Fatal("installed a state that does not hash to the digest")
	}
	_, otherDigest := ckptState()
	otherDigest[0] ^= 1
	n.handleCheckpointState(core.CheckpointStateMsg{SeqNum: CHECKPOINT_INTERVAL, Digest: otherDigest, From: 2, Balances: balances})
	if n.lastExecuted != 100 {
		t.Fatal("installed a checkpoint without a local certificate")
	}
	n.handleCheckpointState(core.CheckpointStateMsg{SeqNum: 2 * CHECKPOINT_INTERVAL, Digest: digest, From: 2, Balances: balances})
	if n.lastExecuted != 100 {
		t.Fatal("installed an uncertified newer checkpoint")
	}

	n.lastExecuted = CHECKPOINT_INTERVAL + 10 // caught up before the reply
	n.handleCheckpointState(core.CheckpointStateMsg{SeqNum: CHECKPOINT_INTERVAL, Digest: digest, From: 2, Balances: balances})
	if n.lastExecuted != CHECKPOINT_INTERVAL+10 || n.GetLastStableCheckpointSeq() != 0 {
		t.Fatal("a stale reply moved the node")
	}
}

// The peer answers with the asked-for checkpoint, or its newer stable one.
func TestCheckpointFetchServe(t *testing.T) {
	n := ckptFetchNode(t, 2, true)
	balances, digest := ckptState()
	k := checkpoint{seq: CHECKPOINT_INTERVAL, digest: digest}
	req := core.CheckpointFetchMsg{SeqNum: k.seq, Digest: k.digest, From: 4}
	if _, _, ok := n.checkpointStateToServe(req); ok {
		t.Fatal("served a checkpoint this node never executed")
	}

	n.checkpointManager.checkpoints[k] = CheckpointData{votes: map[int]core.CheckpointMsgSig{}, balances: balances}
	if key, got, ok := n.checkpointStateToServe(req); !ok || key != k || digestBalances(got) != digest {
		t.Fatal("did not serve the asked-for checkpoint")
	}

	newer := checkpoint{seq: 2 * CHECKPOINT_INTERVAL, digest: [32]byte{7}}
	newBalances := map[string]*big.Int{"alice": big.NewInt(1)}
	delete(n.checkpointManager.checkpoints, k)
	n.checkpointManager.checkpoints[newer] = CheckpointData{votes: map[int]core.CheckpointMsgSig{}, balances: newBalances}
	n.checkpointManager.laststableCheckpoint = newer
	if key, _, ok := n.checkpointStateToServe(req); !ok || key != newer {
		t.Fatal("did not fall back to the newer stable checkpoint")
	}
}

// Disabled: no stall window, no install.
func TestCheckpointFetchDisabled(t *testing.T) {
	n := ckptFetchNode(t, 4, false)
	n.lastExecuted = 100
	balances, digest := ckptState()
	giveCertificate(n, CHECKPOINT_INTERVAL, digest)
	if n.ckptFetchTimerCh != nil {
		t.Fatal("stall window armed with checkpoint_fetch disabled")
	}
	n.handleCheckpointState(core.CheckpointStateMsg{SeqNum: CHECKPOINT_INTERVAL, Digest: digest, From: 2, Balances: balances})
	if n.lastExecuted != 100 {
		t.Fatal("installed a fetched state with checkpoint_fetch disabled")
	}
}

// Both messages survive envelope signing, Deliver's checks and decoding, and reach
// the checkpoint channel tagged with their type.
func TestDeliverCheckpointFetchAndState(t *testing.T) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	n := &Node{
		log:                logger.NewLogger(2, "node"),
		encryptionKeyStore: &KeyStore{publicKeys: map[int]ed25519.PublicKey{2: pub}},
		checkpointMsgChan:  make(chan CheckpointMsg, 2),
	}
	hub := &NodeMessageHub{node_ref: n, log: n.log}
	balances, digest := ckptState()

	fetchPB := transportpb.CheckpointFetchToPB(core.CheckpointFetchMsg{SeqNum: 250, Digest: digest, From: 2})
	signed, _ := marshalDeterministic(fetchPB)
	ack, _ := hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgCheckpointFetchMessage, From: 2,
		Signature: ed25519.Sign(priv, signed), Body: &transportpb.Envelope_CheckpointFetch{CheckpointFetch: fetchPB}})
	if !ack.Ok {
		t.Fatalf("fetch ack = %+v", ack)
	}
	got := <-n.checkpointMsgChan
	if fetch := got.Msg.(core.CheckpointFetchMsg); got.MsgType != core.MsgCheckpointFetchMessage || fetch.SeqNum != 250 || fetch.Digest != digest {
		t.Fatalf("delivered %+v", got)
	}

	statePB := transportpb.CheckpointStateToPB(core.CheckpointStateMsg{SeqNum: 250, Digest: digest, From: 2, Balances: balances})
	signed, _ = marshalDeterministic(statePB)
	ack, _ = hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgCheckpointStateMessage, From: 2,
		Signature: ed25519.Sign(priv, []byte("other")), Body: &transportpb.Envelope_CheckpointState{CheckpointState: statePB}})
	if ack.Ok || len(n.checkpointMsgChan) != 0 {
		t.Fatal("state with a bad signature delivered")
	}
	ack, _ = hub.Deliver(context.Background(), &transportpb.Envelope{MsgType: core.MsgCheckpointStateMessage, From: 2,
		Signature: ed25519.Sign(priv, signed), Body: &transportpb.Envelope_CheckpointState{CheckpointState: statePB}})
	if !ack.Ok {
		t.Fatalf("state ack = %+v", ack)
	}
	got = <-n.checkpointMsgChan
	if state := got.Msg.(core.CheckpointStateMsg); got.MsgType != core.MsgCheckpointStateMessage || digestBalances(state.Balances) != digest {
		t.Fatalf("delivered state does not hash to its digest: %+v", got)
	}
}
