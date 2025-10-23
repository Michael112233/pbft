package node

import (
	"math/rand"
	"time"
)

// GenerateSequenceNumber generates a random int64 sequence number
func GenerateRandomSequenceNumber(upperBound int64, lowerBound int64) int64 {
	rand.Seed(time.Now().UnixNano())
	return lowerBound + rand.Int63n(upperBound-100000-lowerBound)
}
