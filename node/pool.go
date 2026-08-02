package node

import (
	"sync"

	"github.com/michael112233/pbft/core"
)

// func makeClientRequestKey(msg core.ClientMsg) clientRequestKey {
// 	return clientRequestKey{
// 		clientName: msg.ClientName,
// 		id:         msg.Id,
// 	}
// }

type Pool struct {
	lock      sync.RWMutex
	existsMap map[[32]byte]core.ClientMsgSignature
	delMap    map[[32]byte]int64
}

func NewPool() *Pool {
	return &Pool{
		existsMap: make(map[[32]byte]core.ClientMsgSignature),
		delMap:    make(map[[32]byte]int64),
	}
}

// func (p *Pool) AddforLeader(digest [32]byte, msg core.ClientMsgSignature, view int64) bool {
// 	p.lock.Lock()
// 	defer p.lock.Unlock()
// 	if data, exists := p.existsMap[digest]; !exists {
// 		if _, deleted := p.delMap[digest]; !deleted {
// 			p.existsMap[digest] = PoolData{
// 				msg:          msg,
// 				proposalView: view,
// 			}
// 			return true
// 		}
// 	} else {
// 		if view > data.proposalView {
// 			data.proposalView = view
// 			p.existsMap[digest] = data
// 			return true
// 		} else {
// 			return false
// 		}
// 	}
// 	return false
// }

// second boolean is for executed
func (p *Pool) Add(digest [32]byte, msg core.ClientMsgSignature) (bool, bool) {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, exists := p.existsMap[digest]; !exists {
		if _, deleted := p.delMap[digest]; !deleted {
			p.existsMap[digest] = msg
			return true, false
		} else {
			return false, true
		}
	}
	return false, false
}

func (p *Pool) Delete(digest [32]byte, seq int64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	// key := makeClientRequestKey(msg)
	delete(p.existsMap, digest)
	p.delMap[digest] = seq
}

func (p *Pool) PendingRequests() int {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return len(p.existsMap)
}

func (p *Pool) Get(digest [32]byte) (core.ClientMsgSignature, bool, bool) {
	p.lock.RLock()
	defer p.lock.RUnlock()
	msg, exists := p.existsMap[digest]

	_, executed := p.delMap[digest]
	return msg, exists, executed
}

// func (p *Pool) GetforLeader(digest [32]byte, view int64) (core.ClientMsgSignature, bool, bool) {
// 	p.lock.Lock()
// 	defer p.lock.Unlock()
// 	msg, exists := p.existsMap[digest]
// 	if exists {
// 		if view > msg.proposalView {
// 			msg.proposalView = view
// 			p.existsMap[digest] = msg
// 		}
// 	}

//		_, executed := p.delMap[digest]
//		return msg.msg, exists, executed
//	}
func (p *Pool) GetMultiple(digests [][32]byte) []core.MissingClientData {
	p.lock.RLock()
	defer p.lock.RUnlock()
	// var msgs []core.ClientMsgSignature
	msgs := make([]core.MissingClientData, 0, len(digests))
	for _, digest := range digests {
		if msg, exists := p.existsMap[digest]; exists {
			msgs = append(msgs, core.MissingClientData{
				Digest: digest,
				Msg:    msg,
			})
		}
	}
	return msgs
}

func (p *Pool) AddMultiple(msgs []core.MissingClientData) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	added := false
	for _, msg := range msgs {
		if _, exists := p.existsMap[msg.Digest]; !exists {
			if _, deleted := p.delMap[msg.Digest]; !deleted {
				p.existsMap[msg.Digest] = msg.Msg
				added = true
			}
		}
	}
	return added
}
func (p *Pool) GCDelMap(seq int64) {
	p.lock.Lock()
	defer p.lock.Unlock()
	for digest, executedSeq := range p.delMap {
		if executedSeq < seq {
			delete(p.delMap, digest)
		}
	}
}
