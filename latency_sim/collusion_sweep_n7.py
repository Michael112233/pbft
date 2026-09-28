"""n=7, f=2: sweep the fake-link attack over delay sets (VALUES) and true 1-2 link delays.

Examples:
  python3 collusion_sweep_n7.py
  python3 collusion_sweep_n7.py --values 5,10,20,50 --values 5,50 --true12 5,20,50,max
  python3 collusion_sweep_n7.py --values 1,5,10,20,50,100 --true12 max --trials 20000 --ties
"""
import argparse
import random

from sim import argmin, predict_latency, random_matrix, with_link

N, F = 7, 2
DEFAULT_VALUES = ["10", "5,10", "5,50", "5,10,20,50", "1,5,10,20,50,100", "5,10,20,50,100,200,500"]
DEFAULT_TRUE12 = "50,max"


def parse_ints(s):
    return [int(x) for x in s.split(",") if x]


def run(values, true12, fake12, trials, seed, ties):
    rng = random.Random(seed)
    honest_best = active = hits = 0
    gain_sum = 0
    for _ in range(trials):
        D = with_link(random_matrix(N, values, rng), 1, 2, true12)
        L = with_link(D, 1, 2, fake12)
        true = [predict_latency(D, F, p) for p in range(N)]
        lie = [predict_latency(L, F, p) for p in range(N)]
        best_colluder = min(lie[:2])
        gain = min(true[:2]) - best_colluder
        hb = argmin(true) >= 2
        wins = best_colluder <= min(lie[2:]) if ties else best_colluder < min(lie[2:])
        honest_best += hb
        if gain > 0:
            active += 1
            gain_sum += gain
        hits += hb and wins
    return {
        "honest_best": honest_best / trials,
        "active": active / trials,
        "avg_gain": gain_sum / active if active else 0.0,
        "hit": hits / trials,
    }


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--values", action="append",
                    help="comma-separated delay set in ms for ordinary links; repeat for several sets")
    ap.add_argument("--true12", default=DEFAULT_TRUE12,
                    help="comma-separated true 1-2 link delays in ms; 'max' means max(values) (default: %(default)s)")
    ap.add_argument("--fake12", type=int, default=0, help="delay the colluders claim, ms (default: %(default)s)")
    ap.add_argument("--trials", type=int, default=6000, help="topologies per cell (default: %(default)s)")
    ap.add_argument("--seed", type=int, default=1, help="RNG seed, same for every cell (default: %(default)s)")
    ap.add_argument("--ties", action="store_true", help="count ties with the best honest node as colluder wins")
    args = ap.parse_args()

    value_sets = [parse_ints(v) for v in (args.values or DEFAULT_VALUES)]
    true12_specs = [t for t in args.true12.split(",") if t]

    print(f"n={N} f={F} fake12={args.fake12} trials={args.trials} seed={args.seed} "
          f"win={'<= (ties count)' if args.ties else '< (strict)'}")
    header = f"{'values':32s} {'true12':>6s} {'honest best':>12s} {'lie active':>11s} {'avg gain':>9s} {'HIT':>7s}"
    print(header)
    print("-" * len(header))
    for values in value_sets:
        true12s = [max(values) if spec == "max" else int(spec) for spec in true12_specs]
        for true12 in dict.fromkeys(true12s):
            r = run(values, true12, args.fake12, args.trials, args.seed, args.ties)
            print(f"{','.join(map(str, values)):32s} {true12:6d} {r['honest_best']:12.1%} "
                  f"{r['active']:11.1%} {r['avg_gain']:6.1f} ms {r['hit']:7.1%}")
        print()


if __name__ == "__main__":
    main()
