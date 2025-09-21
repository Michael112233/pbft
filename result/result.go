package result

import (
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/logger"
)

var (
	startTime time.Time
	endTime   time.Time

	committedTransactionNum atomic.Int64
	log                     *logger.Logger
)

func init() {
	log = logger.NewLogger(0, "result")
	committedTransactionNum.Store(0)
}

func CalculateTPS() float64 {
	return float64(committedTransactionNum.Load()) / (endTime.Sub(startTime).Seconds())
}

func SetStartTime(t time.Time) {
	startTime = t
}

func SetEndTime(t time.Time) {
	endTime = t
}

func AddCommittedTransactionNum(n int64) {
	committedTransactionNum.Add(n)
}

func PrintResult() {
	log.Info("Result:")
	log.Info("TPS: %f\n", CalculateTPS())
	log.Info("Latency: %f\n", endTime.Sub(startTime).Seconds())
	log.Info("Committed Transaction Num: %d\n", committedTransactionNum.Load())
}
