package controller

import (
	"math/rand"
	"time"
)

// GenerateSequenceNumber generates a random int64 sequence number
func GenerateRandomSequenceNumber() int64 {
	rand.Seed(time.Now().UnixNano())
	return rand.Int63()
}
