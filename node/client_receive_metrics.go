package node

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

func (n *Node) recordClientRequestReceived(txCount int) {
	if !n.cfg.Logging || txCount <= 0 {
		return
	}
	n.clientReceivedTxs.Add(int64(txCount))
}

func (n *Node) clientReceiveRateLogger() {
	defer close(n.clientReceiveRateDone)

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	csvFile, csvWriter := n.openClientReceiveRateCSV()
	if csvFile != nil {
		defer csvFile.Close()
	}
	if csvWriter != nil {
		defer csvWriter.Flush()
	}

	start := time.Now()
	lastSample := start
	var last int64
	for {
		select {
		case now := <-ticker.C:
			current := n.clientReceivedTxs.Load()
			delta := current - last
			last = current
			windowSeconds := now.Sub(lastSample).Seconds()
			lastSample = now
			windowRate := 0.0
			if windowSeconds > 0 {
				windowRate = float64(delta) / windowSeconds
			}
			elapsedSeconds := now.Sub(start).Seconds()
			averageRate := 0.0
			if elapsedSeconds > 0 {
				averageRate = float64(current) / elapsedSeconds
			}

			// n.log.Warn("node client receive rate: %.2f tx/s total_received=%d", windowRate, current)
			n.writeClientReceiveRateCSV(csvWriter, now, elapsedSeconds, current, delta, windowSeconds, windowRate, averageRate)
		case <-n.clientReceiveRateStop:
			return
		}
	}
}

func (n *Node) openClientReceiveRateCSV() (*os.File, *csv.Writer) {
	if err := os.MkdirAll("logs", 0755); err != nil {
		if n.log != nil {
			n.log.Error("Failed to create logs directory for client receive rate CSV: %v", err)
		}
		return nil, nil
	}

	path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_client_receive_rate.csv")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0666)
	if err != nil {
		if n.log != nil {
			n.log.Error("Failed to open client receive rate CSV %s: %v", path, err)
		}
		return nil, nil
	}

	writer := csv.NewWriter(file)
	if err := writer.Write([]string{
		"timestamp_unix_nano",
		"timestamp",
		"elapsed_sec",
		"received_total",
		"received_delta",
		"window_sec",
		"window_tps",
		"average_tps",
	}); err != nil {
		if n.log != nil {
			n.log.Error("Failed to write client receive rate CSV header: %v", err)
		}
		_ = file.Close()
		return nil, nil
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		if n.log != nil {
			n.log.Error("Failed to flush client receive rate CSV header: %v", err)
		}
		_ = file.Close()
		return nil, nil
	}

	return file, writer
}

func (n *Node) writeClientReceiveRateCSV(writer *csv.Writer, sampleTime time.Time, elapsedSeconds float64, total int64, delta int64, windowSeconds float64, windowRate float64, averageRate float64) {
	if writer == nil {
		return
	}

	record := []string{
		strconv.FormatInt(sampleTime.UnixNano(), 10),
		sampleTime.Format(time.RFC3339Nano),
		strconv.FormatFloat(elapsedSeconds, 'f', 6, 64),
		strconv.FormatInt(total, 10),
		strconv.FormatInt(delta, 10),
		strconv.FormatFloat(windowSeconds, 'f', 6, 64),
		strconv.FormatFloat(windowRate, 'f', 6, 64),
		strconv.FormatFloat(averageRate, 'f', 6, 64),
	}
	if err := writer.Write(record); err != nil {
		if n.log != nil {
			n.log.Error("Failed to write client receive rate CSV row: %v", err)
		}
		return
	}
	writer.Flush()
	if err := writer.Error(); err != nil && n.log != nil {
		n.log.Error("Failed to flush client receive rate CSV row: %v", err)
	}
}
