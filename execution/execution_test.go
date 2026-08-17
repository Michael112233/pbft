package execution_test

import (
	"fmt"
	"math/big"
	"testing"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/execution"
)

func TestAccountStateMachineApplyAndCheckpointMaterialAreDeterministic(t *testing.T) {
	var sm execution.StateMachine = execution.NewAccountStateMachine()

	msgs := []core.ClientMsg{
		newClientMsg(1, "alice", "bob", 10),
		newClientMsg(2, "bob", "carol", 4),
	}

	for _, msg := range msgs {
		result := sm.Apply(msg)
		if !result.Success {
			t.Fatalf("expected success for msg %d, got error %q", msg.Id, result.Error)
		}
	}

	material1, _, err := sm.CheckpointMaterial()
	if err != nil {
		t.Fatalf("checkpoint material failed: %v", err)
	}
	material2, _, err := sm.CheckpointMaterial()
	if err != nil {
		t.Fatalf("checkpoint material failed on repeat call: %v", err)
	}
	if string(material1) != string(material2) {
		t.Fatalf("checkpoint material changed between calls: %q != %q", material1, material2)
	}
	digest1, _, err := sm.CheckpointDigest()
	if err != nil {
		t.Fatalf("checkpoint digest failed: %v", err)
	}
	digest2, _, err := sm.CheckpointDigest()
	if err != nil {
		t.Fatalf("checkpoint digest failed on repeat call: %v", err)
	}
	if digest1 != digest2 {
		t.Fatalf("checkpoint digest changed between calls: %x != %x", digest1, digest2)
	}

	var sm2 execution.StateMachine = execution.NewAccountStateMachine()
	for _, msg := range msgs {
		result := sm2.Apply(msg)
		if !result.Success {
			t.Fatalf("expected success for second machine msg %d, got error %q", msg.Id, result.Error)
		}
	}
	material3, _, err := sm2.CheckpointMaterial()
	if err != nil {
		t.Fatalf("checkpoint material failed on second machine: %v", err)
	}
	if string(material1) != string(material3) {
		t.Fatalf("checkpoint material mismatch across identical state: %q != %q", material1, material3)
	}
	digest3, _, err := sm2.CheckpointDigest()
	if err != nil {
		t.Fatalf("checkpoint digest failed on second machine: %v", err)
	}
	if digest1 != digest3 {
		t.Fatalf("checkpoint digest mismatch across identical state: %x != %x", digest1, digest3)
	}
}

func TestAccountStateMachineRejectsInvalidTransfersWithoutStateMutation(t *testing.T) {
	sm := execution.NewAccountStateMachine()

	success := sm.Apply(newClientMsg(1, "alice", "bob", 10))
	if !success.Success {
		t.Fatalf("expected initial transfer to succeed, got error %q", success.Error)
	}

	beforeAlice := sm.BalanceOf("alice")
	beforeBob := sm.BalanceOf("bob")

	result := sm.Apply(core.ClientMsg{
		Id: 2,
		Txn: core.Transaction{
			Sender:   "alice",
			Receiver: "bob",
			Amount:   new(big.Int).Add(beforeAlice, big.NewInt(1)),
		},
	})
	if result.Success {
		t.Fatal("expected overdraft transfer to fail")
	}
	if result.Error == "" {
		t.Fatal("expected overdraft transfer to return an error message")
	}

	afterAlice := sm.BalanceOf("alice")
	afterBob := sm.BalanceOf("bob")
	if afterAlice.Cmp(beforeAlice) != 0 {
		t.Fatalf("alice balance changed on failed transfer: before=%s after=%s", beforeAlice, afterAlice)
	}
	if afterBob.Cmp(beforeBob) != 0 {
		t.Fatalf("bob balance changed on failed transfer: before=%s after=%s", beforeBob, afterBob)
	}
}

func newClientMsg(id int64, sender, receiver string, amount int64) core.ClientMsg {
	return core.ClientMsg{
		Id: id,
		Txn: core.Transaction{
			Sender:   sender,
			Receiver: receiver,
			Amount:   big.NewInt(amount),
		},
	}
}

func TestTimeToCreateCopyOfBalances(t *testing.T) {
	sm := execution.NewAccountStateMachine()

	// Create a large number of accounts and balances
	for i := 0; i < 10000; i++ {

		account := fmt.Sprintf("account%d", i)
		sm.CreateAccount(account)
	}

	start := time.Now()
	_ = sm.CheckpointSnapshot()
	end := time.Now()

	t.Logf("Time to create copy of balances: %v", end.Sub(start))
}
