package node

import (
	"testing"

	"github.com/michael112233/pbft/core"
)

func TestCreateVCContentCopiesPreparedCerts(t *testing.T) {
	n, _ := newTestNodeWithKeys(t, 1, 4)
	n.view = 2

	slot := &consensusSlot{
		view: 2,
		prePrepare: &core.PreprepareMsg{
			View:            2,
			SeqNum:          11,
			DigestClientMsg: [32]byte{1},
		},
		prePrepareSig: []byte{9, 8, 7},
		prepares: map[int]*core.PrepareMsgSig{
			2: {
				PrepareMsg: core.PrepareMsg{
					View:   2,
					SeqNum: 11,
					Digest: [32]byte{1},
					From:   2,
				},
				Signature: []byte{1, 2, 3},
			},
		},
		commits:    make(map[int][32]byte),
		commitSent: true,
	}

	n.consensusLog.slots[slotKey{View: 2, SeqNum: 11}] = slot

	preparedCerts := n.createVCContent(0)
	cert := preparedCerts[11]
	if cert == nil {
		t.Fatal("expected prepared cert for seq 11")
	}

	slot.mu.Lock()
	slot.prePrepareSig[0] = 5
	slot.prepares[2].Signature[0] = 6
	slot.prepares[2].PrepareMsg.From = 4
	slot.mu.Unlock()

	if cert.PreprepareMsg.Signature[0] != 9 {
		t.Fatalf("preprepare signature mutated to %d, want 9", cert.PreprepareMsg.Signature[0])
	}
	if cert.PrepareLog[2].Signature[0] != 1 {
		t.Fatalf("prepare signature mutated to %d, want 1", cert.PrepareLog[2].Signature[0])
	}
	if cert.PrepareLog[2].PrepareMsg.From != 2 {
		t.Fatalf("prepare payload mutated to from=%d, want 2", cert.PrepareLog[2].PrepareMsg.From)
	}
}
