"""Vectorised leader-latency model and candidate rules for the aware_leader study.

predict_batch is latency_sim/sim.py's predict_latency (AWARE PredictLatency, one matrix,
PBFT quorums from node/node.go) evaluated for every leader of a batch of matrices at
once. check_against_reference() asserts the two agree.

All matrices are one-way delays in ms, 0-indexed (node id = index + 1).
"""
import os
import sys

import numpy as np

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "..", "latency_sim"))
import sim as ref  # noqa: E402

BIG = 1e6  # a link a Byzantine node reports as unusable (same role as sim.INF)


def kth(a, k, axis):
    """k-th smallest (1-based) along axis."""
    return np.take(np.partition(a, k - 1, axis=axis), k - 1, axis=axis)


def predict_batch(D, f):
    """D: (B, n, n). Returns (B, n): predicted commit latency at leader p, per p."""
    B, n, _ = D.shape
    out = np.empty((B, n))
    for p in range(n):
        pp = D[:, p, :]                                   # (B, n) PrePrepare arrival
        arr = pp[:, :, None] + D                          # arr[b, j, i] = pp_j + D[j][i]
        arr = np.delete(arr, p, axis=1)                   # leader sends no Prepare
        prep = kth(arr, 2 * f, axis=1)                    # (B, n)
        mask = np.arange(n) != p
        prep[:, mask] = np.maximum(prep[:, mask], pp[:, mask])
        commits = prep + D[:, :, p]                       # commit from j reaches p
        out[:, p] = kth(commits, 2 * f + 1, axis=1)
    return out


def check_against_reference(trials=300, n=7, f=2, seed=3):
    rng = np.random.default_rng(seed)
    for _ in range(trials):
        A = rng.choice([1, 5, 10, 20, 50, 100], size=(n, n)).astype(float)
        D = np.triu(A, 1)
        D = D + D.T
        got = predict_batch(D[None], f)[0]
        want = [ref.predict_latency(D.tolist(), f, p) for p in range(n)]
        assert np.allclose(got, want), (got, want)


# ---------------------------------------------------------------------------
# Candidate membership rules. scores: (n,) array. Return a sorted list of 0-based ids
# for membership; ordering is applied separately.
# ---------------------------------------------------------------------------

def threshold_set(scores, alpha, eps):
    best = scores.min()
    return set(np.flatnonzero(scores <= best + max(best * alpha, eps)).tolist())


def members_pad(scores, f, alpha, eps):
    """Pilot rule (node/awarePredict.go): threshold set, padded with the next-best
    nodes by (score, id) up to 2f+1."""
    n = len(scores)
    order = sorted(range(n), key=lambda i: (scores[i], i))
    floor = min(2 * f + 1, n)
    thr = threshold_set(scores, alpha, eps)
    return [i for r, i in enumerate(order) if i in thr or r < floor]


def members_or_all(scores, f, alpha, eps):
    """Threshold set if it reaches 2f+1, otherwise every node."""
    thr = threshold_set(scores, alpha, eps)
    if len(thr) >= 2 * f + 1:
        return sorted(thr)
    return list(range(len(scores)))


# ---------------------------------------------------------------------------
# Orderings: the leader of the first view of generation g, given a member list.
# ---------------------------------------------------------------------------

def first_leader_score_order(members, scores, g):
    """Pilot rule: C sorted by (score, id), leader(g, 1) = C[g mod k]."""
    C = sorted(members, key=lambda i: (scores[i], i))
    return C[g % len(C)]


def first_leader_ring(members, n, g):
    """Proposed rule: walk the id ring from position (g-1) mod n, take the first member."""
    s = (g - 1) % n
    mem = set(members)
    for step in range(n):
        i = (s + step) % n
        if i in mem:
            return i
    raise ValueError("empty member set")


if __name__ == "__main__":
    check_against_reference()
    print("predict_batch matches latency_sim/sim.py predict_latency")
