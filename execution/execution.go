package execution

import (
	"bytes"
	"fmt"
	"math/big"
	"sort"
	"sync"

	"github.com/michael112233/pbft/core"
)

type Result struct {
	Success bool
	Error   string
}

type StateMachine interface {
	Apply(core.ClientMsg) Result
	CheckpointMaterial() ([]byte, error)
}

type AccountStateMachine struct {
	mu       sync.RWMutex
	balances map[string]*big.Int
}

func NewAccountStateMachine() *AccountStateMachine {
	return &AccountStateMachine{
		balances: make(map[string]*big.Int),
	}
}

func (sm *AccountStateMachine) Apply(msg core.ClientMsg) Result {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if msg.Txn == nil {
		return Result{Success: false, Error: "missing transaction"}
	}
	if msg.Txn.Sender == "" {
		return Result{Success: false, Error: "missing sender"}
	}
	if msg.Txn.Receiver == "" {
		return Result{Success: false, Error: "missing receiver"}
	}
	if msg.Txn.Amount == nil {
		return Result{Success: false, Error: "missing amount"}
	}
	if msg.Txn.Amount.Sign() < 0 {
		return Result{Success: false, Error: "negative amount"}
	}

	senderBalance := sm.ensureAccountLocked(msg.Txn.Sender)
	receiverBalance := sm.ensureAccountLocked(msg.Txn.Receiver)
	if senderBalance.Cmp(msg.Txn.Amount) < 0 {
		return Result{Success: false, Error: "insufficient funds"}
	}

	senderBalance.Sub(senderBalance, msg.Txn.Amount)
	receiverBalance.Add(receiverBalance, msg.Txn.Amount)
	return Result{Success: true}
}

func (sm *AccountStateMachine) CheckpointMaterial() ([]byte, error) {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	accounts := make([]string, 0, len(sm.balances))
	for account := range sm.balances {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)

	var buf bytes.Buffer
	for _, account := range accounts {
		if _, err := fmt.Fprintf(&buf, "%s=%s\n", account, sm.balances[account].String()); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (sm *AccountStateMachine) BalanceOf(account string) *big.Int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	balance, exists := sm.balances[account]
	if !exists {
		return defaultAccountBalance()
	}
	return new(big.Int).Set(balance)
}

func (sm *AccountStateMachine) ensureAccountLocked(account string) *big.Int {
	if balance, exists := sm.balances[account]; exists {
		return balance
	}
	balance := defaultAccountBalance()
	sm.balances[account] = balance
	return balance
}

func defaultAccountBalance() *big.Int {
	balance, ok := new(big.Int).SetString("999999999999999999", 10)
	if !ok {
		panic("invalid default account balance")
	}
	return balance
}
