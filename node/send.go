package node

// var sequenceNumber int64 = -1

// func (n *Node) GenerateBlocks() *core.RequestMessage {
// 	n.mempoolLock.Lock()
// 	defer n.mempoolLock.Unlock()
// 	// Get the first config.MaxBlockSize transactions
// 	txs := n.Mempool[:n.cfg.MaxBlockSize]
// 	n.Mempool = n.Mempool[n.cfg.MaxBlockSize:]
// 	n.log.Info(fmt.Sprintf("Current mempool size is %d", len(n.Mempool)))
// 	data := &core.RequestMessage{
// 		Timestamp: time.Now().UnixNano(),
// 		From:      config.ClientAddr,
// 		To:        n.GetAddr(),
// 		Txs:       txs,
// 		Id:        sequenceNumber,
// 	}
// 	return data
// }

// func (n *Node) SendPreprepareMessage(newSequenceNumber int64) {
// 	if newSequenceNumber == -1 {
// 		sequenceNumber = GenerateRandomSequenceNumber(n.cfg.SeqNumberUpperBound, n.cfg.SeqNumberLowerBound)
// 	} else {
// 		sequenceNumber = newSequenceNumber
// 	}
// 	for len(n.Mempool) > 0 {
// 		sequenceNumber++
// 		timerID := fmt.Sprintf("request_%d_%d", n.NodeID, sequenceNumber)
// 		n.StartExpireTimer(timerID)
// 		data := n.GenerateBlocks()
// 		for _, othersIp := range config.NodeAddr {
// 			if othersIp == n.GetAddr() {
// 				continue
// 			}
// 			preprepareMessage := core.PreprepareMessage{
// 				Timestamp:      time.Now().UnixNano(),
// 				From:           n.GetAddr(),
// 				To:             othersIp,
// 				SequenceNumber: sequenceNumber,
// 				ViewNumber:     n.viewChange.currentView.Load(),
// 				Digest:         utils.GetDigest(data),
// 				RequestMessage: data,
// 			}
// 			// n.log.Info(fmt.Sprintf("Send preprepare message to %s with sequence number %d, current mempool size is %d", othersIp, sequenceNumber, len(n.Mempool)))
// 			n.SetPreprepareSequenceNumber(sequenceNumber, &preprepareMessage)
// 			n.messageHub.Send(core.MsgPreprepareMessage, othersIp, preprepareMessage, nil)
// 		}
// 		if n.viewChange.IsInViewChange() {
// 			break
// 		}
// 	}
// 	// n.preprepareStarted = false
// }

// func (n *Node) SendPrepareMessage(data core.PreprepareMessage) {
// 	if n.viewChange.IsInViewChange() {
// 		return
// 	}
// 	n.AddPrepareMessageNumber(data.SequenceNumber)
// 	// n.log.Info(fmt.Sprintf("SeqNumber %d: After receiving from %s to itself, current prepare messages number is %d", data.SequenceNumber, data.From, n.GetPrepareMessageNumber(data.SequenceNumber)))
// 	// Send Prepare Message to Others.
// 	for _, othersIp := range config.NodeAddr {
// 		if othersIp == n.GetAddr() {
// 			continue
// 		}
// 		prepareMessage := core.PrepareMessage{
// 			Timestamp:      time.Now().UnixNano(),
// 			From:           n.GetAddr(),
// 			To:             othersIp,
// 			SequenceNumber: data.SequenceNumber,
// 			ViewNumber:     n.viewChange.currentView.Load(),
// 			Digest:         data.Digest,
// 			RequestMessage: data.RequestMessage,
// 		}
// 		// n.log.Info(fmt.Sprintf("Send prepare message to %s with sequence number %d", othersIp, data.SequenceNumber))
// 		n.messageHub.Send(core.MsgPrepareMessage, othersIp, prepareMessage, nil)
// 	}
// }

// func (n *Node) SendCommitMessage(data core.PrepareMessage) {
// 	if n.viewChange.IsInViewChange() {
// 		return
// 	}
// 	n.AddCommitMessageNumber(data.SequenceNumber)
// 	n.log.Info(fmt.Sprintf("SeqNumber %d: After receiving from %s to itself, current commit messages number is %d", data.SequenceNumber, data.From, n.GetCommitMessageNumber(data.SequenceNumber)))

// 	// Send Prepare Message to Others.
// 	for _, othersIp := range config.NodeAddr {
// 		if othersIp == n.GetAddr() {
// 			continue
// 		}
// 		commitMessage := core.CommitMessage{
// 			Timestamp:      time.Now().UnixNano(),
// 			From:           n.GetAddr(),
// 			To:             othersIp,
// 			SequenceNumber: data.SequenceNumber,
// 			ViewNumber:     n.viewChange.currentView.Load(),
// 			Digest:         data.Digest,
// 			RequestMessage: data.RequestMessage,
// 		}
// 		// n.log.Info(fmt.Sprintf("Send commit message to %s with sequence number %d", othersIp, data.SequenceNumber))
// 		n.messageHub.Send(core.MsgCommitMessage, othersIp, commitMessage, nil)
// 	}
// }

// func (n *Node) SendReplyMessage(data core.CommitMessage) {
// 	if n.viewChange.IsInViewChange() {
// 		return
// 	}
// 	replyMessage := core.ReplyMessage{
// 		Timestamp:      time.Now().UnixNano(),
// 		From:           n.GetAddr(),
// 		To:             config.ClientAddr,
// 		SequenceNumber: data.SequenceNumber,
// 		ViewNumber:     n.viewChange.currentView.Load(),
// 		RequestMessage: data.RequestMessage,
// 	}
// 	// n.log.Info(fmt.Sprintf("Send reply message to %s with sequence number %d", config.ClientAddr, data.SequenceNumber))
// 	timerID := fmt.Sprintf("request_%d_%d", n.NodeID, data.RequestMessage.Id)
// 	n.StopExpireTimer(timerID)
// 	n.messageHub.Send(core.MsgReplyMessage, config.ClientAddr, replyMessage, nil)
// }
