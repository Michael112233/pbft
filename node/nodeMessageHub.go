package node

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/gob"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/michael112233/pbft/core"
	"github.com/michael112233/pbft/logger"
	"github.com/michael112233/pbft/network"
)

// --------------------------------------------------------
// For Data Structure Definition
// --------------------------------------------------------
var (
	conns2Node = network.NewConnectionsMap()
	listenConn net.Listener
)

type NodeMessageHub struct {
	exitChan chan struct{}
	node_ref *Node

	log *logger.Logger
}

func NewNodeMessageHub() *NodeMessageHub {
	return &NodeMessageHub{
		exitChan: make(chan struct{}, 1),
	}
}

func (hub *NodeMessageHub) Start(node *Node, wg *sync.WaitGroup) {
	if node != nil {
		hub.node_ref = node
		hub.log = node.log
		wg.Add(1)
		go hub.listen(hub.node_ref.GetAddr(), wg)
	}
}

func (hub *NodeMessageHub) Close() {
	// 关闭所有tcp连接，防止资源泄露
	hub.log.Debug("nodeMessageHub closing...")
	for _, conn := range conns2Node.Connections {
		conn.Close()
	}
	// if listenConn != nil {
	// 	listenConn.Close()
	// }
	hub.log.Debug("messageHub is close.")
}

// --------------------------------------------------------
// Basic Communication Principles Implementation (like Dial & Listen)
// --------------------------------------------------------
func (hub *NodeMessageHub) Dial(addr string) (net.Conn, error) {
	// 设置连接超时
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		hub.log.Debug(fmt.Sprintf("DialTCPError: target_addr=%s, err=%v", addr, err))
		// 再dial一次，但增加延迟
		time.Sleep(100 * time.Millisecond)
		hub.log.Debug(fmt.Sprintf("Try dial again... target_addr=%s", addr))
		conn, err = net.DialTimeout("tcp", addr, 5*time.Second)
		if err != nil {
			hub.log.Debug(fmt.Sprintf("DialTCPError: target_addr=%s, err=%v", addr, err))
			return nil, err
		} else {
			hub.log.Debug(fmt.Sprintf("dial success. target_addr=%s", addr))
		}
	}

	// 设置TCP缓冲区大小
	if tcpConn, ok := conn.(*net.TCPConn); ok {
		if hub.node_ref != nil && hub.node_ref.cfg != nil {
			// 设置接收缓冲区
			if err := tcpConn.SetReadBuffer(hub.node_ref.cfg.TCPReadBufferSize); err != nil {
				hub.log.Debug(fmt.Sprintf("Failed to set TCP read buffer: %v", err))
			} else {
				hub.log.Debug(fmt.Sprintf("Set TCP read buffer to %d bytes", hub.node_ref.cfg.TCPReadBufferSize))
			}
			// 设置发送缓冲区
			if err := tcpConn.SetWriteBuffer(hub.node_ref.cfg.TCPWriteBufferSize); err != nil {
				hub.log.Debug(fmt.Sprintf("Failed to set TCP write buffer: %v", err))
			} else {
				hub.log.Debug(fmt.Sprintf("Set TCP write buffer to %d bytes", hub.node_ref.cfg.TCPWriteBufferSize))
			}
		}
	}

	return conn, nil
}

func (hub *NodeMessageHub) packMsg(msgType string, data []byte) []byte {
	msg := &core.Message{
		MsgType: msgType,
		Data:    data,
	}

	var buf bytes.Buffer
	msgEnc := gob.NewEncoder(&buf)
	err := msgEnc.Encode(msg)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr: err=%v, msg=%v", err, msg))
	}

	msgBytes := buf.Bytes()

	// 前缀加上长度，防止粘包
	networkBuf := make([]byte, 4+len(msgBytes))
	binary.BigEndian.PutUint32(networkBuf[:4], uint32(len(msgBytes)))
	copy(networkBuf[4:], msgBytes)

	return networkBuf
}

func (hub *NodeMessageHub) Send(msgType string, ip string, msg interface{}, callback func(...interface{})) {
	switch msgType {
	case core.MsgPreprepareMessage:
		hub.sendPreprepareMessage(msg)
	case core.MsgPrepareMessage:
		hub.sendPrepareMessage(msg)
	case core.MsgCommitMessage:
		hub.sendCommitMessage(msg)
	case core.MsgReplyMessage:
		hub.sendReplyMessage(msg)
	case core.MsgViewChangeMessage:
		hub.sendViewChangeMessage(msg)
	case core.MsgCheckpointMessage:
		hub.sendCheckpointMessage(msg)
	case core.MsgNewViewMessage:
		hub.sendNewViewMessage(msg)
	case core.MsgMempoolMessage:
		hub.sendMempoolMessage(msg)
	// case core.MsgRequestVote:
	// 	hub.sendRequestVoteMessage(msg)
	// case core.MsgRequestVoteResponse:
	// 	hub.sendRequestVoteResponseMessage(msg)
	// case core.MsgAppendEntries:
	// 	hub.sendAppendEntriesMessage(msg)
	default:
		hub.log.Error("Unknown message type received. msgType=" + msgType)
	}
}

func (hub *NodeMessageHub) listen(addr string, wg *sync.WaitGroup) {
	defer wg.Done()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		hub.log.Error(fmt.Sprintf("Error setting up listener: err=%v", err))
		return
	}
	hub.log.Info(fmt.Sprintf("start listening on %s", addr))
	listenConn = ln
	defer ln.Close()

	for {
		// // 超过时间限制没有收到新的连接则退出
		// ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			hub.log.Debug(fmt.Sprintf("Error accepting connection: err=%v", err))
			return
		}

		// 设置TCP缓冲区大小（对于接收到的连接）
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			if hub.node_ref != nil && hub.node_ref.cfg != nil {
				// 设置接收缓冲区
				if err := tcpConn.SetReadBuffer(hub.node_ref.cfg.TCPReadBufferSize); err != nil {
					hub.log.Debug(fmt.Sprintf("Failed to set TCP read buffer: %v", err))
				} else {
					hub.log.Debug(fmt.Sprintf("Set TCP read buffer to %d bytes for incoming connection", hub.node_ref.cfg.TCPReadBufferSize))
				}
				// 设置发送缓冲区
				if err := tcpConn.SetWriteBuffer(hub.node_ref.cfg.TCPWriteBufferSize); err != nil {
					hub.log.Debug(fmt.Sprintf("Failed to set TCP write buffer: %v", err))
				} else {
					hub.log.Debug(fmt.Sprintf("Set TCP write buffer to %d bytes for incoming connection", hub.node_ref.cfg.TCPWriteBufferSize))
				}
			}
		}

		go hub.handleConnection(conn, ln)
	}
}

func (hub *NodeMessageHub) unpackMsg(packedMsg []byte) *core.Message {
	var networkBuf bytes.Buffer
	networkBuf.Write(packedMsg)
	msgDec := gob.NewDecoder(&networkBuf)

	var msg core.Message
	err := msgDec.Decode(&msg)
	if err != nil {
		hub.log.Error(fmt.Sprintf("unpackMsgErr: err=%v, msgBytes=%v", err, packedMsg))
	}

	return &msg
}

func (hub *NodeMessageHub) handleConnection(conn net.Conn, ln net.Listener) {
	defer conn.Close()
	for {
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(conn, lenBuf)
		if err != nil {
			if err.Error() == "EOF" {
				// 发送端主动关闭连接
				return
			}
			hub.log.Debug(fmt.Sprintf("Error reading from connection: err=%v", err))
			return
		}
		length := int(binary.BigEndian.Uint32(lenBuf))
		packedMsg := make([]byte, length)
		_, err = io.ReadFull(conn, packedMsg)
		if err != nil {
			hub.log.Error(fmt.Sprintf("Error reading from connection: err=%v", err))
		}

		msg := hub.unpackMsg(packedMsg)
		switch msg.MsgType {
		case core.MsgRequestMessage:
			hub.handleRequestMessage(msg.Data)
		case core.MsgPreprepareMessage:
			hub.handlePreprepareMessage(msg.Data)
		case core.MsgPrepareMessage:
			hub.handlePrepareMessage(msg.Data)
		case core.MsgCommitMessage:
			hub.handleCommitMessage(msg.Data)
		case core.MsgCloseMessage:
			hub.handleCloseMessage(msg.Data)
		case core.MsgViewChangeMessage:
			hub.handleViewChangeMessage(msg.Data)
		case core.MsgCheckpointMessage:
			hub.handleCheckpointMessage(msg.Data)
		case core.MsgNewViewMessage:
			hub.handleNewViewMessage(msg.Data)
		case core.MsgMempoolMessage:
			hub.handleMempoolMessage(msg.Data)
		// case core.MsgRequestVote:
		// 	hub.handleRequestVoteMessage(msg.Data)
		// case core.MsgRequestVoteResponse:
		// 	hub.handleRequestVoteResponseMessage(msg.Data)
		// case core.MsgAppendEntries:
		// 	hub.handleAppendEntriesMessage(msg.Data)
		default:
			hub.log.Error(fmt.Sprintf("Unknown message type received: msgType=%s", msg.MsgType))
		}
	}
}

// --------------------------------------------------------
// Communication for Unmarshalling messages to Node
// --------------------------------------------------------

func (hub *NodeMessageHub) handleRequestMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.RequestMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleRequestMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}

	hub.node_ref.HandleRequestMessage(data)
}

func (hub *NodeMessageHub) handlePreprepareMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.PreprepareMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handlePreprepareMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}

	hub.node_ref.HandlePreprepareMessage(data)
}

func (hub *NodeMessageHub) handlePrepareMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.PrepareMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handlePrepareMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandlePrepareMessage(data)
}

func (hub *NodeMessageHub) handleCommitMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.CommitMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleCommitMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandleCommitMessage(data)
}

func (hub *NodeMessageHub) handleCloseMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.CloseMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleCloseMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandleCloseMessage(data)
}

func (hub *NodeMessageHub) handleViewChangeMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.ViewChangeMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleViewChangeMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandleViewChangeMessage(data)
}

func (hub *NodeMessageHub) handleCheckpointMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.CheckpointMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleCheckpointMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandleCheckpointMessage(data)
}

func (hub *NodeMessageHub) handleNewViewMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.NewViewMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleNewViewMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandleNewViewMessage(data)
}

func (hub *NodeMessageHub) handleMempoolMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.MempoolMsg
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleNewViewMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.node_ref.HandleMempoolMessage(data)
}

// --------------------------------------------------------
// Communication for Marshalling Messages to Send
// --------------------------------------------------------
func (hub *NodeMessageHub) sendPreprepareMessage(msg interface{}) {
	data := msg.(core.PreprepareMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Preprepare Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgPreprepareMessage", buf.Bytes())
	msg_size := len(msg_bytes)
	tx_count := 0
	if data.RequestMessage != nil && data.RequestMessage.Txs != nil {
		tx_count = len(data.RequestMessage.Txs)
	}
	hub.log.Debug(fmt.Sprintf("Preprepare Message size: %d bytes, SeqNum: %d, TxCount: %d, From: %s, To: %s", 
		msg_size, data.SequenceNumber, tx_count, data.From, data.To))

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Preprepare Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send Preprepare Message. caller: %s targetAddr: %s, msg_size=%d bytes, err=%v", data.From, addr, msg_size, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send Preprepare Message. caller: %s targetAddr: %s, msg_size=%d bytes, err=%v", data.From, addr, msg_size, err))
		return
	}
}

func (hub *NodeMessageHub) sendPrepareMessage(msg interface{}) {
	data := msg.(core.PrepareMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Prepare Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgPrepareMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Prepare Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send Prepare Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send Prepare Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
}

func (hub *NodeMessageHub) sendCommitMessage(msg interface{}) {
	data := msg.(core.CommitMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Commit Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgCommitMessage", buf.Bytes())
	msg_size := len(msg_bytes)
	tx_count := 0
	if data.RequestMessage != nil && data.RequestMessage.Txs != nil {
		tx_count = len(data.RequestMessage.Txs)
	}
	hub.log.Debug(fmt.Sprintf("Commit Message size: %d bytes, SeqNum: %d, TxCount: %d, From: %s, To: %s", 
		msg_size, data.SequenceNumber, tx_count, data.From, data.To))

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Commit Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send Commit Message. caller: %s targetAddr: %s, msg_size=%d bytes, err=%v", data.From, addr, msg_size, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send Commit Message. caller: %s targetAddr: %s, msg_size=%d bytes, err=%v", data.From, addr, msg_size, err))
		return
	}
}

func (hub *NodeMessageHub) sendReplyMessage(msg interface{}) {
	data := msg.(core.ReplyMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Reply Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgReplyMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Reply Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send Reply Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send Reply Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
}

func (hub *NodeMessageHub) sendCheckpointMessage(msg interface{}) {
	data := msg.(core.CheckpointMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Checkpoint Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgCheckpointMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Checkpoint Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send Checkpoint Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send Checkpoint Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
}

func (hub *NodeMessageHub) sendViewChangeMessage(msg interface{}) {
	data := msg.(core.ViewChangeMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send View Change Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgViewChangeMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send View Change Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send View Change Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send View Change Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
}

func (hub *NodeMessageHub) sendNewViewMessage(msg interface{}) {
	data := msg.(core.NewViewMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send New View Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgNewViewMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send New View Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send New View Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send New View Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
}

func (hub *NodeMessageHub) sendMempoolMessage(msg interface{}) {
	data := msg.(core.MempoolMsg)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Mempool Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgMempoolMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok || conn == nil {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Mempool Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	if _, err := writer.Write(msg_bytes); err != nil {
		hub.log.Error(fmt.Sprintf("Write Error. Send Mempool Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
	if err := writer.Flush(); err != nil {
		hub.log.Error(fmt.Sprintf("Flush Error. Send Mempool Message. caller: %s targetAddr: %s, err=%v", data.From, addr, err))
		return
	}
}
