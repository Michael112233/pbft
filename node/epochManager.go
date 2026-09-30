package node

import (
	"sort"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

type EpochNode interface {
	GetNodeID() int
	QuorumSize() int
	NodeCount() int
	asyncBroadCast(msgType string, msg interface{}, signature []byte)
	signEpochAggregateMsg(msg core.EpochAggregateMsgMini) ([]byte, error)
	GetForViewID() core.ViewID
	assert(condition bool, message string, args ...interface{})
	SendLearningDataToAgent(epoch uint64, currAction core.Action, throughput float64, proposalRate float64, vcrRate float64, inactiveNodes uint8)
	GetCurrAction() core.Action
	stopEpochTimer()
	startAggregateGraceTimer()
	stopAggregateGraceTimer()
	storeAwareAggregate(gen uint64, sigs []core.EpochDataMsgSig)
}
type EpochManager struct {
	epochMsgSig map[uint64]map[int]core.EpochDataMsgSig
	// aggregateSent guards flushAggregate: the aggregate for a generation goes
	// out exactly once, from either the all-n path or the grace timer.
	aggregateSent map[uint64]bool
	// graceGen is the generation the running grace timer was started for.
	graceGen uint64
	node     EpochNode
	log      *logger.Logger
}

func NewEpochManager(log *logger.Logger, node EpochNode) *EpochManager {
	return &EpochManager{
		epochMsgSig:   make(map[uint64]map[int]core.EpochDataMsgSig),
		aggregateSent: make(map[uint64]bool),
		node:          node,
		log:           log,
	}
}

func (em *EpochManager) CreateEpochMsg(generation uint64) *core.EpochDataMsg {
	epochMsg := &core.EpochDataMsg{
		EpochGeneration: generation,
		From:            em.node.GetNodeID(),
	}
	return epochMsg
}

// HandleEpochDataMsg collects epoch data on the aggregator. At 2f+1 messages it
// starts the grace timer; the aggregate goes out when all n arrived or the
// timer fires, whichever comes first.
func (em *EpochManager) HandleEpochDataMsg(msg core.EpochDataMsg, signature []byte) {
	// later will add gen check
	forView := em.node.GetForViewID()
	em.node.assert(msg.EpochGeneration <= forView.Generation, "Received epoch data message for generation %d which is greater than my for view generation %d", msg.EpochGeneration, forView.Generation)
	if msg.EpochGeneration != forView.Generation {
		em.log.Info("Received epoch data message for generation %d which is not equal to my for view generation %d, ignoring", msg.EpochGeneration, forView.Generation)
		return
	}
	gen := msg.EpochGeneration
	if em.aggregateSent[gen] {
		return
	}

	if _, exists := em.epochMsgSig[gen]; !exists {
		em.epochMsgSig[gen] = make(map[int]core.EpochDataMsgSig)
	}
	em.epochMsgSig[gen][msg.From] = core.EpochDataMsgSig{
		EpochDataMsg: msg,
		Signature:    append([]byte(nil), signature...),
	}

	count := len(em.epochMsgSig[gen])
	switch {
	case count >= em.node.NodeCount():
		em.flushAggregate(gen)
	case count == em.node.QuorumSize():
		em.log.Info("Epoch data quorum reached for generation %d; starting aggregate grace timer", gen)
		em.graceGen = gen
		em.node.startAggregateGraceTimer()
	}
}

// onGraceTimeout sends the aggregate for the generation the grace timer was
// started for, if the node is still in it.
// we do stop timer at flush so no lingering grace timer
// timer only start at quorum so at timeout we already have quorum
func (em *EpochManager) onGraceTimeout() {
	gen := em.graceGen
	if gen != em.node.GetForViewID().Generation {
		// right now its defensive as node 4 cant move up gen, node 4 is the one which initiate gen move
	
		em.log.Info("Aggregate grace timer fired for generation %d but node moved on; dropping", gen)
		return
	}
	em.log.Info("Aggregate grace timer fired for generation %d with %d/%d epoch data messages", gen, len(em.epochMsgSig[gen]), em.node.NodeCount())
	em.flushAggregate(gen)
}

// flushAggregate builds, signs and broadcasts the aggregate for gen once.

		// in fcrash will have grace timer as dead node not send epoch msg
		// in farnode grace timeer based flush not happen as not that far
		// inndfcrash grace timer only starts when all 3 (quorum) epoch is received and then we wait 500ms to send the aggregate as dead node will never reply
		// in ndelay it shouldnt fire as 170ms uniform so all arrive at same time
func (em *EpochManager) flushAggregate(gen uint64) {
	if em.aggregateSent[gen] || len(em.epochMsgSig[gen]) < em.node.QuorumSize() {


		return
	}
	em.aggregateSent[gen] = true
	em.node.stopAggregateGraceTimer()

	epochMsgSigs := em.epochMsgSig[gen]
	epochDataMsgSigs := make([]core.EpochDataMsgSig, 0, len(epochMsgSigs))
	for _, epochMsgSig := range epochMsgSigs {
		epochDataMsgSigs = append(epochDataMsgSigs, epochMsgSig)
	}
	sort.Slice(epochDataMsgSigs, func(i, j int) bool {
		return epochDataMsgSigs[i].EpochDataMsg.From < epochDataMsgSigs[j].EpochDataMsg.From
	})

	currAction := em.node.GetCurrAction()
	epochAggregateMsg := core.EpochAggregateMsg{
		EpochGeneration:  gen,
		From:             em.node.GetNodeID(),
		EpochData:        core.EpochData{Throughput: 0, ProposalInterval: 0, VCRate: 0, InactiveNodes: 0}, // Placeholder values
		CurrentAction:    currAction,
		EpochDataMsgSigs: epochDataMsgSigs, // epochdatamsgs have rtt vector
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
	em.log.Info("Epoch aggregate message created for generation %d with %d epoch data messages", gen, len(epochDataMsgSigs))
	em.node.asyncBroadCast(core.MsgEpochAggregateMessage, epochAggregateMsg, signature)
	em.node.stopEpochTimer()
	// The aggregator never receives its own broadcast, so it stores the matrix here;
	// it is applied when the node moves to the next generation.
	em.node.storeAwareAggregate(gen, epochDataMsgSigs)
	go em.node.SendLearningDataToAgent(gen, currAction, epochAggregateMsg.EpochData.Throughput, epochAggregateMsg.EpochData.ProposalInterval, epochAggregateMsg.EpochData.VCRate, uint8(epochAggregateMsg.EpochData.InactiveNodes))
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
	for g := range em.aggregateSent {
		if g < gen {
			delete(em.aggregateSent, g)
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
	// TODO(safety): the embedded epoch data signatures are not verified, and the
	// aggregate signature does not cover them, so the aggregator can forge vectors.
	em.node.storeAwareAggregate(msg.EpochGeneration, msg.EpochDataMsgSigs)
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
