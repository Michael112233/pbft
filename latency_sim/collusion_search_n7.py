"""n=7, f=2: random search for topologies where nodes 1 and 2 (truly 50 ms apart)
claiming a 0 ms link makes one of them the strict argmin while an honest node is truly best."""
import random

from sim import argmin, predict_latency, print_matrix, random_matrix, with_link

N, F = 7, 2
VALUES = [5, 10, 20, 50]
TRIALS = 60000
random.seed(1)

hits = []
for _ in range(TRIALS):
    D = with_link(random_matrix(N, VALUES), 1, 2, 50)
    L = with_link(D, 1, 2, 0)
    true = [predict_latency(D, F, p) for p in range(N)]
    lie = [predict_latency(L, F, p) for p in range(N)]
    lp = argmin(lie)
    # not counting tie as byz win
    #aware see lie matrix and in that margin diff in pred latency of lie node over honest best node
    # extra latency is lie node real latency - best latency from ture matrix
    if argmin(true) >= 2 and lp in (0, 1) and lie[lp] < min(lie[2:]):
        hits.append((min(lie[2:]) - lie[lp], true[lp] - min(true), D, true, lie))

print(f"trials={TRIALS}  promotions={len(hits)} ({len(hits) / TRIALS:.1%})")
hits.sort(key=lambda h: (-h[0], -h[1]))
#  sorted hit in margin first then real extra latency
# aware only reconfigure if it seees big margin so that is why margin imp
#  switch zero and to sort on extra latency
for margin, real_cost, D, true, lie in hits[:2]:
    print(f"\npredicted margin over best honest: {margin} ms, real extra latency: {real_cost} ms")
    print("real latency     ", true)
    print("predicted w/ lie ", lie)
    print_matrix(D)
