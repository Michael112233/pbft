package core

import "github.com/michael112233/pbft/logger"

type Blockchain struct {
	Blocks             []*Block
	InitSequenceNumber int64
	LastSequenceNumber int64

	logger *logger.Logger
}

func NewBlockchain(sequenceNumber int64) *Blockchain {
	return &Blockchain{
		Blocks:             make([]*Block, 0),
		InitSequenceNumber: sequenceNumber,
		LastSequenceNumber: sequenceNumber,
		logger:             logger.NewLogger(0, "blockchain"),
	}
}

func (b *Blockchain) AddBlock(block *Block) {
	b.Blocks = append(b.Blocks, block)
	b.logger.Info("add block %d", block.SequenceNumber)
}

func (b *Blockchain) GetBlock(index int64) *Block {
	if index <= b.InitSequenceNumber || index >= b.LastSequenceNumber {
		b.logger.Error("index out of range: %d", index)
		return nil
	}
	return b.Blocks[index]
}

func (b *Blockchain) GetLastBlock() *Block {
	return b.Blocks[b.LastSequenceNumber]
}
