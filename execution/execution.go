package execution

import (
	"bytes"
	"crypto/sha256"
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
	CheckpointSnapshot() map[string]*big.Int
	CheckpointMaterial() ([]byte, map[string]*big.Int, error)
	CheckpointDigest() ([32]byte, map[string]*big.Int, error)
	RestoreCheckpoint(map[string]*big.Int)
	CreateAccount(string)
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

	if msg.Txn.Sender == "" && msg.Txn.Receiver == "" && msg.Txn.Amount == nil {
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

func (sm *AccountStateMachine) CheckpointMaterial() ([]byte, map[string]*big.Int, error) {
	copyOfBalances := sm.CheckpointSnapshot()
	material, err := checkpointMaterialFromBalances(copyOfBalances)
	if err != nil {
		return nil, nil, err
	}
	return material, copyOfBalances, nil
}

func (sm *AccountStateMachine) CheckpointSnapshot() map[string]*big.Int {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return sm.copyOfBalancesLocked()
}

func checkpointMaterialFromBalances(balances map[string]*big.Int) ([]byte, error) {
	accounts := make([]string, 0, len(balances))
	for account := range balances {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)

	var buf bytes.Buffer
	for _, account := range accounts {
		balance := balances[account]
		if balance == nil {
			balance = big.NewInt(0)
		}
		if _, err := fmt.Fprintf(&buf, "%s=%s\n", account, balance.String()); err != nil {
			return nil, err
		}
	}
	return buf.Bytes(), nil
}

func (sm *AccountStateMachine) CheckpointDigest() ([32]byte, map[string]*big.Int, error) {
	material, copyOfBalances, err := sm.CheckpointMaterial()
	if err != nil {
		return [32]byte{}, nil, err
	}
	return sha256.Sum256(material), copyOfBalances, nil
}

func (sm *AccountStateMachine) RestoreCheckpoint(balances map[string]*big.Int) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.balances = make(map[string]*big.Int, len(balances))
	for account, balance := range balances {
		if balance == nil {
			sm.balances[account] = big.NewInt(0)
			continue
		}
		sm.balances[account] = new(big.Int).Set(balance)
	}
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
func (sm *AccountStateMachine) CreateAccount(account string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if _, exists := sm.balances[account]; !exists {
		sm.balances[account] = defaultAccountBalance()
	}
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
	balance, ok := new(big.Int).SetString("999999999999", 10)
	if !ok {
		panic("invalid default account balance")
	}
	return balance
}

func (sm *AccountStateMachine) copyOfBalancesLocked() map[string]*big.Int {
	copyOfBalances := make(map[string]*big.Int, len(sm.balances))
	for account, balance := range sm.balances {
		if balance == nil {
			copyOfBalances[account] = big.NewInt(0)
			continue
		}
		copyOfBalances[account] = new(big.Int).Set(balance)
	}
	return copyOfBalances
}
