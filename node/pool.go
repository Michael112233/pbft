package node

import (
	"fmt"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
)

type PoolData struct {
	req      core.ClientMsgSignature
	executed bool
	seqNum   int64
	view     int64
}
type Pool struct {
	existsMap map[[32]byte]PoolData
	log       *logger.Logger
}

func NewPool(log *logger.Logger) *Pool {
	return &Pool{
		existsMap: make(map[[32]byte]PoolData),
		log:       log,
	}
}

func (p *Pool) AddBatch(reqs []core.ClientMsgSignature, digests [][32]byte, seqNum int64, view int64) {
	for i, req := range reqs {
		p.existsMap[digests[i]] = PoolData{
			req:      req,
			executed: false,
			seqNum:   seqNum,
			view:     view,
		}
	}
}

func (p *Pool) GetBatch(digests [][32]byte) ([]core.ClientMsgSignature, bool) {
	reqs := make([]core.ClientMsgSignature, len(digests))
	for i, digest := range digests {
		if data, exists := p.existsMap[digest]; exists {
			reqs[i] = data.req
		} else {
			return nil, false
		}
	}
	return reqs, true
}

func (p *Pool) MarkExecuted(digests [][32]byte) error {
	for _, digest := range digests {
		if data, exists := p.existsMap[digest]; exists {
			if data.executed {
				return fmt.Errorf("request with digest %x already marked as executed", digest)
			}
			data.executed = true
			p.existsMap[digest] = data
		} else {
			return fmt.Errorf("request with digest %x not found in pool", digest)
		}
	}
	return nil
}
