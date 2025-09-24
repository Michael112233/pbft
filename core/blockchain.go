package core

import (
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/result"
)

type Blockchain struct {
	Blocks []*Block

	logger          *logger.Logger
	addMutex        sync.Mutex
	cfg             *config.Config
	FinishInjecting sync.WaitGroup
}

var Chain *Blockchain

func NewBlockchain(cfg *config.Config) {
	log := logger.NewLogger(0, "blockchain")
	log.Info("blockchain initialized")
	Chain = &Blockchain{
		Blocks:          make([]*Block, 0),
		logger:          log,
		cfg:             cfg,
		FinishInjecting: sync.WaitGroup{},
	}
}

func (b *Blockchain) AddBlock(block *Block) {
	b.addMutex.Lock()
	defer b.addMutex.Unlock()

	if existingBlock, ok := b.GetBlock(block.SequenceNumber); ok {
		existingBlock.AddCommittedNode(block.committedNode[0])
		b.logger.Info("current committed: %v to block %d", existingBlock.committedNode, block.SequenceNumber)
	} else {
		b.Blocks = append(b.Blocks, block)
		b.logger.Info("add block %d, who committed: %v, who proposed: %s", block.SequenceNumber, block.committedNode, block.proposedLeader)
		result.AddCommittedTransactionNum(int64(len(block.Transactions)))
		if b.cfg.MaxTxNum == result.GetCommittedTransactionNum() {
			b.logger.Info("finish injecting: %d=%d", b.cfg.MaxTxNum, result.GetCommittedTransactionNum())
		}
		result.PrintResult()
	}
}

func (b *Blockchain) GetBlock(index int64) (*Block, bool) {
	for _, block := range b.Blocks {
		if block.SequenceNumber == index {
			return block, true
		}
	}
	return nil, false
}

func (b *Blockchain) GetLastBlock() *Block {
	b.addMutex.Lock()
	defer b.addMutex.Unlock()
	return b.Blocks[len(b.Blocks)-1]
}
