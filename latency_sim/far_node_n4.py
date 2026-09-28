"""n=4: one far node (node 1, 35 ms to all, others 1 ms), with and without a Byzantine
node inflating its links."""
from sim import matrix_from_links, predict_latency

N, F = 4, 1
BASE = {(1, 2): 35, (1, 3): 35, (1, 4): 35, (2, 3): 1, (2, 4): 1, (3, 4): 1}

CASES = [
    ("honest", BASE),
    ("node 3 inflates 3-2 to 500", {**BASE, (2, 3): 500}),
    ("node 3 inflates 3-2 and 3-4 to 500", {**BASE, (2, 3): 500, (3, 4): 500}),
]

for name, links in CASES:
    D = matrix_from_links(N, links)
    lat = {p + 1: predict_latency(D, F, p) for p in range(N)}
    print(f"{name:38s} predicted leader latency (ms): {lat}")
