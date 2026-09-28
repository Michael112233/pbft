# latency_sim

Offline model of PBFT leader latency, used to evaluate an AWARE-style latency-aware
leader policy before building it. Nothing here touches the Go code. Plain Python 3; the
Vivaldi embedding defences (`vivaldi_embed*`, used by `combined_attack_n7.py`) need
numpy, and the Beware port (`beware_vcs.py`) also needs scikit-learn. Beware's own code
is kept unmodified under `references/beware/`. Run each script from this folder, e.g. `python3 far_node_n4.py`.

Design notes that motivated these scripts: `notes/latencyaware.md`,
`docs/pipeline_throughput_bottleneck.md`.

## The model (`sim.py`)

`D` is a symmetric **one-way** delay matrix in ms, 0-indexed in code (node id =
index + 1). For AWARE-style measurements use `D = sanitized RTT / 2`.

`predict_latency(D, f, p)` predicts the time from leader `p` sending a PrePrepare to
`p` committing the slot. It is AWARE's PredictLatency (Algorithm 2) reduced to one
matrix, no voting weights and a single round (slots are pipelined here, unlike
BFT-SMaRt's one-instance-at-a-time, so AWARE's multi-round offsets are dropped).
Quorum rules follow `node/node.go`:

1. **PrePrepare**: reaches node `i` at `D[p][i]`.
2. **Prepare**: every backup broadcasts a Prepare as soon as it has the PrePrepare; the
   leader sends none. A backup is prepared at the `2f`-th earliest Prepare, counting
   its own (and never before its own PrePrepare). The leader is prepared at the
   `2f`-th earliest backup Prepare.
3. **Commit**: every node broadcasts a Commit once prepared. The leader commits at the
   `(2f+1)`-th earliest Commit, counting its own.

Each "k-th earliest" step is AWARE's `formQV` (Algorithm 1) with all weights 1, i.e. a
k-th order statistic: the quorum ignores the slowest `n - k` arrivals.

Other helpers:

| Function | What it is |
|---|---|
| `predict_trace` | Same as `predict_latency` but also returns the per-node PrePrepare, prepared and commit-arrival times, for hand-checking. |
| `shortcut_score` | The cheap approximation: `2 ×` the `2f`-th smallest one-way delay from `p`. Looks only at the leader's own links. |
| `remove_nodes_score` | Candidate mitigation: worst `predict_latency` over silencing any `f` nodes other than `p`. |
| `triangle_clamp` | Candidate mitigation: raise each link `(i,j)` to `max_c |D[i][c] - D[j][c]|`, a lower bound that holds when the network obeys the triangle inequality. `ignore_top=f` uses the (f+1)-th largest bound instead of the max. |
| `vivaldi_embed`, `vivaldi_embed_batch` | Candidate mitigation (Beware-style): deterministic Vivaldi embedding of the reported matrix into 3-D (Beware's `ce = cc = 0.25`, start error 0.1, 300 iterations, seeded start), returning predicted delays as distances between the points. `trim="fixed"` drops the `f` forces on each node farthest from the per-axis median force, `trim="outlier"` drops them only if they are outliers, `clip=True` caps forces at the median size. The batch version embeds many matrices at once, with identical results. |
| `matrix_from_links`, `random_matrix`, `geometric_matrix`, `with_link` | Topology builders. `geometric_matrix` places nodes at random points in a 100×100 plane, so its delays satisfy the triangle inequality (up to rounding). |

The model ignores batching, CPU, signature checks, queueing and message size. Treat
outputs as relative rankings and order-of-magnitude latencies.

## Scripts

### `far_node_n4.py` — the scenario Aware is meant to win

n=4, f=1. Node 1 is 35 ms from everyone, all other links 1 ms (what
`setup_netem_node1` in `alt_run_project.sh` builds). Prints predicted latency per
leader for three cases:

- **honest**: node 1 = 71 ms, nodes 2–4 = 3 ms. Confirms the far node is the only bad
  leader, at about `2d + 2ε`.
- **node 3 inflates link 3–2**: every leader becomes 71 ms. The order statistic
  tolerates one slow arrival per quorum *in total* at n=4, and the far node already
  uses it, so one lie removes all fast quorums. A liar can flatten the ranking (Aware
  falls back to RoundRobin) but cannot make itself look better.
- **node 3 inflates both its fast links**: node 3 looks terrible (1000 ms), the rest
  stay at 71. Inflation hurts the liar too.

### `shortcut_flip_n4.py` — why the shortcut score is not enough

n=4, f=1. Exhaustively enumerates every matrix with link delays in
`{1, 5, 10, 20, 40, 70, 100}` and counts those where `shortcut_score` and
`predict_latency` pick different leaders (6072 of them). Prints the phase trace of the
worst one: a hub leader 1 ms from two nodes that are 100 ms from each other. The
shortcut rates it 2 ms; the simulation gives 80 ms, because in PBFT each backup must
hear a Prepare from *another backup*, and the hub's two neighbours can only reach each
other slowly. The shortcut's pick is 34 ms (74%) slower than the real best (46 ms).

### `collusion_cases_n7.py` — hand-built f=2 collusion cases

n=7, f=2, nodes 1 and 2 Byzantine. At f≥2 the link between two colluders has no honest
endpoint, so AWARE's `max(M[i,j], M[j,i])` sanitization cannot stop them claiming it is
0 ms. Cases:

- **A/B**: uniform 10 ms. The fake 0 ms link changes nothing: a 5-node quorum needs
  many links and one free link does not move the k-th order statistic.
- **C/D**: node 3 genuinely a bit better. The fake link actually helps honest node 3
  (nodes 1 and 2, as backups, now swap Prepares instantly).
- **E**: colluders also inflate their links to nodes 3–5; they only make themselves
  look worse.

Takeaway: the lie is harmless in symmetric topologies; the next script finds where it
is not.

### `collusion_search_n7.py` — how often the fake link promotes a colluder

n=7, f=2. 60,000 random topologies (link delays from `{5, 10, 20, 50}`), true 1–2 link
fixed at 50 ms. Counts topologies where an honest node is truly best but the fake
0 ms claim makes node 1 or 2 the strict argmin: **23% of all trials**. Prints the two
clearest cases, e.g. node 2 predicted at 20 ms against an honest best of 60 ms while
its real latency is 65 ms. The latency cost is small; the damage is that a Byzantine
node gets leadership, and under a Fixed trigger it keeps it.

### `collusion_sweep_n7.py` — which topologies the attack works on

Same attack as `collusion_search_n7.py`, swept over ordinary-link delay sets
(`--values`, repeatable) and true 1–2 link delays (`--true12`, comma list; `max` means
the slowest ordinary link). For each cell it prints:

| Column | Meaning |
|---|---|
| honest best | an honest node is strictly the best leader in reality |
| lie active | the fake link lowers some colluder's predicted latency |
| avg gain | how much it lowers it, averaged over trials where it does |
| HIT | honest best **and** a colluder strictly wins after the lie (`--ties` counts ties too) |

```bash
python3 collusion_sweep_n7.py                                   # default sets, true12 = 50 and max
python3 collusion_sweep_n7.py --values 5,10,20,50 --true12 5,20,50,100,500
python3 collusion_sweep_n7.py --values 5,50 --true12 max --ties  # see how many near-hits are ties
```

Other flags: `--fake12` (claimed delay, default 0), `--trials` (default 6000),
`--seed` (default 1, same for every cell so rows are comparable).

What the defaults show:

- A uniform set (`10`) never hits: every leader ties, so no honest node is strictly best.
- Two-level sets (`5,10`, `5,50`) rarely hit strictly (4–5%) but about 35% with
  `--ties`: with two delay levels, leader scores collapse onto a few values.
- The hit rate rises as the true 1–2 link gets slower, and stops rising once it
  reaches the slowest ordinary link (`5,10,20,50`: 5 → 0%, 20 → 14%, 50 → 23%,
  100/500 → 23%). Past that point the quorum already skips the link.
- A wide set with the true link left at 50 (`…,500` / 50) drops to 10.5% because 50 is
  now a fast link; with the true link at `max` (500) it reaches 28.4%. Compare spreads
  with `--true12 max`, not a fixed value.

Default run takes about 30 s.

### `mitigations_n7.py` — which defence works

n=7, f=2, same attack. Counts promotions *caused by the lie* (Byzantine strictly best
with the lie, not without it).

- **Random topologies, 3000 trials — plain vs `remove_nodes_score`**: 31.9% vs 27.9%.
  Removing `f` helpers defends against *absent* nodes, not a *present, falsely fast*
  one; the lie still lowers a colluder's score by 18 ms on average (max 60).
- **Geometric topologies, 2000 trials — plain vs `triangle_clamp`**: 46.6% vs **0.0%**.
  If 1 and 2 really were 0 ms apart, every honest node would see them at the same
  distance; the clamp enforces that using links that each have an honest endpoint.
  Limits it also prints: the clamp alters 18.6% of honest matrices (1 ms rounding
  noise), and the liars can keep up to 33 ms of predicted gain, just never enough to
  win in these runs. The clamp assumes the triangle inequality; netem topologies that
  violate it (e.g. a star with 200 ms spokes and a 10 ms hub) would be wrongly
  raised.

**This 0% only holds for the naive attack (claim 0, nothing else).** Against an
attacker that also picks the fake value and inflates links, the clamp fails; see
`combined_attack_n7.py`.

### `combined_attack_n7.py` — optimized collusion vs six defences

n=7, f=2, geometric topologies, nodes 1 and 2 colluding. The colluders choose what to
report: the 1–2 link (0, 25, 50, 75 or 100% of its true value) and each of their links
to honest nodes, at or above the true value (sanitization blocks lower). Inflation
moves per link:

- **inflate**: raise to 1000 ms, to knock honest leaders' quorums off fast links.
- **equalize**: raise a colluder's faster link halfway or all the way to its partner's,
  to shrink the triangle bound `|D[1,c] − D[2,c]|` (attacking the clamp).

A hill-climber with random restarts searches these reports to make a colluder the
strict argmin. Each step's candidate reports are evaluated as one batch. It is a
heuristic, so the success rates are lower bounds. Each (topology, moves, defence)
attack has its own seeded RNG, so a subset run gives the same numbers as a full run.

| Defence | What it does |
|---|---|
| `plain` | the reported (max-sanitized) matrix as is, i.e. AWARE |
| `clamp` | `triangle_clamp` |
| `robust` | `triangle_clamp(ignore_top=f)`: drops the f largest triangle bounds per link |
| `vivaldi` | Vivaldi embedding, forces simply averaged |
| `median-trim` | Vivaldi, drop the f forces farthest from the median force, cap at median size (closest to Beware, minus HDBSCAN) |
| `median-outlier` | Vivaldi, drop up to f forces only if they are outliers, no cap |

The `honest (no lie)` row reports truthfully. Anything above 0% there is the defence
itself picking a colluder, from embedding error.

```bash
python3 combined_attack_n7.py --defenses plain,clamp,robust --trials 1000   # seconds
python3 combined_attack_n7.py --trials 400 --restarts 3                     # ~3 min on 40 cores
```

Results, 400 topologies (262 with an honest node truly best), 3 restarts:

| Attacker moves | plain | clamp | robust | vivaldi | median-trim | median-outlier |
|---|---|---|---|---|---|---|
| honest (no lie) | 0.0% | 0.0% | 0.0% | 0.8% | 22.5% | 3.8% |
| fake link only | 61.8% | 3.8% | 34.7% | 26.3% | 63.0% | 23.7% |
| fake + inflate | 86.6% | 53.8% | 71.0% | 87.0% | 94.7% | 87.0% |
| fake + equalize | 77.1% | 35.1% | 69.8% | 71.8% | 84.7% | 68.7% |
| fake + inflate + equalize | 87.4% | 65.3% | 79.0% | 95.4% | 95.0% | 91.6% |

Clamp rates vary between runs with different restart counts and seeds (for example
fake + inflate was 72.0% in an earlier 1000-topology, 6-restart run). The ordering
between defences is stable.

What it shows:

- **Inflation is the strongest attack, with or without the clamp.** At f=2 the colluders
  split roles: one inflates its links to honest nodes (making itself look bad) while
  the other keeps its good links and takes the lead. At f=1 a liar can only hurt
  itself, so this split is new at f≥2. It needs about one inflated link on average.
- **The clamp amplifies inflation.** The triangle bound `D[i,j] ≥ D[i,c] − D[j,c]` is
  only valid if `D[i,c]` is not overstated, but sanitized values are only guaranteed
  at or above the truth. With a Byzantine node as the third point `c`, one inflated
  link (e.g. 2–4 set to 1000) raises every link of honest node 4 to about 900–1000 ms,
  wiping out a good honest leader.
- **The robust clamp stops that poisoning but does not fix inflation**, which works
  without any clamp, and it weakens the bound on the fake link (3.7% → 33.6%).
- **Even the fake link alone beats the clamp occasionally (about 4%)** once the colluders
  pick a non-zero fake value, which reduces how much the clamp raises their other links.
- **Plain embedding partly absorbs the fake link** (62% → 26%): the fake force is
  averaged against five honest forces, so the embedded 1–2 distance lands between the
  claim and the truth (e.g. true 46, claimed 12, embedded 31).
- **Median filtering of forces makes it worse at n=7.** Once the embedding has moved
  the colluders together, the fake force is satisfied, near zero, and sits right at the
  median, while the honest forces that still disagree are the farthest from it and get
  dropped. The filter protects the lie (same example: embedded 16). `median-trim` also
  picks a colluder 22.5% of the time with no lie at all, because always dropping 2 of 6
  honest forces leaves the embedding inaccurate (about 14% error).
- **No defence stops inflation**, because a colluder that inflates all its links
  consistently just looks like a genuinely far node, which is a valid geometry that no
  consistency check can reject. A Byzantine node can also make itself far for real by
  delaying replies.
- The real extra latency of a promoted colluder is only 13–20 ms on average; the harm
  is that a Byzantine node leads, and under a Fixed trigger keeps leading.

Open question: an inflated link is hard to tell apart from a real one, because a
Byzantine node can genuinely refuse to help an honest leader by withholding votes.
The prediction assumes every node behaves the same whichever node leads, but colluders
can help their own leader and nobody else. Matrix sanitization and embedding do not
look sufficient at f≥2 and small n; limiting how long a prediction can pin the leader
(rotation among candidates, reselecting every generation) is the structural fallback.

Not tested: Beware's actual filter (HDBSCAN clustering of forces by direction, cosine
distance), which may behave differently from the per-axis median used here, and larger
n, where each node has many more forces to filter.

### `beware_attack_n7.py` — Beware's actual sanitizer vs Aware and the clamp

`beware_vcs.py` is a faithful port of Beware's clustering VCS
(`references/beware/src/python/beware/vcs/newton/newton_model.py`, `observe_cluster`):
HDBSCAN on cosine distance with `min_cluster_size = int(0.9·(n−f))` and
`min_samples = 1`, forces above the cluster's median size capped to it, a step size that
shrinks over the rounds, nodes updated one at a time, and the round whose embedding
best fits the reported matrix kept. It reads the raw reported matrix, and the force on
node i from node j uses j's own report, as the original does. It needs scikit-learn and
takes about 18 s per embedding at the paper's 1000 rounds.

Reports are a raw matrix: honest rows are true, and the colluders (nodes 1, 2) choose
their own rows. Aware and the clamp see `max(R[i][j], R[j][i])`; Beware sees the raw
matrix. The search is too slow for a hill-climber, so each topology gets a fixed set
of attacks:

- Beware's own three attacks, exactly as their benchmark code builds them
  (`scripts/run_vcs_benchmarks.py`): each Byzantine row ÷3 (deflation), ×3
  (inflation), or ÷3 between colluders and ×3 to honest nodes (inflation–deflation).
- The fake 0 ms link, and the role split (one colluder inflates its honest links ×3 or
  ×10 while the other leads), with and without the fake link, with and without the
  leader also deflating its honest links, and with both role assignments.

"Best of all" lets the colluders pick whichever attack works on that topology.

```bash
python3 beware_attack_n7.py --trials 120              # ~10 min on 40 cores
python3 beware_attack_n7.py --trials 20 --rounds 200  # quick look
```

Results, 120 geometric topologies (72 with an honest node truly best), 1000 rounds:

| Attack | Byzantine leads: aware | clamp | beware | Extra real latency: aware | clamp | beware |
|---|---|---|---|---|---|---|
| honest | 0.0% | 0.0% | 0.0% | 0.0 | 0.0 | 0.0 |
| Beware's deflation | 47.2% | 0.0% | 11.1% | 6.0 | 2.1 | 3.7 |
| Beware's inflation | 0.0% | 1.4% | 13.9% | 4.8 | 11.4 | 3.5 |
| Beware's inflation–deflation | 1.4% | 1.4% | 23.6% | 4.9 | 10.8 | 5.5 |
| fake link = 0 | 65.3% | 0.0% | 13.9% | 9.6 | 3.1 | 2.2 |
| role split, best variant | 12.5% | 1.4% | 20.8% | | | |
| **best of all attacks** | **66.7%** | **2.8%** | **58.3%** | **12.9** | **24.4** | **11.2** |

What it shows:

- **By Beware's own metric (latency of the chosen configuration) Beware is best**
  (11.2 ms extra against 12.9 for Aware), which qualitatively matches the paper.
- **By leadership capture it barely helps**: a Byzantine node is picked in 58.3% of
  topologies against 66.7% for Aware. On Beware's own inflation and
  inflation–deflation attacks it is captured more often than Aware (13.9% and 23.6%
  against 0.0% and 1.4%), because those attacks barely move Aware's ranking but do
  move the embedding.
- **With honest reports Beware is accurate**: no false promotion and no latency loss.
- **The clamp resists capture against this set of attacks (2.8%) but pays in latency**
  (24.4 ms extra, the worst): inflated reports poison honest links and push it to a
  worse honest leader. This set has no targeted single-link inflation; the adaptive
  search in `combined_attack_n7.py` beats the clamp 54–65% of the time.
- 72 eligible topologies means roughly ±11 points of sampling noise around 50%.

Gaps in Beware's own evaluation, from reading `scripts/run_vcs_benchmarks.py`:

- The code's "deflation" divides each Byzantine node's **whole** row by 3, not only
  links between colluders as the paper describes. Aware's max then restores every
  honest link from the honest node's own report.
- All attacks are fixed ×3 / ÷3 on the Byzantine rows; none adapts to the defence.
- The reported metric is the latency of the chosen configuration on the true matrix,
  assuming everyone then behaves honestly. Whether a Byzantine node ends up as leader
  is never measured.

Not tested: Beware against an adaptive attacker (too slow at 18 s per embedding), and
Beware at n=4, f=1, where each node has only 3 forces and `min_cluster_size` is 2.

### `single_liar_n4.py` — what one Byzantine node can do at n=4, f=1

Node 1 lies. After `max(M[i,j], M[j,i])` it can only report its own three links at or
above the truth, so the search tries every combination of {true, ×2, ×5, 1000 ms} on
them, against Aware, the clamp and the robust clamp. "Worst regret" is the extra real
latency of the leader chosen under the most damaging report.

| Topologies (2000 each) | Defence | Regret, honest reports | Liar gets chosen | Worst regret under attack |
|---|---|---|---|---|
| geometric (triangle inequality holds) | aware | 0.0 ms | 0.0% | 8.7 ms |
| | clamp | 0.0 ms | 0.0% | 8.8 ms |
| random {5,10,20,50} (often violates it) | aware | 0.0 ms | **10.4%** | 5.7 ms |
| | clamp | **3.8 ms** | 0.0% | 10.4 ms |

On the netem one-far-node topology (liar far or near) nothing the liar reports changes
the chosen leader.

How a single liar gets chosen by Aware on random delays: it inflates a link it does not
need itself but the best honest leader does. In the example the script finds, liar 1
reports link 1–2 as 100 ms instead of 50. Honest leader 4's backup 2 needs a Prepare
from another backup and loses the one via node 1, so leader 4 goes from 60 to 75 ms
while the liar stays at 65 and becomes the argmin. This was never seen on geometric
topologies.

## Reproducibility

Random scripts use fixed seeds (`collusion_search_n7.py` and the random half of
`mitigations_n7.py`: seed 1; geometric half: seed 7), so reruns print the same numbers.
`collusion_search_n7.py` and `mitigations_n7.py` take about 25 s each,
`shortcut_flip_n4.py` about 10 s, the rest under a second.
