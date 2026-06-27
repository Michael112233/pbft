package verifiablerandomness

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"testing"
)

func TestCreateProofAndBetaVerifyProof(t *testing.T) {
	privateKey := generateP256Key(t)
	input := []byte("view=7:checkpoint=42")

	proof, beta, err := CreateProofAndBeta(privateKey, input)
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error: %v", err)
	}
	if len(proof) == 0 {
		t.Fatal("CreateProofAndBeta returned empty proof")
	}
	if len(beta) == 0 {
		t.Fatal("CreateProofAndBeta returned empty beta")
	}

	verifiedBeta, err := VerifyProof(&privateKey.PublicKey, input, proof)
	if err != nil {
		t.Fatalf("VerifyProof returned error: %v", err)
	}
	if !bytes.Equal(beta, verifiedBeta) {
		t.Fatalf("beta mismatch: created %x verified %x", beta, verifiedBeta)
	}
}

func TestCreateProofAndBetaDeterministic(t *testing.T) {
	privateKey := generateP256Key(t)
	input := []byte("same input")

	proofA, betaA, err := CreateProofAndBeta(privateKey, input)
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error: %v", err)
	}
	proofB, betaB, err := CreateProofAndBeta(privateKey, input)
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error: %v", err)
	}

	if !bytes.Equal(proofA, proofB) {
		t.Fatalf("proofs differ for same key and input: %x vs %x", proofA, proofB)
	}
	if !bytes.Equal(betaA, betaB) {
		t.Fatalf("betas differ for same key and input: %x vs %x", betaA, betaB)
	}
}

func TestCreateProofAndBetaAllowsEmptyInput(t *testing.T) {
	privateKey := generateP256Key(t)

	proof, beta, err := CreateProofAndBeta(privateKey, nil)
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error for empty input: %v", err)
	}

	verifiedBeta, err := VerifyProof(&privateKey.PublicKey, nil, proof)
	if err != nil {
		t.Fatalf("VerifyProof returned error for empty input: %v", err)
	}
	if !bytes.Equal(beta, verifiedBeta) {
		t.Fatalf("beta mismatch for empty input: created %x verified %x", beta, verifiedBeta)
	}
}

func TestVerifyProofRejectsWrongInput(t *testing.T) {
	privateKey := generateP256Key(t)
	proof, _, err := CreateProofAndBeta(privateKey, []byte("input-a"))
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error: %v", err)
	}

	if _, err := VerifyProof(&privateKey.PublicKey, []byte("input-b"), proof); err == nil {
		t.Fatal("VerifyProof accepted proof for wrong input")
	}
}

func TestVerifyProofRejectsWrongPublicKey(t *testing.T) {
	privateKey := generateP256Key(t)
	wrongPrivateKey := generateP256Key(t)
	proof, _, err := CreateProofAndBeta(privateKey, []byte("input"))
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error: %v", err)
	}

	if _, err := VerifyProof(&wrongPrivateKey.PublicKey, []byte("input"), proof); err == nil {
		t.Fatal("VerifyProof accepted proof for wrong public key")
	}
}

func TestVerifyProofRejectsCorruptedProof(t *testing.T) {
	privateKey := generateP256Key(t)
	input := []byte("input")
	proof, _, err := CreateProofAndBeta(privateKey, input)
	if err != nil {
		t.Fatalf("CreateProofAndBeta returned error: %v", err)
	}

	corruptedProof := append([]byte(nil), proof...)
	corruptedProof[len(corruptedProof)-1] ^= 0x01

	if _, err := VerifyProof(&privateKey.PublicKey, input, corruptedProof); err == nil {
		t.Fatal("VerifyProof accepted corrupted proof")
	}
}

func TestVerifyProofRejectsMalformedProof(t *testing.T) {
	privateKey := generateP256Key(t)

	if _, err := VerifyProof(&privateKey.PublicKey, []byte("input"), []byte{0x01, 0x02, 0x03}); err == nil {
		t.Fatal("VerifyProof accepted malformed proof")
	}
}

func TestRejectsNilKeys(t *testing.T) {
	if _, _, err := CreateProofAndBeta(nil, []byte("input")); err == nil {
		t.Fatal("CreateProofAndBeta accepted nil private key")
	}
	if _, err := VerifyProof(nil, []byte("input"), []byte{0x01}); err == nil {
		t.Fatal("VerifyProof accepted nil public key")
	}
}

func TestRejectsEmptyProof(t *testing.T) {
	privateKey := generateP256Key(t)

	if _, err := VerifyProof(&privateKey.PublicKey, []byte("input"), nil); err == nil {
		t.Fatal("VerifyProof accepted empty proof")
	}
}

func TestRejectsWrongCurveKeys(t *testing.T) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate P-384 key: %v", err)
	}

	if _, _, err := CreateProofAndBeta(privateKey, []byte("input")); err == nil {
		t.Fatal("CreateProofAndBeta accepted non-P-256 private key")
	}
	if _, err := VerifyProof(&privateKey.PublicKey, []byte("input"), []byte{0x01}); err == nil {
		t.Fatal("VerifyProof accepted non-P-256 public key")
	}
}

func TestNumberFromBetaWithinInclusiveRange(t *testing.T) {
	beta := []byte("test beta")
	min := 1
	max := 4

	for i := 0; i < 50; i++ {
		got, err := NumberFromBeta(append(beta, byte(i)), min, max)
		if err != nil {
			t.Fatalf("NumberFromBeta returned error: %v", err)
		}
		if got < min || got > max {
			t.Fatalf("NumberFromBeta returned %d outside [%d, %d]", got, min, max)
		}
	}
}

func TestNumberFromBetaDeterministic(t *testing.T) {
	beta := []byte("deterministic beta")

	gotA, err := NumberFromBeta(beta, 1, 10)
	if err != nil {
		t.Fatalf("NumberFromBeta returned error: %v", err)
	}
	gotB, err := NumberFromBeta(beta, 1, 10)
	if err != nil {
		t.Fatalf("NumberFromBeta returned error: %v", err)
	}
	if gotA != gotB {
		t.Fatalf("NumberFromBeta was not deterministic: %d vs %d", gotA, gotB)
	}
}

func TestNumberFromBetaDifferentBetasAreValid(t *testing.T) {
	betas := [][]byte{
		[]byte("beta-a"),
		[]byte("beta-b"),
		[]byte("beta-c"),
	}

	for _, beta := range betas {
		got, err := NumberFromBeta(beta, 10, 20)
		if err != nil {
			t.Fatalf("NumberFromBeta returned error for beta %q: %v", beta, err)
		}
		if got < 10 || got > 20 {
			t.Fatalf("NumberFromBeta returned %d outside [10, 20]", got)
		}
	}
}

func TestNumberFromBetaSingleValueRange(t *testing.T) {
	got, err := NumberFromBeta([]byte("beta"), 7, 7)
	if err != nil {
		t.Fatalf("NumberFromBeta returned error: %v", err)
	}
	if got != 7 {
		t.Fatalf("NumberFromBeta returned %d, want 7", got)
	}
}

func TestNumberFromBetaRejectsEmptyBeta(t *testing.T) {
	if _, err := NumberFromBeta(nil, 1, 10); err == nil {
		t.Fatal("NumberFromBeta accepted empty beta")
	}
}

func TestNumberFromBetaRejectsInvalidRange(t *testing.T) {
	if _, err := NumberFromBeta([]byte("beta"), 10, 1); err == nil {
		t.Fatal("NumberFromBeta accepted min greater than max")
	}
}

func TestNumberFromBetaNegativeRange(t *testing.T) {
	got, err := NumberFromBeta([]byte("negative beta"), -5, 5)
	if err != nil {
		t.Fatalf("NumberFromBeta returned error: %v", err)
	}
	if got < -5 || got > 5 {
		t.Fatalf("NumberFromBeta returned %d outside [-5, 5]", got)
	}
}

func generateP256Key(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate P-256 key: %v", err)
	}

	return privateKey
}
