// Package verifiablerandomness wraps the repo's VRF proof/beta helpers.
//
// The primary implementation uses P-256 ECVRF keys through go-ecvrf. These are
// intentionally separate from the Ed25519 signing keys used elsewhere in the
// repo; do not convert Ed25519 keys into ECDSA keys. If Ed25519 key reuse is
// required later, add separate, explicitly named helpers for that construction.
package verifiablerandomness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	ecvrf "github.com/vechain/go-ecvrf"
)

var p256Curve = elliptic.P256()

const betaRangeDomain = "pbft-vrf-beta-range-v1"

// CreateProofAndBeta creates a P-256 ECVRF proof for input using privateKey.
//
// It returns proof first and beta second to match the package API. The wrapped
// go-ecvrf implementation returns beta first.
func CreateProofAndBeta(privateKey *ecdsa.PrivateKey, input []byte) (proof []byte, beta []byte, err error) {
	if err := validateP256PrivateKey(privateKey); err != nil {
		return nil, nil, err
	}

	beta, proof, err = ecvrf.P256Sha256Tai.Prove(privateKey, input)
	if err != nil {
		return nil, nil, fmt.Errorf("create VRF proof: %w", err)
	}
	if len(proof) == 0 {
		return nil, nil, errors.New("create VRF proof: empty proof")
	}
	if len(beta) == 0 {
		return nil, nil, errors.New("create VRF proof: empty beta")
	}

	return proof, beta, nil
}

// VerifyProof verifies proof against publicKey and input, then returns beta.
//
// If callers transmit beta separately, compare their transmitted beta with this
// returned value after verification succeeds.
func VerifyProof(publicKey *ecdsa.PublicKey, input []byte, proof []byte) (beta []byte, err error) {
	if err := validateP256PublicKey(publicKey); err != nil {
		return nil, err
	}
	if len(proof) == 0 {
		return nil, errors.New("VRF proof cannot be empty")
	}

	beta, err = ecvrf.P256Sha256Tai.Verify(publicKey, input, proof)
	if err != nil {
		return nil, fmt.Errorf("verify VRF proof: %w", err)
	}
	if len(beta) == 0 {
		return nil, errors.New("verify VRF proof: empty beta")
	}

	return beta, nil
}

// NumberFromBeta deterministically maps beta to an int in the inclusive range
// [min, max]. The beta should come from a verified VRF proof.
func NumberFromBeta(beta []byte, min int, max int) (int, error) {
	if len(beta) == 0 {
		return 0, errors.New("beta cannot be empty")
	}
	if min > max {
		return 0, errors.New("min cannot be greater than max")
	}
	if min == max {
		return min, nil
	}

	minBig := big.NewInt(int64(min))
	rangeSize := big.NewInt(int64(max))
	rangeSize.Sub(rangeSize, minBig)
	rangeSize.Add(rangeSize, big.NewInt(1))

	offset, err := numberFromBetaUniform(beta, rangeSize)
	if err != nil {
		return 0, err
	}

	result := new(big.Int).Add(minBig, offset)
	return int(result.Int64()), nil
}

func validateP256PrivateKey(privateKey *ecdsa.PrivateKey) error {
	if privateKey == nil {
		return errors.New("VRF private key cannot be nil")
	}
	if privateKey.D == nil {
		return errors.New("VRF private key scalar cannot be nil")
	}
	if privateKey.D.Sign() <= 0 || privateKey.D.Cmp(p256Curve.Params().N) >= 0 {
		return errors.New("VRF private key scalar is out of range")
	}
	if err := validateP256PublicKey(&privateKey.PublicKey); err != nil {
		return fmt.Errorf("invalid VRF private key public component: %w", err)
	}

	x, y := p256Curve.ScalarBaseMult(privateKey.D.Bytes())
	if x == nil || y == nil || x.Cmp(privateKey.X) != 0 || y.Cmp(privateKey.Y) != 0 {
		return errors.New("VRF private key public component does not match private scalar")
	}

	return nil
}

func validateP256PublicKey(publicKey *ecdsa.PublicKey) error {
	if publicKey == nil {
		return errors.New("VRF public key cannot be nil")
	}
	if publicKey.Curve == nil {
		return errors.New("VRF public key curve cannot be nil")
	}
	if !isP256Curve(publicKey.Curve) {
		return errors.New("VRF public key must use P-256 curve")
	}
	if publicKey.X == nil || publicKey.Y == nil {
		return errors.New("VRF public key point cannot be nil")
	}
	if !publicKey.Curve.IsOnCurve(publicKey.X, publicKey.Y) {
		return errors.New("VRF public key point is not on curve")
	}

	return nil
}

func isP256Curve(curve elliptic.Curve) bool {
	if curve == nil || curve.Params() == nil {
		return false
	}

	return curve.Params().Name == p256Curve.Params().Name
}

func numberFromBetaUniform(beta []byte, maxExclusive *big.Int) (*big.Int, error) {
	if maxExclusive == nil || maxExclusive.Sign() <= 0 {
		return nil, errors.New("range size must be positive")
	}

	twoTo256 := new(big.Int).Lsh(big.NewInt(1), 256)
	limit := new(big.Int).Div(new(big.Int).Set(twoTo256), maxExclusive)
	limit.Mul(limit, maxExclusive)

	for counter := uint64(0); ; counter++ {
		sum := betaRangeHash(beta, counter)
		candidate := new(big.Int).SetBytes(sum[:])
		if candidate.Cmp(limit) < 0 {
			return candidate.Mod(candidate, maxExclusive), nil
		}
		if counter == ^uint64(0) {
			return nil, errors.New("exhausted beta expansion counter")
		}
	}
}

func betaRangeHash(beta []byte, counter uint64) [sha256.Size]byte {
	var counterBytes [8]byte
	binary.BigEndian.PutUint64(counterBytes[:], counter)

	hasher := sha256.New()
	_, _ = hasher.Write([]byte(betaRangeDomain))
	_, _ = hasher.Write(beta)
	_, _ = hasher.Write(counterBytes[:])

	var sum [sha256.Size]byte
	copy(sum[:], hasher.Sum(nil))
	return sum
}
