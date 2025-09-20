package node

import (
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

func (hub *NodeMessageHub) listen(addr string, wg *sync.WaitGroup) {
	defer wg.Done()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		hub.log.Error("Error setting up listener", "err", err)
	}
	hub.log.Info(fmt.Sprintf("start listening on %s", addr))
	listenConn = ln
	defer ln.Close()

	for {
		// // 超过时间限制没有收到新的连接则退出
		// ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			hub.log.Debug("Error accepting connection", "err", err)
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
		hub.log.Error("unpackMsgErr", "err", err, "msgBytes", packedMsg)
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
			hub.log.Debug("Error reading from connection", "err", err)
			return
		}
		length := int(binary.BigEndian.Uint32(lenBuf))
		packedMsg := make([]byte, length)
		_, err = io.ReadFull(conn, packedMsg)
		if err != nil {
			hub.log.Error("Error reading from connection", "err", err)
		}

		msg := hub.unpackMsg(packedMsg)
		switch msg.MsgType {
		case core.MsgRequestMessage:
			hub.handleRequestMessage(msg.Data)
		default:
			hub.log.Error("Unknown message type received", "msgType", msg.MsgType)
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
		hub.log.Error("handleRequestMessageErr", "err", err, "dataBytes", dataBytes)
	}

	hub.node_ref.HandleRequestMessage(data)
}
