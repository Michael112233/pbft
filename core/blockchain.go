package core

import "github.com/michael112233/pbft/logger"

type Blockchain struct {
	Blocks             []*Block
	InitSequenceNumber int64
	LastSequenceNumber int64

	logger *logger.Logger
}

func NewBlockchain() *Blockchain {
	log := logger.NewLogger(0, "blockchain")
	log.Info("blockchain initialized")
	return &Blockchain{
		Blocks:             make([]*Block, 0),
		InitSequenceNumber: -1,
		LastSequenceNumber: -1,
		logger:             log,
	}
}

func (b *Blockchain) AddBlock(block *Block) {
	b.Blocks = append(b.Blocks, block)
	if len(b.Blocks) == 1 {
		b.InitSequenceNumber = block.SequenceNumber
	}
	b.LastSequenceNumber = block.SequenceNumber
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
