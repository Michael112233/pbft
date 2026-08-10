package node

import (
	"sort"
	"sync"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/transportpb"
)

type EpochAggData struct {
	throughput   float64
	proposalRate float64
}
type EpochAggregator struct {
	mu          sync.Mutex
	node        EpochAggregatorNode
	log         *logger.Logger
	epochAggLog map[int64]map[int]EpochAggData
}
type EpochAggregatorNode interface {
	GetNodeID() int
	GetCurrentEpoch() int64
	BroadcastEpochAggData(nodeId int, epoch int64, throughput float64, proposalRate float64)
	GetNumberOfNodes() int
	SendEpochDataToAgent(epoch int64, throughput float64, proposalRate float64)
}

func NewEpochAggregator(node EpochAggregatorNode, log *logger.Logger) *EpochAggregator {
	return &EpochAggregator{
		node:        node,
		log:         log,
		epochAggLog: make(map[int64]map[int]EpochAggData),
	}
}

func (ea *EpochAggregator) ReceiveEpochData(epoch int64, nodeID int, throughput float64, proposalRate float64) {
	currentEpoch := ea.node.GetCurrentEpoch()
	if epoch != currentEpoch {
		ea.log.Error("Received epoch data for epoch %d, but current epoch is %d. Ignoring.", epoch, currentEpoch)
		return
	}
	ea.mu.Lock()
	defer ea.mu.Unlock()

	if _, exists := ea.epochAggLog[epoch]; !exists {
		ea.epochAggLog[epoch] = make(map[int]EpochAggData)
	}

	ea.epochAggLog[epoch][nodeID] = EpochAggData{
		throughput:   throughput,
		proposalRate: proposalRate,
	}
	ea.log.Info("Received epoch data from node %d for epoch %d: throughput=%.2f, proposalRate=%.2f", nodeID, epoch, throughput, proposalRate)
	ea.log.FeatureInfo("Received epoch data from node %d for epoch %d: throughput=%.2f, proposalRate=%.2f", nodeID, epoch, throughput, proposalRate)
	if len(ea.epochAggLog[epoch]) == ea.node.GetNumberOfNodes() {
		ea.log.Info("All nodes have sent epoch data for epoch %d. Aggregating data.", epoch)
		ea.log.FeatureInfo("All nodes have sent epoch data for epoch %d. Aggregating data.", epoch)
		totalThroughput := 0.0
		totalProposalRate := 0.0
		nodeIds := make([]int, 0, len(ea.epochAggLog[epoch]))
		for nodeID := range ea.epochAggLog[epoch] {
			nodeIds = append(nodeIds, nodeID)
		}
		sort.Ints(nodeIds)
		for _, nodeID := range nodeIds {
			data := ea.epochAggLog[epoch][nodeID]
			totalThroughput += data.throughput
			totalProposalRate += data.proposalRate
		}

		avgThroughput := totalThroughput / float64(len(ea.epochAggLog[epoch]))
		avgProposalRate := totalProposalRate / float64(len(ea.epochAggLog[epoch]))
		ea.log.Info("Aggregated epoch data for epoch %d: avgThroughput=%.2f, avgProposalRate=%.2f", epoch, avgThroughput, avgProposalRate)
		ea.log.FeatureInfo("Aggregated epoch data for epoch %d: avgThroughput=%.2f, avgProposalRate=%.2f", epoch, avgThroughput, avgProposalRate)
		// go ea.node.SendEpochDataToAgent(epoch, avgThroughput, avgProposalRate)

	}
}

func (ea *EpochAggregator) SendandEnterEpochData(epoch int64, throughput float64, proposalRate float64) {
	nodeID := ea.node.GetNodeID()
	ea.ReceiveEpochData(epoch, nodeID, throughput, proposalRate)
	ea.node.BroadcastEpochAggData(nodeID, epoch, throughput, proposalRate)
}

func (n *Node) GetCurrentEpoch() int64 {
	return n.epochManager.GetCurrentEpoch()
}

func (n *Node) BroadcastEpochAggData(nodeId int, epoch int64, throughput float64, proposalRate float64) {
	msg := core.EpochDataForAggregation{
		EpochNumber:  epoch,
		Throughput:   throughput,
		ProposalRate: proposalRate,
		From:         nodeId,
	}

	pbMsg := transportpb.EpochDataForAggregationToPB(msg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal EpochDataForAggregation message for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	for _, othersIp := range config.NodeAddr {
		if othersIp == n.GetAddr() {
			continue
		}
		// msg.To = othersIp
		go n.messageHub.Send(core.MsgEpochAggDataMessage, othersIp, msg, signature)
	}

}

func (n *Node) HandleEpochAggData(msg core.EpochDataForAggregation, signature []byte) {
	n.epochAggregator.ReceiveEpochData(msg.EpochNumber, msg.From, msg.Throughput, msg.ProposalRate)
}

func (n *Node) SendEpochDataToAgent(epoch int64, throughput float64, proposalRate float64) {
	n.SendLearningDataToAgent(epoch, throughput, proposalRate)
}
