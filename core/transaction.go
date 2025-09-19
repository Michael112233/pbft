package core

import "math/big"

type Transaction struct {
	Sender   string
	Receiver string
	Amount   *big.Int
}

func NewTransaction(sender, receiver string, amount *big.Int) *Transaction {
	return &Transaction{
		Sender:   sender,
		Receiver: receiver,
		Amount:   amount,
	}
}
