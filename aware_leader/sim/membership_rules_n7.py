"""Member sets of three membership rules on honest (no lying) n=7 scenarios, with crashes.

A crashed node answers no probes and reports no row, so every link it has is unknown on
both ends and the sanitized matrix holds +inf there (node/awareMatrix.go buildMatrix).

Rules (F = 2f + 1 = 5):
  pad        pilot rule: threshold set, padded with the next-best by (score, id) up to F
  or-all     threshold set if it has >= F members, else every node
  hybrid     threshold set if >= F, else every node with a finite score if >= F, else every node

Run:  python3 membership_rules_n7.py
"""
import numpy as np

from model import members_pad, predict_batch, threshold_set

N, F_BYZ = 7, 2
FLOOR = 2 * F_BYZ + 1
ALPHA, EPS = 0.1, 1.0


def members_or_all(scores):
    thr = threshold_set(scores, ALPHA, EPS)
    return sorted(thr) if len(thr) >= FLOOR else list(range(N))


def members_hybrid(scores):
    thr = threshold_set(scores, ALPHA, EPS)
    if len(thr) >= FLOOR:
        return sorted(thr)
    finite = [i for i in range(N) if np.isfinite(scores[i])]
    if len(finite) >= FLOOR:
        return finite
    return list(range(N))


def matrix(d_close, far=(), d_far=None, dead=()):
    D = np.full((N, N), float(d_close))
    for a in far:
        D[a, :] = D[:, a] = d_far
    for a in far:
        for b in far:
            D[a, b] = d_close
    for a in dead:
        D[a, :] = D[:, a] = np.inf
    np.fill_diagonal(D, 0)
    return D


def fmt(ids):
    return "{" + ",".join(str(i + 1) for i in ids) + "}"


SCENARIOS = [
    ("Healthy (uniform 0.2 ms)", matrix(0.2)),
    ("NetworkDelay (uniform 170 ms)", matrix(170)),
    ("NetworkDelayFCrash (170 ms, 3 and 5 dead)", matrix(170, dead=(2, 4))),
    ("FarNode (6,7 at 50 ms, rest 5 ms)", matrix(5, far=(5, 6), d_far=50)),
    ("FarNode + 2 close dead (3, 5)", matrix(5, far=(5, 6), d_far=50, dead=(2, 4))),
    ("FarNode + 1 close dead (3)", matrix(5, far=(5, 6), d_far=50, dead=(2,))),
]


def main():
    for name, D in SCENARIOS:
        s = predict_batch(D[None], F_BYZ)[0]
        print(name)
        print("  scores:", " ".join(f"{i+1}:{v:.1f}" for i, v in enumerate(s)))
        print("  pad", fmt(sorted(members_pad(s, F_BYZ, ALPHA, EPS))),
              "| or-all", fmt(members_or_all(s)),
              "| hybrid", fmt(members_hybrid(s)))


if __name__ == "__main__":
    main()
