"""Leader-latency model for PBFT (AWARE-style PredictLatency, one matrix, no weights).

D is a symmetric one-way delay matrix in ms, 0-indexed (node id = index + 1).
Quorum rules match node/node.go: backups send a Prepare on PrePrepare receipt and count
their own; the leader sends no Prepare; prepared = 2f Prepares, committed = 2f+1 Commits.
"""
import itertools
import math
import random

INF = 10**6


def kth(xs, k):
    return sorted(xs)[k - 1]


def predict_latency(D, f, p):
    return predict_trace(D, f, p)[0]


def predict_trace(D, f, p):
    n = len(D)
    pp = [D[p][i] for i in range(n)]
    prep = [0] * n
    for i in range(n):
        arrivals = [pp[j] + D[j][i] for j in range(n) if j != p]
        prep[i] = kth(arrivals, 2 * f)
        if i != p:
            prep[i] = max(prep[i], pp[i])
    commits = [prep[j] + D[j][p] for j in range(n)]
    return kth(commits, 2 * f + 1), pp, prep, commits


def shortcut_score(D, f, p):
    return 2 * kth([D[p][j] for j in range(len(D)) if j != p], 2 * f)


def remove_nodes_score(D, f, p):
    """Worst case over silencing any f nodes other than p."""
    n = len(D)
    worst = 0
    for silenced in itertools.combinations([j for j in range(n) if j != p], f):
        E = [row[:] for row in D]
        for s in silenced:
            for j in range(n):
                if j != s:
                    E[s][j] = E[j][s] = INF
        worst = max(worst, predict_latency(E, f, p))
    return worst


def triangle_clamp(D, ignore_top=0):
    """Raise each link to its reverse-triangle lower bound |D[i,c] - D[j,c]|.

    ignore_top=f drops the f largest bounds per link, so up to f Byzantine third points
    cannot raise a link between two honest nodes.
    """
    n = len(D)
    C = [row[:] for row in D]
    for i in range(n):
        for j in range(n):
            if i != j:
                bounds = sorted((abs(D[i][c] - D[j][c]) for c in range(n) if c not in (i, j)), reverse=True)
                C[i][j] = max(D[i][j], bounds[ignore_top])
    return C


def vivaldi_embed(D, **kw):
    """Single-matrix wrapper around vivaldi_embed_batch."""
    return vivaldi_embed_batch([D], **kw)[0]


def vivaldi_embed_batch(Ds, f=0, trim="none", clip=False, tau=3.0, iters=300, dim=3, seed=0,
                        ce=0.25, cc=0.25, e0=0.1):
    """Deterministic Vivaldi embedding of reported matrices; returns predicted delay matrices.

    Every matrix starts from the same seeded coordinates, as every replica would.
    Beware-style median filtering of the forces acting on each node:
      trim="fixed":   always drop the f forces farthest from the coordinate-wise median force
      trim="outlier": drop up to f of those, only if farther than tau x the median distance
      clip=True:      clip kept forces to their median norm
    """
    import numpy as np

    B, n = len(Ds), len(Ds[0])
    off = ~np.eye(n, dtype=bool)
    M = np.maximum(np.array(Ds, dtype=float)[:, off].reshape(B, n, n - 1), 1e-3)
    others = np.array([[j for j in range(n) if j != i] for i in range(n)])
    rng = np.random.default_rng(seed)
    X = np.broadcast_to(rng.uniform(-1, 1, (n, dim)), (B, n, dim)).copy()
    fallback = rng.normal(size=(n, n - 1, dim))
    fallback /= np.linalg.norm(fallback, axis=2, keepdims=True)
    e = np.full((B, n), e0)
    for _ in range(iters):
        diff = X[:, :, None, :] - X[:, others]
        dist = np.linalg.norm(diff, axis=3)
        unit = np.where(dist[..., None] > 1e-9, diff / np.maximum(dist, 1e-9)[..., None], fallback)
        w = e[:, :, None] / (e[:, :, None] + e[:, others])
        F = (cc * w * (M - dist))[..., None] * unit
        keep = np.ones((B, n, n - 1), dtype=bool)
        if trim != "none" and f > 0:
            med = np.median(F, axis=2)
            spread = np.linalg.norm(F - med[:, :, None, :], axis=3)
            worst = np.argsort(spread, axis=2)[:, :, -f:]
            if trim == "fixed":
                np.put_along_axis(keep, worst, False, axis=2)
            else:
                limit = tau * np.median(spread, axis=2, keepdims=True)
                np.put_along_axis(keep, worst, np.take_along_axis(spread, worst, axis=2) <= limit, axis=2)
        if clip:
            norms = np.linalg.norm(F, axis=3)
            med_norm = np.nanmedian(np.where(keep, norms, np.nan), axis=2, keepdims=True)
            F = F * np.minimum(1.0, med_norm / np.maximum(norms, 1e-9))[..., None]
        k = keep.sum(axis=2)
        X = X + (F * keep[..., None]).sum(axis=2) / k[..., None]
        cw = ce * w
        sample_err = np.abs(dist - M) / M
        e = np.clip((keep * (sample_err * cw + e[:, :, None] * (1 - cw))).sum(axis=2) / k, 1e-3, 1.0)
    dist = np.linalg.norm(X[:, :, None, :] - X[:, None, :, :], axis=3)
    return dist.tolist()


def matrix_from_links(n, links, default=0):
    """links: {(i, j): ms} with 1-based node ids."""
    D = [[0 if i == j else default for j in range(n)] for i in range(n)]
    for (i, j), v in links.items():
        D[i - 1][j - 1] = D[j - 1][i - 1] = v
    return D


def random_matrix(n, values, rng=random):
    D = [[0] * n for _ in range(n)]
    for i in range(n):
        for j in range(i + 1, n):
            D[i][j] = D[j][i] = rng.choice(values)
    return D


def geometric_matrix(n, size=100, rng=random):
    pts = [(rng.uniform(0, size), rng.uniform(0, size)) for _ in range(n)]
    return [[round(math.dist(pts[i], pts[j])) for j in range(n)] for i in range(n)]


def with_link(D, i, j, v):
    """Copy of D with link (i, j) set to v; 1-based ids."""
    E = [row[:] for row in D]
    E[i - 1][j - 1] = E[j - 1][i - 1] = v
    return E
# changing i,j like for collusion

def argmin(scores):
    return min(range(len(scores)), key=lambda p: (scores[p], p))


def print_matrix(D):
    for row in D:
        print("  " + " ".join(f"{v:4d}" for v in row))
