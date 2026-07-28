package node

import (
	"encoding/csv"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"

	"github.com/michael112233/pbft/logger"
)

// hot path file writes
const (
	EPOCH_INTERVAL     int64 = 120000
	WATERMARK_INTERVAL int64 = 60000 // sizes related to sliding window
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

func (em *EpochManager) writeEpochCSV(epochNumber int64, throughput float64, proposalRate float64) error {
	if em.csvWriter == nil {

		return fmt.Errorf("epoch CSV writer is not initialized")
	}

	record := []string{
		strconv.FormatInt(epochNumber, 10),
		strconv.FormatFloat(throughput, 'f', 6, 64),
		strconv.FormatFloat(proposalRate, 'f', 6, 64),
	}
	if err := em.csvWriter.Write(record); err != nil {

		return fmt.Errorf("write epoch CSV row: %w", err)
	}

	em.csvWriter.Flush()
	if err := em.csvWriter.Error(); err != nil {
		return fmt.Errorf("flush epoch CSV: %w", err)
	}
	return nil
}

func (em *EpochManager) updateEpochThroughputCSV(epochNumber int64, throughput float64) error {
	if em.csvWriter == nil || em.csvFile == nil {
		return fmt.Errorf("epoch CSV writer is not initialized")
	}

	em.csvWriter.Flush()
	if err := em.csvWriter.Error(); err != nil {
		return fmt.Errorf("flush epoch CSV before update: %w", err)
	}

	file, err := os.Open(em.csvFile.Name())
	if err != nil {
		return fmt.Errorf("open epoch CSV for reading: %w", err)
	}
	records, readErr := csv.NewReader(file).ReadAll()
	closeErr := file.Close()
	if readErr != nil {
		return fmt.Errorf("read epoch CSV: %w", readErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close epoch CSV after reading: %w", closeErr)
	}

	if len(records) == 0 || len(records[0]) != 3 ||
		records[0][0] != "epoch" ||
		records[0][1] != "throughput" ||
		records[0][2] != "proposal_rate" {
		return fmt.Errorf("epoch CSV has an invalid header")
	}

	found := false
	for rowIndex, record := range records[1:] {
		if len(record) != 3 {
			return fmt.Errorf("epoch CSV row %d has %d fields, want 3", rowIndex+2, len(record))
		}

		recordEpoch, err := strconv.ParseInt(record[0], 10, 64)
		if err != nil {
			return fmt.Errorf("parse epoch at CSV row %d: %w", rowIndex+2, err)
		}
		if recordEpoch == epochNumber {
			record[1] = strconv.FormatFloat(throughput, 'f', 6, 64)
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("epoch %d not found in CSV", epochNumber)
	}

	if err := em.csvFile.Truncate(0); err != nil {
		return fmt.Errorf("truncate epoch CSV: %w", err)
	}
	if _, err := em.csvFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("seek to start of epoch CSV: %w", err)
	}

	writer := csv.NewWriter(em.csvFile)
	writer.WriteAll(records)
	if err := writer.Error(); err != nil {
		return fmt.Errorf("rewrite epoch CSV: %w", err)
	}
	em.csvWriter = writer

	return nil
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
			writeTime := time.Now()
			writeErr := em.writeEpochCSV(em.currentEpoch, float64(throughput), proposalRate)
			writeDuration := time.Since(writeTime)
			if writeDuration > 5*time.Millisecond {
				em.log.Error("Writing epoch CSV at watermark for current epoch number %d took longer than 5ms: %v", em.currentEpoch, writeDuration)
			}
			if writeErr != nil {
				em.log.Error("Failed to write epoch CSV at watermark for current epoch number %d: %v", em.currentEpoch, writeErr)
				return
			}
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
			writeTime := time.Now()
			writeErr := em.writeEpochCSV(em.currentEpoch, float64(0), proposalRate) // throughput will be updated later when epoch ends
			if writeErr != nil {
				em.log.Error("Failed to write epoch CSV at watermark for current epoch number %d: %v", em.currentEpoch, writeErr)
				return
			}
			writeDuration := time.Since(writeTime)
			if writeDuration > 5*time.Millisecond {
				em.log.Error("Writing epoch CSV at watermark for current epoch number %d took longer than 5ms: %v", em.currentEpoch, writeDuration)
			}

			throughput := lastEpochData.ThroughputData.Throughput
			// at watermark we measure current epoch state and last epoch tput
			if throughput == 0 {
				em.log.Error("Throughput is zero at watermark for last epoch number %d", em.currentEpoch-1)
				return
			}
			updateTime := time.Now()
			updateErr := em.updateEpochThroughputCSV(em.currentEpoch-1, throughput)
			if updateErr != nil {
				em.log.Error("Failed to update epoch CSV throughput at watermark for last epoch number %d: %v", em.currentEpoch-1, updateErr)
				return
			}
			updateDuration := time.Since(updateTime)
			if updateDuration > 5*time.Millisecond {
				em.log.Error("Updating epoch CSV throughput at watermark for last epoch number %d took longer than 5ms: %v", em.currentEpoch-1, updateDuration)
			}
		}
	}
}

// since we dont do one req at a time and window is big so preprepare can be out of order
func (em *EpochManager) ActiononProposalInterval(seqNum int64) { // might get same  seqnum multiple times if in old view its not committed
	em.mu.Lock()
	defer em.mu.Unlock()

	if seqNum == (em.currentEpoch-1)*EPOCH_INTERVAL+WATERMARK_INTERVAL {
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

func (n *Node) EpochReqExecuted(seq int64) {
	n.epochManager.ActiononLastExeSeq(seq)
}

func (n *Node) EpochProposalInterval(seq int64) {
	n.epochManager.ActiononProposalInterval(seq)
}

// access to preprepare seq number would be good, but need to fix its locking and handling
