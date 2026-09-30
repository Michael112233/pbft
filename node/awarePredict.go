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
	// only this sort break ties on id when scores are equal
	sort.SliceStable(order, func(a, b int) bool { return scores[order[a]] < scores[order[b]] })
	best := scores[order[0]] // indexing node with the lowest score
	threshold := best + math.Max(best*alpha, eps)
	// score is leader commit is 3rounds
	// in lant best is 0.5 and best*alpha is less than eps 1ms, alpha is 10
	// in 170ms uniform best is 510 and best*alpha is more than eps
	// bet always pass bar and best + best*alpha so 10% higher one also pass bar
	floor := 2*f + 1
	if floor > n {
		floor = n
	}
	out := make([]int, 0, n)
	for i, idx := range order {
		if scores[idx] <= threshold || i < floor {
			// if under threshold then all nodes in cand in score order
			// the floor check is only there to ensure we always have at least 2f+1 candidates
			// out purely on scores and always have at least 2f+1 candidates
			out = append(out, idx+1)
		}
	}
	return out
}

// lowe score mean faster to commit as leader
// // order[0] is node with lowest score, order is soreted by increasing score

// scores unsorted and idx zero is score of node 0+1 = node 1
// order has ids of nodes which have lowest to high score in order, ids are 0 based as those are idxes to scores

// order[rank] + 1 is the node id

// in healthy scores very small bar set by 1ms and everyon pass it
// in nd score in 500 so score*alpha set bar still everyone pass it

// in far node the far node doesnt meet the bar 
// and bar is best on eps as best*alpha is small best is lan speed
// far node score much bigger than 1ms eps so outside of bar
// its ranked 3rd os 3<3 also fail cant meet floor condition