"""n=7, f=2: Beware's actual clustering sanitizer (beware_vcs.py) vs Aware's max-sanitization
and the triangle clamp, under Beware's own attacks and ours, on geometric topologies.

Reports are a raw matrix R: R[i][j] is what node i reports for link i-j. Honest rows are
true; the colluders (nodes 1, 2) choose their own rows. Aware and the clamp see
max(R[i][j], R[j][i]); Beware sees R itself, as in the original code. Each topology is
attacked with a fixed set of attacks (Beware is ~18 s per embedding at 1000 rounds, too slow
for a search); "best of all" means the colluders pick whichever attack works on that
topology.

Examples:
  python3 beware_attack_n7.py --trials 120
  python3 beware_attack_n7.py --trials 20 --rounds 200      # quick look
"""
import argparse
import os
import random
from multiprocessing import Pool

from beware_vcs import beware_sanitize
from sim import geometric_matrix, predict_latency, triangle_clamp

N, F = 7, 2
B1, B2 = 0, 1
HONEST = range(2, N)


def scaled(R, b, cols, k):
    for c in cols:
        R[b][c] = R[b][c] * k


def attack(D, name):
    R = [row[:] for row in D]
    if name == "honest":
        pass
    elif name == "beware: deflation (rows /3)":
        for b in (B1, B2):
            scaled(R, b, range(N), 1 / 3)
    elif name == "beware: inflation (rows x3)":
        for b in (B1, B2):
            scaled(R, b, range(N), 3)
    elif name == "beware: inflation-deflation":
        for b in (B1, B2):
            scaled(R, b, (B1, B2), 1 / 3)
            scaled(R, b, HONEST, 3)
    elif name == "fake 1-2 link = 0":
        R[B1][B2] = R[B2][B1] = 0
    else:
        leader, helper = (B1, B2) if name.endswith("[1 leads]") else (B2, B1)
        base = name.rsplit(" [", 1)[0]
        if base.startswith("fake +"):
            R[B1][B2] = R[B2][B1] = 0
        if "inflate x10" in base:
            scaled(R, helper, HONEST, 10)
        elif "inflate" in base:
            scaled(R, helper, HONEST, 3)
        if "leader deflates" in base:
            scaled(R, leader, HONEST, 1 / 3)
    return R


SPLIT = ["helper inflates x3", "fake + helper inflates x3", "fake + helper inflate x10",
         "fake + helper inflates x3 + leader deflates"]
ATTACKS = ["honest", "beware: deflation (rows /3)", "beware: inflation (rows x3)",
           "beware: inflation-deflation", "fake 1-2 link = 0"] + \
          [f"{s} [{who}]" for s in SPLIT for who in ("1 leads", "2 leads")]
DEFENSES = ["aware", "clamp", "beware"]


def max_sanitize(R):
    return [[max(R[i][j], R[j][i]) for j in range(N)] for i in range(N)]


def scores(M):
    return [predict_latency(M, F, p) for p in range(N)]


def job(args):
    idx, name, seed, rounds = args
    D = geometric_matrix(N, rng=random.Random(seed * 1_000_003 + idx))
    true = scores(D)
    if not min(true[2:]) < min(true[:2]):
        return None
    R = attack(D, name)
    S = max_sanitize(R)
    views = {
        "aware": S,
        "clamp": triangle_clamp(S),
        "beware": beware_sanitize(R, F, rounds=rounds, seed=seed * 1_000_003 + idx),
    }
    out = {}
    for d, M in views.items():
        s = scores(M)
        pick = min(range(N), key=lambda p: (s[p], p))
        out[d] = (min(s[:2]) < min(s[2:]), true[pick] - min(true))
    return idx, name, out


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--trials", type=int, default=120, help="geometric topologies (default: %(default)s)")
    ap.add_argument("--rounds", type=int, default=1000, help="Beware VCS rounds, paper uses 1000 (default: %(default)s)")
    ap.add_argument("--seed", type=int, default=1, help="(default: %(default)s)")
    ap.add_argument("--workers", type=int, default=os.cpu_count(), help="(default: all cores)")
    args = ap.parse_args()

    jobs = [(i, a, args.seed, args.rounds) for i in range(args.trials) for a in ATTACKS]
    res = {}
    with Pool(args.workers) as pool:
        for r in pool.imap_unordered(job, jobs, chunksize=1):
            if r is not None:
                idx, name, out = r
                res.setdefault(idx, {})[name] = out
    topo = sorted(res)
    E = len(topo)
    print(f"n={N} f={F} geometric topologies={args.trials}, honest strictly best in reality: {E}, "
          f"Beware rounds={args.rounds}\n")
    print("Byzantine node strictly best leader  |  mean extra real latency of the chosen leader (ms)")
    head = f"{'attack':52s}" + "".join(f"{d:>9s}" for d in DEFENSES) + "   |" + "".join(f"{d:>9s}" for d in DEFENSES)
    print(head)
    print("-" * len(head))
    for a in ATTACKS:
        rate = "".join(f"{sum(res[t][a][d][0] for t in topo) / E:9.1%}" for d in DEFENSES)
        cost = "".join(f"{sum(res[t][a][d][1] for t in topo) / E:9.1f}" for d in DEFENSES)
        print(f"{a:52s}{rate}   |{cost}")
    attacks_only = [a for a in ATTACKS if a != "honest"]
    rate = "".join(f"{sum(any(res[t][a][d][0] for a in attacks_only) for t in topo) / E:9.1%}" for d in DEFENSES)
    cost = "".join(f"{sum(max(res[t][a][d][1] for a in attacks_only) for t in topo) / E:9.1f}" for d in DEFENSES)
    print("-" * len(head))
    print(f"{'best of all attacks (per topology)':52s}{rate}   |{cost}")


if __name__ == "__main__":
    main()
