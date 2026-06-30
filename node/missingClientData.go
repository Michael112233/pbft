package node

import (
	"sync"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
)

type reqStatus uint8

const (
	reqStatusUnknown reqStatus = iota

	reqStatusRequested
	reqStatusReceived
)

const (
	missingClientLargeMsgThresholdBytes = 20 * 1024
	missingClientReplyChunkSize         = 20
)

type MissingClientDataDetail struct {
	viewRequested int64
	timeRequested int64
	reqStatus     reqStatus
}
type MissingClientStateManager struct {
	lock              sync.Mutex
	stateTransferring bool
	missingData       map[[32]byte]MissingClientDataDetail
}

func NewMissingClientStateManager() *MissingClientStateManager {
	return &MissingClientStateManager{
		missingData: make(map[[32]byte]MissingClientDataDetail),
	}
}

func (m *MissingClientStateManager) getMissingClientData(missingClientMsgs [][32]byte, viewRequested int64, currentLeader int) [][32]byte {
	time := time.Now().Unix()
	m.lock.Lock()
	defer m.lock.Unlock()

	askFor := make([][32]byte, 0, len(missingClientMsgs))
	for _, msg := range missingClientMsgs {
		if detail, exists := m.missingData[msg]; exists {
			if detail.viewRequested >= viewRequested || detail.reqStatus == reqStatusReceived {
				continue
			}
			detail.viewRequested = viewRequested
			detail.timeRequested = time
			detail.reqStatus = reqStatusRequested
			m.missingData[msg] = detail
			askFor = append(askFor, msg)

		} else {
			m.missingData[msg] = MissingClientDataDetail{
				viewRequested: viewRequested,
				timeRequested: time,
				reqStatus:     reqStatusRequested,
			}
			askFor = append(askFor, msg)

		}
	}
	if len(askFor) > 0 {
		m.stateTransferring = true
	}
	return askFor

	// return askFor
}

func (n *Node) sendReqMissingClientMsg(missingClientMsgs [][32]byte, viewRequested int64, currentLeader int) {
	askFor := n.missingClientStateManager.getMissingClientData(missingClientMsgs, viewRequested, currentLeader)
	if len(askFor) == 0 {
		return
	}
	reqMsg := core.ReqMissingClientMsg{
		MissingClientMsgs: askFor,
		From:              n.GetNodeID(),
	}
	pbMsg := transportpb.ReqMissingClientMsgToPB(reqMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		n.log.Error("Failed to marshal ReqMissingClientMsg for signing: %v", err)
		return
	}
	signature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
	sentTo := 0
	for _, otherIP := range config.NodeAddr {
		if sentTo >= n.fNodes+1 {
			break
		}
		if otherIP == n.GetAddr() || otherIP == config.NodeAddr[currentLeader] {
			continue
		}
		go n.messageHub.Send(core.MsgReqMissingClientMessage, otherIP, reqMsg, signature)
		sentTo++
	}
}

func (n *Node) HandleReqMissingClientMsg(req core.ReqMissingClientMsg, signature []byte) {
	clientData := n.pool.GetMultiple(req.MissingClientMsgs)

	if len(clientData) == 0 {
		n.log.Warn("got asked for missing client data but none found")
		return
	}
	if len(clientData) != len(req.MissingClientMsgs) {
		n.log.Warn("got asked for missing client data but not all found")
		// return
	}
	singleClientMsg := clientData[0].Msg
	pbSingleClientMsg := transportpb.ClientMsgSigToPB(singleClientMsg)
	payloadBytesSingleClientMsg, err := marshalDeterministic(pbSingleClientMsg)
	if err != nil {
		n.log.Error("Failed to marshal ClientMsgSig for signing: %v", err)
		return
	}
	n.log.Info("clientData size = %d bytes", len(payloadBytesSingleClientMsg))

	toAddr := config.NodeAddr[req.From]
	sendReply := func(msgs []core.MissingClientData) bool {
		replyMsg := core.ReplyMissingClientMsg{
			MissingClientMsgs: msgs,
			From:              n.GetNodeID(),
		}
		pbMsg := transportpb.ReplyMissingClientMsgToPB(replyMsg)
		payloadBytes, err := marshalDeterministic(pbMsg)
		if err != nil {
			n.log.Error("Failed to marshal ReplyMissingClientMsg for signing: %v", err)
			return false
		}
		replySignature := crypto.SignMessageEd25519(payloadBytes, n.encryptionKeyStore.GetPrivateKey())
		n.messageHub.Send(core.MsgReplyMissingClientMessage, toAddr, replyMsg, replySignature)
		return true
	}

	if len(payloadBytesSingleClientMsg) >= missingClientLargeMsgThresholdBytes {
		for start := 0; start < len(clientData); start += missingClientReplyChunkSize {
			end := start + missingClientReplyChunkSize
			if end > len(clientData) {
				end = len(clientData)
			}
			if !sendReply(clientData[start:end]) {
				return
			}
		}
		return
	}

	sendReply(clientData)

}

func (m *MissingClientStateManager) addMissingClientData(msgs []core.MissingClientData) []core.MissingClientData {
	m.lock.Lock()
	if len(m.missingData) == 0 {
		m.lock.Unlock()
		return nil
	}

	addToPool := make([]core.MissingClientData, 0, len(msgs))
	for _, msg := range msgs {
		if _, exists := m.missingData[msg.Digest]; exists {
			addToPool = append(addToPool, msg)
			delete(m.missingData, msg.Digest)

		}
	}
	if len(m.missingData) == 0 {
		m.stateTransferring = false
	}
	m.lock.Unlock()
	return addToPool
}

func (n *Node) HandleReplyMissingClientMsg(reply core.ReplyMissingClientMsg, signature []byte) {
	addToPool := n.missingClientStateManager.addMissingClientData(reply.MissingClientMsgs)
	if len(addToPool) == 0 {
		return
	}
	somethingAdded := n.pool.AddMultiple(addToPool) // should run before gc of executed, dont want to recover messages which were executed
	// when client retry that may also add to pool same recovered messages
	if somethingAdded {
		n.clientMissingDataReceived <- struct{}{}
	}

}

func (m *MissingClientStateManager) isMissingClientStateTransferring() bool {
	m.lock.Lock()
	defer m.lock.Unlock()
	return m.stateTransferring
}
