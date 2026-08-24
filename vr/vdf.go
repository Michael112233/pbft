package vr

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
)

const (
	vdfGroupDomain     = "pbft-vdf-group-v1"
	vdfChallengeDomain = "pbft-vdf-challenge-v1"
	vdfPrimeBytes      = 16
)

var (
	bigOne = big.NewInt(1)
	bigTwo = big.NewInt(2)
)

// EvalVDF evaluates a repeated-squaring VDF for delaySteps sequential steps.
//
// The input is deterministically mapped into the RSA group modulo modulus.
// EvalVDF returns y = x^(2^delaySteps) mod modulus and a Wesolowski-style proof.
func EvalVDF(input []byte, modulus *big.Int, delaySteps uint64) (y []byte, proof []byte, err error) {
	if err := validateVDFModulus(modulus); err != nil {
		return nil, nil, err
	}

	x, err := vdfInputToGroup(input, modulus)
	if err != nil {
		return nil, nil, err
	}

	yInt := new(big.Int).Set(x)
	for i := uint64(0); i < delaySteps; i++ {
		yInt.Mul(yInt, yInt)
		yInt.Mod(yInt, modulus)
	}

	challenge := vdfChallengePrime(input, yInt, modulus, delaySteps)
	exponent, err := twoToDelay(delaySteps)
	if err != nil {
		return nil, nil, err
	}

	quotient := new(big.Int).Div(exponent, challenge)
	proofInt := new(big.Int).Exp(x, quotient, modulus)

	return yInt.Bytes(), proofInt.Bytes(), nil
}

// ValidateVDF verifies y and proof for input, modulus, and delaySteps.
func ValidateVDF(input []byte, y []byte, proof []byte, modulus *big.Int, delaySteps uint64) (bool, error) {
	if err := validateVDFModulus(modulus); err != nil {
		return false, err
	}

	yInt, err := parseVDFResidue(y, modulus, "y")
	if err != nil {
		return false, err
	}
	proofInt, err := parseVDFResidue(proof, modulus, "proof")
	if err != nil {
		return false, err
	}
	x, err := vdfInputToGroup(input, modulus)
	if err != nil {
		return false, err
	}

	challenge := vdfChallengePrime(input, yInt, modulus, delaySteps)
	remainder := new(big.Int).Exp(bigTwo, new(big.Int).SetUint64(delaySteps), challenge)

	left := new(big.Int).Exp(proofInt, challenge, modulus)
	right := new(big.Int).Exp(x, remainder, modulus)
	left.Mul(left, right)
	left.Mod(left, modulus)

	return left.Cmp(yInt) == 0, nil
}

func validateVDFModulus(modulus *big.Int) error {
	if modulus == nil {
		return errors.New("VDF modulus cannot be nil")
	}
	if modulus.Sign() <= 0 {
		return errors.New("VDF modulus must be positive")
	}
	if modulus.Cmp(big.NewInt(3)) <= 0 {
		return errors.New("VDF modulus must be greater than 3")
	}
	if modulus.Bit(0) == 0 {
		return errors.New("VDF modulus must be odd")
	}

	return nil
}

func vdfInputToGroup(input []byte, modulus *big.Int) (*big.Int, error) {
	byteLen := (modulus.BitLen() + 7) / 8
	for counter := uint64(0); ; counter++ {
		candidate := new(big.Int).SetBytes(vdfExpandHash(vdfGroupDomain, input, counter, byteLen))
		candidate.Mod(candidate, modulus)
		if candidate.Sign() > 0 && isCoprime(candidate, modulus) {
			return candidate, nil
		}
		if counter == ^uint64(0) {
			return nil, errors.New("exhausted VDF input-to-group counter")
		}
	}
}

func parseVDFResidue(value []byte, modulus *big.Int, name string) (*big.Int, error) {
	if len(value) == 0 {
		return nil, fmt.Errorf("VDF %s cannot be empty", name)
	}

	residue := new(big.Int).SetBytes(value)
	if residue.Sign() <= 0 {
		return nil, fmt.Errorf("VDF %s must be positive", name)
	}
	if residue.Cmp(modulus) >= 0 {
		return nil, fmt.Errorf("VDF %s must be less than modulus", name)
	}
	if !isCoprime(residue, modulus) {
		return nil, fmt.Errorf("VDF %s must be coprime to modulus", name)
	}

	return residue, nil
}

func vdfChallengePrime(input []byte, y *big.Int, modulus *big.Int, delaySteps uint64) *big.Int {
	transcript := vdfChallengeTranscript(input, y, modulus, delaySteps)
	for counter := uint64(0); ; counter++ {
		candidate := new(big.Int).SetBytes(vdfExpandHash(vdfChallengeDomain, transcript, counter, vdfPrimeBytes))
		candidate.SetBit(candidate, (vdfPrimeBytes*8)-1, 1)
		candidate.SetBit(candidate, 0, 1)
		if candidate.ProbablyPrime(32) {
			return candidate
		}
	}
}

func vdfChallengeTranscript(input []byte, y *big.Int, modulus *big.Int, delaySteps uint64) []byte {
	var transcript []byte
	transcript = appendLengthPrefixedBytes(transcript, input)
	transcript = appendLengthPrefixedBytes(transcript, y.Bytes())
	transcript = appendLengthPrefixedBytes(transcript, modulus.Bytes())
	transcript = binary.BigEndian.AppendUint64(transcript, delaySteps)
	return transcript
}

func vdfExpandHash(domain string, seed []byte, counter uint64, byteLen int) []byte {
	out := make([]byte, 0, byteLen)
	for block := uint64(0); len(out) < byteLen; block++ {
		hasher := sha256.New()
		_, _ = hasher.Write([]byte(domain))
		_, _ = hasher.Write(seed)
		var counters [16]byte
		binary.BigEndian.PutUint64(counters[:8], counter)
		binary.BigEndian.PutUint64(counters[8:], block)
		_, _ = hasher.Write(counters[:])
		out = append(out, hasher.Sum(nil)...)
	}

	return out[:byteLen]
}

func appendLengthPrefixedBytes(dst []byte, value []byte) []byte {
	dst = binary.BigEndian.AppendUint64(dst, uint64(len(value)))
	return append(dst, value...)
}

func twoToDelay(delaySteps uint64) (*big.Int, error) {
	if delaySteps > uint64(^uint(0)) {
		return nil, errors.New("delaySteps is too large for this platform")
	}

	return new(big.Int).Lsh(bigOne, uint(delaySteps)), nil
}

func isCoprime(a *big.Int, b *big.Int) bool {
	return new(big.Int).GCD(nil, nil, a, b).Cmp(bigOne) == 0
}

func VDFLargeModulus() *big.Int {
	// 2^521 - 1 and 2^607 - 1 are known Mersenne primes.
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 521), big.NewInt(1))
	q := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 607), big.NewInt(1))
	return new(big.Int).Mul(p, q)
}
