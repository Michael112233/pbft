# aware_leader: latency-aware leader selection for PBFT, with a liveness argument

A self-contained study of the Aware leader policy. It doesn't touch the Go code. It
covers three things: a refined algorithm, a liveness argument stated as lemmas with
proofs, and two kinds of mechanical checks. The first is a TLA+ model of the schedule
layer, checked with TLC. The second is an exhaustive search over what Byzantine nodes can
report, on the n=7 far-node topology.

```
aware_leader/
  README.md                 this file
  tla/AwareSchedule.tla     schedule-layer model (pilot vs certified variants)
  tla/*.cfg, run_tlc.sh     TLC configurations and runner; results in tla/out/
  sim/model.py              vectorised PredictLatency (checked against latency_sim/sim.py)
  sim/farnode_attack_n7.py  what 2 Byzantine nodes can do to the candidate list, n=7
  sim/membership_rules_n7.py  membership rules on honest scenarios with crashes
  sim/out/                  outputs quoted below
```

PBFT safety isn't argued here. The data plane is assumed to follow the original PBFT
rules: signed messages, PrePrepare and NewView for view v accepted only from L(v), and
ViewChange carrying prepared certificates. In original PBFT, safety does not depend on
which node leads a view: two prepared certificates for the same (v, seq) would need an
honest node to accept two PrePrepares in v.

## 1. Summary

1. **Liveness doesn't depend on the scores or on the order of the list.** It rests on
   three properties:
   - **Agreement:** every honest node computes the same leader for every view.
   - **Floor:** the list has at least F ≥ f + s + 1 distinct members.
   - **Rotation:** consecutive views take consecutive positions of the list.

   An adversary that moves nodes around the list changes *which* views have a bad leader.
   It can't change *how many* come in a row: at most f + s (Lemma 3). Here f counts
   Byzantine nodes and s counts honest nodes that are too slow for the active trigger.
2. **The pilot doesn't give agreement.** Node 4 can send different aggregates to
   different nodes. A node that missed the aggregate adopts the rows of the single
   ViewChange or NewView that pulled it forward. TLC finds both. After GST, with every
   honest node in the generation, views keep failing past the bound. With n = 3f+1 every
   view needs all 2f+1 honest nodes when the Byzantine ones withhold, so one honest node
   with a different list is enough for the adversary to pick which views succeed.
3. **The fix is the two-layer design you described, with one rule added.**
   - A coordination consensus decides each epoch's report set with a transferable commit
     certificate.
   - A node enters generation g+1 only while holding epoch g's certificate.
   - ViewChange and NewView messages carry that certificate.

   TLC checks agreement, the bounded bad run and eventual liveness exhaustively at n=4
   and n=7, over three generations (93M states at n=7). In every generation the adversary
   picks *any* member set of size ≥ F.
4. **Scores under attack can hurt performance, not liveness.** Two Byzantine nodes can
   decide who fills the list. With the pilot's padding rule they can cut the honest close
   nodes' share of the rotation to 0.20, against 0.43 for round robin. With
   "threshold set, or every node" the worst is 0.33. With the pilot's score order they can
   also make the first leader of a generation Byzantine in 90–100% of generations. That
   only matters under the Fixed trigger, which never removes a leader that keeps
   committing slowly. A score-independent order brings it down to 0.31–0.70.
5. **α-hysteresis (switch only when the gain exceeds α) is a performance knob.** It picks
   between two lists that both satisfy the floor, so it can't affect liveness. It is
   optional (§2.6).

## 2. The algorithm

### 2.1 Two planes

| Plane | Runs | Leader | Decides |
|---|---|---|---|
| Data plane | PBFT, views (g, c) | L(g, c) from the Aware list C_g (§2.5) | client requests |
| Coordination plane (CP) | one single-shot PBFT instance per epoch e | round robin, L_CP(e, u) = ((e + u) mod n) + 1, its own view change, timeouts doubling from ~1 s | the epoch record M_e |

The CP must have its own view change and rotation. A fixed coordinator (node 4 today)
that is Byzantine can stall the CP for ever, and then generations never advance (§2.3).
That is not a data-plane liveness failure, because the data plane keeps running on C_g,
but adaptation stops. Offsetting the CP's rotation by the epoch number stops the same
node from opening every epoch.

### 2.2 Epoch record M_e

At the end of epoch e, every node broadcasts a signed report: its RTT vector (median
over the probe window) and the epoch number. The CP leader proposes M_e:

- For every node j, the most recent signed report of j with epoch in [e − E, e], or ⊥.
  E is the staleness bound and replaces the pilot's carry-forward.
- **Valid** iff every included report's signature checks, every epoch is in range, and
  at least 2f+1 reports are from epoch e.

M_e is self-contained: a node can compute C_{e+1} from M_e alone, without the earlier
records. A Byzantine CP leader can leave out up to f fresh honest reports, or include
stale ones within E. That changes scores, not liveness, because the floor still holds.

CP output: M_e plus the certificate Q_e, which is 2f+1 signed COMMITs on (e, digest(M_e)).
CP agreement means at most one M_e ever has a certificate.

### 2.3 Generation gate and catch-up (what makes agreement hold)

- **Gate:** an honest node enters generation g+1 only while holding (M_g, Q_g). Its list
  for g+1 is C_{g+1} = Members(M_g), a pure function (§2.4).
- **Catch-up:** every ViewChange for a view in g+1 and every NewView for g+1 carries
  (M_g, Q_g); a digest plus fetch-on-miss also works. A node pulled forward by f+1
  ViewChanges or by a NewView verifies Q_g and adopts M_g. It never adopts unverified rows
  from one sender, which replaces `maybeAdoptAwareRows`.
- **No blocking:** while epoch g's CP instance is undecided, nodes stay in generation g
  and the data plane keeps rotating over C_g. The CP delays adaptation only.

The generation's *action* (trigger, policy) needs the same agreement. Assumption A7 below
requires the agent to be a deterministic function of the certified record sequence. The
current agent already relies on this, which is the source of the `DECIDER SYNC` warning.

### 2.4 Members(M): who is in the list

1. Matrix: AWARE max-sanitization of the reported RTTs, halved to one-way delays. A link
   with neither end reported is +∞, as in `node/awareMatrix.go`.
2. Scores: `predictAwareLatency` per node (`node/awarePredict.go`, equal to
   `latency_sim/sim.py`).
3. Threshold set T = {p : score(p) ≤ best + max(α·best, ε)}.
4. Members:
   - **recommended (hybrid):** T if |T| ≥ F; else every node with a finite score, if that
     is ≥ F; else every node.
   - pilot: T padded with the next-best nodes by (score, id) up to F.

   F = 2f+1 (5 at n=7). Liveness needs only F ≥ f + s + 1 (§3), so any rule that keeps
   the floor is live. The two rules differ only under lying (§4.2): padding by score lets
   the adversary pick the padding. On the honest scenarios, including crashes, all three
   rules give the same set (`sim/out/membership_rules.txt`).

### 2.5 Order and leader function

L(g, c) = the member at position (g + c) mod |C_g| of C_g, **ordered independently of
scores**. Options, from simplest to strongest:

| Order | Adversary controls | First leader of a generation made Byzantine (§4.2) |
|---|---|---|
| by score (pilot) | position directly | 0.90–1.00 |
| by id | position, through membership | 0.62–0.95 |
| public per-generation permutation π_g = sort ids by H(g ‖ id) | membership only, knowing π_g in advance | 0.31–0.70 |
| (truthful reports, for reference) | — | 0.20–0.40 |

An unpredictable permutation would need a beacon: a seed the adversary can't predict when
it sends its report and can't grind afterwards. `H(digest(M_g))` doesn't qualify, because
Byzantine nodes write part of M_g and can grind it. Per-node VRF ranks revealed after M_g
is certified would qualify. That is left out here because none of it is needed for
liveness.

### 2.6 α-hysteresis

The pilot recomputes C every generation. AWARE only reconfigures when the predicted gain
exceeds α. The deterministic version keeps C_{g+1} = C_g unless the new list's mean
predicted latency under M_g beats the old one's by a factor (1 − α_h). Both inputs are
certified, so every honest node makes the same choice. Both candidate lists satisfy the
floor, so Lemmas 1–3 hold either way.

Effects:
- It damps membership churn from probe noise.
- It narrows what a lying adversary can change per generation: a lie must clear α_h to
  move the list at all.
- It does not stop the leader from changing every generation. The offset (g + c) moves
  the start one place per generation, just as round robin does (§5).

### 2.7 Pseudocode (one honest node)

```
on epoch timer (generation g):        broadcast SignedReport(g, rttVector)
CP instance for epoch g:              propose/validate M_g (§2.2); on commit -> (M_g, Q_g)
on (M_g, Q_g) and agent decision for g+1:
    C[g+1] := Members(M_g); action := Agent(certified history)
    enter generation g+1: ViewChange((g+1, 1), action, cert = (digest(M_g), Q_g))
on f+1 ViewChanges / a NewView for (g+1, c) carrying a valid Q_g:
    adopt M_g; C[g+1] := Members(M_g); join (g+1, c)
leader(g, c) := Order(C[g], g)[(g + c) mod |C[g]|]
accept NewView / PrePrepare for (g, c) only from leader(g, c)
```

## 3. Liveness argument

### 3.1 Model and assumptions

- **A1** n = 3f + 1, at most f Byzantine nodes (set B). Partial synchrony with unknown
  GST.
- **A2** The data plane follows standard PBFT view synchronization: a node joins a higher
  view on f+1 ViewChanges, starts its new-view timer only after 2f+1, and moves on when it
  expires. After GST all honest nodes therefore try views of a generation together.
- **A3 (trigger).** For the active trigger T, an honest node p is *good* if, once
  installed by all honest nodes after GST, T doesn't remove it before the generation ends.
  For Fixed and Perf, its view change and per-slot progress fit the timer. Under Periodic
  it is removed after P seconds having committed requests. S_T is the set of honest nodes
  that are not good, and s = |S_T|. A view with a bad leader (B ∪ S_T) ends within one
  timeout. Under Periodic, a Byzantine leader that stalls loses P seconds.
- **A4 (CP).** At most one certified M_e per epoch. After GST each epoch's instance
  decides in bounded time (standard PBFT with rotation).
- **A5** F ≥ f + s + 1.
- **A6 (epoch length).** A generation lasts at least (f + s + 1) view attempts after its
  honest nodes synchronize.
- **A7** The action of generation g+1 is a deterministic function of the certified
  records.

In the far-node scenario, whether the far nodes belong to S_T depends on the trigger.
With 50 ms one-way delays and the 350 ms Fixed floor they probably still count as good:
they are bad for latency, not for liveness. Then s = 0 and the bound is f + 1 views. If
they do time out, s = 2, and F = 5 = f + s + 1 still meets A5.

### 3.2 Lemmas

**Lemma 1 (schedule agreement).** Take two honest nodes i and j that enter generation g.
Then C_g^i = C_g^j, so L^i(g, c) = L^j(g, c) for every c.
*Proof.* By the gate, each node entering g by its own decision holds a certified
M_{g−1}. By catch-up, each node pulled into g verifies a carried certificate and adopts
the certified M_{g−1}. A4 allows only one certified M_{g−1}, and Members is
deterministic. Generation 1 comes from the genesis config. ∎

**Lemma 2 (a good member exists).** Under A5, every C_g contains a good node.
*Proof.* |C_g| ≥ F > f + s ≥ |B ∪ S_T|. ∎
This holds whatever the Byzantine nodes report: it uses only the floor.

**Lemma 3 (bounded bad run).** In every generation g, at most |C_g ∩ (B ∪ S_T)| ≤ f + s
consecutive views have a bad leader. So views (g, c), …, (g, c + f + s) include a good
leader for every c.
*Proof.* c ↦ L(g, c) walks the cyclic arrangement of the distinct members of C_g, one
position per counter. A cyclic run of positions that are all in a set X has length at
most |C_g ∩ X|, unless C_g ⊆ X, which Lemma 2 excludes. ∎
The proof works for **every** arrangement. An adversary that controls the order, for
example through scores, chooses where the bad views fall, not how many come in a row.
This answers the "manipulate positions" concern for liveness.

**Lemma 4 (non-blocking).** No data-plane step waits on the CP. In generation g, views use
C_g, which every honest node holds on entering g. An undecided CP only delays the move to
g+1.

### 3.3 Theorem

Under A1–A7, after GST, every generation that all honest nodes enter installs a good
leader within f + s + 1 view attempts. That leader is accepted by every honest node and
stays, as A3 defines. Generations keep advancing (A4). So client requests keep committing.

*Proof.* After GST the honest nodes of g move through consecutive views together (A2).
They agree on each view's leader (Lemma 1), and a good one comes within f + s + 1 views
(Lemma 3). A good leader agreed by all 2f+1 honest nodes collects 2f+1 ViewChanges. Its
NewView passes every honest node's L(v) check, and A3 keeps it. A6 gives the generation
time to reach it, and Lemma 4 together with A4 moves the system on to g+1. ∎

Cost per generation: at most f + s wasted views, each one timeout plus a view change.
Across a boundary the runs can add up to 2(f + s) when the old generation ends inside a
bad run, but A6 rules out an unbounded chain.

### 3.4 What the theorem does not give

- **Latency optimality under attack.** Byzantine reports decide which nodes fill the list
  beyond the floor. The worst good share is (F − f − s)/F = 0.20 at F=5, f=s=2, and the
  pilot's padding rule reaches exactly that (§4.2). Round robin gets
  (n − f − s)/n = 0.43. So Aware under attack can do worse than round robin on latency.
  It can never do worse on liveness.
- **Capture under Fixed.** A Byzantine leader that commits one slot every <τ is never
  removed by Fixed, under any policy (FixedRR leads with node 1 in every generation). The
  order only decides how often a Byzantine node gets to be first. Countering it is the
  trigger's job: Perf removes slow leaders.
- **Selective voting.** Byzantine nodes can help their own leaders' quorums and starve
  honest ones. Any honest leader is then really as slow as a far one (§4.2). No matrix
  defence can tell a real slow link from a deliberately slow one; `latency_sim/README.md`
  reaches the same conclusion.

## 4. Mechanical checks

### 4.1 TLA+: `tla/AwareSchedule.tla`

**What the model covers.**
- Generations, each node's generation and list, catch-up through f+1 ViewChanges or a
  NewView, GST, and a view synchronizer.
- The synchronizer rule: a view succeeds iff every honest node is in the generation, they
  agree on its leader, and that leader is good. Otherwise the adversary may fail it.
  Byzantine leaders never succeed.
- Scores are not modelled: **the adversary picks any member set of size ≥ F every time a
  set is produced.** That is the strongest possible score manipulation, so any result
  that holds here holds for every real scoring rule that keeps the floor.

**Variants.**
- `pilot`: a fixed aggregator; a node missing the aggregate adopts one sender's set,
  which can be forged when the sender is Byzantine.
- `certified`: one CP decision per epoch; the gate and catch-up of §2.3.

**Properties.**
- `Agreement`: honest nodes in the same generation have the same list.
- `BoundedBadRun`: after GST, once every honest node is in g, at most |B ∪ Slow| views fail
  before a good install.
- `EventuallyGoodLeader`: a temporal property, checked under weak fairness.

| Config | n | Byz | Slow | F | Variant | Result | Distinct states |
|---|---|---|---|---|---|---|---|
| n4_certified | 4 | {4} | {} | 3 | certified | all properties hold | 13.4M (6 min) |
| n4_certified_slow | 4 | {1} | {2} | 3 | certified | all hold | 11.8M (6 min) |
| n4_certified_floor2 | 4 | {1} | {2} | 2 | certified | **BoundedBadRun violated**: F < f+s+1 allows C = {1,2} | — |
| n4_pilot_byz_aggregator | 4 | {4} = aggregator | {} | 3 | pilot | **Agreement violated** | — |
| n4_pilot_byz_aggregator_only | 4 | {4} = aggregator, forged catch-up off | {} | 3 | pilot | **Agreement violated** by equivocation alone: node 4 gives node 1 {1,2,3} and node 2 {1,2,4}; both enter generation 2 | — |
| n4_pilot_byz_sender | 4 | {1}, aggregator honest | {} | 3 | pilot | **Agreement violated**: node 3 missed the aggregate and adopts {1,2,4} from Byzantine node 1's ViewChange; node 2 has {1,2,3} | — |
| n4_pilot_*_liveness (all three) | 4 | as above | {} | 3 | pilot | **BoundedBadRun violated**: all honest nodes in g after GST, views keep failing | — |
| n7_certified | 7 | {1,2} | {6,7} | 5 | certified | all hold | 1.45M (29 s) |
| n7_certified_b | 7 | {3,5} | {1,4} | 5 | certified | all hold | 1.45M (29 s) |
| n7_certified_3gen | 7 | {2,6} | {3,7} | 5 | certified | all hold | 93.2M (39 min, depth 41) |
| n7_certified_floor4 | 7 | {1,2} | {6,7} | 4 | certified | **BoundedBadRun violated**: the adversary picks C = {1,2,6,7} = Byz ∪ Slow, and every view fails | — |

The output and counterexample traces of each run are in `tla/out/<config>.txt`. Rerun with `tla/run_tlc.sh <cfg>…` (needs Java 11+; the script
downloads `tla2tools.jar` once).

What the model leaves out:
- PBFT safety internals.
- The CP's internals: it is an atomic decision with uniqueness, justified by A4.
- Message timing: A6 is encoded as "after GST a node leaves g only after a good install
  or more than f + s failures".
- The order: the model uses id order. Lemma 3 covers every order on paper.

A negative result (F too small) comes from an adversary choosing arbitrary sets. Real
scores may not always reach that set, but nothing prevents them from reaching it.

### 4.2 Score-level search: `sim/farnode_attack_n7.py`

Setup:
- n=7, f=2, five close nodes, two far ones.
- Two Byzantine nodes choose every link they report from
  {true, d_far, 2·d_far, unusable}, plus the Byzantine–Byzantine link from
  {0, true, unusable}. That is 3.1M reports per case, enumerated exhaustively. Sanitization
  means the reported value is max(true, report).
- Delays: 5 ms close, 50 ms far. Far nodes are either co-located (5 ms apart) or in two
  regions (100 ms apart), with an optional ±10% honest jitter.

**Good share** below is the honest close nodes' share of the list, i.e. of the rotation:

| Topology | Byz / far | pilot pad (worst) | threshold-or-all (worst) | round robin | truthful Aware |
|---|---|---|---|---|---|
| one far region | {1,2} / {6,7} | 0.43 | 0.43 | 0.43 | 0.60 |
| one far region | {2,3} / {1,4} | **0.20** | 0.43 | 0.43 | 0.60 |
| one far region | {5,7} / {1,4} | 0.40 | 0.43 | 0.43 | 0.60 |
| two far regions | {1,2} / {6,7} | 0.33 | 0.33 | 0.43 | 0.60 |
| two far regions | {2,3} / {1,4} | **0.20** | 0.33 | 0.43 | 0.60 |
| ±10% jitter | {5,7} / {1,4} | 0.33 | 0.33 | 0.43 | 0.60 |

Full output, including every reachable member set: `sim/out/attack_*.txt`.

Findings:
- Every reachable list kept at least one honest close node, in every case. Lemma 2
  predicts this.
- In the one-far-region topology "threshold-or-all" never goes below round robin.
- In the other two topologies neither rule holds round robin's 0.43. The proof in
  §3.4 allows down to 0.20, and the pilot rule reaches it.
- Score-level defences end at the floor. This agrees with the f=2 conclusions in
  `latency_sim/README.md`.

**Who leads first in a generation** (Fixed keeps that leader while it commits): the share
of generations whose first leader the adversary can make Byzantine is in the §2.5 table.
Score order gives 0.90–1.00. The seeded permutation gives 0.31–0.70, against 0.20–0.40
with truthful reports.

**Crash scenarios with no lying** (`sim/out/membership_rules.txt`): pad, threshold-or-all
and hybrid give the same set in every case:
- Healthy and NetworkDelay: all 7.
- NetworkDelayFCrash: the 5 live nodes.
- FarNode: the 5 close nodes.
- FarNode with 1 or 2 close nodes dead: every live node.

With crashes, every live leader's quorum needs the far or slow nodes, so the scores
flatten.

## 5. Notes for the far-node scenario and the Go pilot

- **Honest far-node run.** C = the 5 close nodes, and far nodes never lead. Under Fixed the
  leader changes once per generation, because the offset moves, so leadership rotates
  over the close nodes. That costs one view change per 75 s.
- **Reward.** Below the window crossover, throughput can't see leader placement, so the
  far-node scenario needs a latency term in the reward. The earlier proposal:
  throughput-feasible actions first, then the lowest latency.
- **Gaps between the pilot code and §2.** Listed here; nothing was changed in the code:
  - `maybeAdoptAwareRows` trusts one message → certificate-carrying catch-up.
  - Node 4 as the fixed aggregator → the CP with rotation and Q_e.
  - The aggregate's embedded signatures are not verified (TODO in `epochManager.go`) →
    report validity in §2.2.
  - Candidates ordered by score → score-independent order.
  - Padding by score → hybrid membership.
  - A NewView/PrePrepare sender check against L(v) is assumed here; you're bringing that
    path in line with original PBFT.
