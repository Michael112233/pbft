package verifiablerandomness

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

const (
	MIN = 100000
	MAX = 200000
)

var input = []byte("input")

type SimConfig struct {
	Min                  int
	Max                  int
	input                []byte
	modulus              *big.Int
	fnodes               int
	totalNodes           int
	NodesLatencyFromRest map[int]int
	NodeStart            map[int]int
	NodeDead             int
	leaderTracker        *LeaderTracker
}

type VoteRMsg struct {
	from      int
	proof1    []byte
	proof2    []byte
	beta      []byte
	y         []byte
	askVote   bool
	replyVote bool
}

type LeaderSummary struct {
	Leaders []LeaderResult `json:"leaders"`
}

type LeaderResult struct {
	NodeID      int `json:"node_id"`
	TimesLeader int `json:"times_leader"`
}

type LeaderTracker struct {
	mu         sync.Mutex
	totalNodes int
	counts     map[int]int
}

func NewLeaderTracker(totalNodes int) *LeaderTracker {
	return &LeaderTracker{
		totalNodes: totalNodes,
		counts:     make(map[int]int),
	}
}

func (tracker *LeaderTracker) Record(nodeId int) {
	if tracker == nil {
		return
	}

	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	tracker.counts[nodeId]++
}

func (tracker *LeaderTracker) Summary() LeaderSummary {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	leaders := make([]LeaderResult, 0, tracker.totalNodes)
	for nodeId := 1; nodeId <= tracker.totalNodes; nodeId++ {
		leaders = append(leaders, LeaderResult{
			NodeID:      nodeId,
			TimesLeader: tracker.counts[nodeId],
		})
	}

	return LeaderSummary{Leaders: leaders}
}

func (tracker *LeaderTracker) WriteJSON(path string) error {
	data, err := json.MarshalIndent(tracker.Summary(), "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')
	return os.WriteFile(path, data, 0644)
}

func leaderCountsPath() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return "leader_counts.json"
	}

	return filepath.Join(filepath.Dir(filename), "leader_counts.json")
}

// func generateP256Key(t *testing.T) *ecdsa.PrivateKey {
// 	t.Helper()

// 	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
// 	if err != nil {
// 		t.Fatalf("failed to generate P-256 key: %v", err)
// 	}

//		return privateKey
//	}
func RandomTimer(privateKey *ecdsa.PrivateKey, config SimConfig, timerChan chan VoteRMsg, nodeId int, doneChan chan struct{}, waitGroup *sync.WaitGroup) {
	defer waitGroup.Done()

	proof1, beta, err := CreateProofAndBeta(privateKey, config.input)
	if err != nil {
		panic(fmt.Sprintf("failed to create proof and beta: %v", err))
	}

	got, err := NumberFromBeta(beta, config.Min, config.Max)
	if err != nil {
		panic(fmt.Sprintf("failed to generate number from beta: %v", err))
	}

	delaySteps := uint64(got)
	fmt.Printf("Node %d generated delay steps: %d\n", nodeId, delaySteps)
	timeNow := time.Now()
	y, proof2, err := EvalVDF(config.input, config.modulus, delaySteps)
	if err != nil {
		panic(fmt.Sprintf("EvalVDF returned error: %v", err))
	}
	fmt.Printf("Node %d took %v to evaluate VDF\n", nodeId, time.Since(timeNow))

	select {
	case timerChan <- VoteRMsg{
		from:      nodeId,
		proof1:    proof1,
		proof2:    proof2,
		beta:      beta,
		y:         y,
		askVote:   true,
		replyVote: false,
	}:
		return
	case <-doneChan:
		return
	}
}

func sendVote(ch chan VoteRMsg, msg VoteRMsg) {
	ch <- msg
}

func Node(keyMap map[int]*ecdsa.PrivateKey, nodeId int, config SimConfig, commChan map[int]chan VoteRMsg, stopChan chan struct{}, orchWaitGroup *sync.WaitGroup, timerChan chan VoteRMsg, nodeDead int, nodesLatencyFromRest map[int]int, nodeStart map[int]int) {
	defer orchWaitGroup.Done()
	if nodeId == nodeDead {
		return
	}
	privateKey := keyMap[nodeId]
	waitGroup := &sync.WaitGroup{}
	doneChan := make(chan struct{})
	waitGroup.Add(1)
	time.Sleep(time.Duration(nodeStart[nodeId]) * time.Millisecond)
	go RandomTimer(privateKey, config, timerChan, nodeId, doneChan, waitGroup)
	receivedVoteReq := false
	sentVoteReq := false
	receivedVotes := 0
	for {
		select {
		case msg := <-commChan[nodeId]:
			if msg.askVote && !sentVoteReq && !receivedVoteReq {
				receivedVoteReq = true
				commChan[msg.from] <- VoteRMsg{
					from:      nodeId,
					proof1:    nil,
					proof2:    nil,
					beta:      nil,
					y:         nil,
					askVote:   false,
					replyVote: true,
				}
			} else if sentVoteReq && msg.replyVote {
				receivedVotes++
				if receivedVotes == len(commChan)-config.fnodes {
					config.leaderTracker.Record(nodeId)
					fmt.Printf("Node %d is leader\n", nodeId)
				}
			}

			// Handle received message

		case msg := <-timerChan:
			if !receivedVoteReq {
				sentVoteReq = true
				receivedVoteReq = true
				receivedVotes = 1
				time.Sleep(time.Duration(nodesLatencyFromRest[nodeId]) * time.Millisecond)
				for id, ch := range commChan {
					if id != nodeId {

						go sendVote(ch, msg)
					}
				}
			}

		case <-stopChan:
			close(doneChan)
			waitGroup.Wait()
			// orchWaitGroup.Done()
			return
		}

	}

}

func GenerateP256Key() *ecdsa.PrivateKey {

	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(fmt.Sprintf("failed to generate P-256 key: %v", err))
	}

	return privateKey
}

func orchestrator(config SimConfig) {
	waitGroup := &sync.WaitGroup{}
	keyMap := make(map[int]*ecdsa.PrivateKey)
	for i := 0; i < config.totalNodes; i++ {
		keyMap[i+1] = GenerateP256Key()
	}
	stopChanMap := make(map[int]chan struct{})
	for i := 0; i < config.totalNodes; i++ {
		stopChanMap[i+1] = make(chan struct{})
	}
	commChan := make(map[int]chan VoteRMsg)
	for i := 0; i < config.totalNodes; i++ {
		commChan[i+1] = make(chan VoteRMsg, 250)
	}
	timerChanMap := make(map[int]chan VoteRMsg)
	for i := 0; i < config.totalNodes; i++ {
		timerChanMap[i+1] = make(chan VoteRMsg)
	}
	for i := 0; i < config.totalNodes; i++ {
		waitGroup.Add(1)
		go Node(keyMap, i+1, config, commChan, stopChanMap[i+1], waitGroup, timerChanMap[i+1], config.NodeDead, config.NodesLatencyFromRest, config.NodeStart)
	}
	time.Sleep(4 * time.Second)
	for i := 0; i < config.totalNodes; i++ {
		close(stopChanMap[i+1])
	}
	waitGroup.Wait()

}

func runSimulation(config SimConfig) {
	for i := 0; i < 100; i++ {
		orchestrator(config)
	}
}

func TestSimulation(t *testing.T) {
	totalNodes := 7
	NodeStart := make(map[int]int)
	NodesLatencyFromRest := make(map[int]int)
	for i := 0; i < totalNodes; i++ {
		if i+1 == 1 || i+1 == 3 {
			NodeStart[i+1] = 50
			NodesLatencyFromRest[i+1] = 50
		} else {
			NodeStart[i+1] = 200
			NodesLatencyFromRest[i+1] = 200
		}

	}

	config := SimConfig{
		totalNodes:           totalNodes,
		fnodes:               2,
		input:                []byte("test"),
		modulus:              VDFLargeModulus(),
		Min:                  200000, //600ms
		Max:                  500000, //2s
		NodeStart:            NodeStart,
		NodesLatencyFromRest: NodesLatencyFromRest,
		NodeDead:             2,
		leaderTracker:        NewLeaderTracker(totalNodes),
	}
	runSimulation(config)

	if err := config.leaderTracker.WriteJSON(leaderCountsPath()); err != nil {
		t.Fatalf("failed to write leader counts JSON: %v", err)
	}
}

func VDFLargeModulus() *big.Int {
	// 2^521 - 1 and 2^607 - 1 are known Mersenne primes.
	p := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 521), big.NewInt(1))
	q := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 607), big.NewInt(1))
	return new(big.Int).Mul(p, q)
}

// go test ./verifiablerandomness -run TestSimulation  -v -count=1 -timeout 30m
