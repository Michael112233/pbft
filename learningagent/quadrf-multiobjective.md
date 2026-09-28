# QuadRF Multi-Objective

`QuadRFMultiObjective` (`server.py`) is QuadRF with two objectives. Like
QuadRF, it keeps one experience bucket for each (previous action, candidate
action) pair. Each record stores the state together with two targets:

$$
y = [\text{throughput}, \text{latency}]
$$

On every `predict`, each tried candidate gets one Thompson sample
$(\tilde T_a, \tilde L_a)$ from `sample()`. `sample()` does four things:

1. It draws a bootstrap resample of the bucket's replay window.
2. It scales both targets.
3. It fits a new two-output `RandomForestRegressor`.
4. It predicts at the current state.

The agent then chooses:

$$
\text{feasible} = \{a : \tilde T_a \ge (1-\delta)\max_b \tilde T_b\}, \qquad
\text{choice} = \arg\min_{a \in \text{feasible}} \tilde L_a
$$

Pairs that have never been tried are chosen first, as in QuadRF.

This document explains two details of `sample()`: why the targets are
scaled, and why a new random seed on every fit still keeps all nodes in
agreement.

## Why the targets are scaled

```python
scale = by.std(axis=0)          # by is (n, 2), so scale is (2,): [σ_throughput, σ_latency]
scale[scale == 0] = 1.0         # a constant column (n=1, or all rows duplicates) would divide by 0
model.fit(bX, by / scale)       # column 0 is divided by σ_T, column 1 by σ_L
...
model.predict(x)[0] * scale     # predict gives (1, 2); [0] gives (2,); multiply to get tx/s and ms back
```

A multi-output forest uses the `squared_error` criterion. It picks each split
by how much the split reduces each output's variance, averaged over the
outputs. Variance scales with the square of the units. Throughput is in tx/s
(thousands) and latency is in ms (tens), so throughput's variance is millions
while latency's is hundreds. Without scaling, throughput decides almost every
split. The trees spend their depth (`max_depth=5`) on small throughput
differences and learn little about latency. Latency is the value the
selection step minimises, so that would hurt the choice.

Dividing each output by its standard deviation gives both outputs variance 1.
A split then counts by the *fraction* of each output's variance it explains,
not by that output's units.

Mean-centering is not needed. Adding a constant to an output does not change
its variance, so the splits stay the same. Undoing the scale after predicting
is exact. A leaf predicts the mean of its training targets, and the forest
averages its trees' predictions. Both steps are linear, so
$\text{mean}(y/\sigma)\cdot\sigma = \text{mean}(y)$.

### Worked example

Take a bootstrap sample of four rows with two state features. `x1` separates
throughput and `x2` separates latency:

| Row | `x1` | `x2` | Throughput | Latency |
| ---: | ---: | ---: | ---: | ---: |
| 1 | 0 | 0 | 9000 | 20 |
| 2 | 0 | 1 | 9100 | 60 |
| 3 | 1 | 0 | 5000 | 22 |
| 4 | 1 | 1 | 5100 | 58 |

The variances before any split are 4,002,500 for throughput and 362 for
latency.

**Unscaled.** The table compares the two possible root splits by the
variance left after each split:

| Split | Throughput variance | Latency variance | Total gain |
| --- | --- | --- | ---: |
| on `x1` | 4,002,500 → 2,500 | 362 → 362 | ≈ 4,000,000 |
| on `x2` | 4,002,500 → 4,000,000 | 362 → 1 | ≈ 2,861 |

The split on `x1` wins by about 1400×. The same imbalance repeats at the next
level down. Inside the `{1, 2}` node, a 100 tx/s throughput gap (variance
2,500) outweighs a 40 ms latency gap (variance 400).

**Scaled.** Here $\sigma_T \approx 2000.6$ and $\sigma_L \approx 19.03$, so
both outputs start with variance 1:

| Split | Throughput variance | Latency variance | Total gain |
| --- | --- | --- | ---: |
| on `x1` | 1 → 0.0006 | 1 → 1 | ≈ 0.999 |
| on `x2` | 1 → 0.9994 | 1 → 0.003 | ≈ 0.998 |

The two splits now score about the same, because each one explains almost all
of one output. The forest uses both splits, and the latency prediction becomes
useful.

`scale` is computed on each bootstrap resample, so it varies slightly from one
call to the next. The variation only changes how splits are weighted. The
prediction is multiplied back by the same `scale`, so the output units are
always correct.

## Seeding: why all nodes fit the same models

Each node runs its own agent process. The nodes must all reach the same
decision for each generation. `sample()` gives every forest a new seed:

```python
random_state=int(self.rng.integers(2**31 - 1))
```

The call returns **one** integer in $[0, 2^{31}-1)$. The upper bound keeps
the value in the 32-bit range that sklearn accepts for `random_state`. The
`int()` call converts numpy's `np.int64` to a plain Python `int`.

The seed is not random across nodes. It is drawn from `self.rng`, which is
`np.random.default_rng(seed)`. Each draw moves the generator forward, so
successive fits get different seeds. With the same starting seed, the sequence
of seeds is identical in every process:

```python
>>> rng = np.random.default_rng(5)
>>> [int(rng.integers(2**31 - 1)) for _ in range(3)]
[1440510675, 1728730614, 48647418]      # the same list on every node
```

### Draws from `self.rng`, in order

A `predict` call draws from `self.rng` in a fixed order:

| Step | Draw |
| --- | --- |
| An untried pair exists | `integers(len(untried))` picks it, and `predict` returns |
| For each tried candidate, in `self.actions` order | `choice(n, n, replace=True)` for the bootstrap indices, then `integers(2**31 - 1)` for the forest seed |
| After sampling | `integers(len(tied))` breaks latency ties |

All the nodes start from the same seed and make the same calls with the same
data, so they draw the same numbers at every step. As a result they draw the
same bootstrap indices and the same forest seeds, fit the same forests, and
make the same choice.

Giving each fit a new seed from `self.rng` is equivalent to QuadRF's fixed
`random_state=seed` as far as node agreement goes. QuadRF also draws its
bootstrap indices and tie-breaks from `self.rng`, so the seeded generator is
what keeps its nodes in agreement too. A new seed per fit also makes each
Thompson sample's internal tree randomness independent of the previous fit.

### Conditions for agreement

The nodes stay in lockstep only while all of the following hold:

1. **Every node uses the same seed.** `QuadRFMultiObjective` defaults to
   `seed=None`, which gives a different stream on each node. Pass an explicit
   seed, as `QuadRF(seed=5)` does.
2. **Every node receives identical inputs in the same order.** This holds today
   because `generate_state` and `generate_reward` are fixed lookup tables.
   Once the real data replaces them, it holds only if every node uses the
   `EpochAggregateMsg` broadcast by node 4. If a node uses its own local
   measurements, it will diverge.
3. **Every node makes exactly the same sequence of `self.rng` draws.** A single
   extra or missing draw shifts the stream for good. In a test with two
   `seed=5` agents fed identical data, one extra `rng.random()` at step 5 made
   17 of the next 40 decisions differ. The same agents without the extra draw
   agreed on all 40. An agent restart, a skipped epoch, or a repeated `predict`
   on one node is enough to cause this. The risk applies equally to QuadRF.

To remove the third condition, derive each decision's randomness from values
the nodes already agree on, instead of from a running stream:

```python
rng = np.random.default_rng([self.seed, sequence_id, prev_idx, cand_idx])
```

Each decision would then depend only on the seed, `sequence_id`, and the data,
so a node that missed a step would still agree on the next decision. This is
not implemented.
