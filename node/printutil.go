package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/michael112233/pbft/core"
)

type latencySummaryResult struct {
	Count            int       `json:"count"`
	MedianMs         float64   `json:"median_ms"`
	P95Ms            float64   `json:"p95_ms"`
	P99Ms            float64   `json:"p99_ms"`
	P95MedianRatio   float64   `json:"p95_median_ratio"`
	P99MedianRatio   float64   `json:"p99_median_ratio"`
	LatencySamplesMs []float64 `json:"latency_samples_ms"`
}

func (n *Node) PrintDetails() {
	type checkpointSnapshot struct {
		seq    int64
		digest [32]byte
		votes  []int
	}

	type preparedCertSnapshot struct {
		seq            int64
		preprepareView int64
		preprepareSeq  int64
		digest         [32]byte
		prepareVotes   []int
	}

	n.executionMu.Lock()
	lastExecuted := n.lastExecuted
	n.executionMu.Unlock()

	n.checkpointMu.Lock()
	lastStableCheckpoint := n.lastStableCheckpoint
	checkpointSnapshots := make([]checkpointSnapshot, 0, len(n.checkpoints))
	for cp, checkpointData := range n.checkpoints {
		voterIDs := make([]int, 0, len(checkpointData.votes))
		for voterID := range checkpointData.votes {
			voterIDs = append(voterIDs, voterID)
		}
		sort.Ints(voterIDs)
		checkpointSnapshots = append(checkpointSnapshots, checkpointSnapshot{
			seq:    cp.seq,
			digest: cp.digest,
			votes:  voterIDs,
		})
	}
	n.checkpointMu.Unlock()

	sort.Slice(checkpointSnapshots, func(i, j int) bool {
		if checkpointSnapshots[i].seq != checkpointSnapshots[j].seq {
			return checkpointSnapshots[i].seq < checkpointSnapshots[j].seq
		}
		return bytes.Compare(checkpointSnapshots[i].digest[:], checkpointSnapshots[j].digest[:]) < 0
	})

	n.viewMu.RLock()
	currentView := n.view
	forView := n.forView
	leaderID := n.leaderId
	votedFor := n.votedFor
	fNodes := n.fNodes
	dead := n.dead
	split := n.split
	periodic := n.periodic
	peakTpsTest := n.peakTpsTest
	vcType := n.vcType
	preprepareSeq := n.preprepareSeqNumber.Load()

	fmt.Printf("------ Node and View CHnange Details ------\n")
	fmt.Printf("Node ID: %d, Address: %s, Current View: %d, For View: %d, leaderID: %d, voted For: %d, PrePrepare Sequence Number: %d, last Stable Checkpoint Sequence: %d, last Executed Sequence: %d, no Ops Executed: %d, fnodes: %d, dead: %t, split: %t, periodic: %t, peakTpsTest: %t, vcType: %s\n\n", n.NodeID, n.GetAddr(), currentView, forView, leaderID, votedFor, preprepareSeq, lastStableCheckpoint.seq, lastExecuted, n.noOpsExecuted.Load(), fNodes, dead, split, periodic, peakTpsTest, vcTypeString(vcType))

	fmt.Printf("------ Checkpoint Details ------\n")
	fmt.Printf("Last Stable Checkpoint: seq=%d digest=%x\n", lastStableCheckpoint.seq, lastStableCheckpoint.digest)
	if len(checkpointSnapshots) == 0 {
		fmt.Printf("No checkpoint votes recorded\n\n")
	} else {
		for _, cp := range checkpointSnapshots {
			fmt.Printf("Checkpoint seq=%d digest=%x votes=%d voters=%v\n", cp.seq, cp.digest, len(cp.votes), cp.votes)
		}
		fmt.Printf("\n")
	}

	fmt.Printf("------ View Change Details (%s) ------\n", vcTypeString(vcType))
	viewKeys := make([]int64, 0, len(n.viewChangeMsgsLog))
	for viewNumber := range n.viewChangeMsgsLog {
		viewKeys = append(viewKeys, viewNumber)
	}
	sort.Slice(viewKeys, func(i, j int) bool { return viewKeys[i] < viewKeys[j] })
	if len(viewKeys) == 0 {
		fmt.Printf("No view change messages recorded\n")
	}
	for _, forView := range viewKeys {
		viewChangeMsgs := n.viewChangeMsgsLog[forView]
		fmt.Printf("For View %d, View Change Messages:\n", forView)
		for _, msg := range viewChangeMsgs {
			if msg == nil {
				fmt.Printf("  <nil view change message>\n")
				continue
			}

			viewChange := msg.ViewChangeMsg
			fmt.Printf("  From Node %d, View Number: %d, Checkpoint Seq: %d, Prepared Certs: %d\n", viewChange.From, viewChange.ViewNumber, viewChange.CheckpointSeqNumber, len(viewChange.PreparedCerts))

			switch viewChange.Type {
			case core.VCTypeElection:
				if viewChange.ElectionData != nil {
					fmt.Printf("    Election Data: reqVote=%t grantVote=%t grantTo=%d\n", viewChange.ElectionData.ReqVote, viewChange.ElectionData.GrantVote, viewChange.ElectionData.GrantTo)
				} else {
					fmt.Printf("    Election Data: <nil>\n")
				}
			case core.VCTypeRoundRobin:
				if viewChange.RoundRobinData != nil {
					fmt.Printf("    Round Robin Data: grantVote=%t\n", viewChange.RoundRobinData.GrantVote)
				} else {
					fmt.Printf("    Round Robin Data: <nil>\n")
				}
			default:
				fmt.Printf("    VC Type: %s\n", vcTypeString(viewChange.Type))
			}

			if len(viewChange.PreparedCerts) == 0 {
				fmt.Printf("    Prepared Certs: none\n")
				continue
			}

			preparedSeqs := make([]int64, 0, len(viewChange.PreparedCerts))
			for seq := range viewChange.PreparedCerts {
				preparedSeqs = append(preparedSeqs, seq)
			}
			sort.Slice(preparedSeqs, func(i, j int) bool { return preparedSeqs[i] < preparedSeqs[j] })

			preparedSnapshots := make([]preparedCertSnapshot, 0, len(preparedSeqs))
			for _, seq := range preparedSeqs {
				preparedCert := viewChange.PreparedCerts[seq]
				if preparedCert == nil {
					continue
				}
				prepareVotes := make([]int, 0, len(preparedCert.PrepareLog))
				for from := range preparedCert.PrepareLog {
					prepareVotes = append(prepareVotes, from)
				}
				sort.Ints(prepareVotes)
				preparedSnapshots = append(preparedSnapshots, preparedCertSnapshot{
					seq:            seq,
					preprepareView: preparedCert.PreprepareMsg.PreprepareMsgMini.View,
					preprepareSeq:  preparedCert.PreprepareMsg.PreprepareMsgMini.SeqNum,
					digest:         preparedCert.PreprepareMsg.PreprepareMsgMini.DigestClientMsg,
					prepareVotes:   prepareVotes,
				})
			}

			for _, prepared := range preparedSnapshots {
				fmt.Printf("    Prepared Cert seq=%d preprepareView=%d preprepareSeq=%d digest=%x prepareVotes=%v\n", prepared.seq, prepared.preprepareView, prepared.preprepareSeq, prepared.digest, prepared.prepareVotes)
			}
		}
	}

	if vcType == core.VCTypeElection {
		fmt.Printf("---------------- VOTE LOG DETAILS ------------------\n")
		voteViewKeys := make([]int64, 0, len(n.voteLog))
		for viewNumber := range n.voteLog {
			voteViewKeys = append(voteViewKeys, viewNumber)
		}
		sort.Slice(voteViewKeys, func(i, j int) bool { return voteViewKeys[i] < voteViewKeys[j] })
		if len(voteViewKeys) == 0 {
			fmt.Printf("No vote log entries recorded\n")
		}
		for _, forView := range voteViewKeys {
			grantVotes := append([]int(nil), n.voteLog[forView]...)
			sort.Ints(grantVotes)
			fmt.Printf("For View %d, Grant Vote Messages:\n", forView)
			for _, msg := range grantVotes {
				fmt.Printf("  From Node %d\n", msg)
			}
		}
	}
	fmt.Printf("------------------ LOG DETAILS ------------------\n")
	n.viewMu.RUnlock()
	// n.consensusLog.PrintDetails(currentView)
}

func (n *Node) PrintSlot(seqNum int64) {

	n.consensusLog.PrintSlot([]int64{seqNum}, n.view)
}

func (n *Node) PrintExecutedSlots() {

	n.consensusLog.PrintExecutedSlots(n.view)
}

func (n *Node) PrintAccountBalances() {
	n.executionMu.Lock()
	_, balances, err := n.executionMachine.CheckpointMaterial()
	n.executionMu.Unlock()
	if err != nil {
		fmt.Printf("Failed to get account balances: %v\n", err)
		return
	}

	jsonBalances := make(map[string]*big.Int, len(balances))
	for account, balance := range balances {
		if balance == nil {
			jsonBalances[account] = new(big.Int)
			continue
		}
		jsonBalances[account] = new(big.Int).Set(balance)
	}

	if err := os.MkdirAll("logs", 0755); err != nil {
		fmt.Printf("Failed to create logs directory for account balances: %v\n", err)
	} else {
		path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_acctbalances.json")
		data, marshalErr := json.MarshalIndent(jsonBalances, "", "  ")
		if marshalErr != nil {
			fmt.Printf("Failed to marshal account balances: %v\n", marshalErr)
		} else if writeErr := os.WriteFile(path, append(data, '\n'), 0644); writeErr != nil {
			fmt.Printf("Failed to write account balances file %s: %v\n", path, writeErr)
		} else {
			fmt.Printf("Account balances written to %s\n", path)
		}
	}

	accounts := make([]string, 0, len(balances))
	for account := range balances {
		accounts = append(accounts, account)
	}
	sort.Strings(accounts)

	fmt.Printf("------ Account Balances ------\n")
	if len(accounts) == 0 {
		fmt.Printf("No accounts recorded\n")
		return
	}
	for _, account := range accounts {
		balance := balances[account]
		if balance == nil {
			fmt.Printf("%s=0\n", account)
			continue
		}
		fmt.Printf("%s=%s\n", account, balance.String())
	}
}

func (n *Node) PrintLatencySummary() {
	samples := n.StopLatencyMonitoring()
	result := summarizeLatencies(samples)

	if err := os.MkdirAll("logs", 0755); err != nil {
		fmt.Printf("Failed to create logs directory for latency summary: %v\n", err)
		return
	}

	path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_latencylog.json")
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		fmt.Printf("Failed to marshal latency summary: %v\n", err)
		return
	}
	if err := os.WriteFile(path, append(data, '\n'), 0644); err != nil {
		fmt.Printf("Failed to write latency summary file %s: %v\n", path, err)
		return
	}

	fmt.Printf("Latency summary written to %s\n", path)
}

func summarizeLatencies(samples []time.Duration) latencySummaryResult {
	sampleCount := len(samples)
	if sampleCount > 10 {
		sampleCount = 10
	}

	latencySamplesMs := make([]float64, sampleCount)
	for i := 0; i < sampleCount; i++ {
		latencySamplesMs[i] = durationToMilliseconds(samples[i])
	}

	result := latencySummaryResult{
		Count:            len(samples),
		LatencySamplesMs: latencySamplesMs,
	}
	if len(samples) == 0 {
		return result
	}

	sort.Slice(samples, func(i, j int) bool {
		return samples[i] < samples[j]
	})

	middle := len(samples) / 2
	medianNanoseconds := float64(samples[middle])
	if len(samples)%2 == 0 {
		medianNanoseconds = (float64(samples[middle-1]) + float64(samples[middle])) / 2
	}

	p95 := nearestRankLatency(samples, 0.95)
	p99 := nearestRankLatency(samples, 0.99)
	result.MedianMs = medianNanoseconds / float64(time.Millisecond)
	result.P95Ms = durationToMilliseconds(p95)
	result.P99Ms = durationToMilliseconds(p99)
	if medianNanoseconds != 0 {
		result.P95MedianRatio = float64(p95) / medianNanoseconds
		result.P99MedianRatio = float64(p99) / medianNanoseconds
	}

	return result
}

func nearestRankLatency(sortedSamples []time.Duration, percentile float64) time.Duration {
	if len(sortedSamples) == 0 {
		return 0
	}

	index := int(math.Ceil(percentile*float64(len(sortedSamples)))) - 1
	if index < 0 {
		index = 0
	}
	if index >= len(sortedSamples) {
		index = len(sortedSamples) - 1
	}
	return sortedSamples[index]
}

func durationToMilliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}

func (n *Node) TimesLeader() {
	n.viewMu.RLock()
	leaderIdForView := make(map[int64]int, len(n.leaderIdForView))
	for view, leaderID := range n.leaderIdForView {
		leaderIdForView[view] = leaderID
	}
	nodeNum := int64(0)
	if n.cfg != nil {
		nodeNum = n.cfg.NodeNum
	}
	n.viewMu.RUnlock()

	timesLeader := make(map[int]int)
	for nodeID := 1; nodeID <= int(nodeNum); nodeID++ {
		timesLeader[nodeID] = 0
	}
	for _, leaderID := range leaderIdForView {
		timesLeader[leaderID]++
	}

	nodeIDs := make([]int, 0, len(timesLeader))
	for nodeID := range timesLeader {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Ints(nodeIDs)

	parts := make([]string, 0, len(nodeIDs))
	for _, nodeID := range nodeIDs {
		parts = append(parts, fmt.Sprintf("%d,%d", nodeID, timesLeader[nodeID]))
	}

	output := fmt.Sprintf("times leader\n%s\n", strings.Join(parts, "; "))
	fmt.Print(output)

	if err := os.MkdirAll("logs", 0755); err != nil {
		if n.log != nil {
			n.log.Error("Failed to create logs directory for TimesLeader: %v", err)
		}
		return
	}

	path := filepath.Join("logs", "node_"+strconv.Itoa(n.NodeID)+"_timesleader.txt")
	if err := os.WriteFile(path, []byte(output), 0644); err != nil {
		if n.log != nil {
			n.log.Error("Failed to write TimesLeader file %s: %v", path, err)
		}
	}

}

func (n *Node) PrintCommitSentSummary() {

	n.consensusLog.PrintCommitSentSummary()
}

func vcTypeString(vcType core.VCType) string {
	switch vcType {
	case core.VCTypeElection:
		return "election"
	case core.VCTypeRoundRobin:
		return "round_robin"
	default:
		return fmt.Sprintf("unknown(%d)", vcType)
	}
}
