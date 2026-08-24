package crypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
)

// Generate a new Ed25519 keypair
func GenerateEd25519Keypair() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate keypair: %w", err)
	}
	return publicKey, privateKey, nil
}

// GenerateP256Key generates a new ECDSA private key on the P-256 curve.
func GenerateP256Key() (*ecdsa.PrivateKey, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("failed to generate P-256 key: %w", err)
	}

	return privateKey, nil
}

// SaveECDSAPrivateKey saves an ECDSA private key in PKCS#8 PEM format.
func SaveECDSAPrivateKey(path string, privateKey *ecdsa.PrivateKey) error {
	if privateKey == nil {
		return fmt.Errorf("ECDSA private key cannot be nil")
	}

	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal ECDSA private key: %w", err)
	}

	privateKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	})
	if privateKeyPEM == nil {
		return fmt.Errorf("failed to encode ECDSA private key PEM")
	}

	if err := os.WriteFile(path, privateKeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write ECDSA private key: %w", err)
	}

	return nil
}

// SaveECDSAPublicKey saves an ECDSA public key in PKIX PEM format.
func SaveECDSAPublicKey(path string, publicKey *ecdsa.PublicKey) error {
	if publicKey == nil {
		return fmt.Errorf("ECDSA public key cannot be nil")
	}

	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal ECDSA public key: %w", err)
	}

	publicKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	})
	if publicKeyPEM == nil {
		return fmt.Errorf("failed to encode ECDSA public key PEM")
	}

	if err := os.WriteFile(path, publicKeyPEM, 0644); err != nil {
		return fmt.Errorf("failed to write ECDSA public key: %w", err)
	}

	return nil
}

// ReadECDSAPrivateKey reads an ECDSA private key from a PKCS#8 PEM file.
func ReadECDSAPrivateKey(path string) (*ecdsa.PrivateKey, error) {
	readFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading ECDSA private key: %w", err)
	}

	privateDecoded, _ := pem.Decode(readFile)
	if privateDecoded == nil {
		return nil, fmt.Errorf("failed to decode ECDSA private key PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(privateDecoded.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ECDSA private key: %w", err)
	}

	privateKey, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an ECDSA private key")
	}

	return privateKey, nil
}

// ReadECDSAPublicKey reads an ECDSA public key from a PKIX PEM file.
func ReadECDSAPublicKey(path string) (*ecdsa.PublicKey, error) {
	readFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading ECDSA public key: %w", err)
	}

	publicDecoded, _ := pem.Decode(readFile)
	if publicDecoded == nil {
		return nil, fmt.Errorf("failed to decode ECDSA public key PEM block")
	}

	key, err := x509.ParsePKIXPublicKey(publicDecoded.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse ECDSA public key: %w", err)
	}

	publicKey, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an ECDSA public key")
	}

	return publicKey, nil
}

// Save private key to PEM file
func SavePrivateKey(path string, privateKey ed25519.PrivateKey) error {
	// Marshal private key to PKCS8 format
	privateKeyBytes, err := x509.MarshalPKCS8PrivateKey(privateKey)
	if err != nil {
		return fmt.Errorf("failed to marshal private key: %w", err)
	}

	// Create PEM block
	privateKeyPEM := &pem.Block{
		Type:  "PRIVATE KEY",
		Bytes: privateKeyBytes,
	}

	// Write to file
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if err := pem.Encode(file, privateKeyPEM); err != nil {
		return fmt.Errorf("failed to encode PEM: %w", err)
	}

	return nil
}

// Save public key to PEM file
func SavePublicKey(path string, publicKey ed25519.PublicKey) error {
	// Marshal public key to PKIX format
	publicKeyBytes, err := x509.MarshalPKIXPublicKey(publicKey)
	if err != nil {
		return fmt.Errorf("failed to marshal public key: %w", err)
	}

	// Create PEM block
	publicKeyPEM := &pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicKeyBytes,
	}

	// Write to file
	file, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer file.Close()

	if err := pem.Encode(file, publicKeyPEM); err != nil {
		return fmt.Errorf("failed to encode PEM: %w", err)
	}

	return nil
}

func SignMessageEd25519(message []byte, privateKey ed25519.PrivateKey) []byte {
	// Ed25519 signs the message directly (no pre-hashing needed)
	signature := ed25519.Sign(privateKey, message)
	return signature
}

// Verify an Ed25519 signature
func VerifySignatureEd25519(message []byte, signature []byte, publicKey ed25519.PublicKey) bool {
	return ed25519.Verify(publicKey, message, signature)
}

// Read private key from PEM file
func ReadEd25519PrivateKey(path string) (ed25519.PrivateKey, error) {
	readFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading private key: %w", err)
	}

	privateDecoded, _ := pem.Decode(readFile)
	if privateDecoded == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKCS8PrivateKey(privateDecoded.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	privateKey, ok := key.(ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 private key")
	}

	return privateKey, nil
}

// Read public key from PEM file
func ReadEd25519PublicKey(path string) (ed25519.PublicKey, error) {
	readFile, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("error reading public key: %w", err)
	}

	publicDecoded, _ := pem.Decode(readFile)
	if publicDecoded == nil {
		return nil, fmt.Errorf("failed to decode PEM block")
	}

	key, err := x509.ParsePKIXPublicKey(publicDecoded.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse public key: %w", err)
	}

	publicKey, ok := key.(ed25519.PublicKey)
	if !ok {
		return nil, fmt.Errorf("not an Ed25519 public key")
	}

	return publicKey, nil
}
