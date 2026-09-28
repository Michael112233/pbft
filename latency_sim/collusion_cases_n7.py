"""n=7, f=2, nodes 1 and 2 Byzantine: hand-built cases for the fake 0 ms link between
the colluders, plus inflation of their links to honest nodes."""
from sim import matrix_from_links, predict_latency, with_link

N, F = 7, 2


def show(name, D):
    print(f"{name:48s}", [predict_latency(D, F, p) for p in range(N)])


uniform = matrix_from_links(N, {}, default=10)
show("A uniform 10 ms, honest", uniform)
show("B uniform, 1-2 claim 0", with_link(uniform, 1, 2, 0))

better3 = with_link(with_link(uniform, 3, 4, 5), 3, 5, 5)
show("C node 3 genuinely better (5 ms to 4, 5)", better3)
lie = with_link(better3, 1, 2, 0)
show("D same + 1-2 claim 0", lie)

inflated = lie
for b in (1, 2):
    for c in (3, 4, 5):
        inflated = with_link(inflated, b, c, 500)
show("E D + 1, 2 inflate links to 3, 4, 5", inflated)
