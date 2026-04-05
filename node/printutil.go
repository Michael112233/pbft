package node

import (
	"bytes"
	"fmt"
	"sort"

	"github.com/michael112233/pbft/core"
)

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
	for cp, votes := range n.checkpoints {
		voterIDs := make([]int, 0, len(votes))
		for voterID := range votes {
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
	fmt.Printf("Node ID: %d, Address: %s, Current View: %d, For View: %d, leaderID: %d, voted For: %d, PrePrepare Sequence Number: %d, last Stable Checkpoint Sequence: %d, last Executed Sequence: %d, fnodes: %d, dead: %t, split: %t, periodic: %t, peakTpsTest: %t, vcType: %s\n\n", n.NodeID, n.GetAddr(), currentView, forView, leaderID, votedFor, preprepareSeq, lastStableCheckpoint.seq, lastExecuted, fNodes, dead, split, periodic, peakTpsTest, vcTypeString(vcType))

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
	n.consensusLog.PrintDetails(currentView)
}

func (n *Node) PrintSlot(seqNum int64) {

	n.consensusLog.PrintSlot([]int64{seqNum}, n.view)
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
