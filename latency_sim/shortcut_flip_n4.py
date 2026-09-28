"""n=4: exhaustive search for matrices where the shortcut score (2f-th smallest RTT)
and the full simulation pick different leaders; prints the phase trace of the best one."""
import itertools

from sim import argmin, predict_latency, predict_trace, print_matrix, shortcut_score

N, F = 4, 1
VALUES = [1, 5, 10, 20, 40, 70, 100]
PAIRS = list(itertools.combinations(range(N), 2))

hits = []
for combo in itertools.product(VALUES, repeat=len(PAIRS)):
    D = [[0] * N for _ in range(N)]
    for (i, j), v in zip(PAIRS, combo):
        D[i][j] = D[j][i] = v
    short = [shortcut_score(D, F, p) for p in range(N)]
    sim = [predict_latency(D, F, p) for p in range(N)]
    sp, mp = argmin(short), argmin(sim)
    strict = short[sp] < sorted(short)[1] and sim[mp] < sorted(sim)[1]
    if strict and sp != mp:
        hits.append((sim[sp] - sim[mp], combo, D))

print(f"matrices where the shortcut picks a different leader: {len(hits)}")
hits.sort(key=lambda h: -h[0])
cost, combo, D = hits[0]
print(f"\nworst flip: shortcut's pick is {cost} ms slower than the best leader")
print("one-way delays (ms), nodes 1..4:")
print_matrix(D)
for p in range(N):
    lat, pp, prep, commits = predict_trace(D, F, p)
    print(f"leader {p + 1}: shortcut={shortcut_score(D, F, p):4d}  simulated={lat:4d}  "
          f"preprepare={pp} prepared={prep} commit_arrivals_at_leader={commits}")
