package attacks

import (
	"encoding/binary"
	"hash/fnv"
	"math/rand"
)

// newTaggedRNG derives an RNG from a root seed and a tag to create independent substreams
func newTaggedRNG(rootSeed int64, tag string) *rand.Rand {
	h := fnv.New64a()
	_, _ = h.Write([]byte(tag))
	tagHash := int64(h.Sum64())
	mixed := mix64(uint64(rootSeed) ^ uint64(tagHash))
	return rand.New(rand.NewSource(int64(mixed)))
}

// mix64 is a splitmix64-style mixer to spread seed bits
func mix64(z uint64) uint64 {
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	z = z ^ (z >> 31)
	return z
}

// Uint64ToBytes converts a uint64 to a byte slice (big endian).
func Uint64ToBytes(v uint64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, v)
	return b
}
