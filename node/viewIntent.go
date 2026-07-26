package node

import (
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

type ViewIntent struct {
	forView              int64
	mu                   sync.Mutex
	node                 ViewIntentNode
	changeViewIntentMsgs map[int64]map[int]struct{}
	log                  *logger.Logger
}

type ViewIntentNode interface {
	GetNodeID() int
	GetView() int64
	GetFNodes() int
	InitiateViewChange(view int64)
	BroadcastViewIntent(view int64)
}

func NewViewIntent(node ViewIntentNode, log *logger.Logger) *ViewIntent {
	return &ViewIntent{

		node:                 node,
		changeViewIntentMsgs: make(map[int64]map[int]struct{}),
		log:                  log,
	}
}

func (vi *ViewIntent) sendViewIntent(view int64) {
	currView := vi.node.GetView()
	if view != currView {
		if view < currView {
			vi.log.Warn("Ignoring sendViewIntent for view %d as it is less than current view %d", view, currView)
		} else if view > currView {
			vi.log.Warn("Ignoring sendViewIntent for view %d as it is greater than current view %d", view, currView)
		}
		return
	}

	vi.mu.Lock()
	if _, ok := vi.changeViewIntentMsgs[view]; !ok {
		vi.changeViewIntentMsgs[view] = make(map[int]struct{})
	}
	if _, ok := vi.changeViewIntentMsgs[view][vi.node.GetNodeID()]; !ok {
		vi.changeViewIntentMsgs[view][vi.node.GetNodeID()] = struct{}{}
	} else {
		vi.log.Warn("Node %d has already sent view intent for view %d, skipping", vi.node.GetNodeID(), view)
		vi.mu.Unlock()
		return
	}
	vi.log.Info("Node %d sending view intent for view %d", vi.node.GetNodeID(), view)
	go vi.node.BroadcastViewIntent(view)
	if len(vi.changeViewIntentMsgs[view]) == vi.node.GetFNodes()+1 {
		vi.mu.Unlock()
		vi.node.InitiateViewChange(view)
		return
	}
	vi.mu.Unlock()

}

func (vi *ViewIntent) receiveViewIntent(view int64, fromNode int) {
	currView := vi.node.GetView()
	if view != currView {
		if view < currView {
			vi.log.Warn("Ignoring receiveViewIntent for view %d as it is less than current view %d", view, currView)
		} else if view > currView {
			vi.log.Warn("Ignoring receiveViewIntent for view %d as it is greater than current view %d", view, currView)
		}
		return
	}

	vi.mu.Lock()
	if _, ok := vi.changeViewIntentMsgs[view]; !ok {
		vi.changeViewIntentMsgs[view] = make(map[int]struct{})
	}
	if _, ok := vi.changeViewIntentMsgs[view][fromNode]; !ok {
		vi.changeViewIntentMsgs[view][fromNode] = struct{}{}
	} else {
		vi.log.Warn("have already received view intent from node %d for view %d, skipping", fromNode, view)
		vi.mu.Unlock()
		return
	}
	vi.log.Info("received view intent from node %d for view %d", fromNode, view)
	if len(vi.changeViewIntentMsgs[view]) == vi.node.GetFNodes()+1 {
		// maybe broadcast from here as well and then stop timer in initiate view change
		vi.mu.Unlock()
		vi.node.InitiateViewChange(view)
		return
	}
	vi.mu.Unlock()
}

func (n *Node) BroadcastViewIntent(view int64) {
	intentMsg := core.IntentToChangeViewMsg{
		ViewNumber: view,
		From:       n.GetNodeID(),
	}
	pbMsg := transportpb.IntentToChangeViewToPB(intentMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal IntentToChangeView message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		// msg.To = othersIp
		go n.messageHub.Send(core.MsgIntentToChangeViewMessage, othersIp, intentMsg, signature)
	}

}

func (n *Node) HandleViewIntentMsg(intentMsg core.IntentToChangeViewMsg, signature []byte) {
	n.viewIntent.receiveViewIntent(intentMsg.ViewNumber, intentMsg.From)
}
func (n *Node) InitiateViewChange(view int64) {
	n.viewMu.Lock()
	defer n.viewMu.Unlock()
	if n.viewChangeRunning || n.forView > view {
		n.log.Warn("View change already running, skipping initiation in view intent")
		return
	}
	n.log.Info("Initiating view change for view %d in view intent", n.forView+1)
	n.periodicTimerManager.stopTimer()

	n.handleViewChangeTimeoutDummy()
}
