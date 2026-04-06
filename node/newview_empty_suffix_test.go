package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

func TestCreateOEmptySuffixReturnsNoDummyAndCheckpointSeq(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 2, 4)
	n.lastStableCheckpoint = checkpoint{seq: 4000}

	O, maxSeq := n.createO([]*core.ViewChangeMsgSig{
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                1,
				PreparedCerts:       map[int64]*core.PreparedCert{},
			},
		},
	}, 2, 1)

	if len(O) != 0 {
		t.Fatalf("len(O) = %d, want 0", len(O))
	}
	if maxSeq != 4000 {
		t.Fatalf("maxSeq = %d, want 4000", maxSeq)
	}
}

func TestCreateOReplicaEmptySuffixReturnsNoDummyAndCheckpointSeq(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 2, 4)
	n.lastStableCheckpoint = checkpoint{seq: 4000}

	O, maxSeq := n.createOReplica([]*core.ViewChangeMsgSig{
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                1,
				PreparedCerts:       map[int64]*core.PreparedCert{},
			},
		},
	}, 2)

	if len(O) != 0 {
		t.Fatalf("len(O) = %d, want 0", len(O))
	}
	if maxSeq != 4000 {
		t.Fatalf("maxSeq = %d, want 4000", maxSeq)
	}
}

func TestNewViewWithEmptySuffixDoesNotCreatePhantomSlotAndNextPreprepareUsesCheckpointPlusOne(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 2, 4)
	n.lastStableCheckpoint = checkpoint{seq: 4000}
	n.view = 1
	n.forView = 2
	n.viewChangeMsgsLog[2] = []*core.ViewChangeMsgSig{
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                1,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                2,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                3,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
	}

	n.newView()

	if got := n.preprepareSeqNumber.Load(); got != 4000 {
		t.Fatalf("preprepareSeqNumber = %d, want 4000", got)
	}
	n.consensusLog.slotsMu.RLock()
	_, exists := n.consensusLog.slots[slotKey{View: 2, SeqNum: 4001}]
	n.consensusLog.slotsMu.RUnlock()
	if exists {
		t.Fatal("unexpected phantom slot for seq 4001 after empty-suffix new view")
	}

	n.preprepare(core.ClientMsgSignature{
		Data: core.ClientMsg{
			Id:         99,
			ClientName: "client",
		},
	})

	n.consensusLog.slotsMu.RLock()
	slot, exists := n.consensusLog.slots[slotKey{View: 2, SeqNum: 4001}]
	n.consensusLog.slotsMu.RUnlock()
	if !exists {
		t.Fatal("expected first post-view-change preprepare to use seq 4001")
	}
	slot.mu.Lock()
	if slot.prePrepare == nil || slot.prePrepare.SeqNum != 4001 {
		slot.mu.Unlock()
		t.Fatal("expected slot preprepare seq to be 4001")
	}
	slot.mu.Unlock()
}

func TestHandleNewViewWithEmptySuffixDoesNotCreatePhantomSlot(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.lastStableCheckpoint = checkpoint{seq: 4000}

	viewChangeLog := []*core.ViewChangeMsgSig{
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                1,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                2,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
		{
			ViewChangeMsg: core.ViewChangeMsg{
				ViewNumber:          2,
				CheckpointSeqNumber: 4000,
				From:                3,
				PreparedCerts:       map[int64]*core.PreparedCert{},
				Type:                core.VCTypeRoundRobin,
				RoundRobinData:      &core.RoundRobinVCData{},
			},
		},
	}
	n.viewChangeMsgsLog[2] = viewChangeLog

	n.HandleNewView(core.NewViewMsg{
		NewViewNumber: 2,
		From:          2,
		PreprepareLog: []core.PreprepareMsgSig{},
		ViewChangeLog: viewChangeLog,
	}, nil)

	if n.view != 2 {
		t.Fatalf("view = %d, want 2", n.view)
	}
	n.consensusLog.slotsMu.RLock()
	_, exists := n.consensusLog.slots[slotKey{View: 2, SeqNum: 4001}]
	n.consensusLog.slotsMu.RUnlock()
	if exists {
		t.Fatal("unexpected phantom slot for seq 4001 after HandleNewView empty suffix")
	}
}
