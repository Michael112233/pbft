"""n=4, f=1: what a single Byzantine node (node 1) can do to Aware (max-sanitize), the
triangle clamp and the robust clamp, by exhaustive search over its reports.

With one liar every link keeps an honest endpoint, so after max(M[i,j], M[j,i]) the liar
can only report its own three links at or above the truth. The search tries every
combination of {true, x2, x5, 1000 ms} on those links.

Also measures the clamp with honest reports on topologies that violate the triangle
inequality (random delays), where it can raise honest links by itself.

  python3 single_liar_n4.py
"""
import itertools
import random

from sim import geometric_matrix, matrix_from_links, predict_latency, random_matrix, triangle_clamp

N, F = 4, 1
LIAR = 0
LEVELS = ("true", 2, 5, 1000)
DEFENSES = {
    "aware": lambda M: M,
    "clamp": triangle_clamp,
    "robust clamp": lambda M: triangle_clamp(M, ignore_top=F),
}


def scores(M):
    return [predict_latency(M, F, p) for p in range(N)]


def reports(D):
    for combo in itertools.product(LEVELS, repeat=N - 1):
        M = [row[:] for row in D]
        for c, lv in zip(range(1, N), combo):
            if lv != "true":
                M[LIAR][c] = M[c][LIAR] = 1000 if lv == 1000 else D[LIAR][c] * lv
        yield M


def evaluate(D):
    true = scores(D)
    best = min(true)
    out = {}
    for name, fn in DEFENSES.items():
        honest = scores(fn(D))
        pick = min(range(N), key=lambda p: (honest[p], p))
        captured, worst_regret = False, true[pick] - best
        for M in reports(D):
            s = scores(fn(M))
            if min(true[1:]) < true[LIAR] and s[LIAR] < min(s[1:]):
                captured = True
            p = min(range(N), key=lambda q: (s[q], q))
            worst_regret = max(worst_regret, true[p] - best)
        out[name] = (true[pick] - best, captured, worst_regret)
    return out, min(true[1:]) < true[LIAR]


def run(label, mats):
    rows = [evaluate(D) for D in mats]
    elig = [r for r, e in rows if e]
    print(f"\n{label}: {len(mats)} topologies ({len(elig)} where an honest node is strictly best)")
    print(f"  {'defence':14s}{'honest-report regret':>22s}{'liar can get chosen':>21s}{'worst regret under attack':>27s}")
    for d in DEFENSES:
        hr = sum(r[d][0] for r, _ in rows) / len(rows)
        cap = sum(r[d][1] for r in elig) / max(len(elig), 1)
        wr = sum(r[d][2] for r, _ in rows) / len(rows)
        print(f"  {d:14s}{hr:19.1f} ms{cap:21.1%}{wr:24.1f} ms")


def main():
    random.seed(1)
    run("geometric (triangle inequality holds)", [geometric_matrix(N) for _ in range(2000)])
    random.seed(1)
    run("random delays from {5,10,20,50} (triangle inequality often violated)",
        [random_matrix(N, [5, 10, 20, 50]) for _ in range(2000)])

    far = {(1, 2): 35, (1, 3): 35, (1, 4): 35, (2, 3): 1, (2, 4): 1, (3, 4): 1}
    D = matrix_from_links(N, far)
    print("\nyour netem topology (node 1 far, 35 ms), liar = node 1:")
    for d, (hr, cap, wr) in evaluate(D)[0].items():
        print(f"  {d:14s} honest regret {hr:.0f} ms, liar can get chosen: {cap}, worst regret {wr:.0f} ms")
    near = {(1, 2): 1, (1, 3): 1, (1, 4): 35, (2, 3): 1, (2, 4): 35, (3, 4): 35}
    D = matrix_from_links(N, near)
    print("same shape, node 4 far, liar = node 1 (a near node):")
    for d, (hr, cap, wr) in evaluate(D)[0].items():
        print(f"  {d:14s} honest regret {hr:.0f} ms, liar can get chosen: {cap}, worst regret {wr:.0f} ms")


if __name__ == "__main__":
    main()
