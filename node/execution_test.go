package node

// import (
// 	"math/big"
// 	"testing"

// 	"github.com/michael112233/pbft/core"
// 	"github.com/michael112233/pbft/execution"
// 	"github.com/michael112233/pbft/logger"
// )

// func TestQueueCommittedExecutionRunsInSequenceOrder(t *testing.T) {
// 	log := logger.NewLogger(99, "node")
// 	hub := NewNodeMessageHub()
// 	hub.log = log

// 	n := &Node{
// 		log:               log,
// 		messageHub:        hub,
// 		executionMachine:  execution.NewAccountStateMachine(),
// 		pbftTimerManager:  NewTimerManager(log),
// 		pool:              NewPool(),
// 		pendingExecutions: make(map[int64]pendingExecution),
// 	}

// 	slot1 := &consensusSlot{executionPending: true}
// 	slot2 := &consensusSlot{executionPending: true}

// 	msg1 := testClientMsg(1, "alice", "bob", 10)
// 	msg2 := testClientMsg(2, "bob", "carol", 4)

// 	n.queueCommittedExecution(2, slot2, msg2)
// 	if n.lastExecuted != 0 {
// 		t.Fatalf("expected no executions before seq 1 is ready, got lastExecuted=%d", n.lastExecuted)
// 	}
// 	if slot2.executed {
// 		t.Fatal("seq 2 executed before seq 1 became available")
// 	}

// 	n.queueCommittedExecution(1, slot1, msg1)
// 	if n.lastExecuted != 2 {
// 		t.Fatalf("expected lastExecuted=2 after filling gap, got %d", n.lastExecuted)
// 	}
// 	if !slot1.executed || !slot2.executed {
// 		t.Fatalf("expected both slots executed, got slot1=%t slot2=%t", slot1.executed, slot2.executed)
// 	}

// 	engine := n.executionMachine.(*execution.AccountStateMachine)
// 	if got := engine.BalanceOf("alice"); got.Cmp(big.NewInt(999999999999999989)) != 0 {
// 		t.Fatalf("unexpected alice balance: %s", got)
// 	}
// 	if got := engine.BalanceOf("bob"); got.Cmp(big.NewInt(1000000000000000005)) != 0 {
// 		t.Fatalf("unexpected bob balance: %s", got)
// 	}
// 	if got := engine.BalanceOf("carol"); got.Cmp(big.NewInt(1000000000000000003)) != 0 {
// 		t.Fatalf("unexpected carol balance: %s", got)
// 	}
// }

// func testClientMsg(id int64, sender, receiver string, amount int64) core.ClientMsg {
// 	return core.ClientMsg{
// 		Id: id,
// 		Txn: &core.Transaction{
// 			Sender:   sender,
// 			Receiver: receiver,
// 			Amount:   big.NewInt(amount),
// 		},
// 	}
// }
