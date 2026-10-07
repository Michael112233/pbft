# Targeted throttling: hypotheses and results per arm

Condensed from `docs/throttle-prep-sweep.md` (§ numbers below point there). Covers the
attacker's strategy, the predicted behaviour of each of the four arms, and the `prep_ms`
sweep (500 / 3000 / 8000 ms) that tested them.

---

## 1. Setup

| | |
|---|---|
| Nodes | N = 7, f = 2 |
| Throttle slots | k = 2: the attacker can gate at most 2 nodes at once |
| Prep | `prep_ms`: a slot pointed at a new node goes live only `prep` after the retarget |
| Unthrottled rate F | **163 batches/s** (≈ 8 150 req/s, 50 req/batch), the offered load |
| Throttled rate R | **10 batches/s** (500 req/s) |

**The impairment is the same in every arm.** A gated leader may propose at most one batch
per 100 ms (`proposal_min_interval_ms`), which caps it at 10 batches/s, a 16× cut. The
severity is deliberately the same for all four arms. Only *who* gets gated and *when*
depends on the action.

---

## 2. The attacker's optimal strategy

### 2.1 Against RoundRobin: prepare the next leader in advance

The RoundRobin schedule is public. At the notice that leader *c* has taken over, the
attacker keeps the slot already on *c* and retargets the other slot to *c+1*. So the next
leader's gate starts preparing **one full tenure τ before it takes over**:

```
gate goes live (prep − τ) into the leader's own tenure

  prep ≤ τ        -> gate is live before the leader starts -> 100% throttled
  τ < prep < 2τ   -> gated for (2τ − prep) of τ
  prep ≥ 2τ       -> never gated
```

As long as prep is shorter than the tenure, **every leader is throttled from its first
batch**: 100% throttling.

### 2.2 Against Election: camp if held, retarget if not

The next leader is unknown until it is drawn, so the attacker can only react. With k = 2
the two slots hold the last two distinct leaders. When a leader is drawn:

- **Same leader as a held slot (P = 2/7):** keep that slot (camp). The gate is already
  live, so the leader is **gated from the start**.
- **Different leader (P = 5/7):** retarget the stalest slot to it. The leader runs
  **ungated for `prep`**, then the gate lands mid-tenure.

---

## 3. Hypotheses per arm

### 3.1 PeriodicRoundRobin (PRR)

τ is pinned at 10 s, and every prep tested (0.5, 3, 8 s) is under 10 s. By §2.1, every
successor's gate is live before it takes over.

> **Hypothesis: 100% throttled at every prep, ≈ 10 batches/s ≈ 500 req/s.**
> (The law breaks only once prep > 10 s, which was not tested.)

### 3.2 PeriodicElection (PElec)

τ = 10 s for every leader. The 2/7 ÷ 5/7 split decides the profile:

| Draw | P | Tenure (10 s) |
|---|---|---|
| held | 2/7 | 10 s at R |
| fresh | 5/7 | `prep` s at F, then (10 − prep) s at R |

```
throttled = q + (1 − q)·(10 − prep)/10                         q = 2/7
rate      = q·R + (1 − q)·(prep·F + (10 − prep)·R)/10
```

The 2/7 held leaders are lost regardless. **As prep grows, the 5/7 fresh leaders get a
longer ungated stretch, so throughput rises linearly with prep.**

| prep | throttled | req/s |
|---|---|---|
| 500 | 96% | ~770 |
| 3000 | 79% | ~2 140 |
| 8000 | 43% | ~4 870 |

### 3.3 PerformanceElection (PerfElec)

Same 2/7 ÷ 5/7 split, but the perf bar now sets the tenure instead of a 10 s clock.

**Once the gate lands, the leader is gone at the next tick.** The bar (~150) sits about
92% of the way from R (10) up to F (163). After a leader has run at F for *u* seconds and
then at R, its running average drops below the bar about 0.09·*u* seconds after the gate
lands. So the first 1 s tick after the gate lands evicts it.

| Draw | P | Tenure | τ |
|---|---|---|---|
| held | 2/7 | gated from batch one, evicted at the first tick (window opens 3 batches = 0.3 s in) | **~1.3 s**, all at R |
| fresh | 5/7 | `prep` s at F, survives every tick, gate lands at `prep`, evicted at the next tick | **~prep + 1 s**: prep at F, ~1 s at R |

Worked ticks, fresh leader:

- **prep 500:** gate lands at 0.5 s. At tick 1 the average is (0.5·163 + 0.5·10) = 86.5 < bar,
  so it is evicted.
- **prep 3000:** ticks 1–3 at F pass. At tick 4 the average is (3·163 + 10)/4 = 124.8 < ~156,
  so it is evicted.
- **prep 8000:** ticks 1–8 at F pass. At tick 9 the average is (8·163 + 10)/9 = 146 < ~162,
  so it is evicted.

**With increasing prep:** a held leader always costs a fixed ~1.3 s, and a fresh leader
always loses ~1 s, while its ungated stretch grows with prep. So the throttled fraction
falls and throughput rises. PerfElec should beat PElec at every prep, because a gated
leader is evicted within ~1 s instead of sitting out a 10 s period.

| prep | throttled | req/s (model, d = 0.1 s VC dead time) |
|---|---|---|
| 500 | ~61% | ~2 760 |
| 3000 | ~33% | ~5 400 |
| 8000 | ~16% | ~6 800 |

### 3.4 PerformanceRoundRobin (PerfRR)

RoundRobin lets the attacker prepare ahead, but under Perf, as in §3.3, **a leader is evicted
on the tick right after its gate lands**. So each tenure is about as long as the stretch
before the gate lands, rounded up to a tick, and that sets how far ahead the *next* gate
was prepared:

- **prep 500:** prep is shorter than any tenure, so every leader is gated from the start
  and evicted at tick 1. **100% throttled, same as PRR.**
- **prep 3000 / 8000:** a long tenure means the next leader's gate lands early, so its
  tenure is short (tick 1). A short tenure gives the following leader almost `prep` ungated,
  so it survives to tick prep − 1. Tenures **alternate short ↔ long**: 1 ↔ 2 ticks at 3000,
  1 ↔ 7 ticks at 8000. Each leader is gated only for the second or so before its tick.

> **Hypothesis: crushed like PRR at prep 500; at larger prep, the throttle bites only
> briefly per tenure, so PerfRR rises to perf-trigger levels close to PerfElec.**

(The pre-registered model at 3000 was a τ ≈ 2 s fixed point, ~50% throttled, ~4 100 req/s.
The alternation above is what the runs showed. It is the same model at the larger
measured VC dead time d ≈ 0.3–0.4 s, §10.3.)

---

## 4. Results: `prep_ms` sweep (§9, §10)

### 4.1 Measured vs model

| prep | Arm | **Measured req/s** | Model req/s | **Measured throttled** | Model throttled |
|---|---|---|---|---|---|
| 500 | PRR | **512** | ~500 | **100%** | 100% |
| | PElec | **791** | 782 | **96.2%** | 96.3% |
| | PerfRR | **449** | ~455 | **100%** | 100% |
| | PerfElec | **2 481** | ~2 760 | **72.6%** | ~61% |
| 3000 | PRR | **516** | ~500 | **100%** | 100% |
| | PElec | **2 153** | 2 190 | **78.3%** | 77.9% |
| | PerfRR | **4 956** | 4 929 | **40.2%** | 42% |
| | PerfElec | **5 628** | ~5 400 | **31.7%** | ~33% |
| 8000 | PRR | **527** | ~500 | **100%** | 100% |
| | PElec | **4 879** | 5 011 | **42.5%** | 41.0% |
| | PerfRR | **6 857** | 7 080 | **16.3%** | 14% |
| | PerfElec | **6 998** | ~6 800 | **14.4%** | ~16% |

### 4.2 Which arm wins at which prep

| prep | PRR | PElec | PerfRR | PerfElec | Winner | Runner-up | Plane that decides |
|---|---|---|---|---|---|---|---|
| 500 | 512 | 791 | 449 | **2 481** | **PerfElec** (3.1× PElec) | PElec | **Policy**: RoundRobin is pre-prepped and crushed under both triggers; only Election escapes |
| 3000 | 516 | 2 153 | 4 956 | **5 628** | **PerfElec** (1.14× PerfRR) | PerfRR | **Trigger**: both Perf arms beat both Periodic arms |
| 8000 | 527 | 4 879 | 6 857 | **6 998** | **PerfElec ≈ PerfRR** (2% apart, within noise) | — | **Trigger** |

(req/s)

**Takeaways**

- **PerfElec wins at every prep tested.** The optimal action does not move along the prep
  axis. No prep makes a Periodic arm win.
- **The plane that matters does move.** With short prep, the **policy** plane decides:
  RoundRobin's predictability lets the attacker gate every leader, so Election is the only
  escape. With long prep, the **trigger** plane decides: Perf evicts a gated leader within
  a tick, while Periodic leaves it gated for the rest of a 10 s period.
- **PRR is flat at ~500 req/s throughout.** That is the attacker's best case, and §2.1
  predicts it exactly.

### 4.3 Open points

- **PerfElec vs PerfRR at 8000:** 2% apart in single runs. Repeat before ranking them.
- **All election runs share one draw sequence** (q = 0.263). The comparison across prep is
  paired, but the absolute numbers rest on one sample path.
- **The VC dead time d ≈ 0.3–0.4 s for ungated leaders** and the **post-VC burst** that
  inflates PerfElec's bar are inferred, not yet confirmed from the logs.
