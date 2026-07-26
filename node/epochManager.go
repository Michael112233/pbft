package node

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/michael112233/pbft/logger"
)

const (
	EPOCH_INTERVAL     int64 = 5000
	WATERMARK_INTERVAL int64 = 2500 // sizes related to sliding window
)

type ThroughputData struct {
	TotalRequests int64
	StartTime     time.Time
	TotalTime     time.Duration
	Throughput    float64
}

type ProposalIntervalData struct {
	TotalProposals int64
	StartTime      time.Time
	TotalTime      time.Duration
	ProposalRate   float64
}
type EpochData struct {
	ThroughputData       ThroughputData
	ProposalIntervalData ProposalIntervalData
}
type EpochManager struct {
	mu           sync.RWMutex
	currentEpoch int64
	epochData    map[int64]EpochData
	log          *logger.Logger
	csvFile      *os.File
	csvWriter    *csv.Writer
}

func NewEpochManager(log *logger.Logger) *EpochManager {
	epochManager := &EpochManager{
		currentEpoch: 1,
		epochData:    make(map[int64]EpochData),
		log:          log,
	}
	epochManager.epochData[epochManager.currentEpoch] = EpochData{}
	epochManager.openEpochCSV()
	return epochManager
}

func (em *EpochManager) openEpochCSV() {
	if err := os.MkdirAll("logs", 0755); err != nil {
		em.log.Error("Failed to create logs directory for epoch CSV: %v", err)
		return
	}

	path := filepath.Join("logs", "epoch_metrics.csv")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		em.log.Error("Failed to open epoch CSV %s: %v", path, err)
		return
	}

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"epoch", "throughput", "proposal_rate"}); err != nil {
		em.log.Error("Failed to write epoch CSV header: %v", err)
		_ = file.Close()
		return
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		em.log.Error("Failed to flush epoch CSV header: %v", err)
		_ = file.Close()
		return
	}

	em.csvFile = file
	em.csvWriter = writer
}

func (em *EpochManager) writeEpochCSV(epochNumber int64, throughput float64, proposalRate float64) {
	if em.csvWriter == nil {
		em.log.Error("Epoch CSV writer is not initialized")
		return
	}

	record := []string{
		strconv.FormatInt(epochNumber, 10),
		strconv.FormatFloat(throughput, 'f', 6, 64),
		strconv.FormatFloat(proposalRate, 'f', 6, 64),
	}
	if err := em.csvWriter.Write(record); err != nil {
		em.log.Error("Failed to write epoch CSV row: %v", err)
		return
	}

	em.csvWriter.Flush()
	if err := em.csvWriter.Error(); err != nil {
		em.log.Error("Failed to flush epoch CSV row: %v", err)
	}
}

// state transfer jumps
// node functions
// outer node fun inner em function
// lock even necessary
func (em *EpochManager) ActiononLastExeSeq(lastExeSeq int64) {
	em.mu.Lock()
	defer em.mu.Unlock()
	if lastExeSeq == 1 {
		throughputStartTime := time.Now()
		proposalStartTime := time.Now() // a little delayed but its fine
		em.epochData[em.currentEpoch] = EpochData{
			ThroughputData: ThroughputData{
				StartTime: throughputStartTime,
			},
			ProposalIntervalData: ProposalIntervalData{
				StartTime: proposalStartTime,
			},
		}
		return
	}
	if lastExeSeq == EPOCH_INTERVAL*em.currentEpoch {

		epochData, ok := em.epochData[em.currentEpoch]
		if !ok {
			em.log.Error("Failed to retrieve epoch data at end of epoch number %d", em.currentEpoch)
			return
		}
		epochData.ThroughputData.TotalTime = time.Since(epochData.ThroughputData.StartTime)
		epochData.ThroughputData.TotalRequests = EPOCH_INTERVAL
		epochData.ThroughputData.Throughput = float64(epochData.ThroughputData.TotalRequests) / epochData.ThroughputData.TotalTime.Seconds()
		em.epochData[em.currentEpoch] = epochData

		em.currentEpoch++
		throughputStartTime := time.Now()
		proposalStartTime := time.Now() // a little delayed but its fine
		em.epochData[em.currentEpoch] = EpochData{
			ThroughputData: ThroughputData{
				StartTime: throughputStartTime,
			},
			ProposalIntervalData: ProposalIntervalData{
				StartTime: proposalStartTime,
			},
		}
		return
	}

	if lastExeSeq == (em.currentEpoch-1)*EPOCH_INTERVAL+WATERMARK_INTERVAL {
		if em.currentEpoch == 1 {
			epochData, ok := em.epochData[em.currentEpoch]
			if !ok {
				em.log.Error("Failed to retrieve epoch data at watermark for current epoch number %d and its the first epoch", em.currentEpoch)
				return
			}
			proposalRate := epochData.ProposalIntervalData.ProposalRate
			if proposalRate == 0 {
				em.log.Error("Proposal rate is zero at watermark for current epoch number %d and its the first epoch", em.currentEpoch)
				return
			}
			throughput := 0 // cant have throughput of last epoch as this is the first epoch
			_ = throughput  // used when the epoch CSV write call is added
			// at watermark we measure current epoch state and last epoch tput
		} else {
			currentEpochData, ok := em.epochData[em.currentEpoch]
			if !ok {
				em.log.Error("Failed to retrieve current epoch data at watermark for current epoch number %d", em.currentEpoch)
				return
			}
			lastEpochData, ok := em.epochData[em.currentEpoch-1]
			if !ok {
				em.log.Error("Failed to retrieve last epoch data at watermark for current epoch number %d", em.currentEpoch)
				return
			}
			proposalRate := currentEpochData.ProposalIntervalData.ProposalRate
			if proposalRate == 0 {
				em.log.Error("Proposal rate is zero at watermark for current epoch number %d", em.currentEpoch)
				return
			}
			throughput := lastEpochData.ThroughputData.Throughput
			// at watermark we measure current epoch state and last epoch tput
			if throughput == 0 {
				em.log.Error("Throughput is zero at watermark for last epoch number %d", em.currentEpoch-1)
				return
			}
		}
	}
}

// since we dont do one req at a time and window is big so preprepare can be out of order
func (em *EpochManager) ActiononProposalInterval(seqNum int64) {
	em.mu.Lock()
	defer em.mu.Unlock()
	// if seqNum == 1 {
	// 	proposalStartTime := time.Now()
	// 	epochData, ok := em.epochData[em.currentEpoch]
	// 	if !ok {
	// 		em.log.Error("Failed to retrieve epoch data")
	// 		return
	// 	}
	// 	epochData.ProposalIntervalData.StartTime = proposalStartTime
	// 	em.epochData[em.currentEpoch] = epochData
	// 	return
	// }
	if seqNum == WATERMARK_INTERVAL*em.currentEpoch {
		epochData, ok := em.epochData[em.currentEpoch]
		if !ok {
			em.log.Error("Failed to retrieve epoch data") // out of order
			return
		}
		if epochData.ProposalIntervalData.StartTime.IsZero() {
			em.log.Error("Proposal interval start time is not set")
			return
		}
		epochData.ProposalIntervalData.TotalTime = time.Since(epochData.ProposalIntervalData.StartTime)
		epochData.ProposalIntervalData.TotalProposals = WATERMARK_INTERVAL
		epochData.ProposalIntervalData.ProposalRate = float64(epochData.ProposalIntervalData.TotalProposals) / epochData.ProposalIntervalData.TotalTime.Seconds()
		em.epochData[em.currentEpoch] = epochData
		return
	}
}

// access to preprepare seq number would be good, but need to fix its locking and handling
