# Paper structure: adaptive leader selection and view change (SIGMOD 2027 target)

Planning document for writing up the adaptive PBFT testbed as a paper. Covers
positioning against the existing adaptive-BFT literature, a section-by-section
structure, and detailed guidance on the four parts that carry the contribution:
state, learning algorithm, reward, and action space.

Not a protocol spec. See `CLAUDE.md` for what the system actually does today, and
the other files in `docs/` for the experiment write-ups this paper would draw on.

## 1. Where we sit in the literature

Surveyed October 2026. The relevant neighbours:

| Work | Venue | What it adapts | Relation to us |
|---|---|---|---|
| Abstract | TOCS'15 | whole protocols, fixed switching order | ancestor; no learning |
| ADAPT | IPDPS'15 | whole protocols, supervised, centralized | the baseline everyone beats |
| **BFTBrain** | **NSDI'25** | **whole protocols (PBFT, Zyzzyva, CheapBFT, Prime, SBFT, HotStuff-2), CMAB + Thompson sampling** | **the paper to position against** |
| BFTide | arXiv 2609.31388 | leader-based vs. leaderless, offline-trained RL | adjacent action space, also protocol-level |
| AutoPilot | arXiv 2606.09120 | protocol parameters (batching, timeouts) | parameter steering, not mechanism switching |
| Carousel | FC'22 | leader selection only (reputation), no learning | one cell of our policy plane |
| PrestigeBFT | 2023 | view-change triggering via reputation campaigns | one cell of our trigger plane |
| TimesNet-BFT | 2026 | timeout calibration + leader prediction, deep temporal model | adapts both planes, but by prediction not by mechanism switching |
| AdaChain | VLDB'23 | blockchain architecture, RL | the DB-venue precedent for "learned adaptive X" |
| Marlin / Adaptive Sharding | SIGMOD'26 | shard assignment under Byzantine faults | shows the target venue takes this class of work |

Two observations that shape the paper:

1. Everything above either adapts **whole protocols** (replication layer changes,
   switching is expensive) or adapts **one plane** of the leader-change mechanism
   (Carousel: who; PrestigeBFT: when). Nobody adapts the *pair* with the
   replication layer held fixed.
2. The DB venues have an appetite for this exact shape of work (AdaChain, Marlin),
   but they want it framed as *configuration under changing conditions* with a
   rigorous measurement study, not as a consensus-protocol paper.

### The positioning that has to land in the intro

The reviewer's first thought will be "this is BFTBrain with a smaller action
space." Three claims answer it, and they should appear in the first page:

**C1 — Switching is nearly free, and provably safe.** Because the replication
layer never changes, a switch is a generation bump in `ViewID` that reuses the
view-change path the protocol already performs. BFTBrain needs Abstract-style
epoch abort plus unforgeable init history, and a special NOOP-committed-in-the-
slow-path hack for speculative protocols (their Appendix B). We need none of it.
The claim to state and then measure: *a switch costs exactly one view change, and
that view change is one the protocol already knows how to do.*

**C2 — The action space is factored, not a flat menu.**
`Action = (Trigger, Policy)` separates *what removes the leader* from *who
replaces it*. These are orthogonal mechanisms that the literature has only ever
varied one at a time. The factorization is itself a contribution: it makes the
design space enumerable and makes adding an arm a matter of adding a predicate or
a selector.

**C3 — The one-step dependency is structural here, not incidental.** BFTBrain
argued qualitatively that the previous protocol biases the slowness-of-proposal
feature. In our setting the trigger *mechanically determines* the view-change
rate, which determines nearly every fault and view-dynamics feature we measure.
That is a stronger and cleaner motivation for the per-(previous, candidate) model
than the one in the BFTBrain paper.

To make the novelty concrete rather than architectural, we want at least one
measurement BFTBrain cannot produce: a condition where every fixed protocol in
their pool loses to a protocol-constant system that only rotates its leader-change
rule.

## 2. Section structure

Target: PACMMOD / SIGMOD format, roughly 12 pages plus references.

### 1. Introduction (~1 page)
The knob nobody tunes. The headline number from the scenario sequence. Three
contributions: the design-space study, the learning formulation over a factored
action space, and near-zero-cost switching.

### 2. The View-Change Design Space
**This section decides whether the paper gets in.** It is a measurement study, not
background. Structure it as BFTBrain's Section 2: the cross product of triggers x
policies evaluated under each scenario, presented as a table where the winner
changes from row to row, with the margin over the second-best action in the last
column.

Every row needs a *mechanistic* explanation, not just a number. We have four:

- `Healthy` -> `FixedRoundRobin`: nothing is wrong, cheapest rotation, the leader
  is removed only when it stalls.
- `ProposalDelay` -> `PerformanceRoundRobin`: a slow-but-live leader passes the
  fixed 150 ms timer; only the throughput bar catches it.
- `NetworkDelay` -> `PeriodicRoundRobin`: the 150 ms timers expire faster than a
  view change completes, so the system cascades and no leader is ever installed;
  the 10 s period gives each leader time to make progress.
- `NetworkDelayFCrash` -> `PeriodicElection`: the only action answering both
  halves. The 10 s period keeps progress under delay, and Election skips crashed
  nodes for free, because candidacy requires broadcasting RequestVote after the
  VDF race and a crashed node never does. RoundRobin has no such filter and burns
  a full timeout whenever the rotation lands on a dead node.

### 3. Overview
System model, threat model, the epoch/generation lifecycle, and the one figure
readers will remember: validator plane, learning plane, and the
`(Trigger, Policy)` box between them.

### 4. Action Space: Triggers and Policies
- 4.1 Triggers as **leader-removal predicates**, defined uniformly: stall
  (Fixed), throughput bar over a stall floor (Perf), unconditional period
  (Periodic).
- 4.2 Policies as **successor-selection functions**: deterministic rotation vs.
  candidacy-filtered election. The key property to state: election implicitly
  filters non-participating nodes; rotation structurally cannot.
- 4.3 Why the two planes switch together (see the caveat in section 3 below).
- 4.4 Extensibility: a new arm is a new predicate or a new selector, with no
  change to the replication code.

### 5. Learning Formulation
State, reward, model. Detailed guidance in section 3 of this document.

### 6. Switching Mechanism
Generation vs. counter in `ViewID`; `incrementGeneration`; the f+1 amplification
rule that pulls straggler nodes into the new generation with the action carried in
the ViewChange messages. Safety and liveness argument, then the measurement:
switch latency and the throughput dip. That plot is the empirical answer to "why
not full protocol switching."

### 7. Decentralized Coordination
Epoch aggregation, the 2f+1 report quorum, the median filter, determinism across
agents.

**This is currently the weakest section**, and reviewers will target it hardest,
because BFTBrain made Byzantine-robust data collection a headline contribution.
Either implement it properly or narrow the threat model explicitly and honestly.
See section 4 below.

### 8. Implementation
Go protocol plus Python agent over gRPC; LoC; and the fact that the agent sits
behind a gRPC boundary so the protocol side only ever receives an action name,
which is what lets the CMAB be replaced later.

### 9. Evaluation
- **Q1** Convergence under each static scenario. Report time-to-converge, as
  BFTBrain does (they report 0.81-5.39 min).
- **Q2** Adaptation across a scenario sequence, against the best fixed action, the
  worst fixed action, and an **oracle**. `oracle_mode` gives us the oracle for
  free; it is a stronger upper bound than most papers in this space have, so use
  it prominently.
- **Q3** Re-convergence when a scenario cycles back (BFTBrain's 2 s vs. 70 s
  result is the comparison point).
- **Q4** Ablations. Flat model vs. per-(previous, candidate) buckets. Trigger-only
  vs. policy-only vs. joint switching — **this ablation is the justification for
  the factored action space; do not skip it.**
- **Q5** Switching overhead (supports C1).
- **Q6** Robustness to polluted reports.
- **Q7** Learning overhead (training and inference per epoch vs. epoch length).

### 10. Related Work
Adaptive BFT (Abstract, ADAPT, BFTBrain, BFTide, AutoPilot); leader rotation
(Carousel, PrestigeBFT, reputation-based election, "Leader Rotation Is Not
Enough"); ML-for-systems in the DB community (AdaChain, Bao, Marlin). **Lead with
the DB-community work** given the venue.

### 11. Conclusion

## 3. The four parts that carry the contribution

### 3.1 State

Organize as BFTBrain does — reviewers recognize the W/F frame — but with our own
categories, since workload is not our story:

- **W (workload).** Request size, batch fill time, client send rate. Cheap to
  measure, and they let us claim generality beyond fault scenarios.
- **V (view dynamics).** *Our novel category and the feature-engineering
  contribution.* View changes per epoch; time from ViewChange quorum to NewView
  install; fraction of the epoch spent in view change vs. committing; new-view
  timer expiries, meaning the incoming primary failed to deliver a valid NewView;
  consecutive failed views, which is the cascade signature under `NetworkDelay`.
- **F (faults).** Inter-proposal interval (slowness); messages received per slot
  (absence); throughput relative to the recent-window maximum, which the perf
  trigger already computes as `maxRecentViewThroughput`; leader-progress timer
  expiry rate.

Three rules to state explicitly, because reviewers check them:

1. Every feature is **node-local** and adds no messages. BFTBrain makes this point
   and gets credit for it.
2. Every feature is **measurable under every action.** A feature that only
   PerfTrigger computes gives that arm a free signal and biases the bandit.
3. Say **which features carry the one-step dependency**: V and F do, W does not.
   That sentence is what earns the per-(previous, candidate) model.

Add a short subsection on **why view-dynamics features separate our scenarios when
BFTBrain's feature set would not.** `NetworkDelay` and `NetworkDelayFCrash` look
nearly identical in throughput and in inter-proposal interval; they differ in
*which* views fail to install. A feature-space scatter showing that separation is
worth a lot — it is the empirical argument for the new feature category.

### 3.2 Machine learning

Keep the current design: CMAB, Thompson sampling via bootstrap resampling, one
`RandomForestRegressor` per (previous action, candidate action) pair. Do not
invent a new algorithm; the contribution is the formulation, not the learner.

Three questions a SIGMOD reviewer will ask, all of which need an answer in the
text:

1. **Why not exploit the factorization?** With K=5 named actions we train K^2=25
   models, but the action is a *pair*, so there is shared structure — all Periodic
   arms behave alike under delay. Either exploit it (factored or structured
   bandit, trees shared per plane) or argue explicitly that at K=5 the sample
   complexity does not justify it, and show the data. Either answer is acceptable;
   silence is not.
2. **Why a bandit rather than full RL?** BFTBrain's answer (episode independence,
   faster convergence, well-studied, asymptotically optimal algorithms exist) plus
   one of our own that is genuinely stronger: switching cost is near zero, so
   there is no long-horizon credit assignment to perform.
3. **Determinism across replicas.** Same seed, same experience buffer, same
   decision. State it, and state what happens when it breaks.

Report **regret against the oracle**, not only throughput curves. A quantity with
a definition reads better at a DB venue than a set of plots.

### 3.3 Reward

Easy to get wrong, so be deliberate:

- Use **committed requests per epoch over a fixed wall-clock epoch** (currently
  45 s). Say why wall-clock beats BFTBrain's k-blocks epoch: a k-block epoch lets
  a bad action run longer, so the reward silently conflates throughput with epoch
  duration. Our choice is defensible — spend a sentence defending it.
- State explicitly that the reward **includes the cost of the view changes the
  action causes.** This is the whole point. The perf trigger's bar starts at
  0.92x of the recent max and compounds 1% per second, so it removes every leader
  after roughly 12 s; that cost must land in the reward rather than being
  amortized away.
- Discuss alternative rewards and why they were not chosen: p99 latency, or a
  throughput/latency combination. A DB audience will ask about tail latency. One
  paragraph plus **one experiment where the reward is swapped for p99 and the
  converged action changes** makes the "pluggable objective" claim credible
  instead of merely asserted.
- The median-over-2f+1 robustness argument applies to reward as well as to state.

### 3.4 Actions

Present actions as **mechanisms with named properties**, not as an enumerated
list. The grid does the work:

|  | RoundRobin | Election |
|---|---|---|
| **Fixed** | cheapest; removed only on stall | |
| **Perf** | throughput bar over a stall floor | |
| **Periodic** | unconditional rotation, long period | skips non-participants for free |

Then state: *each cell is a (removal predicate, successor selector) pair; the five
named actions are the cells we implement.* This makes the space look principled,
and makes a sixth arm read as future work rather than as a gap.

Two caveats to handle in the text, because they are the holes a reviewer finds:

**Joint switching.** "Both planes always switch together" needs justification or
an admission. If it is an implementation constraint rather than a protocol
requirement, say so. A reviewer who notices a 3x2 space with only five cells
reported will ask why. The honest version — the planes are independent in
principle, we switch them jointly because the generation counter carries a single
action, and the Q4 ablation shows joint switching dominates — costs nothing.

**The election candidate shortcut.** `electionCandidateForView` picks
deterministically among nodes excluding node 2, rather than using the VDF race
winner, standing in for "only live nodes finish the race" (`electionExcludedNodeID`).
Put this in the implementation section as a stated experimental simplification,
with the argument for why it preserves the property under test. Reviewers read
artifacts; do not let this be discovered.

## 4. Blockers before submission

Three items in the current code will undermine the evaluation. From `CLAUDE.md`
and the code as of this writing:

1. **The agent decides on synthetic state and reward.** `EpochAggregateMsg`
   carries placeholder zeros, and the real `node_reward` / `node_state` arrive as
   zeros and are logged as `ignored`. Every learning claim in the paper is
   unsupported until this is replaced with the real aggregate. **This is the
   critical path.**
2. **Node 4 is a hardcoded epoch aggregator** (`epochtimer.go`), which directly
   contradicts the Byzantine-robustness story that section 7 has to tell.
3. **The crashed-node set is coupled to the election exclusion.** Node 2 is both
   the node experiments put in `nodes_dead` and the node excluded from election
   candidacy, so the Election arm's advantage under `NetworkDelayFCrash` is partly
   built into the harness. At least one experiment needs a randomized crashed set.

Also worth resolving before the measurement study is frozen: several hardcoded
node ids are experiment scaffolding rather than protocol (node 4 also owns the
scenario-mode netem qdisc via `scenarioNetemNodeID`), and the paper should either
remove them or declare them.

## References

- BFTBrain: Adaptive BFT Consensus with Reinforcement Learning, NSDI'25 — https://arxiv.org/pdf/2408.06432
- Be Aware of Your Leaders (Carousel), FC'22 — https://arxiv.org/abs/2110.00960
- PrestigeBFT: Revolutionizing View Changes in BFT — https://arxiv.org/pdf/2307.08154
- Adaptive Switching Between Leader-Based and Leaderless BFT Protocols — https://arxiv.org/html/2609.31388
- AutoPilot: Learning to Steer High Speed Robust BFT — https://arxiv.org/pdf/2606.09120
- Leader Rotation Is Not Enough: Scrutinizing Leadership Democracy of Chained BFT — https://arxiv.org/abs/2501.02970
- Reputation-Based Leader Election under Partial Synchrony — https://arxiv.org/html/2512.12409
- AdaChain: A Learned Adaptive Blockchain, VLDB'23 — https://www.vldb.org/pvldb/vol16/p2033-wu.pdf
- Adaptive Sharding in Untrusted Environments (Marlin), SIGMOD'26 — https://dl.acm.org/doi/abs/10.1145/3769756
