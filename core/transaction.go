package core

import (
	"math/big"
	"time"
)

type Transaction struct {
	Sender    string
	Receiver  string
	Amount    *big.Int
	Timestamp int64
}

func NewTransaction(sender, receiver string, amount *big.Int) *Transaction {
	return &Transaction{
		Sender:    sender,
		Receiver:  receiver,
		Amount:    amount,
		Timestamp: time.Now().UnixNano(),
	}
}
