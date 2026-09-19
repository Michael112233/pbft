package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

type EpochNode interface {
	GetNodeID() int
	QuorumSize() int
	asyncBroadCast(msgType string, msg interface{}, signature []byte)
	signEpochAggregateMsg(msg core.EpochAggregateMsgMini) ([]byte, error)
	GetForViewID() core.ViewID
	assert(condition bool, message string, args ...interface{})
	SendLearningDataToAgent(epoch uint64, currAction core.Action, throughput float64, proposalRate float64, vcrRate float64, inactiveNodes uint8)
	GetCurrAction() core.Action
	stopEpochTimer()
}
type EpochManager struct {
	epochMsgSig map[uint64]map[int]core.EpochDataMsgSig
	node        EpochNode
	log         *logger.Logger
}

func NewEpochManager(log *logger.Logger, node EpochNode) *EpochManager {
	return &EpochManager{
		epochMsgSig: make(map[uint64]map[int]core.EpochDataMsgSig),
		node:        node,
		log:         log,
	}
}

func (em *EpochManager) CreateEpochMsg(generation uint64) *core.EpochDataMsg {
	epochMsg := &core.EpochDataMsg{
		EpochGeneration: generation,
		From:            em.node.GetNodeID(),
	}
	return epochMsg
}

func (em *EpochManager) HandleEpochDataMsg(msg core.EpochDataMsg, signature []byte) {
	// later will add gen check
	forView := em.node.GetForViewID()
	currAction := em.node.GetCurrAction()
	em.node.assert(msg.EpochGeneration <= forView.Generation, "Received epoch data message for generation %d which is greater than my for view generation %d", msg.EpochGeneration, forView.Generation)
	if msg.EpochGeneration != forView.Generation {
		em.log.Info("Received epoch data message for generation %d which is not equal to my for view generation %d, ignoring", msg.EpochGeneration, forView.Generation)
		return
	}

	if _, exists := em.epochMsgSig[msg.EpochGeneration]; !exists {
		em.epochMsgSig[msg.EpochGeneration] = make(map[int]core.EpochDataMsgSig)
	}
	em.epochMsgSig[msg.EpochGeneration][msg.From] = core.EpochDataMsgSig{
		EpochDataMsg: msg,
		Signature:    append([]byte(nil), signature...),
	}

	if len(em.epochMsgSig[msg.EpochGeneration]) == em.node.QuorumSize() {
		epochMsgSigs := em.epochMsgSig[msg.EpochGeneration]
		epochDataMsgSigs := make([]core.EpochDataMsgSig, 0, len(epochMsgSigs))
		for _, epochMsgSig := range epochMsgSigs {
			epochDataMsgSigs = append(epochDataMsgSigs, epochMsgSig)
		}

		epochAggregateMsg := core.EpochAggregateMsg{
			EpochGeneration:  msg.EpochGeneration,
			From:             em.node.GetNodeID(),
			EpochData:        core.EpochData{Throughput: 0, ProposalInterval: 0, VCRate: 0, InactiveNodes: 0}, // Placeholder values
			CurrentAction:    currAction,
			EpochDataMsgSigs: epochDataMsgSigs,
		}
		epochAggregateMsgMini := core.EpochAggregateMsgMini{
			EpochGeneration: epochAggregateMsg.EpochGeneration,
			From:            epochAggregateMsg.From,
			EpochData:       epochAggregateMsg.EpochData,
			CurrentAction:   epochAggregateMsg.CurrentAction,
		}
		signature, err := em.node.signEpochAggregateMsg(epochAggregateMsgMini)
		if err != nil {
			em.log.Error("Failed to sign epoch aggregate message: %v", err)
			return
		}
		em.log.Info("Epoch aggregate message created for generation %d", msg.EpochGeneration)
		em.node.asyncBroadCast(core.MsgEpochAggregateMessage, epochAggregateMsg, signature)
		em.node.stopEpochTimer()
		go em.node.SendLearningDataToAgent(msg.EpochGeneration, em.node.GetCurrAction(), epochAggregateMsg.EpochData.Throughput, epochAggregateMsg.EpochData.ProposalInterval, epochAggregateMsg.EpochData.VCRate, uint8(epochAggregateMsg.EpochData.InactiveNodes))
	}
}

// GCBelow drops the collected epoch data for every generation < gen. Called when the
// node moves to generation gen. Safe because HandleEpochDataMsg only stores and reads
// entries for forView.Generation and ignores any lower generation, and generations
// never decrease, so nothing can touch those entries again.
func (em *EpochManager) GCBelow(gen uint64) {
	for g := range em.epochMsgSig {
		if g < gen {
			delete(em.epochMsgSig, g)
		}
	}
}

func (em *EpochManager) HandleEpochAggregateMsg(msg core.EpochAggregateMsg, _ []byte) {
	// em.log.Info("Epoch aggregate message received from node %d for generation %d", msg.From, msg.EpochGeneration)
	// it could be less if caught up from amplification but should not happen for our setup
	// greater is again not ordinary that mean node out of sync
	forView := em.node.GetForViewID()
	currAction := em.node.GetCurrAction()
	if msg.EpochGeneration != forView.Generation {
		em.log.Info("Received epoch aggregate message for generation %d which is not equal to my for view generation %d, ignoring", msg.EpochGeneration, forView.Generation)
		return
	}
	em.node.assert(msg.CurrentAction == currAction, "Received epoch aggregate message with action %v which does not match current action %v", msg.CurrentAction, currAction)
	// my epoch may not have expired and may not have send dat so at this point can also stop timer
	em.node.stopEpochTimer()
	go em.node.SendLearningDataToAgent(msg.EpochGeneration, currAction, msg.EpochData.Throughput, msg.EpochData.ProposalInterval, msg.EpochData.VCRate, uint8(msg.EpochData.InactiveNodes))

}

func (n *Node) CreateEpochMsg(generation uint64) *core.EpochDataMsg {
	return n.epochManager.CreateEpochMsg(generation)
}

func (n *Node) HandleEpochDataMsg(msg core.EpochDataMsg, signature []byte) {
	n.epochManager.HandleEpochDataMsg(msg, signature)
}

func (n *Node) HandleEpochAggregateMsg(msg core.EpochAggregateMsg, signature []byte) {
	n.epochManager.HandleEpochAggregateMsg(msg, signature)
}

func (n *Node) signEpochAggregateMsg(msg core.EpochAggregateMsgMini) ([]byte, error) {
	payloadBytes, err := marshalDeterministic(transportpb.EpochAggregateMsgMiniToPB(msg))
	if err != nil {
		return nil, err
	}
	return crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey()), nil
}
