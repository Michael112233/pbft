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
	view     core.ViewID
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

func (p *Pool) AddBatch(reqs []core.ClientMsgSignature, digests [][32]byte, seqNum int64, view core.ViewID) {
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

// GCUpTo drops every request whose batch sequence number is <= stableSeq and returns
// how many were removed. Called from Node.GCLog when a checkpoint becomes stable.
//
// Safe because nothing reads those entries again: the exe loop only looks up
// lastExecuted+1 > stableSeq (a stable checkpoint implies lastExecuted >= stableSeq,
// either by local execution or PushExecutionMachine), preprepares/O-set slots below
// the new low watermark are rejected before AddBatch, and prepared certs (the only
// other reader, via buildPreparedCert) are only built for slots above the watermark.
//
// Keyed on the seqNum stored in the entry, not on the log slot, so entries orphaned by
// a view change (batch accepted at a seq the new view re-used for another batch) are
// collected too once the checkpoint passes that seq. A digest re-added at a higher seq
// keeps the higher seq and survives until that one is stable.

// could be reading 250-500 batches * batch size
// 500 limit as high water mark
func (p *Pool) GCUpTo(stableSeq int64) int {
	removed := 0
	for digest, data := range p.existsMap {
		if data.seqNum <= stableSeq {
			delete(p.existsMap, digest)
			removed++
		}
	}
	return removed
}

func (p *Pool) Len() int {
	return len(p.existsMap)
}
