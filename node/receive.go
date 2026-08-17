package node

import (
	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"
)

func (n *Node) HandleRequestMessage(requests core.RequestMessage) {
	// usually will have one req

	for _, req := range requests.Txs {
		clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(req.Data))
		if err != nil {
			n.log.Error("Failed to marshal client message for signature verification: %v", err)
			return
		}
		verified := crypto.VerifySignatureEd25519(clientMsgBytes, req.Signature, n.encryptionKeyStore.clientKey)
		if !verified {
			n.log.Error("Failed to verify client message signature for request ID %d and client %s", req.Data.Id, req.Data.ClientName)
			continue
		}
		n.ReceiveVerifiedClientRequestCh(req)
	}

}
