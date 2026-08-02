package node

import "sync"

type DuplicationMapData struct {
	view int64
	seq  int64
}
type executedTombstone struct {
	digest [32]byte
	seq    int64
}

type DuplicationMap struct {
	lock     sync.RWMutex
	data     map[[32]byte]DuplicationMapData
	delQueue []executedTombstone
	delHead  int
}

func NewDuplicationMap() *DuplicationMap {
	return &DuplicationMap{
		data: make(map[[32]byte]DuplicationMapData),
	}
}

func (dm *DuplicationMap) Add(digest [32]byte, view int64, seq int64) bool {
	dm.lock.Lock()
	defer dm.lock.Unlock()
	if data, exists := dm.data[digest]; !exists {
		dm.data[digest] = DuplicationMapData{
			view: view,
			seq:  seq,
		}
		return true
	} else {
		if view > data.view {
			data.view = view
			data.seq = seq
			dm.data[digest] = data
			return true
		} else {
			return false
		}

	}
}

func (dm *DuplicationMap) AddNewView(digest [32]byte, view int64, seq int64) {
	dm.lock.Lock()
	defer dm.lock.Unlock()
	if data, exists := dm.data[digest]; !exists {
		dm.data[digest] = DuplicationMapData{
			view: view,
			seq:  seq,
		}
	} else {
		if view > data.view {
			data.view = view
			data.seq = seq
			dm.data[digest] = data
		}
	}
}

func (dm *DuplicationMap) Delete(digest [32]byte, seq int64) {
	// dm.lock.Lock()
	// defer dm.lock.Unlock()
	// executed := executedTombstone{
	// 	digest: digest,
	// 	seq:    seq,
	// }
	// dm.delQueue = append(dm.delQueue, executed)
}

// will need in order call for this rn post action is go routine
func (dm *DuplicationMap) GarbageCollect(cutoff int64) {
	dm.lock.Lock()
	defer dm.lock.Unlock()

	for dm.delHead < len(dm.delQueue) {
		item := dm.delQueue[dm.delHead]
		if item.seq >= cutoff {
			break
		}

		if data, exists := dm.data[item.digest]; exists {
			if data.seq <= item.seq {
				delete(dm.data, item.digest)
			}
		}
		dm.delHead++
	}

	// Occasionally compact the queue backing array.
	if dm.delHead > 100_000 && dm.delHead*2 >= len(dm.delQueue) {
		dm.delQueue = append(
			[]executedTombstone(nil),
			dm.delQueue[dm.delHead:]...,
		)
		dm.delHead = 0
	}
}

func (dm *DuplicationMap) oldGC(view int64) {
	dm.lock.Lock()
	defer dm.lock.Unlock()

	for digest, data := range dm.data {

		if data.view < view {
			delete(dm.data, digest)
		}
	}
}
