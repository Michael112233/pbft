package client

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

type ClientMessageHub struct {
	exitChan   chan struct{}
	client_ref *Client

	log *logger.Logger
}

func NewClientMessageHub() *ClientMessageHub {
	return &ClientMessageHub{
		exitChan: make(chan struct{}, 1),
	}
}

func (hub *ClientMessageHub) Start(client *Client, wg *sync.WaitGroup) {
	if client != nil {
		hub.client_ref = client
		hub.log = client.log
		hub.log.Info("clientMessageHub started")
		wg.Add(1)
		go hub.listen(hub.client_ref.GetAddr(), wg)
	}
}

func (hub *ClientMessageHub) Close() {
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
func (hub *ClientMessageHub) Dial(addr string) (net.Conn, error) {
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

func (hub *ClientMessageHub) packMsg(msgType string, data []byte) []byte {
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

func (hub *ClientMessageHub) Send(msgType string, ip string, msg interface{}, callback func(...interface{})) {
	switch msgType {
	case core.MsgRequestMessage:
		hub.sendRequestMessage(msg)
	default:
		hub.log.Error(fmt.Sprintf("Unknown message type received: msgType=%s", msgType))
	}
}

func (hub *ClientMessageHub) listen(addr string, wg *sync.WaitGroup) {
	defer wg.Done()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		hub.log.Error(fmt.Sprintf("Error setting up listener. err: %v", err))
	}
	hub.log.Info(fmt.Sprintf("start listening on %s", addr))
	listenConn = ln
	defer ln.Close()

	for {
		// // 超过时间限制没有收到新的连接则退出
		// ln.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second))
		conn, err := ln.Accept()
		if err != nil {
			hub.log.Debug("Error accepting connection. Err: " + err.Error())
			return
		}
		go hub.handleConnection(conn, ln)
	}
}

func (hub *ClientMessageHub) unpackMsg(packedMsg []byte) *core.Message {
	var networkBuf bytes.Buffer
	networkBuf.Write(packedMsg)
	msgDec := gob.NewDecoder(&networkBuf)

	var msg core.Message
	err := msgDec.Decode(&msg)
	if err != nil {
		hub.log.Error("unpackMsgErr. Err: " + err.Error() + " msgBytes: " + string(packedMsg))
	}

	return &msg
}

func (hub *ClientMessageHub) handleConnection(conn net.Conn, ln net.Listener) {
	defer conn.Close()
	for {
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(conn, lenBuf)
		if err != nil {
			if err.Error() == "EOF" {
				// 发送端主动关闭连接
				return
			}
			hub.log.Test("Error reading from connection. Err: " + err.Error())
			return
		}
		length := int(binary.BigEndian.Uint32(lenBuf))
		packedMsg := make([]byte, length)
		_, err = io.ReadFull(conn, packedMsg)
		if err != nil {
			hub.log.Error("Error reading from connection. Err: " + err.Error())
		}

		msg := hub.unpackMsg(packedMsg)
		switch msg.MsgType {
		case core.MsgReplyMessage:
			hub.handleReplyMessage(msg.Data)
		default:
			hub.log.Error(fmt.Sprintf("Unknown message type received: msgType=%s", msg.MsgType))
		}
	}
}

// --------------------------------------------------------
// Communication for Unmarshalling Received Messages
// --------------------------------------------------------
func (hub *ClientMessageHub) handleReplyMessage(dataBytes []byte) {
	var buf bytes.Buffer
	buf.Write(dataBytes)
	dataDec := gob.NewDecoder(&buf)

	var data core.ReplyMessage
	err := dataDec.Decode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("handleReplyMessageErr: err=%v, dataBytes=%v", err, dataBytes))
	}
	hub.client_ref.HandleReplyMessage(data)
}

// --------------------------------------------------------
// Communication for Marshalling Messages to Send
// --------------------------------------------------------
func (hub *ClientMessageHub) sendRequestMessage(msg interface{}) {
	data := msg.(core.RequestMessage)
	var buf bytes.Buffer
	enc := gob.NewEncoder(&buf)
	err := enc.Encode(&data)
	if err != nil {
		hub.log.Error(fmt.Sprintf("gobEncodeErr: err=%v, data=%v", err, data))
	}

	msg_bytes := hub.packMsg("MsgRequestMessage", buf.Bytes())

	addr := data.To
	conn, ok := conns2Node.Get(addr)
	if !ok {
		conn, err = hub.Dial(addr)
		if err != nil || conn == nil {
			hub.log.Error(fmt.Sprintf("Dial Error. Send Request Message. caller: %s targetAddr: %s", data.From, addr))
			return
		}
		conns2Node.Add(addr, conn)
	}
	writer := bufio.NewWriter(conn)
	writer.Write(msg_bytes)
	writer.Flush()

	hub.log.Info(fmt.Sprintf("Msg Sent: MsgRequestMessage, From %s, To %s, Txs %d", data.From, data.To, len(data.Txs)))
}
