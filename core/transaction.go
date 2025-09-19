package core

type Transaction struct {
	Sender   string
	Receiver string
	Amount   int64
}

func NewTransaction(sender, receiver string, amount int64) *Transaction {
	return &Transaction{
		Sender:   sender,
		Receiver: receiver,
		Amount:   amount,
	}
}
