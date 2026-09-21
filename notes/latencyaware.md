# Latency-aware leader selection (AWARE-style) as a new Policy

Working notes on adding a third `Policy` alongside `PolicyRoundRobin` and
`PolicyElection`: pick the leader that minimises predicted consensus latency, given
an RTT matrix. Derived from AWARE's leader-positioning component.

## Why the weighting half of AWARE is out

AWARE inherits WHEAT's weighted voting: `n = 3f+1+Δ`, where the `2f` best-connected
replicas get `V_max = 1+Δ/t` and the rest get 1, with quorum `Q_v = 2(f+Δ)+1`.

At n=4, f=1 we have Δ=0, so `V_max = 1` and every replica's weight is 1. **The
weighting scheme is mathematically inert at our size** — dropping it is forced, not a
simplification we chose. Only the leader-positioning half transfers, and that half is
a plain 1-of-4 choice.

## Topologies

- **Star** — one hub close to everyone, spokes far from each other.
  `d(1,x) = 10 ms` for all x; `d(x,y) = 200 ms` for spokes x,y ∈ {2,3,4}.
- **Two-region** — clusters, cheap inside, expensive across.
  `{1,2}` and `{3,4}` at 5 ms internally, 150 ms between.
- **One far node** — `d(i,j) = 5 ms` for i,j ∈ {1,2,3}; `d(4,·) = 150 ms`.

Note that a balanced two-region split is symmetric: every node is equally positioned,
so leader choice cannot matter. Any useful topology must be unbalanced.

## The star does not work in PBFT

First instinct was a star: one well-positioned hub, three badly-positioned spokes,
so round robin runs badly 3 views in 4. That is wrong, because Prepare and Commit are
all-to-all — the spoke↔spoke links gate the critical path regardless of who leads.

Hand-traced on the star above (one-way delays, no batching/processing/queuing):

| Leader | Prepared | Committed | 2 client replies |
|---|---|---|---|
| node 1 (hub) | 210 ms | 220–410 | ~410 ms |
| node 2 (spoke) | 200 ms | 210–400 | ~400 ms |

No meaningful difference. The hub's fast broadcast buys nothing because every replica
still waits on 200 ms spoke-to-spoke Prepares.

**General rule: in an all-to-all protocol the leader-dependent term is only the
leader's own outbound broadcast. The quadratic phases involve everyone symmetrically.**
This is why leader placement matters far more in chained/linear protocols like
HotStuff, where all traffic funnels through the leader. AWARE is built on BFT-SMaRt
and still gains, but much of its gain comes from the weighted-quorum half we cannot
use.

## The topology that does work: one far node

Invert it — one *poorly*-connected node rather than one well-connected one.
`d(i,j) = 5 ms` for i,j ∈ {1,2,3}, `d(4,·) = 150 ms`:

| Leader | Prepared | Committed | 2 client replies |
|---|---|---|---|
| node 1, 2, or 3 | 10 ms | 15 ms | ~15 ms |
| node 4 | 155 ms | 160 ms | ~160 ms |

A 10× spread from leader choice alone. The quorum `{1,2,3}` completes both all-to-all
phases without ever waiting on node 4; node 4 only gates things when it is the one
broadcasting.

So it is 1-in-4 views bad, not 3-in-4. But the wall-clock share is much worse than the
view-count share, because bad views are slow: per 4 views, `3×15 + 160 = 205 ms`, of
which **160/205 = 78% of wall-clock time sits in the single bad view**. Round robin
averages ~51 ms/view; a policy that never picks node 4 gets ~15 ms — about 3.4×.

**Design rule for the scenario: make per-node egress delay heterogeneous.** With
`d(i,j) ≈ e_i` the leader-dependent term is isolated exactly, and per-source delay is
a strict subset of the per-pair flower filters `alt_run_project.sh` already installs
on `lo`.

## The policy

Simplified AWARE `PredictLatency`. For each candidate leader p:

1. Proposal arrives at replica i at `M[p,i]`.
2. Run quorum formation — time for each replica to accumulate 2f+1 votes given the
   matrix.
3. Take the resulting decision time.

Pick the argmin. AWARE only needs simulated annealing above n≈10; at n=4 with no
weights to tune the search space is four candidates, so brute force.

## The hard part is agreement, not prediction

**Every node must compute the same leader**, or they never converge on a NewView. If
each node uses the matrix from whatever 2f+1 ViewChanges it happened to collect,
different nodes see different subsets and pick different leaders.

AWARE's answer: disseminate latency vectors *with total order*, and recompute only at
fixed agreed points (default every c=500 consensus instances).

Mapping onto this codebase:

- Put each node's latency vector into the committed log.
- Derive the leader from the **last stable checkpoint's** matrix — every node has an
  identical stable checkpoint by construction.
- `CHECKPOINT_INTERVAL` then gives AWARE's recalculation interval for free, and
  `checkpoint.go` already carries the proof machinery.

### Anti-oscillation (copy from AWARE)

1. **Calculation interval** — only re-evaluate every c instances, so reconfiguration
   cannot thrash.
2. **α threshold** — only switch if predicted latency improves by a factor α, so the
   system does not flap between two near-equal configurations.

### Measuring the matrix

AWARE piggybacks on the protocol: a WRITE carries timestamp `T1` and a random
challenge, the peer responds immediately, and latency is `(T4' - T1)/2`. PROPOSE
latencies are measured separately with rotating DUMMY-PROPOSE messages, because
proposals carry much larger payloads. Each replica keeps a latency vector over a
configurable monitoring window and disseminates it periodically.

Worth noting for us: ViewChange/NewView are 1 MiB–several MiB here, so PROPOSE-style
and WRITE-style latencies really do differ, and a single matrix may be too coarse.

## Where this policy loses — which is what makes it worth an arm

- **Targeted attack** — a deterministic "always pick the best-positioned node" is
  maximally predictable, so it should be the *worst* arm in the planned targeted-attack
  scenario. See [targettedattack.txt](targettedattack.txt).
- **Leadership concentration** — censorship concern raised in "Leader Rotation Is Not
  Enough".
- **Staleness** — with a static matrix the policy is a constant function and
  degenerates into a stable leader, which under `FixedTrigger` is indistinguishable
  from what we already have. **Shift which node is badly connected partway through the
  scenario** so the arm is actually tested on re-convergence.

A clean win condition plus a clean loss condition is exactly what the bandit needs.
Contrast with the reputation/Carousel idea, which was dropped because Election already
excludes crashed nodes by construction and PerfTrigger already handles slow-but-live
ones, leaving it nearly tied with existing arms.

## Cost note: arm explosion

A third policy takes the action space from 5 arms to 3 triggers × 3 policies = 9 (or
more, with the missing `FixedElection` filled in). QuadRF keys a `RandomForestRegressor`
on **(previous action, candidate action)**, so forests go from 25 to ~81. At 45 s per
epoch that is a real exploration cost — worth considering factorising the model
(separate trigger and policy heads, or a feature vector over (trigger, policy)) before
adding policies.

## Suggested order of work

1. **Feed the matrix from config.** We know the netem delays we injected, so hardcode
   the matrix and let the policy read it. Validates "does latency-aware actually beat
   RR by ~3× here" without building any measurement. Same trick as `oracle_mode`.
2. Confirm the hand-computed tables above against a short netem run.
3. Only then build piggybacked measurement + checkpoint-based agreement.

## Caveats

The latency tables are hand-computed one-way delays. They ignore batching, processing
time, and queuing, and assume own-Prepare counts toward the 2f threshold. Treat them
as order-of-magnitude, not predictions.

## References

- [AWARE: Adaptive Wide-Area Replication for Fast and Resilient Byzantine Consensus (arXiv:2011.01671)](https://arxiv.org/abs/2011.01671)
- [Latency-Aware Leader Selection for Geo-Replicated BFT — Archer](https://www4.cs.fau.de/Publications/2018/eischer_18_bcrb.pdf)
  (uses client-observed end-to-end response times instead of replica self-monitoring)
- [Leader Rotation Is Not Enough (arXiv:2501.02970)](https://arxiv.org/pdf/2501.02970)
- [Chasing the Speed of Light: Low-Latency Planetary-Scale Adaptive Byzantine Consensus (arXiv:2305.15000)](https://arxiv.org/pdf/2305.15000)
- [BFTBrain: Adaptive BFT Consensus with Reinforcement Learning, NSDI'25](https://arxiv.org/html/2408.06432v1)
