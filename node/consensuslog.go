package node

import "github.com/michael112233/pbft/core"

type slotKey struct {
	seqNum int64
}
type LogEntry struct {
	view                int64
	preprepare          *core.PreprepareMsgMini
	preprepareSignature []byte
	prepares            map[int]core.PrepareMsgSig
	commits             map[int][32]byte

	prepareSent bool
	commitSent  bool
	committed   bool
	executed    bool
}

type Log struct {
	low       int64
	high      int64
	maxSeqNum int64
	log       map[slotKey]*LogEntry
}

func NewLog() *Log {
	return &Log{
		low:  1,
		high: 2 * CHECKPOINT_INTERVAL,
		log:  make(map[slotKey]*LogEntry),
	}
}

func (l *Log) GetorCreateEntry(seqNum, view int64) (*LogEntry, bool) {
	// doesnt set view
	slotkey := slotKey{seqNum: seqNum}
	if slot, exists := l.log[slotkey]; exists {
		return slot, true
	}
	slot := &LogEntry{
		prepares: make(map[int]core.PrepareMsgSig),
		commits:  make(map[int][32]byte),
	}
	if seqNum > l.maxSeqNum {
		l.maxSeqNum = seqNum
	}
	l.log[slotkey] = slot
	return slot, false

}

// if something is already there, it will overwrite it, so be careful
func (l *Log) CreateEntry(seqNum int64) *LogEntry {
	slotkey := slotKey{seqNum: seqNum}
	slot := &LogEntry{

		prepares: make(map[int]core.PrepareMsgSig),
		commits:  make(map[int][32]byte),
	}
	if seqNum > l.maxSeqNum {
		l.maxSeqNum = seqNum
	}
	l.log[slotkey] = slot
	return slot
}

func (n *Node) slotPreprepare(slot *LogEntry, preprepareMsgMini *core.PreprepareMsgMini, preprepareSignature []byte, prepareSent bool) {
	slot.preprepare = preprepareMsgMini
	slot.preprepareSignature = preprepareSignature
	slot.prepareSent = prepareSent

}

func (l *Log) GetLogEntry(seqNum int64) (*LogEntry, bool) {
	slotkey := slotKey{seqNum: seqNum}
	if slot, exists := l.log[slotkey]; exists {
		return slot, true
	}
	return nil, false
}

func (l *Log) GetLogLen() int {
	return len(l.log)
}

func (l *Log) GCLog(stableCheckpointSeq int64) {
	for seq := l.low; seq <= stableCheckpointSeq; seq++ {
		slotkey := slotKey{seqNum: seq}
		delete(l.log, slotkey)
		// here can delete entries from pool referencing this seq number as well if needed
	}
	l.low = stableCheckpointSeq + 1
	l.high = l.low + 2*CHECKPOINT_INTERVAL
}

func (l *Log) RemoveLogEntriesAboveSeq(seqNum int64) {
	for seq := seqNum + 1; seq <= l.maxSeqNum; seq++ {
		slotkey := slotKey{seqNum: seq}
		delete(l.log, slotkey)
	}
	l.maxSeqNum = seqNum
}

func (n *Node) GCLog(stableCheckpointSeq int64) {
	n.consensusLog.GCLog(stableCheckpointSeq)
}
