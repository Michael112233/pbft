"""What two Byzantine nodes can do to the Aware candidate list in the n=7 far-node scenario.

Topology: five close nodes and two far ones. Byzantine nodes choose what they report for
each of their links. After AWARE max-sanitization a link with an honest endpoint can only
go up (the honest end reports the truth), and the Byzantine-Byzantine link can be any
value. The search enumerates every combination of
    each Byzantine-honest link in {true, d_far, 2*d_far, BIG}   (4^10 combinations)
    the Byzantine-Byzantine link in {0, true, BIG}
and records, for every resulting matrix, the member set and the member order. It then
reports:

  * which member sets the adversary can force, under the pilot rule (pad to 2f+1 by score)
    and the "threshold set, or every node" rule;
  * the worst share of honest close leaders over a full rotation, against round robin;
  * how often the adversary can make the first leader of a generation Byzantine, under
    the pilot order (C sorted by score, leader(g,1) = C[g mod k]) and the id-ring order
    (walk the ids from (g-1) mod n, first member).

Run:  python3 farnode_attack_n7.py [--d-close 5] [--d-far 50] [--d-farfar 5]
"""
import argparse
import itertools
import math

import numpy as np

from model import BIG, predict_batch

N, F = 7, 2
ALPHA, EPS = 0.1, 1.0


def topology(far, d_close, d_far, d_farfar, jitter, rng):
    D = np.full((N, N), float(d_close))
    for a in far:
        for j in range(N):
            D[a, j] = D[j, a] = d_far
    for a, b in itertools.combinations(far, 2):
        D[a, b] = D[b, a] = d_farfar
    if jitter:
        J = rng.uniform(1 - jitter, 1 + jitter, size=(N, N))
        J = np.triu(J, 1)
        D = D * (J + J.T + np.eye(N))
    np.fill_diagonal(D, 0)
    return D


def attacked_matrices(D, byz, d_far):
    """Every sanitized matrix the search covers, as one (M, N, N) array."""
    honest = [i for i in range(N) if i not in byz]
    links = [(b, h) for b in byz for h in honest]
    levels = [None, d_far, 2 * d_far, BIG]
    combos = np.array(list(itertools.product(range(len(levels)), repeat=len(links))))
    bb_levels = [0.0, None, BIG]
    out = []
    for bb in bb_levels:
        X = np.repeat(D[None], len(combos), axis=0)
        for li, (b, h) in enumerate(links):
            for lv, val in enumerate(levels):
                if val is None:
                    continue
                sel = combos[:, li] == lv
                v = np.maximum(X[sel, b, h], val)
                X[sel, b, h] = v
                X[sel, h, b] = v
        if len(byz) == 2 and bb is not None:
            b1, b2 = byz
            X[:, b1, b2] = bb
            X[:, b2, b1] = bb
        out.append(X)
    return np.concatenate(out)


def membership(scores):
    """(M, N) scores -> (pad mask, or-all mask, order) as numpy arrays."""
    best = scores.min(axis=1, keepdims=True)
    thr = scores <= best + np.maximum(best * ALPHA, EPS)
    order = np.argsort(scores, axis=1, kind="stable")       # ties broken by id
    rank = np.empty_like(order)
    np.put_along_axis(rank, order, np.arange(N)[None].repeat(len(order), 0), axis=1)
    pad = thr | (rank < 2 * F + 1)
    or_all = np.where(thr.sum(axis=1, keepdims=True) >= 2 * F + 1, thr, True)
    return pad, or_all, order


def ordered_members(mask, order):
    """Unique ordered member lists (by score) across the batch."""
    m_ord = np.take_along_axis(mask, order, axis=1)
    key = np.where(m_ord, order + 1, 0)
    uniq = np.unique(key, axis=0)
    return [tuple(int(x) - 1 for x in row if x) for row in uniq]


def first_leader_idorder(members, g):
    """Members sorted by id, leader(g, 1) = C[(g - 1) mod k]."""
    C = sorted(members)
    return C[(g - 1) % len(C)]


def perm_rank(g):
    """A public per-generation permutation of the ids (rank of each id), from a hash of g."""
    import hashlib
    keys = [hashlib.sha256(f"{g}:{i}".encode()).digest() for i in range(N)]
    return {i: keys[i] for i in range(N)}


PERM_G = 840  # generations checked for the seeded permutation


def first_leader_perm(members, g, ranks):
    return min(members, key=lambda i: ranks[g][i])


def first_leader_ring(members, g):
    mem = set(members)
    s = (g - 1) % N
    for step in range(N):
        if (s + step) % N in mem:
            return (s + step) % N
    raise ValueError


def fmt(ids):
    return "{" + ",".join(str(i + 1) for i in sorted(ids)) + "}"


def analyse(D, byz, far, d_far, label):
    byz = tuple(byz)
    honest_close = [i for i in range(N) if i not in byz and i not in far]
    true_scores = predict_batch(D[None], F)[0]
    X = attacked_matrices(D, byz, d_far)
    S = predict_batch(X, F)
    pad, or_all, order = membership(S)

    print(f"\n=== {label}: Byzantine {fmt(byz)}, far {fmt(far)} ===")
    print("true scores (ms):", " ".join(f"{i+1}:{s:.1f}" for i, s in enumerate(true_scores)))
    hp, ha, ho = membership(true_scores[None])
    print("honest reports -> pilot members", fmt(np.flatnonzero(hp[0])),
          "| or-all members", fmt(np.flatnonzero(ha[0])))

    for name, mask in (("pilot (pad by score)", pad), ("threshold-or-all", or_all)):
        lists = ordered_members(mask, order)
        sets = {frozenset(c) for c in lists}
        worst = min(sets, key=lambda c: (len(set(c) & set(honest_close)) / len(c), -len(c)))
        good = len(set(worst) & set(honest_close)) / len(worst)
        far_in = max(len(set(c) & set(far)) for c in sets)
        hc_min = min(len(set(c) & set(honest_close)) for c in sets)
        print(f"  {name}: {len(sets)} reachable member sets; min honest-close members {hc_min}; "
              f"max far members {far_in}; worst set {fmt(worst)} good share {good:.2f} "
              f"(round robin {len(honest_close)/N:.2f})")

        # First leader of each generation: can the adversary make it Byzantine?
        honest_list = ordered_members(hp if mask is pad else ha, ho)[0]
        period = math.lcm(*range(1, N + 1))
        ranks = {g: perm_rank(g) for g in range(1, PERM_G + 1)}
        orders = [
            ("score order", lambda c, g: c[g % len(c)], range(period), lists),
            ("id ring", first_leader_ring, range(1, N + 1), sets),
            ("id order", first_leader_idorder, range(1, period + 1), sets),
            ("seeded perm", lambda c, g: first_leader_perm(c, g, ranks), range(1, PERM_G + 1), sets),
        ]
        parts = []
        for oname, fn, gens, cands in orders:
            gens = list(gens)
            hit = sum(any(fn(c, g) in byz for c in cands) for g in gens) / len(gens)
            base = sum(fn(honest_list, g) in byz for g in gens) / len(gens)
            parts.append(f"{oname} {hit:.2f} (truthful {base:.2f})")
        print("    reachable sets:", " ".join(fmt(c) for c in sorted(sets, key=lambda c: (len(c), sorted(c)))))
        print("    first leader of a generation can be made Byzantine: " + "; ".join(parts))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--d-close", type=float, default=5)
    ap.add_argument("--d-far", type=float, default=50)
    ap.add_argument("--d-farfar", type=float, default=None,
                    help="far-far one-way delay (default: same as d-close, one far region)")
    ap.add_argument("--jitter", type=float, default=0.0, help="per-link +-fraction, honest truth")
    ap.add_argument("--seed", type=int, default=1)
    args = ap.parse_args()
    dff = args.d_close if args.d_farfar is None else args.d_farfar

    global CASES
    CASES = cases = [
        ((5, 6), (0, 1), "far 6,7; Byzantine close 1,2"),
        ((5, 6), (1, 3), "far 6,7; Byzantine close 2,4"),
        ((5, 6), (0, 5), "far 6,7; one Byzantine close, one far"),
        ((0, 3), (1, 2), "far 1,4; Byzantine close 2,3"),
        ((0, 3), (4, 6), "far 1,4; Byzantine close 5,7"),
    ]
    from concurrent.futures import ProcessPoolExecutor
    import contextlib, io

    def job(case):
        far, byz, label = case
        D = topology(far, args.d_close, args.d_far, dff, args.jitter, np.random.default_rng(args.seed))
        buf = io.StringIO()
        with contextlib.redirect_stdout(buf):
            analyse(D, byz, far, args.d_far, label)
        return buf.getvalue()

    global _JOB
    _JOB = job
    with ProcessPoolExecutor(len(cases)) as ex:
        for out in ex.map(_run, range(len(cases))):
            print(out, end="")


_JOB = None
CASES = None


def _run(i):
    return _JOB(CASES[i])


if __name__ == "__main__":
    main()
