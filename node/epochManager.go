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

func (em *EpochManager) CreateEpochMsg() *core.EpochDataMsg {
	epochMsg := &core.EpochDataMsg{
		EpochGeneration: 1, // This should be set to the current epoch generation
		From:            em.node.GetNodeID(),
	}
	return epochMsg
}

func (em *EpochManager) HandleEpochDataMsg(msg core.EpochDataMsg, signature []byte) {
	// later will add gen check
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
			EpochData:        core.EpochData{Throughput: 0, ProposalInterval: 0, InactiveNodes: 0}, // Placeholder values
			EpochDataMsgSigs: epochDataMsgSigs,
		}
		epochAggregateMsgMini := core.EpochAggregateMsgMini{
			EpochGeneration: epochAggregateMsg.EpochGeneration,
			From:            epochAggregateMsg.From,
			EpochData:       epochAggregateMsg.EpochData,
		}
		signature, err := em.node.signEpochAggregateMsg(epochAggregateMsgMini)
		if err != nil {
			em.log.Error("Failed to sign epoch aggregate message: %v", err)
			return
		}
		em.log.Info("Epoch aggregate message created for generation %d", msg.EpochGeneration)
		em.node.asyncBroadCast(core.MsgEpochAggregateMessage, epochAggregateMsg, signature)
	}
}

func (em *EpochManager) HandleEpochAggregateMsg(msg core.EpochAggregateMsg, _ []byte) {
	em.log.Info("Epoch aggregate message received from node %d for generation %d", msg.From, msg.EpochGeneration)
}

func (n *Node) CreateEpochMsg() *core.EpochDataMsg {
	return n.epochManager.CreateEpochMsg()
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
