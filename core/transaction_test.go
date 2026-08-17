package core

import (
	"math/big"
	"testing"
)

func TestNewTransactionCopiesAmount(t *testing.T) {
	amount := big.NewInt(10)
	tx := NewTransaction("alice", "bob", amount)

	amount.SetInt64(20)
	if tx.Amount.Int64() != 10 {
		t.Fatalf("transaction amount changed to %s after input mutation", tx.Amount)
	}
}

func TestTransactionCloneCopiesAmount(t *testing.T) {
	tx := NewTransaction("alice", "bob", big.NewInt(10))
	clone := tx.Clone()

	clone.Amount.SetInt64(20)
	if tx.Amount.Int64() != 10 {
		t.Fatalf("original amount changed to %s after clone mutation", tx.Amount)
	}
}
