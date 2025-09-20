package core

type Block struct {
	SequenceNumber int64
	Transactions   []*Transaction
	isGenesis      bool
	// PreparedMsgs   []*Message
	// CommittedMsgs  []*Message
}

func NewBlock(sequenceNumber int64, isGenesis bool) *Block {
	block := &Block{
		SequenceNumber: sequenceNumber,
		Transactions:   make([]*Transaction, 0),
		isGenesis:      isGenesis,
	}

	return block
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
