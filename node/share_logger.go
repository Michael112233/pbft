package node

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

func (n *Node) shareLogger() {
	defer close(n.shareLoggerDone)

	if n.leaderShares == nil {
		n.leaderShares = make(map[int]int)
	}

	for {
		select {
		case share := <-n.shareChan:
			n.leaderShares[share.leaderId]++
		case <-n.shareLoggerStop:
			n.drainLeaderShares()
			n.writeLeaderSharesJSON()
			return
		}
	}
}

func (n *Node) drainLeaderShares() {
	for {
		select {
		case share := <-n.shareChan:
			n.leaderShares[share.leaderId]++
		default:
			return
		}
	}
}

func (n *Node) writeLeaderSharesJSON() {
	if err := os.MkdirAll("logs", 0755); err != nil {
		if n.log != nil {
			n.log.Error("Failed to create logs directory for leader shares JSON: %v", err)
		}
		return
	}

	path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_leader_shares.json")
	data, err := json.MarshalIndent(n.leaderShares, "", "  ")
	if err != nil {
		if n.log != nil {
			n.log.Error("Failed to marshal leader shares JSON: %v", err)
		}
		return
	}

	if err := os.WriteFile(path, data, 0666); err != nil {
		if n.log != nil {
			n.log.Error("Failed to write leader shares JSON %s: %v", path, err)
		}
	}
}

func (n *Node) recordLeaderShare(leaderId int) {
	if n.cfg == nil || !n.cfg.LogShares || n.shareChan == nil {
		return
	}

	select {
	case n.shareChan <- Share{leaderId: leaderId}:
	default:
		if n.log != nil {
			n.log.Error("Dropped leader share for leader %d because share channel is full", leaderId)
		}
	}
}
