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
	kept := v.samples[peer][:0]
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
