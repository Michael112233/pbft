package core

type Block struct {
	SequenceNumber int64
	Transactions   []*Transaction

	proposedLeader string
	committedNode  []string
}

func NewBlock(sequenceNumber int64, txs []*Transaction, leader string) *Block {
	block := &Block{
		SequenceNumber: sequenceNumber,
		Transactions:   txs,
		proposedLeader: leader,
		committedNode:  make([]string, 0),
	}

	return block
}

func (b *Block) AddTransaction(txs []*Transaction) {
	b.Transactions = append(b.Transactions, txs...)
}

func (b *Block) AddCommittedNode(node string) {
	b.committedNode = append(b.committedNode, node)
}
