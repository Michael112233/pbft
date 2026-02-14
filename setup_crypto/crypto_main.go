package main

import (
	"fmt"

	"crypto/ed25519"

	"os"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/crypto"
)

const (
	cfgPath = "config/run.json"
)

type KeyPair struct {
	PublicKey  ed25519.PublicKey
	PrivateKey ed25519.PrivateKey
}

func main() {
	cfg := config.ReadCfg(cfgPath)
	nodeNum := int(cfg.NodeNum)

	// create keys directory if it doesn't exist
	if _, err := os.Stat("keys"); os.IsNotExist(err) {
		err := os.Mkdir("keys", 0700)
		if err != nil {
			fmt.Printf("Error creating keys directory: %v\n", err)
			return
		}

	}

	for i := 0; i < nodeNum; i++ {
		pubKey, privKey, err := crypto.GenerateEd25519Keypair()
		if err != nil {
			fmt.Printf("Error generating keypair for node %d: %v\n", i, err)
			continue
		}
		pubKeyPath := fmt.Sprintf("keys/node%d_pub.pem", i)
		privKeyPath := fmt.Sprintf("keys/node%d_priv.pem", i)

		err = crypto.SavePublicKey(pubKeyPath, pubKey)
		if err != nil {
			fmt.Printf("Error saving public key for node %d: %v\n", i, err)
			continue
		}

		err = crypto.SavePrivateKey(privKeyPath, privKey)
		if err != nil {
			fmt.Printf("Error saving private key for node %d: %v\n", i, err)
			continue
		}

		fmt.Printf("Generated and saved keypair for node %d\n", i)

	}

	// Generate client keypair
	pubKey, privKey, err := crypto.GenerateEd25519Keypair()
	if err != nil {
		fmt.Printf("Error generating keypair for client: %v\n", err)
		return
	}
	err = crypto.SavePublicKey("keys/client_pub.pem", pubKey)
	if err != nil {
		fmt.Printf("Error saving public key for client: %v\n", err)
		return
	}
	err = crypto.SavePrivateKey("keys/client_priv.pem", privKey)
	if err != nil {
		fmt.Printf("Error saving private key for client: %v\n", err)
		return
	}
	fmt.Println("Generated and saved keypair for client")
}
