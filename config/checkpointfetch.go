package config

import (
	"fmt"
	"time"
)

// Checkpoint state transfer (node/checkpointfetch.go). A node that holds a stable
// checkpoint certificate (2f+1 matching CHECKPOINT votes) above its last executed
// slot, and has executed nothing for DelayMs, fetches that checkpoint's state from a
// node that voted for it instead of waiting for the next NewView to carry it.
//
// It closes the stranding gap seen in the n=7 multi-scenario run: a node that left
// the view before executing up to checkpoint k, while the others stabilized k after
// sending their ViewChanges, installs a NewView whose O-set re-proposes slots <= k
// that every other replica skips (they are below its low watermark). Nothing
// retransmits them, so the node executed nothing for the rest of the view.

// defaultCheckpointFetchDelay is the execution stall, with a certificate above
// the last executed slot, after which a node fetches checkpoint state.
//
// Why 200 ms. The delay must tell a node that is behind but still catching up
// from one that can no longer catch up by normal message processing:
//   - Lower bound: the largest gap between executions at a node that is fine. A
//     node that is merely behind (a far replica D behind the quorum, or one that
//     received the others' votes a few ms before executing the slot itself) keeps
//     executing every few ms, because the pipeline keeps it fed, so only a full gap
//     with no execution counts. The longest normal gaps are 100 ms: the Throttle
//     gate spaces proposals proposal_min_interval_ms (100) apart, and the
//     ProposalDelay leader sleeps 100 ms per proposal on its event loop. 200 ms is
//     2x that. The client tick (24 ms) and the WAN one-way delay do not enter: a
//     lagging node's executions are shifted, not spaced out.
//   - Upper bound: the node must fetch before the others run past its high
//     watermark (its stable checkpoint + 2 x CHECKPOINT_INTERVAL). From then on it
//     drops their messages, which nothing re-sends, so after installing the state it
//     would stall again at the next missing slot. In the stranded case the others
//     are ~250 slots from that point, ~1.5 s at ~166 slots/s (inject_speed 200 per
//     24 ms, batch 50); a stall is detected within 1-2 delays (200-400 ms), plus a
//     round trip for the reply.
//
// Raise it if a scenario has longer normal gaps (a larger proposal_min_interval_ms);
// lower it only if the others can pass the watermark faster than ~1 s.
const defaultCheckpointFetchDelay = 200 * time.Millisecond

type CheckpointFetchConfig struct {
	// Enabled turns checkpoint state transfer on: both fetching (stall timer,
	// requests, installing replies) and serving other nodes' requests. Off, a node
	// that falls below a stable checkpoint waits for a NewView to carry it.
	Enabled bool `json:"enabled"`
	// DelayMs is how long a node with a certificate above its last executed slot
	// must execute nothing before it fetches; 0 means 200. See
	// defaultCheckpointFetchDelay for why.
	DelayMs int `json:"delay_ms"`
}

func (c *Config) CheckpointFetchDelay() time.Duration {
	return msOrDefault(c.CheckpointFetch.DelayMs, defaultCheckpointFetchDelay)
}

// checkpointFetchRetryFactor sizes the retry timeout from the stall delay.
//
// Why a separate timeout. The stall delay answers "is this node stuck?" and is sized
// by gaps between executions. The retry timeout answers "is this peer not answering?"
// and is sized by the round trip. A retry only matters when the voter asked cannot or
// will not answer (crashed after voting, Byzantine, very slow): the voters are 2f+1
// nodes, at least f+1 of them correct, so rotating through them reaches one that
// answers. The first version re-sent after one stall delay (200 ms), shorter than the
// 340 ms round trip under the 170 ms NetworkDelay netem, so every stranding in
// results/ckpt_fetch_n7 sent a second request before the first reply landed.
//
// Why 5x (1 s at the default delay):
//   - Lower bound: longer than any round trip plus serving time we run: ~ms on
//     loopback, 48 ms on the CloudLab WAN link, 340 ms under NetworkDelay.
//   - Upper bound: when the first voter does not answer, the next attempt must still
//     land before the others run past the stuck node's high watermark: detection (1-2
//     delays) + retry + a round trip = 0.2-0.4 + 1 + 0.34 = ~1.5-1.7 s under
//     NetworkDelay, where the others need ~3.3 s (~77 slots/s). At the healthy
//     ~166 slots/s they need ~1.5 s, so a dead first voter on a fast LAN may cost the
//     node one more stall at the next checkpoint; that case needs a crash right after
//     voting, so it is rare.
//
// Scaling with delay_ms keeps the two in proportion if the delay is raised.
const checkpointFetchRetryFactor = 5

// CheckpointFetchRetry is how long a node waits for a fetched state before asking the
// next voter.
func (c *Config) CheckpointFetchRetry() time.Duration {
	return checkpointFetchRetryFactor * c.CheckpointFetchDelay()
}

func (c *Config) ValidateCheckpointFetch() error {
	if c.CheckpointFetch.DelayMs < 0 {
		return fmt.Errorf("checkpoint_fetch.delay_ms must be >= 0 (0 means 200), got %d", c.CheckpointFetch.DelayMs)
	}
	return nil
}
