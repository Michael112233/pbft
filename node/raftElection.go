package node

// import (
// 	"fmt"
// 	"math/rand"
// 	"sync"
// 	"sync/atomic"
// 	"time"

// 	"github.com/michael112233/pbft/config"
// 	"github.com/michael112233/pbft/core"
// )

// type RaftElection struct {
// 	voteMutex sync.Mutex
// 	haveVoted bool

// 	addvoteMutex       sync.Mutex
// 	receivedVoteNumber atomic.Int32
// }

// func (r *RaftElection) ResetRaftElection() {
// 	r.haveVoted = false
// 	r.receivedVoteNumber.Store(0)
// 	r.addvoteMutex = sync.Mutex{}
// 	r.voteMutex = sync.Mutex{}
// }

// func (n *Node) StartRaftElection(viewId int64) {
// 	sleepDuration := time.Duration(rand.Intn(1000)) * time.Millisecond
// 	time.Sleep(sleepDuration)
// 	n.SendRequestVote()
// 	n.raftElection.ResetRaftElection()
// }

// func (n *Node) HasLeader(viewId int64) bool {
// 	return n.viewChange.leaderElection.HasLeader(viewId)
// }

// func (r *RaftElection) HaveVoted() bool {
// 	r.voteMutex.Lock()
// 	defer r.voteMutex.Unlock()
// 	return r.haveVoted
// }

// func (r *RaftElection) SetHaveVoted(haveVoted bool) {
// 	r.voteMutex.Lock()
// 	defer r.voteMutex.Unlock()
// 	r.haveVoted = haveVoted
// }

// func (r *RaftElection) AddReceivedVoteNumber() {
// 	r.addvoteMutex.Lock()
// 	defer r.addvoteMutex.Unlock()
// 	r.receivedVoteNumber.Add(1)
// }

// func (r *RaftElection) GetReceivedVoteNumber() int32 {
// 	r.addvoteMutex.Lock()
// 	defer r.addvoteMutex.Unlock()
// 	return r.receivedVoteNumber.Load()
// }

// // --------------------------------------------------------
// // basic logic to elect leaders
// // --------------------------------------------------------

// func (n *Node) SendRequestVote() {
// 	if n.HasLeader(n.viewChange.currentView.Load() + 1) {
// 		return
// 	}
// 	requestVoteMessage := core.RequestVoteData{
// 		ViewNumber: n.viewChange.currentView.Load() + 1,
// 		From:       n.GetAddr(),
// 	}

// 	for _, addr := range config.NodeAddr {
// 		if addr == n.GetAddr() {
// 			continue
// 		}
// 		requestVoteMessage.To = addr
// 		n.messageHub.Send(core.MsgRequestVote, addr, requestVoteMessage, nil)
// 		n.log.Info(fmt.Sprintf("Send Request Vote Message to %s with view number %d", addr, n.viewChange.currentView.Load()+1))
// 	}
// }

// func (n *Node) HandleRequestVoteMessage(data core.RequestVoteData) {
// 	n.log.Info(fmt.Sprintf("Received Request Vote Message from %s with view number %d", data.From, data.ViewNumber))
// 	if data.ViewNumber != n.viewChange.currentView.Load()+1 {
// 		return
// 	}
// 	if n.raftElection.HaveVoted() {
// 		return
// 	}
// 	n.raftElection.SetHaveVoted(true)
// 	n.SendRequestVoteResponse(data)
// }

// func (n *Node) SendRequestVoteResponse(data core.RequestVoteData) {
// 	if n.HasLeader(data.ViewNumber) {
// 		return
// 	}
// 	requestVoteResponseMessage := core.RequestVoteResponseData{
// 		ViewNumber:  data.ViewNumber,
// 		From:        n.GetAddr(),
// 		VoteGranted: true,
// 	}

// 	for _, addr := range config.NodeAddr {
// 		if addr == n.GetAddr() {
// 			continue
// 		}
// 		requestVoteResponseMessage.To = addr
// 		n.messageHub.Send(core.MsgRequestVoteResponse, addr, requestVoteResponseMessage, nil)
// 		n.log.Info(fmt.Sprintf("Send Request Vote Response Message to %s with view number %d", addr, data.ViewNumber))
// 	}
// }

// func (n *Node) HandleRequestVoteResponseMessage(data core.RequestVoteResponseData) {
// 	if n.HasLeader(data.ViewNumber) {
// 		return
// 	}
// 	if data.ViewNumber != n.viewChange.currentView.Load()+1 {
// 		return
// 	}
// 	n.log.Info(fmt.Sprintf("Received Request Vote Response Message from %s with view number %d", data.From, data.ViewNumber))

// 	if data.VoteGranted {
// 		n.raftElection.AddReceivedVoteNumber()
// 	}
// 	currentVoteNumber := n.raftElection.GetReceivedVoteNumber()
// 	n.log.Info(fmt.Sprintf("Received %d votes for view number %d", currentVoteNumber, data.ViewNumber))
// 	if currentVoteNumber >= int32(len(config.NodeAddr)/2) {
// 		n.log.Info(fmt.Sprintf("Received enough votes for view number %d, start new view", data.ViewNumber))
// 		n.SendAppendEntriesMessage()
// 	}
// }

// func (n *Node) SendAppendEntriesMessage() {
// 	if n.HasLeader(n.viewChange.currentView.Load() + 1) {
// 		return
// 	}
// 	currentVoteNumber := n.raftElection.GetReceivedVoteNumber()
// 	appendEntriesMessage := core.AppendEntriesData{
// 		ViewNumber:    n.viewChange.currentView.Load() + 1,
// 		VoteNumber:    int64(currentVoteNumber),
// 		CurrentLeader: n.NodeID,
// 	}
// 	for _, addr := range config.NodeAddr {
// 		appendEntriesMessage.To = addr
// 		n.messageHub.Send(core.MsgAppendEntries, addr, appendEntriesMessage, nil)
// 		n.log.Info(fmt.Sprintf("Send Append Entries Message to %s with view number %d", addr, n.viewChange.currentView.Load()+1))
// 	}
// }

// func (n *Node) HandleAppendEntriesMessage(data core.AppendEntriesData) {
// 	if n.HasLeader(n.viewChange.currentView.Load() + 1) {
// 		return
// 	}
// 	n.log.Info(fmt.Sprintf("Handle Append Entries Message: from %s to %s, view number %d, vote number %d", config.NodeAddr[int(data.CurrentLeader)], data.To, data.ViewNumber, data.VoteNumber))
// 	n.viewChange.leaderElection.SetLeader(data.ViewNumber, data.CurrentLeader)
// 	n.log.Info(fmt.Sprintf("current leader: %d -> %s", data.CurrentLeader, config.NodeAddr[int(data.CurrentLeader)]))
// 	n.log.Info("Raft ended!")
// }
