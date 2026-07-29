# Epsilon-Greedy Reward Update

`EpsilonGreedyBandit.train()` maintains a throughput estimate for each
protocol using a constant-step update:

$$
Q_{\text{new}} = Q_{\text{old}} + \alpha(R - Q_{\text{old}})
$$

The same formula can be written as a weighted average:

$$
Q_{\text{new}} = (1-\alpha)Q_{\text{old}} + \alpha R
$$

Here:

- $Q_{\text{old}}$ is the arm's current estimated throughput.
- $R$ is the newly observed throughput reward.
- $R-Q_{\text{old}}$ is the prediction error.
- $\alpha$ is the learning rate.
- $Q_{\text{new}}$ is the updated estimated throughput.

The update moves the estimate toward the latest observed reward. The learning
rate determines how far it moves.

## Comparing learning rates

Suppose the current estimated throughput is 800 TPS and the new reward is
1,000 TPS. The prediction error is:

$$
1000 - 800 = 200
$$

The resulting estimate depends on `alpha`:

| Alpha | Old estimate weight | New reward weight | Calculation | New estimate |
| ---: | ---: | ---: | --- | ---: |
| `0.01` | 99% | 1% | $800 + 0.01(200)$ | 802 TPS |
| `0.10` | 90% | 10% | $800 + 0.10(200)$ | 820 TPS |
| `0.25` | 75% | 25% | $800 + 0.25(200)$ | 850 TPS |
| `0.50` | 50% | 50% | $800 + 0.50(200)$ | 900 TPS |
| `1.00` | 0% | 100% | $800 + 1.00(200)$ | 1,000 TPS |

### Small alpha

A small learning rate, such as `0.01`:

- Produces a stable throughput estimate.
- Reduces the effect of short throughput spikes.
- Adapts slowly when the workload or system behavior changes.

### Moderate alpha

A moderate learning rate, such as `0.1`:

- Retains information from previous rewards.
- Still responds to sustained changes in throughput.
- Is a reasonable starting point for experiments.

### Large alpha

A large learning rate, such as `0.5`:

- Reacts quickly to new throughput observations.
- Is more sensitive to measurement noise.
- May cause the selected arm to change more frequently.

With `alpha=1.0`, the estimate completely forgets its history and becomes the
most recently observed throughput.

## Exponential weighting

After rewards $R_1,\ldots,R_n$, the estimate is:

$$
Q_n = (1-\alpha)^n Q_0
      + \alpha\sum_{i=1}^{n}(1-\alpha)^{n-i}R_i
$$

Each older reward receives exponentially less weight. A useful approximation
for the effective number of remembered observations is:

$$
\text{effective memory} \approx \frac{1}{\alpha}
$$

For example:

| Alpha | Approximate effective memory |
| ---: | ---: |
| `0.01` | 100 rewards |
| `0.10` | 10 rewards |
| `0.25` | 4 rewards |
| `0.50` | 2 rewards |
| `1.00` | 1 reward |

This is an approximation rather than a hard cutoff. Older rewards continue to
contribute, but their weights become progressively smaller.

## Zero initialization

The current implementation initializes both arm values to zero. If
`alpha=0.1` and an arm repeatedly receives a reward of 1,000 TPS, its estimate
starts as follows:

| Observation | Calculation | Estimate |
| ---: | --- | ---: |
| 1 | $0 + 0.1(1000-0)$ | 100 TPS |
| 2 | $100 + 0.1(1000-100)$ | 190 TPS |
| 3 | $190 + 0.1(1000-190)$ | 271 TPS |
| 4 | $271 + 0.1(1000-271)$ | 343.9 TPS |

The estimate approaches 1,000 TPS over time. This initial rise is caused by
starting at zero, not by low observed throughput.

If the first observation should immediately initialize an arm, `train()` can
instead use:

```python
if self.reward_counts[protocol] == 0:
    self.values[protocol] = reward
else:
    current_value = self.values[protocol]
    self.values[protocol] = current_value + self.alpha * (
        reward - current_value
    )

self.reward_counts[protocol] += 1
```

This alternative removes the initial bias toward zero while retaining the
constant-step update for later rewards. It is not the behavior of the current
implementation.

## Reward counts

The current constant-alpha update increments the number of rewards after each
observation:

```python
self.reward_counts[protocol] += 1
```

This count is currently bookkeeping. It records how many throughput rewards an
arm has received, but it does not change the update calculation.

In a sample-average bandit, the count would determine a decreasing learning
rate:

$$
\alpha_n = \frac{1}{N_a}
$$

where $N_a$ is the number of rewards observed for arm $a$. That version
computes the arithmetic mean of all rewards, whereas the current constant-alpha
version gives more weight to recent throughput.
