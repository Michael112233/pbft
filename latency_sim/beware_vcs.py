"""Faithful port of Beware's clustering VCS sanitizer (Chotkan et al., DSN 2026).

Source: references/beware/src/python/beware/vcs/newton/newton_model.py
  NewtonNode.observe_cluster, all_nodes_observe_cluster, stabilized_cluster_vcs,
  latency_matrix_from_vcs.
Differences: numpy vectors instead of EuclideanVector, and a seeded RNG for the random
start (the original uses the global `random`). Heights start at lat[i][i] = 0 and never
change there either, so distances are plain Euclidean.

Input is the raw reported matrix: lat[i][j] is what node i reports for link i-j. The force
on node i from node j uses lat[j][i], i.e. j's own report, exactly as the original does.
"""
import random
import statistics

import numpy as np
from sklearn.cluster import HDBSCAN

CLUSTER_ADAPTIVE_TIMESTEP = 0.25
CLUSTER_MIN_ERROR = 0.1
MIN_ERROR = 0.1


def _random_unit(rng, dim):
    v = np.array([rng.gauss(0, 1) for _ in range(dim)])
    return v / np.linalg.norm(v)


def _observe_cluster(i, n, f, X, E, lat, step, step_max, rng):
    dim = X.shape[1]
    w, es, forces = [], [], []
    for j in range(n):
        if j == i:
            continue
        rtt = lat[j][i]
        wj = E[i] / (E[i] + E[j])
        predict_rtt = float(np.linalg.norm(X[i] - X[j]))
        es.append(0.0 if rtt == 0 else abs(predict_rtt - rtt) / rtt)
        w.append(wj)
        timestep = CLUSTER_ADAPTIVE_TIMESTEP * wj * (step_max - 0.25 * step) / step_max
        magnitude = timestep * (rtt - predict_rtt)
        v = X[i] - X[j]
        unit = _random_unit(rng, dim) if not v.any() else v / predict_rtt
        forces.append(unit * magnitude)

    labels = HDBSCAN(
        metric="cosine",
        min_cluster_size=int((n - f) * (1 - 0.1)),
        min_samples=1,
        allow_single_cluster=True,
    ).fit(np.array(forces)).labels_

    count, biggest, biggest_count = {}, 0, 0
    for lab in labels:
        if lab == -1:
            continue
        count[lab] = count.get(lab, 0) + 1
        if count[lab] > biggest_count:
            biggest_count, biggest = count[lab], lab

    members = [k for k, lab in enumerate(labels) if lab == biggest]
    sum_e = sum(w[k] for k in members)
    new_error = 0.0 if sum_e == 0 else \
        CLUSTER_MIN_ERROR * (sum(w[k] * es[k] for k in members) / sum_e) + (1 - CLUSTER_MIN_ERROR) * E[i]
    new_error = max(new_error, MIN_ERROR)

    if not members:
        return
    median_mag = statistics.median(float(np.linalg.norm(forces[k])) for k in members)
    agg = np.zeros(dim)
    for k in members:
        mag = float(np.linalg.norm(forces[k]))
        agg += forces[k] / mag * median_mag if mag > median_mag else forces[k]
    X[i] = X[i] + agg / biggest_count
    E[i] = new_error


def beware_sanitize(lat, f, rounds=1000, dim=3, seed=0):
    """Returns Beware's sanitized (embedded) latency matrix for a reported matrix."""
    n = len(lat)
    rng = random.Random(seed)
    X = np.array([[rng.random() for _ in range(dim)] for _ in range(n)])
    E = np.full(n, MIN_ERROR)
    best, best_l2 = None, 100000
    for r in range(rounds):
        for i in range(n):
            _observe_cluster(i, n, f, X, E, lat, r, rounds, rng)
        est = np.linalg.norm(X[:, None, :] - X[None, :, :], axis=2).round(3)
        l2 = float(np.sqrt(((np.array(lat, dtype=float) - est) ** 2).sum()))
        if l2 < best_l2:
            best, best_l2 = est.copy(), l2
    return best.tolist()
