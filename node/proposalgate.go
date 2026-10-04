package node

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/michael112233/pbft/core"
)

// Proposal-rate gate: the node-side half of the targeted proposal throttling
// experiment (see config/throttle.go and client/throttlemanager.go). It stands in
// for a network throttle on this node's outbound proposals.
//
// When the gate is on, this node is allowed at most one proposal every
// proposalMinInterval while it leads. Unlike the ProposalDelay scenario's
// time.Sleep in tryPropose, the gate never blocks the event loop: a proposal
// that is not yet permitted arms a one-shot timer and returns having dequeued
// nothing, and the timer re-drives tryPropose.
//
// The gate bites only on the leader, which falls out of tryPropose's own leader
// guard. It is enabled and disabled by the client over the unary Deliver RPC
// (core.MsgEventMessage with EventTypeThrottleGateOn / Off), and the command is
// applied here, on the loop, before the RPC is acknowledged.
//
// All of this state is loop-owned, like the other timers: no locking.

// throttleGateCmd carries a gate command from the gRPC handler goroutine to the
// event loop. The handler blocks on applied so the unary Ack reports that the
// loop actually applied the change, which is what the controller's slot state
// machine needs before it reuses a slot.
type throttleGateCmd struct {
	enable  bool
	applied chan struct{}

	// claimed is won exactly once, by whichever of the event loop and the gRPC
	// handler reaches it first: the loop claims before applying, the handler
	// claims when its RPC deadline expires. The winner decides whether the
	// command takes effect at all.
	//
	// Without it, a deadline that expires after the command was queued leaves
	// the loop applying a change the controller has already recorded as failed,
	// so the gate ends up on a node no slot is tracking and nothing ever turns
	// it off (client/throttlemanager.go). It is a pointer because the command
	// travels by value over a channel.
	claimed *atomic.Bool
}

// claim returns true for the single caller that wins the right to decide this
// command's fate.
func (c throttleGateCmd) claim() bool {
	return c.claimed.CompareAndSwap(false, true)
}

func (n *Node) resetProposalGateTimer(wait time.Duration) {
	n.proposalGateTimerCh = resetOneShotTimer(&n.proposalGateTimer, wait)
}

func (n *Node) stopProposalGateTimer() {
	stopOneShotTimer(n.proposalGateTimer)
	n.proposalGateTimerCh = nil
}

// proposalGateBlocks reports whether the gate currently forbids a proposal, and
// arms the retry timer when it does. Called from tryPropose below every cheap
// early return and above the destructive Dequeue.
func (n *Node) proposalGateBlocks() bool {
	if !n.proposalGateOn || n.proposalMinInterval <= 0 || n.lastProposalAt.IsZero() {

		return false
	}
	wait := n.proposalMinInterval - time.Since(n.lastProposalAt)
	if wait <= 0 {
		return false
	}
	n.resetProposalGateTimer(wait)
	return true
}

func (n *Node) handleProposalGateTimeout() {
	n.stopProposalGateTimer()
	n.tryPropose(true)
}

// handleThrottleGateCmd applies an enable/disable command on the event loop.
// Disabling cancels any pending limit immediately and re-drives proposing in
// the same tick, so releasing a slot takes effect at once.
//
// applied is closed as soon as the claim is won, before the flag is even
// written, because from that point the command is certain to take effect in this
// tick and nothing below blocks. That releases the gRPC handler, and with it the
// client's 1s deadline, without waiting for tryPropose — which computes a batch
// digest and broadcasts, and can take a while.
func (n *Node) handleThrottleGateCmd(cmd throttleGateCmd) {
	// event loop comes here
	if !cmd.claim() {
		// The handler's deadline expired first and the controller has already
		// recorded this command as failed. Applying it now would leave the gate
		// in a state no slot is tracking, so drop it; the controller retries.
		n.log.Info("GATE_RPC cancelled enable=%v: client deadline expired before the loop applied it", cmd.enable)
		return
	}
	close(cmd.applied)

	view := n.GetViewID()
	if n.proposalGateOn == cmd.enable {
		n.log.Info("GATE unchanged enabled=%v interval=%v view=(%d,%d)",
			cmd.enable, n.proposalMinInterval, view.Generation, view.Counter)
		return
	}
	n.proposalGateOn = cmd.enable
	n.log.Info("GATE enabled=%v interval=%v view=(%d,%d) leader=%v",
		cmd.enable, n.proposalMinInterval, view.Generation, view.Counter, n.IsLeader())

	if !cmd.enable {
		n.stopProposalGateTimer()
		n.tryPropose(true)
	}
}

// ReceiveThrottleGateCmd hands a gate command to the event loop and waits for it
// to be applied. Runs on a gRPC handler goroutine, never on the loop.
func (n *Node) ReceiveThrottleGateCmd(ctx context.Context, enable bool) error {
	cmd := throttleGateCmd{enable: enable, applied: make(chan struct{}), claimed: &atomic.Bool{}}
	// close applied means the loop owns the command and applies it in this tick,
	// so the unary Ack reports that to the client.
	select {
	case n.throttleGateCh <- cmd: // send to loop
	case <-ctx.Done():
		// Nothing was queued, so nothing can be applied later: the failure the
		// controller records is accurate and a retry is all that is needed.
		n.log.Info("GATE_RPC expired before enqueue enable=%v: not applied", enable)
		return ctx.Err()
	case <-n.eventLoopStopCh:
		return errEventLoopStopped
	}

	select {
	case <-cmd.applied: // happy path
		return nil
	case <-ctx.Done():
		// The command is already queued, so claim it to stop the loop applying
		// something the client has given up on. That keeps "failed Ack" meaning
		// "not applied".
		if cmd.claim() {
			n.log.Info("GATE_RPC expired after enqueue enable=%v: cancelled, not applied", enable)
			return ctx.Err()
		}
		// Lost the claim: the loop got there first, so the command did take
		// effect. gRPC has already failed the call client-side at the deadline,
		// so the controller believes otherwise and its view of this node's gate
		// may be stale until its next command. Watch this line.
		<-cmd.applied
		n.log.Error("GATE_RPC expired after enqueue enable=%v: APPLIED ANYWAY, controller recorded a failure", enable)
		return nil
	case <-n.eventLoopStopCh:
		cmd.claim() // the loop is going away; make sure a queued command cannot apply
		return errEventLoopStopped
	}
}

// throttleGateEnableFor maps a gate event type to its enable flag. ok is false for
// any other event type.
func throttleGateEnableFor(eventType string) (enable bool, ok bool) {
	switch eventType {
	case core.EventTypeThrottleGateOn:
		return true, true
	case core.EventTypeThrottleGateOff:
		return false, true
	default:
		return false, false
	}
}
