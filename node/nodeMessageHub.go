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
	listenConn.Close()
	hub.log.Debug("messageHub is close.")
}

// --------------------------------------------------------
// Basic Communication Principles Implementation (like Dial & Listen)
// --------------------------------------------------------
func (hub *NodeMessageHub) Dial(addr string) (net.Conn, error) {
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		hub.log.Debug(fmt.Sprintf("DialTCPError: target_addr=%s, err=%v", addr, err))
		// 再dial一次
		hub.log.Debug(fmt.Sprintf("Try dial again... target_addr=%s", addr))
		conn, err = net.Dial("tcp", addr)
		if err != nil {
			hub.log.Debug(fmt.Sprintf("DialTCPError: target_addr=%s, err=%v", addr, err))
			return nil, nil
		} else {
			hub.log.Debug(fmt.Sprintf("dial success. target_addr=%s", addr))
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
	default:
		hub.log.Error("Unknown message type received. msgType=" + msgType)
	}
}

func (hub *NodeMessageHub) listen(addr string, wg *sync.WaitGroup) {
	defer wg.Done()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		hub.log.Error(fmt.Sprintf("Error setting up listener: err=%v", err))
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

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok {
		conn, err = hub.Dial(addr)
		if err != nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Preprepare Message. caller: %s targetAddr: %s", data.From, addr))
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	writer.Write(msg_bytes)
	writer.Flush()
}

func (hub *NodeMessageHub) sendPrepareMessage(msg interface{}) {
	data := msg.(core.PrepareMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Preprepare Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgPrepareMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok {
		conn, err = hub.Dial(addr)
		if err != nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Prepare Message. caller: %s targetAddr: %s", data.From, addr))
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	writer.Write(msg_bytes)
	writer.Flush()
}

func (hub *NodeMessageHub) sendCommitMessage(msg interface{}) {
	data := msg.(core.CommitMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr. Send Preprepare Message. caller: %s targetAddr: %s", data.From, data.To))
	}

	msg_bytes := hub.packMsg("MsgCommitMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok {
		conn, err = hub.Dial(addr)
		if err != nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Commit Message. caller: %s targetAddr: %s", data.From, addr))
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	writer.Write(msg_bytes)
	writer.Flush()
}
