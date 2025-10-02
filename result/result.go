package result

import (
	"encoding/csv"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/logger"
)

var (
	startTime time.Time
	endTime   time.Time

	committedTransactionNum atomic.Int64
	log                     *logger.Logger
	Tps_list                []float64
	Time_list               []float64
	Latency_list            []float64
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

func GetCommittedTransactionNum() int64 {
	return committedTransactionNum.Load()
}

func PrintResult() {
	SetEndTime(time.Now())
	current_tps := CalculateTPS()
	current_time := endTime.Sub(startTime).Seconds()
	log.Info("Result:")
	log.Info("TPS: %f\n", current_tps)
	log.Info("Time: %f\n", current_time)
	log.Info("Committed Transaction Num: %d\n", committedTransactionNum.Load())
	Tps_list = append(Tps_list, current_tps)
	Time_list = append(Time_list, current_time)
}

func AddLatency(latency float64) {
	Latency_list = append(Latency_list, latency)
}

// ExportToCSV exports Tps_list, Time_list, and Latency_list to a CSV file
func ExportToCSV(filename string) error {
	file, err := os.Create(filename)
	if err != nil {
		return fmt.Errorf("failed to create CSV file: %v", err)
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	defer writer.Flush()

	// Write header
	if err := writer.Write([]string{"Time", "TPS", "Latency"}); err != nil {
		return fmt.Errorf("failed to write CSV header: %v", err)
	}

	// Write data - use the maximum length to ensure all data is written
	maxLen := len(Time_list)
	if len(Tps_list) > maxLen {
		maxLen = len(Tps_list)
	}
	if len(Latency_list) > maxLen {
		maxLen = len(Latency_list)
	}

	for i := 0; i < maxLen; i++ {
		var timeStr, tpsStr, latencyStr string

		// Handle Time data
		if i < len(Time_list) {
			timeStr = strconv.FormatFloat(Time_list[i], 'f', 6, 64)
		} else {
			timeStr = ""
		}

		// Handle TPS data
		if i < len(Tps_list) {
			tpsStr = strconv.FormatFloat(Tps_list[i], 'f', 6, 64)
		} else {
			tpsStr = ""
		}

		// Handle Latency data
		if i < len(Latency_list) {
			latencyStr = strconv.FormatFloat(Latency_list[i], 'f', 6, 64)
		} else {
			latencyStr = ""
		}

		if err := writer.Write([]string{timeStr, tpsStr, latencyStr}); err != nil {
			return fmt.Errorf("failed to write CSV data: %v", err)
		}
	}

	log.Info("CSV file exported successfully: %s", filename)
	return nil
}
