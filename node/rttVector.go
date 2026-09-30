package node

import (
	"sort"
	"time"
)

// rttSample is one probe round trip to peer, produced by the prober goroutine.
type rttSample struct {
	peer  int
	rttMs float64
	at    time.Time
}

type timedRTT struct {
	rttMs float64
	at    time.Time
}

// rttVector keeps this node's recent RTT samples per peer and reports the median
// of those inside the window. Owned by the event loop; no locking.
type rttVector struct {
	window  time.Duration
	samples map[int][]timedRTT
}

func newRTTVector(window time.Duration) *rttVector {
	return &rttVector{window: window, samples: make(map[int][]timedRTT)}
}

func (v *rttVector) add(s rttSample) {
	v.samples[s.peer] = append(v.samples[s.peer], timedRTT{rttMs: s.rttMs, at: s.at})
	v.prune(s.peer, s.at)
}

func (v *rttVector) prune(peer int, now time.Time) {
	kept := v.samples[peer][:0] // zero length slice pointing to same array so filter in place no new allocation
	for _, x := range v.samples[peer] {
		if now.Sub(x.at) <= v.window {
			kept = append(kept, x)
		}
	}
	v.samples[peer] = kept
}

// snapshot returns RTTms for nodes 1..nodeNum (index = id-1): the median of the
// samples within the window, or 0 when there are none (unknown, e.g. a peer that
// stopped answering). self is always 0.
// at epoch aggregate rtt vector has median of last 10s for each peer
// if nothing in last 10s from a peer so its RTT is considered unknown (0).
func (v *rttVector) snapshot(nodeNum, self int, now time.Time) []float64 {
	out := make([]float64, nodeNum)
	for peer := 1; peer <= nodeNum; peer++ {
		if peer == self {
			continue
		}
		v.prune(peer, now)
		xs := v.samples[peer]
		if len(xs) == 0 {
			continue
		}
		vals := make([]float64, len(xs))
		for i, x := range xs {
			vals[i] = x.rttMs
		}
		sort.Float64s(vals)
		m := len(vals) / 2
		if len(vals)%2 == 1 {
			out[peer-1] = vals[m]
		} else {
			out[peer-1] = (vals[m-1] + vals[m]) / 2
		}
	}
	return out
}

// Cost of the per-sample prune, with the values used in the experiments
// (aware_probe_interval_ms = 500, aware_rtt_window_s = 10, node_num = 4):
//
//	samples kept per peer  = window / probe interval = 10 s / 0.5 s = 20
//	scanned per add        = 20 kept + the new one   = ~21 comparisons (one peer)
//	adds per second        = 3 peers * (1 / 0.5 s)   = 6
//	scanned per second     = 6 * 21                  = ~126 comparisons
//
// snapshot prunes every peer (~60 elements), then copies and sorts each peer's
// ~20 values. It runs once per snapshot, not per probe.
//
// 20 is the steady state. A probe that times out, or a tick skipped because the
// previous probe to that peer is still in flight, only shortens the list. A sample
// exactly `window` old is kept (<=), so the list can briefly hold 21.
