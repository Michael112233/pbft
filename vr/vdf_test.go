package vr

import (
	"bytes"
	"fmt"
	"math/big"
	"testing"
	"time"
)

func TestEvalVDFValidateVDF(t *testing.T) {
	modulus := testVDFModulus()
	input := []byte("view=10:checkpoint=20")
	delaySteps := uint64(130)

	y, proof, err := EvalVDF(input, modulus, delaySteps)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}
	if len(y) == 0 {
		t.Fatal("EvalVDF returned empty y")
	}
	if len(proof) == 0 {
		t.Fatal("EvalVDF returned empty proof")
	}

	ok, err := ValidateVDF(input, y, proof, modulus, delaySteps)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if !ok {
		t.Fatal("ValidateVDF rejected valid proof")
	}
}

func TestTimeEvalVDF(t *testing.T) {
	modulus := testVDFModulus()
	input := []byte("input")
	delaySteps := uint64(130)

	start := time.Now()
	_, _, err := EvalVDF(input, modulus, delaySteps)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}
	duration := time.Since(start)
	t.Logf("EvalVDF took %v", duration)
}

func TestTimeEvalVDFLargeModulus(t *testing.T) {
	modulus := testVDFLargeModulus()
	input := []byte("input")
	delaySteps := uint64(800000)

	start := time.Now()
	_, _, err := EvalVDF(input, modulus, delaySteps)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}
	duration := time.Since(start)
	t.Logf("EvalVDF with %d-bit modulus took %v", modulus.BitLen(), duration)
}

func BenchmarkEvalVDFLargeModulus(b *testing.B) {
	modulus := testVDFLargeModulus()
	input := []byte("input")

	for _, delaySteps := range []uint64{100000, 200000, 300000} {
		b.Run(fmt.Sprintf("delaySteps=%d", delaySteps), func(b *testing.B) {
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _, err := EvalVDF(input, modulus, delaySteps)
				if err != nil {
					b.Fatalf("EvalVDF returned error: %v", err)
				}
			}
		})
	}
}

// go test ./verifiablerandomness -bench BenchmarkEvalVDFLargeModulus -run '^$' -benchmem -benchtime=10s

func TestEvalVDFDeterministic(t *testing.T) {
	modulus := testVDFModulus()
	input := []byte("same input")
	delaySteps := uint64(130)

	yA, proofA, err := EvalVDF(input, modulus, delaySteps)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}
	yB, proofB, err := EvalVDF(input, modulus, delaySteps)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	if !bytes.Equal(yA, yB) {
		t.Fatalf("y differs for same input: %x vs %x", yA, yB)
	}
	if !bytes.Equal(proofA, proofB) {
		t.Fatalf("proof differs for same input: %x vs %x", proofA, proofB)
	}
}

func TestValidateVDFRejectsWrongInput(t *testing.T) {
	modulus := testVDFModulus()
	y, proof, err := EvalVDF([]byte("input-a"), modulus, 130)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	ok, err := ValidateVDF([]byte("input-b"), y, proof, modulus, 130)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if ok {
		t.Fatal("ValidateVDF accepted proof for wrong input")
	}
}

func TestValidateVDFRejectsWrongDelay(t *testing.T) {
	modulus := testVDFModulus()
	y, proof, err := EvalVDF([]byte("input"), modulus, 130)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	ok, err := ValidateVDF([]byte("input"), y, proof, modulus, 129)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if ok {
		t.Fatal("ValidateVDF accepted proof for wrong delay")
	}
}

func TestValidateVDFRejectsWrongModulus(t *testing.T) {
	modulus := testVDFModulus()
	wrongModulus := testVDFWrongModulus()
	y, proof, err := EvalVDF([]byte("input"), modulus, 130)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	ok, err := ValidateVDF([]byte("input"), y, proof, wrongModulus, 130)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if ok {
		t.Fatal("ValidateVDF accepted proof for wrong modulus")
	}
}

func TestValidateVDFRejectsTamperedY(t *testing.T) {
	modulus := testVDFModulus()
	input := []byte("input")
	y, proof, err := EvalVDF(input, modulus, 130)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	tamperedY := append([]byte(nil), y...)
	tamperedY[len(tamperedY)-1] ^= 0x01

	ok, err := ValidateVDF(input, tamperedY, proof, modulus, 130)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if ok {
		t.Fatal("ValidateVDF accepted tampered y")
	}
}

func TestValidateVDFRejectsTamperedProof(t *testing.T) {
	modulus := testVDFModulus()
	input := []byte("input")
	y, proof, err := EvalVDF(input, modulus, 130)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	tamperedProof := append([]byte(nil), proof...)
	tamperedProof[len(tamperedProof)-1] ^= 0x01

	ok, err := ValidateVDF(input, y, tamperedProof, modulus, 130)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if ok {
		t.Fatal("ValidateVDF accepted tampered proof")
	}
}

func TestEvalVDFAllowsEmptyInput(t *testing.T) {
	modulus := testVDFModulus()

	y, proof, err := EvalVDF(nil, modulus, 130)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	ok, err := ValidateVDF(nil, y, proof, modulus, 130)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if !ok {
		t.Fatal("ValidateVDF rejected empty-input proof")
	}
}

func TestEvalVDFZeroDelay(t *testing.T) {
	modulus := testVDFModulus()
	input := []byte("zero delay")

	y, proof, err := EvalVDF(input, modulus, 0)
	if err != nil {
		t.Fatalf("EvalVDF returned error: %v", err)
	}

	ok, err := ValidateVDF(input, y, proof, modulus, 0)
	if err != nil {
		t.Fatalf("ValidateVDF returned error: %v", err)
	}
	if !ok {
		t.Fatal("ValidateVDF rejected zero-delay proof")
	}
}

func TestVDFRejectsInvalidInputs(t *testing.T) {
	modulus := testVDFModulus()

	if _, _, err := EvalVDF([]byte("input"), nil, 1); err == nil {
		t.Fatal("EvalVDF accepted nil modulus")
	}
	if _, _, err := EvalVDF([]byte("input"), big.NewInt(10), 1); err == nil {
		t.Fatal("EvalVDF accepted even modulus")
	}
	if _, _, err := EvalVDF([]byte("input"), big.NewInt(3), 1); err == nil {
		t.Fatal("EvalVDF accepted too-small modulus")
	}
	if _, err := ValidateVDF([]byte("input"), nil, []byte{1}, modulus, 1); err == nil {
		t.Fatal("ValidateVDF accepted empty y")
	}
	if _, err := ValidateVDF([]byte("input"), []byte{1}, nil, modulus, 1); err == nil {
		t.Fatal("ValidateVDF accepted empty proof")
	}
}

func testVDFModulus() *big.Int {
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	q := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 89), big.NewInt(1))
	return new(big.Int).Mul(p, q)
}

func testVDFWrongModulus() *big.Int {
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 127), big.NewInt(1))
	q := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 107), big.NewInt(1))
	return new(big.Int).Mul(p, q)
}

func testVDFLargeModulus() *big.Int {
	// 2^521 - 1 and 2^607 - 1 are known Mersenne primes.
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 521), big.NewInt(1))
	q := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 607), big.NewInt(1))
	return new(big.Int).Mul(p, q)
}
