package node

import (
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/transportpb"
	"google.golang.org/protobuf/proto"

	"testing"
)

func dummyClientMsg(id, paddingBytes int) core.ClientMsgSignature {

	clientMsgData := core.ClientMsg{
		Id:         int64(id),
		Timestamp:  time.Now().Unix(),
		ClientName: "test-client",
		Padding:    strings.Repeat("x", paddingBytes),
		Txn: &core.Transaction{
			Sender:    fmt.Sprintf("sender-%d", id),
			Receiver:  fmt.Sprintf("receiver-%d", id),
			Amount:    big.NewInt(int64(id + 1)),
			Timestamp: time.Now().Unix(),
		},
	}
	clientMsgBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(transportpb.ClientMsgToPB(clientMsgData))
	if err != nil {
		panic(err)
	}
	privKey, err := crypto.ReadEd25519PrivateKey("../keys/client_priv.pem")
	if err != nil {
		panic("Error reading client private key: " + err.Error())
	}
	signature := crypto.SignMessageEd25519(clientMsgBytes, privKey)

	return core.ClientMsgSignature{
		Data:      clientMsgData,
		Signature: signature,
	}
}

func dummyPreprepareMsgSig(clientMsgSig core.ClientMsgSignature) (core.PreprepareMsgSig, [32]byte) {
	digestClientMsg, err := ComputeBatchDigest(clientMsgSig.Data)
	if err != nil {
		panic(err)
	}
	preprepareMsgMini := core.PreprepareMsgMini{
		DigestClientMsg: digestClientMsg,
		View:            0,
		SeqNum:          0,
	}
	payloadBytes, err := marshalDeterministic(preprepareSignPayload(0, 0, digestClientMsg[:]))
	if err != nil {
		panic(err)
	}
	privKey, err := crypto.ReadEd25519PrivateKey("../keys/node1_priv.pem")
	if err != nil {
		panic("Error reading node private key: " + err.Error())
	}
	signature := crypto.SignMessageEd25519(payloadBytes, privKey)
	return core.PreprepareMsgSig{
		PreprepareMsgMini: preprepareMsgMini,
		Signature:         signature,
		ActualMsg:         clientMsgSig,
	}, digestClientMsg
}

func dummyPrepareMsgSig(digestClientMsg [32]byte) *core.PrepareMsgSig {
	prepareMsg := core.PrepareMsg{
		Digest: digestClientMsg,
		View:   0,
		SeqNum: 0,
		From:   0,
	}
	pbMsg := transportpb.PrepareToPB(prepareMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		panic(err)
	}
	privKey, err := crypto.ReadEd25519PrivateKey("../keys/node1_priv.pem")
	if err != nil {
		panic("Error reading node private key: " + err.Error())
	}
	signature := crypto.SignMessageEd25519(payloadBytes, privKey)
	return &core.PrepareMsgSig{
		PrepareMsg: prepareMsg,
		Signature:  signature,
	}
}

func dummyVCMsg(count int, padding int) core.ViewChangeMsg {
	preparedCerts := make(map[int64]*core.PreparedCert, count)
	for i := 0; i < count; i++ {
		clientMsg := dummyClientMsg(i, padding)
		preprepareMsgSig, digest := dummyPreprepareMsgSig(clientMsg)
		prepareLog := make(map[int]*core.PrepareMsgSig, 3)
		for x := 0; x < 3; x++ {
			prepareLog[x] = dummyPrepareMsgSig(digest)
		}
		preparedCerts[int64(i)] = &core.PreparedCert{
			PreprepareMsg: preprepareMsgSig,
			PrepareLog:    prepareLog,
		}

		// preparedCerts[i] = core.PreparedCert{
		// 	PreprepareMsgSig: preprepareMsgSig,
		// 	Digest:             digest,
		// }
	}
	return core.ViewChangeMsg{
		PreparedCerts: preparedCerts,
	}
}

func dummyVCMsgSig(vcMsg core.ViewChangeMsg) core.ViewChangeMsgSig {
	pbMsg := transportpb.ViewChangeToPB(vcMsg)
	payloadBytes, err := marshalDeterministic(pbMsg)
	if err != nil {
		panic(err)
		// return
	}
	privKey, err := crypto.ReadEd25519PrivateKey("../keys/node1_priv.pem")
	if err != nil {
		panic("Error reading node private key: " + err.Error())
	}
	signature := crypto.SignMessageEd25519(payloadBytes, privKey)
	return core.ViewChangeMsgSig{
		ViewChangeMsg: vcMsg,
		Signature:     signature,
	}
}

func timedDummyVCMsgSig(count int, padding int) core.ViewChangeMsgSig {

	vcMsg := dummyVCMsg(count, padding)
	start := time.Now()
	vcMsgSig := dummyVCMsgSig(vcMsg)

	fmt.Printf("created ViewChangeMsgSig with %d prepared certs and %d padding bytes in %s\n", count, padding, time.Since(start))

	return vcMsgSig
}

func TestTimedDummyVCMsgSig(t *testing.T) {
	vcCount := 1000
	vcPadding := 100
	_ = timedDummyVCMsgSig(vcCount, vcPadding)
}

// go test ./node -run TestTimedDummyVCMsgSig -v -count=1
