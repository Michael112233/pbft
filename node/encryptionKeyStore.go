package node

import (
	"crypto/ed25519"
	"fmt"

	"github.com/michael112233/pbft/crypto"
)

type KeyStore struct {
	privateKey ed25519.PrivateKey
	publicKeys map[int]ed25519.PublicKey
	clientKey  ed25519.PublicKey
}

func NewKeyStore(nodeId int, nodeNum int64) *KeyStore {
	myPrivKeyPath := fmt.Sprintf("keys/node%d_priv.pem", nodeId)
	privKey, err := crypto.ReadEd25519PrivateKey(myPrivKeyPath)
	if err != nil {
		fmt.Printf("Error reading private key for node %d: %v\n", nodeId, err)
	}
	pubKeys := make(map[int]ed25519.PublicKey, int(nodeNum))
	for i := 1; i <= int(nodeNum); i++ {
		pubKeyPath := fmt.Sprintf("keys/node%d_pub.pem", i)
		pubKey, err := crypto.ReadEd25519PublicKey(pubKeyPath)
		if err != nil {
			fmt.Printf("Error reading public key for node %d: %v\n", i, err)
			continue
		}
		pubKeys[i] = pubKey
	}

	clientPubKeyPath := "keys/client_pub.pem"
	clientPubKey, err := crypto.ReadEd25519PublicKey(clientPubKeyPath)
	if err != nil {
		fmt.Printf("Error reading client public key: %v\n", err)
	}

	return &KeyStore{
		privateKey: privKey,
		publicKeys: pubKeys,
		clientKey:  clientPubKey,
	}
}

func (ks *KeyStore) GetPrivateKey() ed25519.PrivateKey {
	return ks.privateKey
}

func (ks *KeyStore) GetPublicKey(nodeId int) (ed25519.PublicKey, bool) {
	pubKey, exists := ks.publicKeys[nodeId]
	return pubKey, exists
}
