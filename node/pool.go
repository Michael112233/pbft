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

// AddBatchIfNewer is AddBatch for bodies taken from PrePrepares the node may not act
// on (view change running, other view). It never lowers an entry's seqNum, and it
// skips a batch whose request and digest counts differ instead of panicking.
//
// Plain AddBatch would be unsafe here because GCUpTo deletes by the stored seqNum:
//  1. Request R is proposed at seq 100 in view 5; view 5 dies before R prepares anywhere.
//  2. The client retries and the view-6 leader proposes R at seq 300. Pool: R -> seq 300.
//  3. The old view-5 PrePrepare for seq 100 reaches this node late (in flight or
//     buffered). AddBatch would overwrite R -> seq 100.
//  4. Checkpoint 250 stabilizes; GCUpTo(250) deletes R because 100 <= 250.
//  5. Seq 300 commits, GetBatch fails ("some req for batch not found") and execution
//     halts at seq 300 permanently.
//
// Skipping the lower-seq write at step 3 prevents this.
func (p *Pool) AddBatchIfNewer(reqs []core.ClientMsgSignature, digests [][32]byte, seqNum int64, view core.ViewID) {
	if len(reqs) != len(digests) {
		p.log.Error("AddBatchIfNewer: %d requests but %d digests for seq %d, skipping", len(reqs), len(digests), seqNum)
		return
	}
	for i, req := range reqs {
		if data, exists := p.existsMap[digests[i]]; exists && data.seqNum >= seqNum {
			continue
		}
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

// claude once suggest bucket version to sped up gc Better: drop a whole map
// Bucket the pool by checkpoint window, one map per 250-seq window (bucket = (seq-1)/CHECKPOINT_INTERVAL).
// but will need to measure if it isworth it
func (p *Pool) Len() int {
	return len(p.existsMap)
}
