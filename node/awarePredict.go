package node

import (
	"math"
	"sort"
)

// kthSmallest returns the k-th smallest value (1-based) of xs. It copies xs.
func kthSmallest(xs []float64, k int) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[k-1]
}

// predictAwareLatency predicts the time in ms from leader p (0-based) sending a
// PrePrepare until p commits, given one-way delays D in ms (D[i][i] == 0,
// +Inf for unreachable links). Quorum rules follow node.go: backups send
// Prepare on PrePrepare receipt and count their own, the leader sends no
// Prepare, prepared = 2f-th Prepare, committed = (2f+1)-th Commit counting
// one's own. Port of latency_sim/sim.py predict_latency.
func predictAwareLatency(D [][]float64, f, p int) float64 {
	n := len(D)
	pp := make([]float64, n)
	for i := 0; i < n; i++ {
		pp[i] = D[p][i]
	}
	prep := make([]float64, n)
	arrivals := make([]float64, 0, n)
	for i := 0; i < n; i++ {
		arrivals = arrivals[:0]
		for j := 0; j < n; j++ {
			if j == p {
				continue
			}
			arrivals = append(arrivals, pp[j]+D[j][i])
		}
		prep[i] = kthSmallest(arrivals, 2*f)
		if i != p {
			prep[i] = math.Max(prep[i], pp[i])
		}
	}
	commits := make([]float64, n)
	for j := 0; j < n; j++ {
		commits[j] = prep[j] + D[j][p]
	}
	return kthSmallest(commits, 2*f+1)
}

// selectAwareCandidates returns node ids (1-based) whose score is within
// max(best*alpha, eps) of the best, ordered by (score, id), padded with the
// next-best nodes up to 2f+1 so honest nodes always hold a majority of the
// rotation.
func selectAwareCandidates(scores []float64, f int, alpha, eps float64) []int {
	n := len(scores)
	order := make([]int, n)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] < scores[order[b]] })
	best := scores[order[0]]
	threshold := best + math.Max(best*alpha, eps)
	floor := 2*f + 1
	if floor > n {
		floor = n
	}
	out := make([]int, 0, n)
	for i, idx := range order {
		if scores[idx] <= threshold || i < floor {
			out = append(out, idx+1)
		}
	}
	return out
}
