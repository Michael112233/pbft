package node

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"fmt"
	"math/big"

	"github.com/michael112233/pbft/crypto"
	"github.com/michael112233/pbft/vr"
)

type KeyStore struct {
	privateKey    ed25519.PrivateKey
	privateVRFKey *ecdsa.PrivateKey
	publicKeys    map[int]ed25519.PublicKey
	publicVRFKeys map[int]*ecdsa.PublicKey
	clientKey     ed25519.PublicKey
	modulus       *big.Int // can do better rsa modulus at construction and delete p q which generate the modulus
}

func NewKeyStore(nodeId int, nodeNum int64) *KeyStore {
	myPrivKeyPath := fmt.Sprintf("keys/node%d_priv.pem", nodeId)
	privKey, err := crypto.ReadEd25519PrivateKey(myPrivKeyPath)
	if err != nil {
		fmt.Printf("Error reading private key for node %d: %v\n", nodeId, err)
	}

	myVRFPrivKeyPath := fmt.Sprintf("keys/node%d_vrf_priv.pem", nodeId)
	privateVRFKey, err := crypto.ReadECDSAPrivateKey(myVRFPrivKeyPath)
	if err != nil {
		fmt.Printf("Error reading VRF private key for node %d: %v\n", nodeId, err)
	}

	pubKeys := make(map[int]ed25519.PublicKey, int(nodeNum))
	publicVRFKeys := make(map[int]*ecdsa.PublicKey, int(nodeNum))
	for i := 1; i <= int(nodeNum); i++ {
		pubKeyPath := fmt.Sprintf("keys/node%d_pub.pem", i)
		pubKey, err := crypto.ReadEd25519PublicKey(pubKeyPath)
		if err != nil {
			fmt.Printf("Error reading public key for node %d: %v\n", i, err)
		} else {
			pubKeys[i] = pubKey
		}

		vrfPubKeyPath := fmt.Sprintf("keys/node%d_vrf_pub.pem", i)
		vrfPubKey, err := crypto.ReadECDSAPublicKey(vrfPubKeyPath)
		if err != nil {
			fmt.Printf("Error reading VRF public key for node %d: %v\n", i, err)
		} else {
			publicVRFKeys[i] = vrfPubKey
		}
	}

	clientPubKeyPath := "keys/client_pub.pem"
	clientPubKey, err := crypto.ReadEd25519PublicKey(clientPubKeyPath)
	if err != nil {
		fmt.Printf("Error reading client public key: %v\n", err)
	}

	return &KeyStore{
		privateKey:    privKey,
		privateVRFKey: privateVRFKey,
		publicKeys:    pubKeys,
		publicVRFKeys: publicVRFKeys,
		clientKey:     clientPubKey,
		modulus:       vr.VDFLargeModulus(),
	}
}

func (ks *KeyStore) GetPrivateKey() ed25519.PrivateKey {
	return ks.privateKey
}

func (ks *KeyStore) GetPublicKey(nodeId int) (ed25519.PublicKey, bool) {
	pubKey, exists := ks.publicKeys[nodeId]
	return pubKey, exists
}

func (ks *KeyStore) GetPrivateVRFKey() *ecdsa.PrivateKey {
	return ks.privateVRFKey
}

func (ks *KeyStore) GetPublicVRFKey(nodeId int) (*ecdsa.PublicKey, bool) {
	pubKey, exists := ks.publicVRFKeys[nodeId]
	return pubKey, exists
}

// GetVDFModulus returns a copy so callers cannot mutate the shared modulus.
func (ks *KeyStore) GetVDFModulus() *big.Int {
	if ks.modulus == nil {
		return nil
	}

	return new(big.Int).Set(ks.modulus)
}
