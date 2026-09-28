"""n=7, f=2: how often the fake 1-2 link promotes a Byzantine leader, under plain scoring,
remove-f-nodes scoring (random topologies) and the triangle clamp (geometric topologies)."""
import random

from sim import (geometric_matrix, predict_latency, random_matrix, remove_nodes_score,
                 triangle_clamp, with_link)

N, F = 7, 2
VALUES = [5, 10, 20, 50]
RANDOM_TRIALS = 3000
GEO_TRIALS = 2000


def byz_strictly_best(scores):
    return min(scores[:2]) < min(scores[2:])


def scores(D, fn):
    return [fn(D, F, p) for p in range(N)]


print("random topologies: plain vs remove-f-nodes scoring")
random.seed(1)
plain = robust = 0
drops = []
for _ in range(RANDOM_TRIALS):
    D = with_link(random_matrix(N, VALUES), 1, 2, 50)
    L = with_link(D, 1, 2, 0)
    true, lie = scores(D, predict_latency), scores(L, predict_latency)
    rob_true, rob_lie = scores(D, remove_nodes_score), scores(L, remove_nodes_score)
    plain += byz_strictly_best(lie) and not byz_strictly_best(true)
    robust += byz_strictly_best(rob_lie) and not byz_strictly_best(rob_true)
    drops += [rob_true[b] - rob_lie[b] for b in (0, 1)]
print(f"  lie promotes a Byzantine node: plain={plain / RANDOM_TRIALS:.1%}  "
      f"remove-f-nodes={robust / RANDOM_TRIALS:.1%}")
print(f"  lie lowers a Byzantine node's remove-f-nodes score: "
      f"mean={sum(drops) / len(drops):.1f} ms  max={max(drops)} ms")

print("\ngeometric topologies: plain vs triangle clamp")
random.seed(7)
plain = clamped = altered = 0
residual = []
for _ in range(GEO_TRIALS):
    D = geometric_matrix(N)
    L = with_link(D, 1, 2, 0)
    true = scores(D, predict_latency)
    lie = scores(L, predict_latency)
    cl = scores(triangle_clamp(L), predict_latency)
    plain += byz_strictly_best(lie) and not byz_strictly_best(true)
    clamped += byz_strictly_best(cl) and not byz_strictly_best(true)
    altered += triangle_clamp(D) != D
    residual.append(min(true[:2]) - min(cl[:2]))
print(f"  lie promotes a Byzantine node: plain={plain / GEO_TRIALS:.1%}  "
      f"clamped={clamped / GEO_TRIALS:.1%}")
print(f"  clamp changes an honest matrix (rounding): {altered / GEO_TRIALS:.1%}")
print(f"  residual predicted gain for the liars after clamp: max={max(residual)} ms")
