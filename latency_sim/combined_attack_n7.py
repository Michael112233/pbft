"""n=7, f=2: optimized collusion attack (fake 1-2 link + inflating colluders' links to honest
nodes) against several latency-matrix defences, on geometric topologies.

The colluders (nodes 1, 2) choose what to report: the 1-2 link (any value, both ends lie) and
each of their links to honest nodes (any value >= the true one; sanitization blocks lower).
A hill-climber with random restarts searches those reports to make a colluder the strict
argmin of predicted latency. The attacker is heuristic, so success rates are lower bounds.

Defences:
  plain           reported matrix as is (AWARE's max-sanitized matrix)
  clamp           triangle_clamp
  robust          triangle_clamp(ignore_top=f)
  vivaldi         Vivaldi embedding, no filtering
  median-trim     Vivaldi, drop the f forces farthest from the median force, clip to median norm
  median-outlier  Vivaldi, drop up to f forces only if they are outliers, no clipping

Examples:
  python3 combined_attack_n7.py --defenses plain,clamp,robust --trials 1000
  python3 combined_attack_n7.py --trials 200 --restarts 3
"""
import argparse
import os
import random
import zlib
from multiprocessing import Pool

from sim import geometric_matrix, predict_latency, triangle_clamp, vivaldi_embed_batch

N, F = 7, 2
COLLUDERS = (0, 1)
HONEST = range(2, N)
BIG = 1000
FAKE_FRACS = (0, 0.25, 0.5, 0.75, 1.0)

MOVE_SETS = {
    "honest (no lie)": ((1.0,), ()),
    "fake link only": (FAKE_FRACS, ()),
    "fake + inflate": (FAKE_FRACS, ("big",)),
    "fake + equalize": (FAKE_FRACS, ("mid", "equal")),
    "fake + inflate + equalize": (FAKE_FRACS, ("mid", "equal", "big")),
}


def per_matrix(fn):
    return lambda Ms: [fn(M) for M in Ms]


DEFENSES = {
    "plain": lambda Ms: Ms,
    "clamp": per_matrix(triangle_clamp),
    "robust": per_matrix(lambda M: triangle_clamp(M, ignore_top=F)),
    "vivaldi": lambda Ms: vivaldi_embed_batch(Ms, f=F, trim="none"),
    "median-trim": lambda Ms: vivaldi_embed_batch(Ms, f=F, trim="fixed", clip=True),
    "median-outlier": lambda Ms: vivaldi_embed_batch(Ms, f=F, trim="outlier"),
}


def scores(M):
    return [predict_latency(M, F, p) for p in range(N)]


def margins(Ms, defense):
    out = []
    for P in DEFENSES[defense](Ms):
        s = scores(P)
        out.append(min(s[2:]) - min(s[:2]))
    return out


def report_levels(D, b, c, kinds):
    true = D[b][c]
    top = max(D[0][c], D[1][c])
    vals = {true}
    for k in kinds:
        if k == "mid":
            vals.add((true + top) // 2)
        elif k == "equal":
            vals.add(top)
        elif k == "big":
            vals.add(BIG)
    return sorted(vals)


def build(D, fake, rep):
    M = [row[:] for row in D]
    M[0][1] = M[1][0] = fake
    for (b, c), v in rep.items():
        M[b][c] = M[c][b] = v
    return M


def attack(D, fracs, kinds, defense, rng, restarts):
    fakes = sorted({round(D[0][1] * fr) for fr in fracs})
    coords = {(b, c): report_levels(D, b, c, kinds) for b in COLLUDERS for c in HONEST}
    coords = {k: v for k, v in coords.items() if len(v) > 1}
    if len(fakes) == 1 and not coords:
        restarts = 1
    best = None
    for r in range(restarts):
        if r == 0:
            fake, rep = fakes[0], {k: v[0] for k, v in coords.items()}
        else:
            fake, rep = rng.choice(fakes), {k: rng.choice(v) for k, v in coords.items()}
        cur = margins([build(D, fake, rep)], defense)[0]
        while cur <= 0:
            moves = [("fake", f) for f in fakes if f != fake]
            moves += [(k, v) for k, vals in coords.items() for v in vals if v != rep[k]]
            if not moves:
                break
            cands = [build(D, v, rep) if k == "fake" else build(D, fake, {**rep, k: v}) for k, v in moves]
            ms = margins(cands, defense)
            i = max(range(len(ms)), key=lambda j: ms[j])
            if ms[i] <= cur:
                break
            cur = ms[i]
            k, v = moves[i]
            if k == "fake":
                fake = v
            else:
                rep[k] = v
        if best is None or cur > best[0]:
            best = (cur, fake, dict(rep))
        if cur > 0:
            break
    return best


def one_topology(args):
    idx, seed, restarts, defenses, moves = args
    D = geometric_matrix(N, rng=random.Random(seed * 1_000_003 + idx))
    true = scores(D)
    if not min(true[2:]) < min(true[:2]):
        return None
    out = {}
    for mname in moves:
        fracs, kinds = MOVE_SETS[mname]
        for dname in defenses:
            rng = random.Random(zlib.crc32(f"{seed}-{idx}-{mname}-{dname}".encode()))
            m, fake, rep = attack(D, fracs, kinds, dname, rng, restarts)
            if m > 0:
                s = scores(DEFENSES[dname]([build(D, fake, rep)])[0])
                winner = min(COLLUDERS, key=lambda b: s[b])
                inflated = sum(1 for (b, c), v in rep.items() if v > D[b][c])
                out[(mname, dname)] = (true[winner] - min(true), fake, inflated)
            else:
                out[(mname, dname)] = None
    return out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--trials", type=int, default=400, help="geometric topologies (default: %(default)s)")
    ap.add_argument("--restarts", type=int, default=4, help="hill-climb restarts per attack (default: %(default)s)")
    ap.add_argument("--seed", type=int, default=1, help="(default: %(default)s)")
    ap.add_argument("--workers", type=int, default=os.cpu_count(), help="(default: all cores)")
    ap.add_argument("--defenses", default=",".join(DEFENSES), help="comma list (default: all)")
    ap.add_argument("--moves", default=",".join(MOVE_SETS), help="comma list of move sets (default: all)")
    args = ap.parse_args()
    defenses = [d for d in args.defenses.split(",") if d]
    moves = [m for m in args.moves.split(",") if m]

    jobs = [(i, args.seed, args.restarts, defenses, moves) for i in range(args.trials)]
    with Pool(args.workers) as pool:
        results = [r for r in pool.imap_unordered(one_topology, jobs) if r is not None]

    eligible = len(results)
    print(f"n={N} f={F} geometric topologies={args.trials}, honest strictly best in reality: {eligible}")
    print(f"restarts={args.restarts} seed={args.seed}  (heuristic attacker: rates are lower bounds)\n")
    print("attack success rate (colluder strictly best in Aware's prediction):")
    header = f"{'attacker moves':28s}" + "".join(f"{d:>16s}" for d in defenses)
    print(header)
    print("-" * len(header))
    for mname in moves:
        row = "".join(f"{sum(r[(mname, d)] is not None for r in results) / eligible:16.1%}" for d in defenses)
        print(f"{mname:28s}{row}")

    print("\nwhen the attack succeeds: mean real extra latency of the colluder leader / mean links inflated")
    print(header)
    print("-" * len(header))
    for mname in moves:
        cells = []
        for d in defenses:
            wins = [r[(mname, d)] for r in results if r[(mname, d)] is not None]
            cells.append(f"{sum(w[0] for w in wins) / len(wins):4.1f}ms/{sum(w[2] for w in wins) / len(wins):3.1f}"
                         if wins else "-")
        print(f"{mname:28s}" + "".join(f"{c:>16s}" for c in cells))


if __name__ == "__main__":
    main()
