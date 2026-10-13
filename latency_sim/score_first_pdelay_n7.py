"""Reproduce score-first AWARE capture with n=7, f=2.

Offline analysis of the proposed rule, NOT the current Go leader schedule.
All internal times are integer microseconds to avoid floating-point tie artifacts.
Reports are RTTs; the sanitized matrix is max(R_ij, R_ji)/2, as in Go.
Only nodes 1 and the target may change reports. Proposal delay is deliberately
absent from the predictor, just as it is absent from the separate RTT probes.

Run: python3 latency_sim/score_first_pdelay_n7.py
"""

from sim import predict_latency

N, F = 7, 2


def scores(matrix):
    return [predict_latency(matrix, F, p) for p in range(N)]


def ordered_members(values):
    best = min(values)
    threshold = best + max(best / 10, 1000)  # alpha=.1, epsilon=1 ms
    members = [p for p, value in enumerate(values) if value <= threshold]
    if len(members) < 2 * F + 1:
        members = list(range(N))
    return sorted(members, key=lambda p: (values[p], p))


def sanitize(reports):
    return [
        [0 if i == j else (max(reports[i][j], reports[j][i]) / 2
                          if max(reports[i][j], reports[j][i]) > 0
                          else float("inf")) for j in range(N)]
        for i in range(N)
    ]


def show(name, truth, changes, target, expected, expected_members):
    # changes: (reporter ID, peer ID, reported RTT in microseconds).
    reports = [[2 * value for value in row] for row in truth]
    for reporter, peer, rtt in changes:
        assert reporter in (1, target)
        reports[reporter - 1][peer - 1] = rtt
    matrix = sanitize(reports)
    actual = scores(matrix)
    assert actual == expected, (name, actual)
    members = ordered_members(actual)
    assert [p + 1 for p in members] == expected_members
    assert members[0] + 1 == target
    # No generation offset: the same first member starts every generation.
    for generation in range(2, 12):
        counter = 1
        assert members[(counter - 1) % len(members)] + 1 == target
    print(name)
    print("  truthful scores (ms):", [v / 1000 for v in scores(truth)])
    print("  reported scores (ms):", [v / 1000 for v in actual])
    print("  ordered membership:", [p + 1 for p in members])
    print("  leader at c=1:", target)
    print("  strict margin (ms):", (min(v for p, v in enumerate(actual)
                                          if p != target - 1) - actual[target - 1]) / 1000)


def main():
    # Node 1 is physically far. Nodes 1 and 3 have pdelay when enabled.
    # This true matrix obeys the triangle inequality; near links are 8--12 ms.
    wan_ms = [
        [0, 100, 100, 100, 100, 100, 100],
        [100, 0, 9, 12, 8, 8, 10],
        [100, 9, 0, 9, 8, 12, 9],
        [100, 12, 9, 0, 9, 12, 11],
        [100, 8, 8, 9, 0, 10, 11],
        [100, 8, 12, 12, 10, 0, 10],
        [100, 10, 9, 11, 11, 10, 0],
    ]
    wan = [[1000 * v for v in row] for row in wan_ms]
    # Only node 3 lies: its RTT to node 6 is really 24 ms, reported as 28 ms.
    # Node 6's truthful 24 ms cannot defeat max(28, 24).
    show("WAN: one far node, one inflated report", wan, [(3, 6, 28000)], 3,
         [210000, 31000, 30000, 33000, 31000, 33000, 33000],
         [3, 2, 5, 4, 6, 7])

    two_far = [[0 if i == j else (100000 if i < 2 or j < 2 else 10000)
                for j in range(N)] for i in range(N)]
    show("WAN: two far nodes, truthful reports (node 3 wins ID tie)", two_far,
         [], 3, [210000, 210000, 30000, 30000, 30000, 30000, 30000],
         [3, 4, 5, 6, 7])
    # Both colluders claim a 0.2 ms RTT on 1--3 (true 200 ms).
    # Node 3 inflates its RTT to node 7 from 20 to 200 ms.
    # Strict capture succeeds, but only four nodes meet the threshold:
    # threshold-or-all therefore restores ALL nodes, including the far nodes.
    show("WAN: two far nodes, strict capture with all-node fallback", two_far,
         [(1, 3, 200), (3, 1, 200), (3, 7, 200000)], 3,
         [210000, 210000, 110100, 120000, 120000, 120000, 200000],
         [3, 4, 5, 6, 7, 1, 2])

    lan = [
        [0, 80, 110, 120, 90, 110, 110],
        [80, 0, 110, 80, 90, 110, 120],
        [110, 110, 0, 80, 120, 90, 110],
        [120, 80, 80, 0, 100, 80, 100],
        [90, 90, 120, 100, 0, 100, 100],
        [110, 110, 90, 80, 100, 0, 80],
        [110, 120, 110, 100, 100, 80, 0],
    ]
    # A same-rack case: both endpoints claim a positive 2 us RTT instead of
    # 220 us. Literal zero would become infinity in the Go sanitizer.
    show("LAN: shared-link deflation, all nodes remain members", lan,
         [(1, 3, 2), (3, 1, 2)], 3,
         [300, 300, 290, 300, 300, 300, 300], [3, 1, 2, 4, 5, 6, 7])

    uniform = [[0 if i == j else 100 for j in range(N)] for i in range(N)]
    # With exactly equal honest links, the target cannot strictly beat the
    # five-honest-node 3d quorum. Move the target to node 2 and win the ID tie:
    # node 1 sacrifices its score by overstating all its honest links.
    changes = [(1, h, 400) for h in range(3, 8)] + [(1, 2, 2), (2, 1, 2)]
    show("Exactly uniform LAN: node 2 wins by ID tie, not strict score", uniform,
         changes, 2, [500, 300, 300, 300, 300, 300, 300], [2, 3, 4, 5, 6, 7, 1])


if __name__ == "__main__":
    main()
