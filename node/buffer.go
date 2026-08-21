package node

import "github.com/michael112233/pbft/core"

type bufferedConsensusMessage struct {
	kind       bufferedConsensusMessageKind
	view       int64
	preprepare core.PreprepareMsg
	prepare    core.PrepareMsg
	commit     core.CommitMsg
	signature  []byte
}
type bufferedConsensusMessageKind uint8

const (
	bufferedPrePrepare bufferedConsensusMessageKind = iota + 1
	bufferedPrepare
	bufferedCommit
)

func (n *Node) replayBufferedMessagesForView(view int64) {
	buffered := n.drainBufferedMessagesForView(view)
	if len(buffered) == 0 {
		n.log.Info("No buffered consensus messages to replay for view %d", view)
		return
	}

	n.log.Info("Replaying %d buffered consensus messages for view %d", len(buffered), view)
	for _, msg := range buffered {
		switch msg.kind {
		case bufferedPrePrepare: //maybe async them
			n.HandlePrePrepare(msg.preprepare, msg.signature)
		case bufferedPrepare:
			n.HandlePrepare(msg.prepare, msg.signature)
		case bufferedCommit:
			n.HandleCommit(msg.commit)
		}
	}
}

func (n *Node) drainBufferedMessagesForView(view int64) []bufferedConsensusMessage {

	if len(n.bufferedMsgs) == 0 {
		return nil
	}

	replay := make([]bufferedConsensusMessage, 0)
	remaining := n.bufferedMsgs[:0]
	for _, msg := range n.bufferedMsgs {
		if msg.view == view {
			replay = append(replay, msg)
			continue
		} else if msg.view < view {
			n.log.Warn("buffer have lower view msgs")
		} else if msg.view > view {
			remaining = append(remaining, msg)
		}
	}
	if len(remaining) > 0 {
		n.log.Info("Still have %d buffered consensus messages for future views after draining for view %d", len(remaining), view)
	}
	n.bufferedMsgs = remaining
	return replay
}

func (n *Node) bufferConsensusMessage(msg bufferedConsensusMessage) {

	n.bufferedMsgs = append(n.bufferedMsgs, msg)

}
