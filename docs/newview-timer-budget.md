# How long the new-view timer has to be at n = 4, 7, 10 (2026-10-14)

**Question.** How long does a view change take, from the moment a replica has 2f+1 ViewChanges to the moment it installs the NewView? How does that scale with node count, P-set size (window) and batch size? Today the new-view timer shares one value with the leader-progress timer: 150 ms for Fixed and Perf (`TriggerManager`, `node/triggerManager.go`).

**Short answer.**
- **The NewView itself takes 20–75 ms at any n.** That is the leader's build, the transfer, and every replica's processing.
- **What breaks 150 ms at n=7 and n=10 is ViewChange verification.** Every node checks every other node's cert signatures, all on one machine. The primary can reach its quorum hundreds of ms after a replica. Each replica's event loop is also still checking the rest of the ViewChanges when the NewView arrives.
- **With today's code on a 16-CPU slice,** a replica needs up to about 290 ms at n=7 and about 890 ms at n=10 (290 certs, batch 50).
- **Phase 3, now implemented, skips prepares a node already verified.** When a node holds the prepares (the healthy case), timer need drops to 23–35 ms at n=4, 34–58 ms at n=7 and 45–73 ms at n=10. 150 ms then covers all three node counts with at least 2× margin. See "With Phase 3" below.

Code measured: branch `vc-compact-certs`, commit `e44ed6b` plus the uncommitted Phase 2 (NewView names ViewChanges by digest). The "With Phase 3" section adds the uncommitted Phase 3. Harness: `node/newview_timing_test.go` (`TestNewViewTimingBudget`). The tables before that section were measured before Phase 3; they equal the miss case of Phase 3.

## What the branch changes

The view-change work on `vc-compact-certs`, one commit per phase, on top of `viewid` `56cf434`.

**Phase 1a: compact certs** (commit `e44ed6b`)
- **Wire format:** cert prepares go on the wire as (from, signature) only. View, seq and digest are rebuilt from the cert's pre-prepare on decode (`PreparedCertToPB` / `PreparedCertFromPB`, new proto message `CertPrepare`). The rebuilt messages are byte-identical to the signed ones.
- **`buildPreparedCert` cap:** it keeps only prepares that match the pre-prepare's view, seq, digest and sender, at most 2f, in ascending order.
- **Sender check:** `Deliver` rejects a Prepare or Commit whose payload `From` is not the envelope sender. Before, a node could cast votes under another node's id.
- **Effect:** ViewChange 651 → 596 KiB at n=7.

**Phase 2: NewView names ViewChanges by digest** (commit `baff766`, `node/newviewrefs.go`)
- **Refs instead of copies:** the NewView carries `(sender, sha256 of the signed ViewChange)` refs instead of the 2f+1 ViewChanges, and is signed again. `Deliver` checks that signature.
- **Replicas use only their own copies:** each replica resolves the refs against ViewChanges it verified itself, so a primary cannot alter one.
- **Fetch:** if a referenced ViewChange is missing, the NewView waits as pending. After `view_change_fetch_delay_ms` (20 ms, one retry at 40 ms) the replica asks the primary, which forwards the sender's original signed ViewChange.
- **Dedup:** per-sender ViewChange dedup is back on. A second, different ViewChange from a counted sender is kept only if a pending NewView names it, and never counts.
- **O-set check:** the O-set must also be complete, with no missing or repeated seq.
- **Not done:** the NewView sender is not checked against the view's primary; a `TODO(safety)` marks it.
- **Effect:** NewView 3.5 MiB → 514 KiB at n=7, and no longer dependent on n.

**Phase 3: skip prepares already verified** (`node/view.go`)
- **The skip:** `verifyVC` counts toward a cert's 2f every prepare this node already holds byte for byte as a verified one (`certsLeftToVerify` / `prepareAlreadyVerified`). It signature-checks only the rest.
- **Also:** a ViewChange below `forView` is dropped before any cert check, instead of after.
- **Effect:** timer need at n=7 on 16 CPUs 289 → 34 ms when the node holds the prepares (see "With Phase 3").

**Not done yet:** dropping the per-txn digest lists from certs and the O-set (rest of Phase 1). That would take the ViewChange to about 114 KiB and the NewView to 32 KiB at n=7.

## Method

Every stage runs the real code on real nodes:
- Nodes are built with `NewNode` from `config/run2new.json`: `carry_state` false, `parallel_workers` true.
- Every node is prepared at seqs 1..certs above stable checkpoint 0.
- Prepare signatures are real Ed25519, 2f per cert.

| stage | what runs | before or inside the timer |
|---|---|---|
| VC build | `VC()`: P-set, marshal, sign | before |
| VC transfer | 1 sender → n−1 receivers over loopback gRPC with production options. Timed until every receiver's real `Deliver` (outer signature, digest, decode) has queued it | before |
| VC verify | `verifyVC` (cert prepare signatures, parallel). Run by one node alone, and by **all n nodes at once**, as on the shared experiment box. Times per node to its quorum (2f others) and to all n−1 | quorum: before; the rest: inside |
| NV build | primary's `newview()`: O-set signing, local install, refs, sign | inside |
| NV transfer | as VC transfer, for the NewView | inside |
| NV handle | `HandleNewView`, by one replica alone and by **all n−1 at once**. The run fails unless every replica installs the view | inside |

**What "timer need" adds up.** It's the worst ordering for one replica:
1. **Quorum skew:** this replica reaches its quorum first, and the primary last (worst node minus first node).
2. **Backlog:** the replica's loop still verifies its remaining ViewChanges (all minus quorum).
3. The primary's `newview()`.
4. The NewView transfer.
5. The replica's own `HandleNewView`, with all replicas running at once.

**Setup details:**
- Nodes are marked `dead` so their own sends return at once; transport is timed separately.
- The run includes production logging and the goroutine spawns of `asyncBroadCast`. It doesn't include the gRPC cost of the C×(n−1) prepares sent after install.
- Medians of 5 reps for the batch-50 table, 3 elsewhere. GC runs before each stage.

**Box:** 2 × Xeon E5-2683 v3 (Haswell, 2.0 GHz, 14 cores each, 56 threads), performance governor, idle. Each config ran twice:
- unpinned (56 CPUs);
- pinned with `taskset` to **16 CPUs**: 8 cores plus their hyperthreads, on NUMA node 0. This is the slice `scripts/run_isolated.sh` gives a run, and **the 16-CPU numbers are the ones to use.**

An Ed25519 verify plus marshal costs about 100 µs here.

## Sizes (CPU-independent)

| n | certs | batch | ViewChange KiB | VC without per-txn lists | NewView KiB | NV without per-txn lists |
|---|---|---|---|---|---|---|
| 4 | 290 | 10 / 50 / 100 | 171 / 556 / 1038 | 74 | 129 / 514 / 996 | 32 |
| 7 | 290 | 10 / 50 / 100 | 211 / 596 / 1077 | 114 | 129 / 514 / 996 | 32 |
| 10 | 290 | 10 / 50 / 100 | 250 / 635 / 1117 | 153 | 129 / 514 / 996 | 32 |
| 7 | 350 | 50 | 719 | 137 | 621 | 39 |
| 7 | 500 | 50 | 1027 | 196 | 886 | 55 |
| 10 | 500 | 100 | 1926 | 265 | 1717 | 55 |

- **The NewView no longer depends on n,** since it names ViewChanges by digest.
- **Both messages scale with batch size,** because of the per-txn digest lists.
- **Dropping the lists (rest of Phase 1)** leaves the ViewChange growing only with 2f prepares per cert, and leaves the NewView at 32–55 KiB.

## Stage times, 16 CPUs, batch 50 (ms)

| n | certs | verify 1 VC | node alone to quorum | all nodes at once: first / worst to quorum | all nodes at once: worst to all n−1 | NV build | NV send per peer | NV fan-out | NV until all replicas queued | NV handle alone / all at once | **timer need** |
|---|---|---|---|---|---|---|---|---|---|---|---|
| 4 | 290 | 7 | 15 | 43 / 65 | 85 | 7.5 | 0.8 | 2.9 | 8.6 | 2.9 / 6.2 | **64** |
| 4 | 350 | 8 | 17 | 50 / 83 | 101 | 8.5 | 0.9 | 3.4 | 10.3 | 3.3 / 7.6 | **77** |
| 4 | 500 | 12 | 25 | 76 / 116 | 146 | 11.3 | 0.7 | 3.7 | 12.9 | 4.6 / 10.9 | **105** |
| 7 | 290 | 15 | 59 | 322 / 436 | 579 | 7.8 | 0.7 | 3.9 | 13.5 | 3.6 / 12.2 | **291** |
| 7 | 350 | 17 | 67 | 419 / 552 | 700 | 9.2 | 0.8 | 4.6 | 14.6 | 4.1 / 14.7 | **319** |
| 7 | 500 | 25 | 97 | 531 / 811 | 997 | 12.4 | 1.3 | 5.7 | 22.7 | 5.7 / 21.8 | **523** |
| 10 | 290 | 22 | 131 | 997 / 1467 | 1843 | 7.9 | 0.9 | 4.7 | 17.2 | 4.0 / 19.2 | **890** |
| 10 | 350 | 25 | 152 | 1259 / 1734 | 2227 | 9.5 | 1.2 | 6.0 | 18.9 | 4.6 / 22.4 | **1019** |
| 10 | 500 | 38 | 219 | 1894 / 2448 | 3189 | 13.2 | 1.7 | 9.2 | 25.5 | 7.4 / 32.8 | **1366** |

**Timer need split into its parts:**

| n | certs | quorum skew | backlog | NewView path (build + transfer + handle) |
|---|---|---|---|---|
| 4 | 290 | 21 | 20 | 22 |
| 7 | 290 | 114 | 143 | 34 |
| 10 | 290 | 469 | 377 | 44 |
| 10 | 500 | 554 | 741 | 72 |

**Batch size barely moves verification.** It is set by certs × 2f, not by the lists. Batch shows up only in NewView transfer, about 5 → 23 ms at n=7 for batch 10 → 100.

**On 56 CPUs** verification is about 3× faster. Worst to quorum is 132 ms at n=7 and 454 ms at n=10 (290 certs). The NewView path doesn't change.

## Reading it

1. **The NewView is no longer the problem.**
   - On the leader, putting the NewView on one peer stream (`stream.Send`) takes about 1 ms, and the whole fan-out 3–9 ms.
   - Replicas have it decoded and signature-checked within 9–26 ms.
   - `HandleNewView` takes 3–7 ms alone, or 6–33 ms when all replicas run it at once on 16 CPUs.
2. **ViewChange verification grows with n².**
   - Every node checks (n−1) ViewChanges × certs × 2f signatures. At n=10 with 290 certs that is about 157k Ed25519 verifies across the cluster per view change, about 16 CPU-seconds on one box.
   - It hurts the timer twice: the primary can get its quorum late (skew), and a replica's loop is still checking the rest of the ViewChanges when the NewView arrives (backlog).
3. **Against 150 ms:**
   - **n=4:** fits up to the watermark cap (105 ms).
   - **n=7:** about 2× over at today's window (291 ms).
   - **n=10:** about 6× over (890 ms).
   - This matches the earlier observation that the problem came back at 7 nodes.

## With Phase 3: skip prepares already verified (16 CPUs, batch 50, ms)

**What it does.** `verifyVC` (`node/view.go`) first runs a pass on the event loop, `certsLeftToVerify`. It counts toward a cert's 2f every prepare this node already holds, byte for byte, as one it verified:
- either in the slot's current-view `prepares`;
- or in its `preparedProof`.

Only the other prepares are signature-checked, in parallel as before.

**Why it's safe.** Every held prepare is either this node's own, or came through `Deliver`. `Deliver` checked its signature against its sender, and checked that the sender is `PrepareMsg.From`; that sender check came with Phase 1a. Ed25519 verification depends only on key, message and signature, so a byte-identical copy would verify again.

Each check logs `VC CERTS from=… certs=… prepares cached=… to_verify=… took=…`, so a live run shows the real hit rate.

**Warm vs cold.** *Warm* means verifiers hold every prepare. That's what a healthy node has after the view that just failed, and what the benchmark builds by default. *Cold* means they hold none (`NVTIMING_COLD=1`), which is the old cost and reproduces the tables above.

| n | certs | verify 1 VC warm / cold | worst to quorum warm / cold | skew + backlog warm / cold | NewView path | **timer need warm / cold** |
|---|---|---|---|---|---|---|
| 4 | 290 | 0.1 / 7.7 | 0.4 / 65 | 0.4 / 42 | 23 | **23 / 64** |
| 4 | 500 | 0.2 / 12.7 | 0.6 / 114 | 0.5 / 73 | 35 | **35 / 109** |
| 7 | 290 | 0.2 / 15.3 | 1.1 / 465 | 0.9 / 255 | 33 | **34 / 289** |
| 7 | 350 | 0.2 / 17.7 | 1.3 / 584 | 1.1 / 330 | 39 | **40 / 370** |
| 7 | 500 | 0.3 / 25.6 | 1.8 / 798 | 1.6 / 480 | 56 | **58 / 535** |
| 10 | 290 | 0.2 / 22.5 | 2.1 / 1480 | 1.9 / 834 | 43 | **45 / 878** |
| 10 | 350 | 0.3 / 26.3 | 2.4 / 1752 | 2.1 / 1052 | 52 | **54 / 1103** |
| 10 | 500 | 0.4 / 38.5 | 3.5 / 2414 | 3.2 / 1214 | 69 | **73 / 1288** |

**What remains is the NewView path:** the primary's `newview()`, the transfer, and `HandleNewView`. The warm column is a best case.

**Real misses.** A real node misses the prepares it never received, for example ones still in flight near the window tip. It also misses certs from a view older than both its slot's current view and its prepared cert. Each miss costs one signature check, about 100 µs.

## What to set, and what to fix

**With Phase 3**, warm, 16 CPUs, today's window:
- **Timer need:** 23 ms (n=4), 34 ms (n=7), 45 ms (n=10). At the 500-cert cap: 35, 58 and 73 ms.
- **The current 150 ms covers all three node counts** with at least 2× margin, if the live hit rate is close to the benchmark's.

**Without Phase 3**, or when a node misses most prepares (for example a lagging node), the cold numbers apply. The rest of this section is the analysis from before Phase 3.

**Timer values with the pre-Phase-3 code**, on a 16-CPU slice, with about 2× margin over timer need. These assume today's window: 290 certs, batch 50.

| n | timer need | new-view timeout |
|---|---|---|
| 4 | 64 ms | 150 ms (today's value) is enough |
| 7 | 291 ms | about 600 ms |
| 10 | 890 ms | about 1.8 s |

At the watermark cap (500 certs) these roughly double for n=7.

These values are long: every view whose primary has failed would cost that much. Two changes would let a short timer work instead.

- **Phase 3, skip re-verifying prepares** (now implemented; see above).
  - Most prepares in a peer's cert are ones this node already verified when they arrived, with identical bytes and signature.
  - Checking only the unseen ones would take skew and backlog close to zero. That leaves the NewView path: 22–35 ms at n=4, 34–57 ms at n=7, 44–72 ms at n=10 across the window range.
  - 150 ms would then cover all three node counts with at least 2× margin.
  - It would also bring the quorum itself about 0.4 s (n=7) to 1.5 s (n=10) earlier.
  - This is the highest-value next step for n ≥ 7, ahead of the rest of Phase 1. The rest of Phase 1 cuts NewView transfer by 10–20 ms and ViewChange size about 5×.
- **Separate new-view timeout from the progress timeout.** Today one value serves both. Making the new-view timeout scale with n, or adding exponential backoff (`docs/newview-cost-and-viewchange-storm.md` §6.1), keeps a short progress timer for detecting a stalled leader.

## Caveats

- **Hardware.** This box is older and slower per core than the experiment host. Re-run the test there; it takes about 2 min on 16 CPUs:
  ```bash
  NVTIMING_BATCH=50 NVTIMING_REPS=5 NVTIMING_CSV=nvtiming.csv \
    taskset -c <the run's 16 CPUs> go test ./node -run TestNewViewTimingBudget -v -timeout 3600s
  ```
  Restrict the sweep with `NVTIMING_NODES`, `NVTIMING_CERTS`, `NVTIMING_BATCH`.
- **No network delay.** Under the NetworkDelay scenario (170 ms per hop), add one hop to the NewView transfer, and to skew, since ViewChanges arrive later too.
- **Scope of "timer need".**
  - It excludes the prepare/commit round after install, which is what the leader-progress timer then has to cover.
  - It excludes the gRPC cost of each replica sending C×(n−1) prepares, which competes for the same CPUs.
- **ViewChange transfer** was timed from one sender. All n sending at once is slower, but that happens before the new-view timer starts.
- **Concurrency is goroutines in one process** sharing the 16 CPUs, not n separate processes. The CPU contention is the same; GC and scheduling details differ.
