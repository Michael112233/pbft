package node

import "github.com/michael112233/pbft/core"

type bufferedConsensusMessage struct {
	kind       bufferedConsensusMessageKind
	view       core.ViewID
	preprepare core.PreprepareMsg
	prepare    core.PrepareMsg
	commit     core.CommitMsg
	signature  []byte
	// buffered while this node was not in a view change: it arrived before both the
	// NewView and the f+1 ViewChanges that would have moved this node
	outsideViewChange bool
}
type bufferedConsensusMessageKind uint8

const (
	bufferedPrePrepare bufferedConsensusMessageKind = iota + 1
	bufferedPrepare
	bufferedCommit
)

func (n *Node) replayBufferedMessagesForView(view core.ViewID) {
	buffered := n.drainBufferedMessagesForView(view)
	if len(buffered) == 0 {
		n.log.Info("No buffered consensus messages to replay for view (%d,%d)", view.Generation, view.Counter)
		return
	}

	outside := 0
	for _, msg := range buffered {
		if msg.outsideViewChange {
			outside++
		}
	}
	n.log.Info("Replaying %d buffered consensus messages for view (%d,%d), %d of them buffered outside a view change", len(buffered), view.Generation, view.Counter, outside)
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

func (n *Node) drainBufferedMessagesForView(view core.ViewID) []bufferedConsensusMessage {

	if len(n.bufferedMsgs) == 0 {
		return nil
	}

	replay := make([]bufferedConsensusMessage, 0)
	remaining := n.bufferedMsgs[:0]
	for _, msg := range n.bufferedMsgs {
		if msg.view == view {
			replay = append(replay, msg)
			continue
		} else if msg.view.LessThan(view) {
			// pruneBufferedBelow runs on every SetForViewID, so this should not happen
			n.log.Warn("buffer have lower view msgs")
		} else if msg.view.GreaterThan(view) {
			remaining = append(remaining, msg)
		}
	}
	if len(remaining) > 0 {
		n.log.Info("Still have %d buffered consensus messages for future views after draining for view (%d,%d)", len(remaining), view.Generation, view.Counter)
	}
	n.bufferedMsgs = remaining
	return replay
}

// bufferFutureViewMessage keeps a PrePrepare, Prepare or Commit for a view above the
// installed one until that view is installed; replayBufferedMessagesForView then runs
// it through its handler like a late arrival. It is kept whether or not this node is
// in a view change: Send is one goroutine per message (asyncBroadCast), and the event
// loop reads NewViews, ViewChanges and consensus messages from separate channels, so
// a message of the new view can be handled before the NewView or the ViewChanges that
// move this node there. Nothing retransmits, so a dropped one is lost for good.
//
// Safe because nothing reads the buffer before the view is installed. Deliver already
// checked the signature (and From, for Prepare and Commit), and replay runs after
// HandleNewView / newview have installed the O-set, through the same view, watermark
// and digest checks as a live message.
//
// A message below forView is dropped: HandleNewView rejects a NewView below forView,
// so that view is never installed here. Returns whether the message was kept.
//
// TODO(byzantine): the buffer is unbounded. A Byzantine sender can fill it with
// messages for far-future views, and buffered PrePrepares carry request bodies. Cap
// entries per (view, sender), e.g. max_inflight_seq x 3 kinds, and keep only the
// lowest views above forView. Not needed while faults are crashes only.
func (n *Node) bufferFutureViewMessage(msg bufferedConsensusMessage) bool {
	forView := n.GetForViewID()
	if msg.view.LessThan(forView) {
		// if greater than view but less than for view so drop as nv less than for view not accepted
		n.log.Debug("Dropped consensus message kind %d for view (%d,%d) below for view (%d,%d)", msg.kind, msg.view.Generation, msg.view.Counter, forView.Generation, forView.Counter)
		return false
	}
	msg.outsideViewChange = !n.viewChangeRunning
	n.bufferedMsgs = append(n.bufferedMsgs, msg)
	return true
}

// pruneBufferedBelow drops buffered messages for views below forView, which can no
// longer be installed. Without it they stay until the next install, and a cascade
// that never installs a view keeps every one of them.
func (n *Node) pruneBufferedBelow(forView core.ViewID) {
	kept := n.bufferedMsgs[:0]
	for _, msg := range n.bufferedMsgs {
		if !msg.view.LessThan(forView) {
			kept = append(kept, msg)
		}
	}
	dropped := len(n.bufferedMsgs) - len(kept)
	if dropped == 0 {
		return
	}
	// clear the tail so the dropped messages (PrePrepares carry bodies) can be collected
	clear(n.bufferedMsgs[len(kept):])
	n.bufferedMsgs = kept
	n.log.Info("Pruned %d buffered consensus messages below for view (%d,%d); %d still buffered", dropped, forView.Generation, forView.Counter, len(kept))
}
