package core

import "time"

type Block struct {
	SequenceNumber    int64
	Transactions      []*Transaction
	Timestamp         int64
	ProposedTimestamp int64

	proposedLeader string
	committedNode  []string
}

func NewBlock(sequenceNumber int64, txs []*Transaction, leader string, proposedTimestamp int64) *Block {
	block := &Block{
		Timestamp:         time.Now().Unix(),
		ProposedTimestamp: proposedTimestamp,
		SequenceNumber:    sequenceNumber,
		Transactions:      txs,
		proposedLeader:    leader,
		committedNode:     make([]string, 0),
	}

	return block
}

func (b *Block) AddTransaction(txs []*Transaction) {
	b.Transactions = append(b.Transactions, txs...)
}

func (b *Block) AddCommittedNode(node string) {
	b.committedNode = append(b.committedNode, node)
}
