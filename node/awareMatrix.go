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
//
// A row's Gen is the generation whose candidate list it feeds, not the one it
// was measured in: the aggregate for generation g-1 is held in pending until the
// node moves to g, then applied with Gen = g and turned into candidates[g]. So
// while a node is in generation g its rows are all <= g and candidates[g] was
// built from exactly those rows, which is also what its ViewChange / NewView
// messages for g carry.
type awareState struct {
	rows       map[int]core.AwareRow
	candidates map[uint64][]int
	// pending holds each received aggregate's rows (Gen unset), keyed by the
	// aggregate's generation, until the node leaves that generation.
	pending map[uint64][]core.AwareRow
}
// we store rows and cand for g
// rows fold to matrix
// we store rtt vector which is to store probes


func newAwareState() *awareState {
	return &awareState{
		rows:       make(map[int]core.AwareRow),
		candidates: make(map[uint64][]int),
		pending:    make(map[uint64][]core.AwareRow),
	}
}

// takePending returns the stored aggregate rows for generation gen and drops
// every entry <= gen, since generations only move forward.
func (s *awareState) takePending(gen uint64) ([]core.AwareRow, bool) {
	rows, ok := s.pending[gen]
	for g := range s.pending {
		if g <= gen {
			delete(s.pending, g)
		}
	}
	return rows, ok
}

// applyRows stores rows, keeping for each node whichever row has the newer
// generation.
// old row carried forward if no new row
func (s *awareState) applyRows(rows []core.AwareRow) {
	for _, row := range rows {
		if len(row.RTTms) == 0 {
			continue
		}
		// if ndont have new row for node carry ol
		if old, ok := s.rows[row.Node]; ok && old.Gen > row.Gen {
			continue
			// impossible to have old gen > coming row.gen on both paths catchup or agg
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
	// third check is really to not index out of bound

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
			// dead peer: live nodes report it as 0 from the first post-crash aggregate, but its own last row carries forward and
			// keeps the link finite until that row is older than staleness; only then is it max(0,0) mapped to inf
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

// computeCandidates builds the matrix as of generation gen and stores the
// candidate list for gen. Rows carry the generation they feed, so the matrix and
// the list share one generation. It returns the matrix and scores for logging.
func (s *awareState) computeCandidates(gen uint64, n, f int, staleness uint64, alpha, eps float64) ([][]float64, []float64, []int) {
	D := s.buildMatrix(n, gen, staleness)
	scores := make([]float64, n)
	for p := 0; p < n; p++ {
		scores[p] = predictAwareLatency(D, f, p)
	}
	cands := selectAwareCandidates(scores, f, alpha, eps)
	s.candidates[gen] = cands
	for g := range s.candidates {
		if g+2 < gen {
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
		// used in gen 1 when no list
		// epoch mode off no gen change but then aware agg also not work
		// if catch up from amp and it doesnt have rows
		// if pulled to g by amp and not received agg for g-1 so we get cand from amp and if that vc dont have cand so missing list
	}
	return cands[int((view.Generation+view.Counter-1)%uint64(len(cands)))]
}

// gen =2 and counter 1  is 3 -1 mod n if cand list is n so idx 2 of cand become leader
// if just counter -1 than start of new gen always cand[0]