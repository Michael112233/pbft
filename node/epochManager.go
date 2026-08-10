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

type EpochNode interface {
	GetNodeID() int
	SendandEnterEpochData(epoch int64, throughput float64, proposalRate float64)
	SwitchTrigger(protocol string)
	GetShadowSuspicionTotal() int64
}

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

type ShadowCount struct {
	shadowEpochStartTotal int64
	count                 int64
}
type EpochData struct {
	ThroughputData       ThroughputData
	ProposalIntervalData ProposalIntervalData
	ShadowCount          ShadowCount
}
type EpochManager struct {
	mu                    sync.RWMutex
	currentEpoch          int64
	epochData             map[int64]EpochData
	epochDecision         map[int64]string // it is decision for watermark send in current epoch and applied in next epoch and its reward come with watermark of next to next epoch
	log                   *logger.Logger
	csvFile               *os.File
	csvWriter             *csv.Writer
	node                  EpochNode
	latencyMonitor        *LatencyMonitor
	shadowEpoch           int64
	shadowEpochStartTotal int64
	shadowEpochStarted    bool
}

func NewEpochManager(node EpochNode, log *logger.Logger) *EpochManager {
	epochManager := &EpochManager{
		currentEpoch:   1,
		epochDecision:  make(map[int64]string),
		epochData:      make(map[int64]EpochData),
		latencyMonitor: NewLatencyMonitor(),

		log:  log,
		node: node,
	}
	epochManager.epochData[epochManager.currentEpoch] = EpochData{}
	epochManager.openEpochCSV()
	return epochManager
}

// recordShadowSuspicionTotal snapshots the monotonic node counter at the
// beginning of an epoch and returns its raw delta at the epoch boundary. It is
// independent of the currently disabled learning-epoch path.
func (em *EpochManager) recordShadowSuspicionTotal(lastExeSeq, total int64) (int64, int64, bool) {
	if lastExeSeq <= 0 {
		return 0, 0, false
	}

	em.mu.Lock()
	defer em.mu.Unlock()
	if !em.shadowEpochStarted {
		em.shadowEpoch = (lastExeSeq-1)/EPOCH_INTERVAL + 1
		em.shadowEpochStartTotal = total
		em.shadowEpochStarted = true
	}

	if lastExeSeq != em.shadowEpoch*EPOCH_INTERVAL {
		return 0, 0, false
	}

	epoch := em.shadowEpoch
	count := total - em.shadowEpochStartTotal
	em.shadowEpoch++
	em.shadowEpochStartTotal = total
	return epoch, count, true
}

func (em *EpochManager) openEpochCSV() {
	if err := os.MkdirAll("logs", 0755); err != nil {
		em.log.Error("Failed to create logs directory for epoch CSV: %v", err)
		return
	}
	nodeID := em.node.GetNodeID()

	path := filepath.Join("logs", fmt.Sprintf("epoch_data_node_%d.csv", nodeID))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		em.log.Error("Failed to open epoch CSV %s: %v", path, err)
		return
	}

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{"epoch", "throughput", "proposal_rate", "shadow_count"}); err != nil {
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

func (em *EpochManager) writeEpochCSV(epochNumber int64, throughput float64, proposalRate float64, shadowCount int64) error {
	if em.csvWriter == nil {

		return fmt.Errorf("epoch CSV writer is not initialized")
	}

	record := []string{
		strconv.FormatInt(epochNumber, 10),
		strconv.FormatFloat(throughput, 'f', 6, 64),
		strconv.FormatFloat(proposalRate, 'f', 6, 64),
		strconv.FormatInt(shadowCount, 10),
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

	if len(records) == 0 || len(records[0]) != 4 ||
		records[0][0] != "epoch" ||
		records[0][1] != "throughput" ||
		records[0][2] != "proposal_rate" ||
		records[0][3] != "shadow_count" {
		return fmt.Errorf("epoch CSV has an invalid header")
	}

	found := false
	for rowIndex, record := range records[1:] {
		if len(record) != 4 {
			return fmt.Errorf("epoch CSV row %d has %d fields, want 4", rowIndex+2, len(record))
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
func (em *EpochManager) ActiononLastExeSeq(lastExeSeq int64) bool {
	return false
	em.mu.Lock()

	if lastExeSeq == 1 {
		throughputStartTime := time.Now()
		proposalStartTime := time.Now() // a little delayed but its fine
		shadowCountStart := em.node.GetShadowSuspicionTotal()
		em.epochData[em.currentEpoch] = EpochData{
			ThroughputData: ThroughputData{
				StartTime: throughputStartTime,
			},
			ProposalIntervalData: ProposalIntervalData{
				StartTime: proposalStartTime,
			},
			ShadowCount: ShadowCount{
				shadowEpochStartTotal: shadowCountStart,
			},
		}

		em.mu.Unlock()
		return false
	}
	if lastExeSeq == EPOCH_INTERVAL*em.currentEpoch {

		epochData, ok := em.epochData[em.currentEpoch]
		if !ok {
			em.log.Error("Failed to retrieve epoch data at end of epoch number %d", em.currentEpoch)
			em.mu.Unlock()
			return false
		}
		epochData.ThroughputData.TotalTime = time.Since(epochData.ThroughputData.StartTime)
		epochData.ThroughputData.TotalRequests = EPOCH_INTERVAL
		epochData.ThroughputData.Throughput = float64(epochData.ThroughputData.TotalRequests) / epochData.ThroughputData.TotalTime.Seconds()
		em.epochData[em.currentEpoch] = epochData
		em.log.Info("Epoch %d completed", em.currentEpoch)
		em.log.FeatureInfo("Epoch %d completed", em.currentEpoch)
		decisionforNextEpoch, decisionExists := em.epochDecision[em.currentEpoch]
		if !decisionExists {
			em.log.Error("No epoch decision found for epoch %d", em.currentEpoch)

		}
		em.currentEpoch++
		throughputStartTime := time.Now()
		proposalStartTime := time.Now() // a little delayed but its fine
		shadowCountStart := em.node.GetShadowSuspicionTotal()
		em.epochData[em.currentEpoch] = EpochData{
			ThroughputData: ThroughputData{
				StartTime: throughputStartTime,
			},
			ProposalIntervalData: ProposalIntervalData{
				StartTime: proposalStartTime,
			},
			ShadowCount: ShadowCount{
				shadowEpochStartTotal: shadowCountStart,
			},
		}
		em.mu.Unlock()
		em.node.SwitchTrigger(decisionforNextEpoch)

		return true
	}

	if lastExeSeq == (em.currentEpoch-1)*EPOCH_INTERVAL+WATERMARK_INTERVAL {
		if em.currentEpoch == 1 {
			epochData, ok := em.epochData[em.currentEpoch]
			if !ok {
				em.log.Error("Failed to retrieve epoch data at watermark for current epoch number %d and its the first epoch", em.currentEpoch)
				em.mu.Unlock()
				return false
			}
			proposalRate := epochData.ProposalIntervalData.ProposalRate
			if proposalRate == 0 {
				em.log.Error("Proposal rate is zero at watermark for current epoch number %d and its the first epoch", em.currentEpoch)
				em.mu.Unlock()
				return false
			}
			shadowCountNow := em.node.GetShadowSuspicionTotal()
			epochData.ShadowCount.count = shadowCountNow - epochData.ShadowCount.shadowEpochStartTotal
			throughput := 0 // cant have throughput of last epoch as this is the first epoch
			_ = throughput  // used when the epoch CSV write call is added
			// at watermark we measure current epoch state and last epoch tput
			em.log.Info("Watermark reached for current epoch number %d", em.currentEpoch)
			em.log.FeatureInfo("Watermark reached for current epoch number %d", em.currentEpoch)
			go em.node.SendandEnterEpochData(em.currentEpoch, float64(throughput), proposalRate)
			writeTime := time.Now()
			writeErr := em.writeEpochCSV(em.currentEpoch, float64(throughput), proposalRate, epochData.ShadowCount.count)
			writeDuration := time.Since(writeTime)
			if writeDuration > 5*time.Millisecond {
				em.log.Error("Writing epoch CSV at watermark for current epoch number %d took longer than 5ms: %v", em.currentEpoch, writeDuration)
			}
			if writeErr != nil {
				em.log.Error("Failed to write epoch CSV at watermark for current epoch number %d: %v", em.currentEpoch, writeErr)
				em.mu.Unlock()
				return false
			}
		} else {
			currentEpochData, ok := em.epochData[em.currentEpoch]
			if !ok {
				em.log.Error("Failed to retrieve current epoch data at watermark for current epoch number %d", em.currentEpoch)
				em.mu.Unlock()
				return false
			}
			lastEpochData, ok := em.epochData[em.currentEpoch-1]
			if !ok {
				em.log.Error("Failed to retrieve last epoch data at watermark for current epoch number %d", em.currentEpoch)
				em.mu.Unlock()
				return false
			}
			proposalRate := currentEpochData.ProposalIntervalData.ProposalRate
			if proposalRate == 0 {
				em.log.Error("Proposal rate is zero at watermark for current epoch number %d", em.currentEpoch)
				em.mu.Unlock()
				return false
			}
			writeTime := time.Now()
			shadowCountNow := em.node.GetShadowSuspicionTotal()
			currentEpochData.ShadowCount.count = shadowCountNow - currentEpochData.ShadowCount.shadowEpochStartTotal
			writeErr := em.writeEpochCSV(em.currentEpoch, float64(0), proposalRate, currentEpochData.ShadowCount.count) // throughput will be updated later when epoch ends
			if writeErr != nil {
				em.log.Error("Failed to write epoch CSV at watermark for current epoch number %d: %v", em.currentEpoch, writeErr)
				em.mu.Unlock()
				return false
			}
			writeDuration := time.Since(writeTime)
			if writeDuration > 5*time.Millisecond {
				em.log.Error("Writing epoch CSV at watermark for current epoch number %d took longer than 5ms: %v", em.currentEpoch, writeDuration)
			}

			throughput := lastEpochData.ThroughputData.Throughput
			// at watermark we measure current epoch state and last epoch tput
			if throughput == 0 {
				em.log.Error("Throughput is zero at watermark for last epoch number %d", em.currentEpoch-1)
				em.mu.Unlock()
				return false
			}
			em.log.Info("Watermark reached for current epoch number %d", em.currentEpoch)
			em.log.FeatureInfo("Watermark reached for current epoch number %d", em.currentEpoch)
			go em.node.SendandEnterEpochData(em.currentEpoch, throughput, proposalRate)
			updateTime := time.Now()
			updateErr := em.updateEpochThroughputCSV(em.currentEpoch-1, throughput)
			if updateErr != nil {
				em.log.Error("Failed to update epoch CSV throughput at watermark for last epoch number %d: %v", em.currentEpoch-1, updateErr)
				em.mu.Unlock()
				return false
			}
			updateDuration := time.Since(updateTime)
			if updateDuration > 5*time.Millisecond {
				em.log.Error("Updating epoch CSV throughput at watermark for last epoch number %d took longer than 5ms: %v", em.currentEpoch-1, updateDuration)
			}
		}
	}
	em.mu.Unlock()
	return false
}

// since we dont do one req at a time and window is big so preprepare can be out of order
func (em *EpochManager) ActiononProposalInterval(seqNum int64) { // might get same  seqnum multiple times if in old view its not committed
	return
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
func (em *EpochManager) ReceiveEpochDecision(epoch int64, protocol string) {
	return
	em.mu.Lock()

	if epoch != em.currentEpoch {
		em.log.Error("Received epoch decision for epoch %d, but current epoch is %d. Ignoring.", epoch, em.currentEpoch)
		em.mu.Unlock()
		return
	}
	em.log.Info("Received epoch decision for epoch %d: protocol=%s", epoch, protocol)
	em.log.FeatureInfo("Received epoch decision for epoch %d: protocol=%s", epoch, protocol)
	em.epochDecision[epoch] = protocol
	em.mu.Unlock()

}

func (em *EpochManager) GetCurrentEpoch() int64 {
	em.mu.RLock()
	defer em.mu.RUnlock()
	return em.currentEpoch
}

func (em *EpochManager) StartLatencyMonitoring() {
	em.latencyMonitor.StartMonitoring()
}

func (em *EpochManager) StopLatencyMonitoring() []time.Duration {
	return em.latencyMonitor.StopMonitoring()
}
func (em *EpochManager) RecordStartTime(digest [32]byte, startTime time.Time) {
	em.latencyMonitor.RecordStartTime(digest, startTime)
}

func (em *EpochManager) RecordEndTime(digest [32]byte, endTime time.Time) {
	em.latencyMonitor.RecordEndTime(digest, endTime)
}

func (n *Node) StartLatencyMonitoring() { //right now called open ended but merge with epoch
	n.epochManager.StartLatencyMonitoring()
}

func (n *Node) StopLatencyMonitoring() []time.Duration {
	return n.epochManager.StopLatencyMonitoring()
}
func (n *Node) RecordStartTime(digest [32]byte, startTime time.Time) { // see its call site in preprepare and new view
	n.epochManager.RecordStartTime(digest, startTime)
}

func (n *Node) RecordEndTime(digest [32]byte, endTime time.Time) {
	n.epochManager.RecordEndTime(digest, endTime)
}
func (n *Node) EpochReqExecuted(seq int64) bool {
	if epoch, count, complete := n.epochManager.recordShadowSuspicionTotal(seq, n.GetShadowSuspicionTotal()); complete {
		n.log.Info("Raw shadow suspicion count for epoch %d on node %d is %d", epoch, n.GetNodeID(), count)
	}
	return n.epochManager.ActiononLastExeSeq(seq)
}

func (n *Node) EpochProposalInterval(seq int64) {
	n.epochManager.ActiononProposalInterval(seq)
}

func (n *Node) SendandEnterEpochData(epoch int64, throughput float64, proposalRate float64) {
	n.epochAggregator.SendandEnterEpochData(epoch, throughput, proposalRate)
}

func (n *Node) HandleDecisionFromLearningAgent(epoch int64, protocol string) {
	n.epochManager.ReceiveEpochDecision(epoch, protocol)
}

// access to preprepare seq number would be good, but need to fix its locking and handling
