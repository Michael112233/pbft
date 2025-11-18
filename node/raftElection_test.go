package node

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/core"
)

// 创建测试用的 Node
func createTestNode(nodeID int64, nodeNum int) *Node {
	config.GenerateLocalNetwork(nodeNum)
	cfg := &config.Config{
		NodeNum:        int64(nodeNum),
		ElectionMethod: "raft",
		FaultyNodesNum: (int64(nodeNum) - 1) / 3,
	}

	node := NewNode(nodeID, cfg)
	// 初始化 messageHub 的 log（通常由 Start 方法设置，但测试中不调用 Start）
	node.messageHub.log = node.log
	// 初始化 raftElection
	node.raftElection = &RaftElection{
		haveVoted:         false,
		receivedVoteNumber: atomic.Int32{},
	}
	return node
}

// ============================================
// RaftElection 结构体方法测试
// ============================================

func TestResetRaftElection(t *testing.T) {
	re := &RaftElection{
		haveVoted:         true,
		receivedVoteNumber: atomic.Int32{},
	}
	re.receivedVoteNumber.Store(5)

	re.ResetRaftElection()

	if re.haveVoted != false {
		t.Errorf("ResetRaftElection: haveVoted should be false, got %v", re.haveVoted)
	}

	if re.receivedVoteNumber.Load() != 0 {
		t.Errorf("ResetRaftElection: receivedVoteNumber should be 0, got %d", re.receivedVoteNumber.Load())
	}
}

func TestHaveVoted(t *testing.T) {
	re := &RaftElection{
		haveVoted: false,
	}

	// 测试初始状态
	if re.HaveVoted() != false {
		t.Errorf("HaveVoted: expected false, got %v", re.HaveVoted())
	}

	// 测试设置后
	re.SetHaveVoted(true)
	if re.HaveVoted() != true {
		t.Errorf("HaveVoted: expected true, got %v", re.HaveVoted())
	}

	// 测试并发访问
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = re.HaveVoted()
		}()
	}
	wg.Wait()
}

func TestSetHaveVoted(t *testing.T) {
	re := &RaftElection{
		haveVoted: false,
	}

	// 测试设置为 true
	re.SetHaveVoted(true)
	if re.haveVoted != true {
		t.Errorf("SetHaveVoted(true): expected true, got %v", re.haveVoted)
	}

	// 测试设置为 false
	re.SetHaveVoted(false)
	if re.haveVoted != false {
		t.Errorf("SetHaveVoted(false): expected false, got %v", re.haveVoted)
	}

	// 测试并发设置
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(val bool) {
			defer wg.Done()
			re.SetHaveVoted(val)
		}(i%2 == 0)
	}
	wg.Wait()
}

func TestAddReceivedVoteNumber(t *testing.T) {
	re := &RaftElection{
		receivedVoteNumber: atomic.Int32{},
	}

	// 测试初始值
	if re.GetReceivedVoteNumber() != 0 {
		t.Errorf("GetReceivedVoteNumber: expected 0, got %d", re.GetReceivedVoteNumber())
	}

	// 测试增加
	re.AddReceivedVoteNumber()
	if re.GetReceivedVoteNumber() != 1 {
		t.Errorf("GetReceivedVoteNumber: expected 1, got %d", re.GetReceivedVoteNumber())
	}

	// 测试多次增加
	for i := 0; i < 5; i++ {
		re.AddReceivedVoteNumber()
	}
	if re.GetReceivedVoteNumber() != 6 {
		t.Errorf("GetReceivedVoteNumber: expected 6, got %d", re.GetReceivedVoteNumber())
	}

	// 测试并发增加
	var wg sync.WaitGroup
	re.receivedVoteNumber.Store(0)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			re.AddReceivedVoteNumber()
		}()
	}
	wg.Wait()
	if re.GetReceivedVoteNumber() != 10 {
		t.Errorf("GetReceivedVoteNumber (concurrent): expected 10, got %d", re.GetReceivedVoteNumber())
	}
}

func TestGetReceivedVoteNumber(t *testing.T) {
	re := &RaftElection{
		receivedVoteNumber: atomic.Int32{},
	}

	// 测试初始值
	if re.GetReceivedVoteNumber() != 0 {
		t.Errorf("GetReceivedVoteNumber: expected 0, got %d", re.GetReceivedVoteNumber())
	}

	// 测试设置后获取
	re.receivedVoteNumber.Store(5)
	if re.GetReceivedVoteNumber() != 5 {
		t.Errorf("GetReceivedVoteNumber: expected 5, got %d", re.GetReceivedVoteNumber())
	}
}

// ============================================
// Node 结构体 Raft 选举相关方法测试
// ============================================

func TestStartRaftElection(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 测试 StartRaftElection（注意：这个函数会 sleep，所以测试会比较慢）
	startTime := time.Now()
	node.StartRaftElection(1)
	duration := time.Since(startTime)

	// 验证 sleep 时间在合理范围内（0-1000ms）
	if duration < 0 || duration > 1100*time.Millisecond {
		t.Errorf("StartRaftElection: sleep duration should be 0-1000ms, got %v", duration)
	}

	// 验证 ResetRaftElection 被调用
	if node.raftElection.HaveVoted() != false {
		t.Errorf("StartRaftElection: haveVoted should be false after reset")
	}

	if node.raftElection.GetReceivedVoteNumber() != 0 {
		t.Errorf("StartRaftElection: receivedVoteNumber should be 0 after reset")
	}
}

func TestHasLeader(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 测试没有 leader 的情况
	if node.HasLeader(1) != false {
		t.Errorf("HasLeader(1): expected false, got true")
	}

	// 设置 leader
	node.viewChange.leaderElection.SetLeader(1, 0)
	if node.HasLeader(1) != true {
		t.Errorf("HasLeader(1): expected true, got false")
	}

	// 测试不同的 viewId
	if node.HasLeader(2) != false {
		t.Errorf("HasLeader(2): expected false, got true")
	}
}

func TestSendRequestVote(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 测试发送 RequestVote（函数会调用 messageHub.Send，但由于没有 leader，应该会尝试发送）
	// 注意：由于 NodeMessageHub.Send 没有处理 MsgRequestVote，消息不会实际发送
	// 但我们可以验证函数没有 panic 并且逻辑正确
	node.SendRequestVote()

	// 验证函数执行完成（没有 panic）
	// 由于无法验证消息是否发送，我们至少验证函数可以正常调用
	if node.viewNumber != 0 {
		t.Errorf("SendRequestVote: viewNumber should remain 0, got %d", node.viewNumber)
	}
}

func TestSendRequestVote_WithExistingLeader(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 设置已有 leader
	node.viewChange.leaderElection.SetLeader(node.viewNumber+1, 1)

	// 测试：如果已有 leader，函数应该提前返回
	// 由于 HasLeader 返回 true，SendRequestVote 应该立即返回
	node.SendRequestVote()

	// 验证函数执行完成（没有 panic）
	if !node.HasLeader(node.viewNumber + 1) {
		t.Errorf("SendRequestVote: leader should exist for view %d", node.viewNumber+1)
	}
}

func TestHandleRequestVoteMessage(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 测试正常情况：接收 RequestVote 并响应
	requestVoteData := core.RequestVoteData{
		ViewNumber: 1,
		From:       config.NodeAddr[1],
		To:         node.GetAddr(),
	}

	node.HandleRequestVoteMessage(requestVoteData)

	// 验证已投票
	if !node.raftElection.HaveVoted() {
		t.Errorf("HandleRequestVoteMessage: should have voted, but haven't")
	}

	// 验证函数执行完成（SendRequestVoteResponse 会被调用，但消息不会实际发送）
	// 由于无法验证消息是否发送，我们至少验证状态变化正确
}

func TestHandleRequestVoteMessage_WrongViewNumber(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 测试错误的 viewNumber
	requestVoteData := core.RequestVoteData{
		ViewNumber: 999, // 错误的 viewNumber
		From:       config.NodeAddr[1],
		To:         node.GetAddr(),
	}

	node.HandleRequestVoteMessage(requestVoteData)

	// 验证没有投票
	if node.raftElection.HaveVoted() {
		t.Errorf("HandleRequestVoteMessage: should not vote for wrong viewNumber")
	}
}

func TestHandleRequestVoteMessage_AlreadyVoted(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 设置已投票
	node.raftElection.SetHaveVoted(true)

	requestVoteData := core.RequestVoteData{
		ViewNumber: 1,
		From:       config.NodeAddr[1],
		To:         node.GetAddr(),
	}

	node.HandleRequestVoteMessage(requestVoteData)

	// 验证仍然保持已投票状态
	if !node.raftElection.HaveVoted() {
		t.Errorf("HandleRequestVoteMessage: should remain voted")
	}
}

func TestSendRequestVoteResponse(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	requestVoteData := core.RequestVoteData{
		ViewNumber: 1,
		From:       config.NodeAddr[1],
		To:         node.GetAddr(),
	}

	// 测试发送响应（函数会调用 messageHub.Send，但消息不会实际发送）
	node.SendRequestVoteResponse(requestVoteData)

	// 验证函数执行完成（没有 panic）
	// 由于无法验证消息是否发送，我们至少验证函数可以正常调用
}

func TestSendRequestVoteResponse_WithExistingLeader(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 设置已有 leader
	node.viewChange.leaderElection.SetLeader(1, 1)

	requestVoteData := core.RequestVoteData{
		ViewNumber: 1,
		From:       config.NodeAddr[1],
		To:         node.GetAddr(),
	}

	// 测试：如果已有 leader，函数应该提前返回
	node.SendRequestVoteResponse(requestVoteData)

	// 验证函数执行完成（没有 panic）
	if !node.HasLeader(1) {
		t.Errorf("SendRequestVoteResponse: leader should exist for view 1")
	}
}

func TestHandleRequestVoteResponseMessage(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 测试接收投票响应
	responseData := core.RequestVoteResponseData{
		ViewNumber:  1,
		From:        config.NodeAddr[1],
		To:          node.GetAddr(),
		VoteGranted: true,
	}

	node.HandleRequestVoteResponseMessage(responseData)

	// 验证投票数增加
	if node.raftElection.GetReceivedVoteNumber() != 1 {
		t.Errorf("HandleRequestVoteResponseMessage: expected vote count 1, got %d", node.raftElection.GetReceivedVoteNumber())
	}

	// 测试达到多数票（4个节点，需要2票）
	responseData2 := core.RequestVoteResponseData{
		ViewNumber:  1,
		From:        config.NodeAddr[2],
		To:          node.GetAddr(),
		VoteGranted: true,
	}

	node.HandleRequestVoteResponseMessage(responseData2)

	// 验证投票数增加到2
	if node.raftElection.GetReceivedVoteNumber() != 2 {
		t.Errorf("HandleRequestVoteResponseMessage: expected vote count 2, got %d", node.raftElection.GetReceivedVoteNumber())
	}

	// 验证 SendAppendEntriesMessage 会被调用（但消息不会实际发送）
	// 由于无法验证消息是否发送，我们至少验证投票数正确
}

func TestHandleRequestVoteResponseMessage_WrongViewNumber(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	responseData := core.RequestVoteResponseData{
		ViewNumber:  999, // 错误的 viewNumber
		From:        config.NodeAddr[1],
		To:          node.GetAddr(),
		VoteGranted: true,
	}

	node.HandleRequestVoteResponseMessage(responseData)

	// 验证投票数没有增加
	if node.raftElection.GetReceivedVoteNumber() != 0 {
		t.Errorf("HandleRequestVoteResponseMessage: should not count vote for wrong viewNumber")
	}
}

func TestHandleRequestVoteResponseMessage_VoteNotGranted(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	responseData := core.RequestVoteResponseData{
		ViewNumber:  1,
		From:        config.NodeAddr[1],
		To:          node.GetAddr(),
		VoteGranted: false,
	}

	node.HandleRequestVoteResponseMessage(responseData)

	// 验证投票数没有增加
	if node.raftElection.GetReceivedVoteNumber() != 0 {
		t.Errorf("HandleRequestVoteResponseMessage: should not count vote when VoteGranted is false")
	}
}

func TestSendAppendEntriesMessage(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0
	node.raftElection.receivedVoteNumber.Store(2)

	// 测试发送 AppendEntries（函数会调用 messageHub.Send，但消息不会实际发送）
	node.SendAppendEntriesMessage()

	// 验证函数执行完成（没有 panic）
	// 由于无法验证消息是否发送，我们至少验证函数可以正常调用
	if node.raftElection.GetReceivedVoteNumber() != 2 {
		t.Errorf("SendAppendEntriesMessage: vote number should remain 2, got %d", node.raftElection.GetReceivedVoteNumber())
	}
}

func TestSendAppendEntriesMessage_WithExistingLeader(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 设置已有 leader
	node.viewChange.leaderElection.SetLeader(node.viewNumber+1, 1)

	// 测试：如果已有 leader，函数应该提前返回
	node.SendAppendEntriesMessage()

	// 验证函数执行完成（没有 panic）
	if !node.HasLeader(node.viewNumber + 1) {
		t.Errorf("SendAppendEntriesMessage: leader should exist for view %d", node.viewNumber+1)
	}
}

func TestHandleAppendEntriesMessage(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	appendData := core.AppendEntriesData{
		ViewNumber:    1,
		VoteNumber:    2,
		CurrentLeader: 1,
		To:            node.GetAddr(),
	}

	node.HandleAppendEntriesMessage(appendData)

	// 验证 leader 被设置
	if !node.HasLeader(1) {
		t.Errorf("HandleAppendEntriesMessage: leader should be set for view 1")
	}

	// 验证 leader 是正确的
	leaderAddr := node.viewChange.leaderElection.GetLeader(1)
	expectedAddr := config.NodeAddr[1]
	if leaderAddr != expectedAddr {
		t.Errorf("HandleAppendEntriesMessage: expected leader %s, got %s", expectedAddr, leaderAddr)
	}
}

func TestHandleAppendEntriesMessage_WithExistingLeader(t *testing.T) {
	node := createTestNode(0, 4)
	node.viewNumber = 0

	// 设置已有 leader（view 1 的 leader 是节点 0）
	node.viewChange.leaderElection.SetLeader(1, 0)

	appendData := core.AppendEntriesData{
		ViewNumber:    1,
		VoteNumber:    2,
		CurrentLeader: 1, // 尝试将 leader 设置为节点 1
		To:            node.GetAddr(),
	}

	// 保存原始 leader
	originalLeaderAddr := node.viewChange.leaderElection.GetLeader(1)

	node.HandleAppendEntriesMessage(appendData)

	// 验证 leader 没有被更新（因为已经有 leader，函数提前返回）
	// 应该仍然是原来的 leader（节点 0）
	leaderAddr := node.viewChange.leaderElection.GetLeader(1)
	if leaderAddr != originalLeaderAddr {
		t.Errorf("HandleAppendEntriesMessage: leader should not be updated when leader already exists, expected %s, got %s", originalLeaderAddr, leaderAddr)
	}

	// 验证仍然是节点 0
	expectedAddr := config.NodeAddr[0]
	if leaderAddr != expectedAddr {
		t.Errorf("HandleAppendEntriesMessage: leader should remain %s, got %s", expectedAddr, leaderAddr)
	}
}

// ============================================
// 集成测试
// ============================================

func TestRaftElectionFlow(t *testing.T) {
	// 创建多个节点模拟完整的 Raft 选举流程
	nodes := make([]*Node, 4)
	for i := 0; i < 4; i++ {
		nodes[i] = createTestNode(int64(i), 4)
		nodes[i].viewNumber = 0
	}

	// 节点0发起选举
	nodes[0].SendRequestVote()

	// 节点1、2、3处理 RequestVote
	for i := 1; i < 4; i++ {
		requestVoteData := core.RequestVoteData{
			ViewNumber: 1,
			From:       nodes[0].GetAddr(),
			To:         nodes[i].GetAddr(),
		}
		nodes[i].HandleRequestVoteMessage(requestVoteData)

		// 验证节点已投票
		if !nodes[i].raftElection.HaveVoted() {
			t.Errorf("RaftElectionFlow: node %d should have voted", i)
		}
	}

	// 节点0接收响应（模拟收到2个响应，达到多数）
	response1 := core.RequestVoteResponseData{
		ViewNumber:  1,
		From:        nodes[1].GetAddr(),
		To:          nodes[0].GetAddr(),
		VoteGranted: true,
	}
	response2 := core.RequestVoteResponseData{
		ViewNumber:  1,
		From:        nodes[2].GetAddr(),
		To:          nodes[0].GetAddr(),
		VoteGranted: true,
	}

	nodes[0].HandleRequestVoteResponseMessage(response1)
	nodes[0].HandleRequestVoteResponseMessage(response2)

	// 验证节点0收到了足够的投票
	if nodes[0].raftElection.GetReceivedVoteNumber() != 2 {
		t.Errorf("RaftElectionFlow: node 0 should have received 2 votes, got %d", nodes[0].raftElection.GetReceivedVoteNumber())
	}

	// 其他节点处理 AppendEntries
	for i := 1; i < 4; i++ {
		appendData := core.AppendEntriesData{
			ViewNumber:    1,
			VoteNumber:    2,
			CurrentLeader: 0,
			To:            nodes[i].GetAddr(),
		}
		nodes[i].HandleAppendEntriesMessage(appendData)

		// 验证所有节点都设置了 leader
		if !nodes[i].HasLeader(1) {
			t.Errorf("RaftElectionFlow: node %d should have leader for view 1", i)
		}

		// 验证 leader 是正确的
		leaderAddr := nodes[i].viewChange.leaderElection.GetLeader(1)
		expectedAddr := config.NodeAddr[0]
		if leaderAddr != expectedAddr {
			t.Errorf("RaftElectionFlow: node %d should have leader %s, got %s", i, expectedAddr, leaderAddr)
		}
	}
}

