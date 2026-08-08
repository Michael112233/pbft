package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"time"

	"github.com/michael112233/pbft/config"
	"github.com/michael112233/pbft/netem"
)

const netemNotificationTimeout = 6 * time.Second

type netemEvent struct {
	socketPath string
	request    netem.ExecutionEventRequest
}

// notifyNetemExecutionEvent tells the external, privileged netem controller
// that a configured sequence has executed. Notification work is asynchronous
// so controller or tc failures cannot delay serialized consensus execution.
func (n *Node) notifyNetemExecutionEvent(seq int64) {
	if n.cfg == nil {
		return
	}
	rule, matches := n.cfg.Netem.ExecutionRule(n.GetNodeID(), seq)
	if !matches {
		return
	}

	event := netemEvent{
		socketPath: n.cfg.Netem.SocketPath,
		request: netem.ExecutionEventRequest{
			Version: netem.ProtocolVersion,
			Type:    config.NetemExecutionEvent,
			RuleID:  rule.ID,
			NodeID:  n.GetNodeID(),
			Seq:     seq,
		},
	}
	select {
	case n.netemEventChan <- event: // if previous event not procesed new is dropped
	default:
		n.log.Error("Netem event queue is full; dropped rule %s seq %d", rule.ID, seq)
	}
}

func (n *Node) netemEventWorker() {
	defer close(n.netemEventDone)
	for {
		select {
		case event := <-n.netemEventChan:
			ctx, cancel := context.WithTimeout(context.Background(), netemNotificationTimeout)
			response, err := sendNetemExecutionEvent(ctx, event.socketPath, event.request)
			cancel()
			if err != nil {
				n.log.Error("Netem controller notification failed for rule %s seq %d: %v", event.request.RuleID, event.request.Seq, err)
				continue
			}
			if !response.Accepted || response.Error != "" {
				n.log.Error("Netem controller rejected rule %s seq %d: %s", event.request.RuleID, event.request.Seq, response.Error)
				continue
			}
			n.log.Info("Netem controller accepted rule %s seq %d applied=%t duplicate=%t", event.request.RuleID, event.request.Seq, response.Applied, response.Duplicate)
		case <-n.netemEventStop:
			return
		}
	}
}

func sendNetemExecutionEvent(ctx context.Context, socketPath string, request netem.ExecutionEventRequest) (netem.EventResponse, error) {
	dialer := net.Dialer{}
	conn, err := dialer.DialContext(ctx, "unix", socketPath)
	if err != nil {
		return netem.EventResponse{}, fmt.Errorf("connect to %s: %w", socketPath, err)
	}
	defer conn.Close()
	return exchangeNetemExecutionEvent(ctx, conn, request)
}

func exchangeNetemExecutionEvent(ctx context.Context, conn net.Conn, request netem.ExecutionEventRequest) (netem.EventResponse, error) {
	var response netem.EventResponse
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return response, fmt.Errorf("set controller deadline: %w", err)
		}
	}

	if err := json.NewEncoder(conn).Encode(request); err != nil { // send request to controller
		return response, fmt.Errorf("send execution event: %w", err)
	}
	if err := json.NewDecoder(conn).Decode(&response); err != nil { // wait for controller response
		return response, fmt.Errorf("read controller response: %w", err)
	}
	if response.Version != netem.ProtocolVersion {
		return response, fmt.Errorf("controller returned protocol version %d", response.Version)
	}
	if response.RuleID != request.RuleID || response.NodeID != request.NodeID || response.Seq != request.Seq {
		return response, fmt.Errorf("controller response does not match request")
	}
	return response, nil
}

// uses socket file and filesystem no actual ip or port to talk between processes
//Unix socket address: /path/to/netem-controller.sock
