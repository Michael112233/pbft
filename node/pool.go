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
	existsMap map[clientRequestKey]core.ClientMsgSignature
	delMap    map[clientRequestKey]struct{}
}

func NewPool() *Pool {
	return &Pool{
		existsMap: make(map[clientRequestKey]core.ClientMsgSignature),
		delMap:    make(map[clientRequestKey]struct{}),
	}
}

func (p *Pool) Add(msg core.ClientMsgSignature) bool {
	p.lock.Lock()
	defer p.lock.Unlock()
	key := makeClientRequestKey(msg.Data)
	if _, exists := p.existsMap[key]; !exists {
		if _, deleted := p.delMap[key]; !deleted {
			p.existsMap[key] = msg
			return true
		}
	}
	return false
}

func (p *Pool) Delete(msg core.ClientMsg) {
	p.lock.Lock()
	defer p.lock.Unlock()
	key := makeClientRequestKey(msg)
	delete(p.existsMap, key)
	p.delMap[key] = struct{}{}
}

func (p *Pool) PendingRequests() int {
	p.lock.RLock()
	defer p.lock.RUnlock()
	return len(p.existsMap)
}
