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

func (p *Pool) Add(digest [32]byte, msg core.ClientMsgSignature) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	if _, exists := p.existsMap[digest]; !exists {
		if _, deleted := p.delMap[digest]; !deleted {
			p.existsMap[digest] = msg
			return true
		}
	}
	return false
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
