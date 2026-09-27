package node

import (
	"math"
	"sort"

	"github.com/michael112233/pbft/core"
)

// awareState holds the Aware latency matrix and the candidate list derived for
// each generation. Owned by the event loop; no locking.
//
// rows persist across epochs: an aggregate overwrites only the rows it carries,
// so a node whose vector was missing keeps its last committed row (carry
// forward) until it becomes older than the staleness bound.
type awareState struct {
	rows       map[int]core.AwareRow
	candidates map[uint64][]int
}

func newAwareState() *awareState {
	return &awareState{
		rows:       make(map[int]core.AwareRow),
		candidates: make(map[uint64][]int),
	}
}

// applyRows stores rows, keeping for each node whichever row has the newer
// generation.
func (s *awareState) applyRows(rows []core.AwareRow) {
	for _, row := range rows {
		if len(row.RTTms) == 0 {
			continue
		}
		if old, ok := s.rows[row.Node]; ok && old.Gen > row.Gen {
			continue
		}
		s.rows[row.Node] = core.AwareRow{Node: row.Node, Gen: row.Gen, RTTms: append([]float64(nil), row.RTTms...)}
	}
}

// snapshotRows returns all rows ordered by node id, for carrying in ViewChange /
// NewView messages.
func (s *awareState) snapshotRows() []core.AwareRow {
	if len(s.rows) == 0 {
		return nil
	}
	out := make([]core.AwareRow, 0, len(s.rows))
	for _, row := range s.rows {
		out = append(out, core.AwareRow{Node: row.Node, Gen: row.Gen, RTTms: append([]float64(nil), row.RTTms...)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })
	return out
}

// rowValue returns node's measured RTT to peer (both 1-based) in ms, or 0 if the
// row is missing, older than staleness generations at gen, or has no
// measurement for peer.
func (s *awareState) rowValue(node, peer int, gen, staleness uint64) float64 {
	row, ok := s.rows[node]
	if !ok || gen > row.Gen+staleness || peer-1 >= len(row.RTTms) {
		return 0
	}
	return row.RTTms[peer-1]
}

// buildMatrix returns the sanitized n x n one-way delay matrix (0-based, ms) as
// of epoch generation gen: each link takes the larger of its two measured ends
// (AWARE max-sanitize) halved to a one-way delay, and a link with neither end
// known is +Inf.
func (s *awareState) buildMatrix(n int, gen, staleness uint64) [][]float64 {
	D := make([][]float64, n)
	for i := range D {
		D[i] = make([]float64, n)
	}
	for i := 1; i <= n; i++ {
		for j := i + 1; j <= n; j++ {
			rtt := math.Max(s.rowValue(i, j, gen, staleness), s.rowValue(j, i, gen, staleness))
			d := math.Inf(1)
			if rtt > 0 {
				d = rtt / 2
			}
			D[i-1][j-1], D[j-1][i-1] = d, d
		}
	}
	return D
}

// computeCandidates builds the matrix as of dataGen and stores the candidate
// list for targetGen. It returns the matrix and scores for logging.
func (s *awareState) computeCandidates(targetGen, dataGen uint64, n, f int, staleness uint64, alpha, eps float64) ([][]float64, []float64, []int) {
	D := s.buildMatrix(n, dataGen, staleness)
	scores := make([]float64, n)
	for p := 0; p < n; p++ {
		scores[p] = predictAwareLatency(D, f, p)
	}
	cands := selectAwareCandidates(scores, f, alpha, eps)
	s.candidates[targetGen] = cands
	for g := range s.candidates {
		if g+2 < targetGen {
			delete(s.candidates, g)
		}
	}
	return D, scores, cands
}

// leader returns the Aware leader of view: candidates[(g + c - 1) mod k]. With
// no candidate list for the generation yet (before the first aggregate) every
// node is a candidate in id order.
func (s *awareState) leader(view core.ViewID, n int) int {
	cands := s.candidates[view.Generation]
	if len(cands) == 0 {
		return int((view.Generation+view.Counter-1)%uint64(n)) + 1
	}
	return cands[int((view.Generation+view.Counter-1)%uint64(len(cands)))]
}
