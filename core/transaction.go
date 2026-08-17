package core

import (
	"math/big"
)

type Transaction struct {
	Sender   string
	Receiver string
	Amount   *big.Int
}

func NewTransaction(sender, receiver string, amount *big.Int) Transaction {
	tx := Transaction{
		Sender:   sender,
		Receiver: receiver,
	}
	if amount != nil {
		tx.Amount = new(big.Int).Set(amount)
	}
	return tx
}

// Clone returns a transaction whose mutable amount is independent of tx.Amount.
func (tx Transaction) Clone() Transaction {
	clone := tx
	if tx.Amount != nil {
		clone.Amount = new(big.Int).Set(tx.Amount)
	}
	return clone
}
