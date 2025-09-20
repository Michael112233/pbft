package core

type Block struct {
	SequenceNumber int64
	Transactions   []*Transaction
	// PreparedMsgs   []*Message
	// CommittedMsgs  []*Message
}

func NewBlock(sequenceNumber int64) *Block {
	return &Block{
		SequenceNumber: sequenceNumber,
		Transactions:   make([]*Transaction, 0),
		// PreparedMsgs:   make([]*Message, 0),
		// CommittedMsgs:  make([]*Message, 0),
	}
}

func (b *Block) AddTransaction(txs []*Transaction) {
	b.Transactions = append(b.Transactions, txs...)
}

// func (b *Block) AddPreparedMsg(msgs []*Message) {
// 	b.PreparedMsgs = append(b.PreparedMsgs, msgs...)
// }

// func (b *Block) AddCommittedMsg(msgs []*Message) {
// 	b.CommittedMsgs = append(b.CommittedMsgs, msgs...)
// }
