package node

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

const (
	throughputMeasurementBatchSize  = 10
	throughputMeasurementBufferSize = 1000
)

// this is on hot path of exe if channel full
// batch size too small

type throughputMeasurement struct {
	MeasurementTime time.Time
	View            int64
	LeaderID        int
	Seq             int64
	ExecutedSlots   int64
	ElapsedSeconds  float64
	Throughput      float64
}

func (n *Node) throughputMeasurementStart() {
	if n.throughputMeasurementsStarted.CompareAndSwap(false, true) {
		go n.throughputMeasurementCSVWriter()
	}
}

func (n *Node) throughputMeasurementStop() {
	n.throughputMeasurementsOnce.Do(func() {
		close(n.throughputMeasurementsStop)
	})
	if n.throughputMeasurementsStarted.Load() {
		<-n.throughputMeasurementsDone
	}
}

func (n *Node) emitThroughputMeasurement(measurement throughputMeasurement) {

	select {
	case n.throughputMeasurementsChan <- measurement:
	default:
		n.log.Warn("too fast for tput measurement")
	}
}

func (n *Node) throughputMeasurementCSVWriter() {
	defer close(n.throughputMeasurementsDone)

	batch := make([]throughputMeasurement, 0, throughputMeasurementBatchSize)
	for {
		select {
		case measurement := <-n.throughputMeasurementsChan:
			batch = append(batch, measurement)
			if len(batch) >= throughputMeasurementBatchSize {
				n.appendThroughputMeasurements(batch)
				batch = batch[:0]
			}
		case <-n.throughputMeasurementsStop:
			for {
				select {
				case measurement := <-n.throughputMeasurementsChan:
					batch = append(batch, measurement)
				default:
					if len(batch) > 0 {
						n.appendThroughputMeasurements(batch)
					}
					return
				}
			}
		}
	}
}

func (n *Node) appendThroughputMeasurements(measurements []throughputMeasurement) {
	if len(measurements) == 0 {
		return
	}
	if err := os.MkdirAll("logs", 0755); err != nil {
		if n.log != nil {
			n.log.Error("Failed to create logs directory for throughput CSV: %v", err)
		}
		return
	}

	path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_throughput.csv")
	needsHeader := true
	if info, err := os.Stat(path); err == nil && info.Size() > 0 {
		needsHeader = false
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		if n.log != nil {
			n.log.Error("Failed to open throughput CSV %s: %v", path, err)
		}
		return
	}
	defer file.Close()

	writer := csv.NewWriter(file)
	if needsHeader {
		if err := writer.Write([]string{"view", "time", "leader_id", "executed_slots", "elapsed_seconds", "tput_measurement", "seq"}); err != nil {
			if n.log != nil {
				n.log.Error("Failed to write throughput CSV header: %v", err)
			}
			return
		}
	}

	for _, measurement := range measurements {
		record := []string{
			strconv.FormatInt(measurement.View, 10),
			measurement.MeasurementTime.Format(time.RFC3339Nano),
			strconv.Itoa(measurement.LeaderID),
			strconv.FormatInt(measurement.ExecutedSlots, 10),
			strconv.FormatFloat(measurement.ElapsedSeconds, 'f', 6, 64),
			strconv.FormatFloat(measurement.Throughput, 'f', 6, 64),
			strconv.FormatInt(measurement.Seq, 10),
		}
		if err := writer.Write(record); err != nil {
			if n.log != nil {
				n.log.Error("Failed to write throughput CSV row: %v", err)
			}
			return
		}
	}

	writer.Flush()
	if err := writer.Error(); err != nil && n.log != nil {
		n.log.Error("Failed to flush throughput CSV: %v", err)
	}
}
