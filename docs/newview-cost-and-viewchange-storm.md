# NewView cost and the view-change storm (2026-09-06)

This document records an investigation into a run where nodes 2 and 4 could
never become leader: they process a view change, install a new view, and their
leader-progress timer expires almost immediately, triggering the next view
change. Views 12 onward degrade into a permanent view-change storm where no
view ever makes progress.

It covers: the observed symptom, the root cause, why the "stale timer fire"
hypothesis is wrong, a measurement harness
([`node/newview_cost_test.go`](../node/newview_cost_test.go)) with numbers, what
the PBFT literature says about signing NewView, and the fixes ranked by impact.

Config for the run: [`config/run2new.json`](../config/run2new.json) —
`leader_type: roundrobin`, `performance_trigger: true`, `performance: true`,
`fixed: true`, `proposal_delay_node: 2`, `proposal_delay_ms: 2000`,
`max_batch_size: 30`, `carry_state: true`, `dummy_account_count: 100000`.
Build: Go 1.26 toolchain, `go 1.25.0` in [`go.mod`](../go.mod). Box: 64-core
Xeon Gold 6142 (Skylake-SP, no `sha_ni`).

Timer constants ([`node/viewtimers.go`](../node/viewtimers.go)):
`leaderProgressTimeout = 90ms`, `newViewTimeout = 90ms`. `CHECKPOINT_INTERVAL = 250`
([`node/node.go`](../node/node.go)).

---

## 1. The symptom

From the four `logs/node_*.log` files:

| view parity | how it is entered | round-robin primary | outcome |
|---|---|---|---|
| odd (5, 7, 9, 11, …) | via timeout ~300 ms after the even view died | node 1 / node 3 | runs ~12 s, then a perf trigger fires |
| even (4, 6, 8, 10, …) | via **perf trigger**, exactly on a checkpoint boundary | node 2 / node 4 | **dies in ~90–160 ms** with `Leader progress timer expired` |

Round-robin primary is `((view-1) mod 4) + 1`, so the even views always land on
node 2 or node 4. They are not special replicas; they are simply the primaries
of the views that are always entered the expensive way.

`maxRecentViewThroughput` in the logs confirms it: `view 5` and `view 7` have
final throughput data (they ran); `view 4` and `view 6` have "no throughput
data" (their primaries never made progress).

From view ~11 (`17:20:03.85`) onward every node times out in every view every
~100–190 ms and never recovers.

### Node 4, view 8 — the canonical trace

```
17:19:42.240914  Throughput 270.99 below target ... seq 13750        <- checkpoint boundary
17:19:42.241024  Starting perf view change ... next for view 8
17:19:42.241034  Stable checkpoint which will be used for vc is seq 13500   <- OLD checkpoint
17:19:42.241134  createVCContent carried 253 prepared certs; forView 8
17:19:42.258779  Stable checkpoint advanced ... seq=13750             <- stabilizes 17 ms too late
17:19:42.313583  Became leader for new view 8 and my id is 4
17:19:42.315433  (createO + perf update done)
17:19:42.381959  No buffered consensus messages to replay for view 8  <- 66 ms: marshal + sign 4.3 MiB
17:19:42.452289  HUB: node stream send for MsgNewViewMessage took 40.71461ms ... size_mib=4.358
17:19:42.472976  Leader progress timer expired; entering view change  <- 91 ms after acceptNewViewTimers
```

For contrast, node 1's NewView for view 9: `carried 3 prepared certs`,
`size_bytes=57502` (0.055 MiB), sent in ~0.5 ms, view runs 12 s.

---

## 2. Root cause

### 2.1 The perf trigger fires on the checkpoint boundary, before the checkpoint stabilizes

`observeExecutedSlotForThroughput` can only return "below target" (the value
that triggers `perfVC`) when `seq % CHECKPOINT_INTERVAL == 0`
([`node/throughputperformance.go:62`](../node/throughputperformance.go)). So
every perf-triggered view change begins at the exact moment a fresh local
checkpoint has been created (by executing seq `k·250`) but is **not yet
stable** — stabilization needs 2f+1 remote CHECKPOINT votes, and in the trace
the third vote arrives 17 ms later.

`VC()` snapshots `GetLastStableCheckpointwithProofandBalances()` and
`createVCContent()` at that instant
([`node/roundrobin.go:20`](../node/roundrobin.go)). The last **stable**
checkpoint is still 13500, so `createVCContent`
([`node/view.go:27`](../node/view.go)) walks `lastStableCheckpointSeq+1 ..
maxSeqNum` = 13501..13753 and packs **253 prepared certs**.

### 2.2 253 carried certs is not a bug — it is a legitimate PBFT state

`preparedProof` is set in `tryAdvancePrepare` and cleared only by `GCLog`, which
runs when a checkpoint becomes stable
([`node/consensuslog.go:157`](../node/consensuslog.go)). So the P-set grows from
~0 right after a stabilization to a full `CHECKPOINT_INTERVAL` right before the
next one. A crash-triggered view change lands at an arbitrary point in that
cycle. The hard ceiling is the high watermark:

```go
l.high = l.low + 2*CHECKPOINT_INTERVAL - 1   // consensuslog.go:164
```

so **up to 500 prepared certs is protocol-legal** and must be survivable. This
matches Castro-Liskov PBFT, where the P-set is bounded by `H = h + 2L`.

### 2.3 The carried certs produce a multi-MB NewView

`createO` uses `minS = vcMsgSigs[0].CheckpointSeqNumber + 1`
([`node/view.go:607`](../node/view.go)) and re-proposes every prepared seq in
`(minS, maxSeq]`. In this implementation each O-set pre-prepare and each
embedded prepared cert carries the full `ActualMsg` batch payload, and the
NewView bundles the whole 2f+1 `ViewChangeLog`. The result is the 4.3 MiB
message in the log (`size_mib=4.358`).

### 2.4 The 90 ms leader-progress timer cannot fit one view-change round trip

`newview()` ([`node/view.go:375`](../node/view.go)) runs on the event loop and,
before `acceptNewViewTimers()` arms the 90 ms timer at
[`node/view.go:442`](../node/view.go), spends ~66 ms marshaling and signing the
4.3 MiB message. The peers then receive it ~40 ms later (per-peer), must verify
~1000 signatures, install the O-set, prepare, and commit — none of which can
complete in the 90 ms window. `handleLeaderProgressTimeout` →
`enterViewChange()` and the cycle repeats.

### 2.5 Why node 2 also fails, for a second independent reason

`proposal_delay_node: 2`, `proposal_delay_ms: 2000`. When node 2 is primary it
delays every proposal by 2 s. With a 90 ms progress timer it can never be a
working primary regardless of message size.

### 2.6 The collapse after view ~11

Once nothing commits, nothing checkpoints, so `createVCContent` carries an
ever-growing prepared-cert backlog for **every** node — and even nodes 1 and 3
can no longer install a view within 90 ms. Self-sustaining. There is no
exponential backoff anywhere (see §3.3), so it never recovers.

---

## 3. The "stale timer fire" hypothesis is wrong

The original theory: a leader-progress timer armed in view 7 fired, its value
sat buffered in the channel, and when node 4 installed view 8 that stale value
was read immediately, causing a spurious timeout.

### 3.1 Timing rules it out

Every failing timeout is ~90–160 ms after `acceptNewViewTimers()` — one full
`leaderProgressTimeout` period (the view-8 gap `17:19:42.381959 →
17:19:42.472976` is 91.0 ms). A stale buffered fire would trigger within
microseconds of the reset.

### 3.2 The code cannot leak a stale fire on this toolchain

All timer state (`leaderProgressTimer`, `leaderProgressTimerCh`, `newViewTimer`,
`newViewTimerCh`) is touched by exactly five functions plus the `select` in
`run()`. Every caller traces back to the single event-loop goroutine:

- `resetLeaderProgressTimer` ← `exeLoop` (via `tryExecute` ← `HandleCommit` ←
  `run()`) and `acceptNewViewTimers` (← `newview` / `HandleNewView` ← `run()`)
- `stopLeaderProgressTimer` ← `stopViewTimers` (← `enterViewChange` / `run()`
  defer) and `handleLeaderProgressTimeout` (← `run()` select)
- `startNewViewTimer` ← `maybeHandleViewChangeQuorum` and the election handlers,
  all ← `run()` select
- `stopNewViewTimer` ← `stopViewTimers` / `acceptNewViewTimers` /
  `handleNewViewTimeout`

Off-loop goroutines (`evalElectionVDF`, `asyncGrantVote`, `sendLeaderIdUpdate`,
`postActions`, the parallel verify/sign fan-outs, `messageHub.Send`) touch no
timer field.

Since `go.mod` declares `go 1.25.0` and the build is Go 1.26, the Go 1.23 timer
semantics apply: `time.NewTimer` channels have capacity 0, and **for any call to
`Stop` or `Reset`, no value prepared before that call can be sent or received
after it**. `resetOneShotTimer` / `stopOneShotTimer` both call `Stop` (and
`Reset`), so a stale fire cannot survive. The `select { case <-C: default: }`
drains are dead code but harmless.

### 3.3 What would reintroduce the risk

1. Lowering the `go` line in `go.mod` below `1.23`, or an older toolchain — the
   old buffered-channel race returns and the non-blocking drain does not fully
   close it (use a blocking `<-t.C` then).
2. Moving any timer op off the `run()` goroutine — a plain data race the 1.23
   guarantee does not cover.
3. `handleLeaderProgressTimeout` has no view/generation guard; if the timeout
   event is ever buffered or delivered async, add an arm-generation counter.

---

## 4. Measurements

Harness: [`node/newview_cost_test.go`](../node/newview_cost_test.go). It builds
synthetic NewView / ViewChange messages with tunable shape and times each stage
of `NewViewToPB → marshalDeterministic → SignMessageEd25519 → buildEnvelope →
proto.Size → proto.Marshal`, then does a real loopback-TCP gRPC broadcast.

```
go test ./node -run TestNewViewCostBreakdown        -v
go test ./node -run TestViewChangeCostBreakdown     -v
go test ./node -run TestNewViewGRPCBroadcast        -v -timeout 900s
go test ./node -run TestNewViewEnvelopeParallelScaling -v
go test ./node -run TestNewViewSharedEnvelopeWin    -v
```

Knobs: `dummyParams` (`numPreparedCerts`, `txnsPerBatch`, `paddingBytes`,
`includeActualMsgOSet`, `includeActualMsgVCCerts`, `dropIndividualDigests`,
`prepareVotesPerCert`, `numViewChangeMsgs`, `checkpointAccounts`) and
`grpcTuning` (buffers, windows, gzip, `sharedEnvelope`, `usePreparedMsg`).
Inner cert signatures are synthetic 64-byte blobs — size-equivalent; only the
outer envelope signature is verified by the receiver.

### 4.1 Stage breakdown by message shape (single peer)

| Shape | Wire size | `NewViewToPB` | `marshal` (sign) | `Sign` | Total pipeline |
|---|---|---|---|---|---|
| 3 certs (healthy) | 0.064 MiB | 0.29 ms | 0.38 ms | 0.27 ms | **1.7 ms** |
| 253 certs (typical failure) | 5.28 MiB | 22.4 ms | 32.0 ms | 19.8 ms | **134.7 ms** |
| 500 certs (watermark limit) | 10.44 MiB | 44.2 ms | 65.5 ms | 39.1 ms | **275.4 ms** |
| 253 certs, no `ActualMsg` | 1.35 MiB | 4.0 ms | 4.8 ms | 5.3 ms | **24.8 ms** |
| 253, no `ActualMsg`, no per-txn digest list | 0.36 MiB | 1.5 ms | 3.7 ms | 1.4 ms | **12.6 ms** |
| …+ only 1 VC in log *(not legal)* | 0.14 MiB | 0.6 ms | 1.4 ms | 0.5 ms | **4.6 ms** |
| 253 certs, 256 B padding | 12.81 MiB | 23.3 ms | 38.3 ms | 48.0 ms | **178.1 ms** |
| 3 certs + 100k `CheckpointBalances` | 7.19 MiB | 142.5 ms | **566.0 ms** | 27.0 ms | **1249.5 ms** |

Notes:
- The "serial on event loop" portion is `NewViewToPB + marshal + Sign`. For 253
  certs that is **74.2 ms**, matching the production 66 ms gap between the perf
  log and the replay log. This runs before the 90 ms timer is armed.
- `proto.Size(env)` is called inside the timed send region
  ([`node/nodeMessageHub.go:835`](../node/nodeMessageHub.go)) purely to print
  `size_mib`. It is a full second traversal — 9.5 ms for 5.28 MiB.
- `NewViewToPB` runs twice: once for signing, once per peer inside
  `buildEnvelope`.
- **`CheckpointBalances` with 100k accounts is catastrophic**: 1.25 s for a
  message with only 3 certs, `marshalDeterministic` alone 566 ms (protobuf
  `map<string,string>`). In this run the map was empty (the small VC was
  14.8 KB), but if `carry_state` ever populates it, it dwarfs the cert bloat.

### 4.2 Real gRPC broadcast over loopback (3 peers), baseline production options

| Shape | Wire size | per-peer `Send` | fan-out wall | end-to-end (peer rx) |
|---|---|---|---|---|
| 3 certs | 0.064 MiB | 0.49 ms | 1.0 ms | **3.0 ms** |
| 253 certs | 5.28 MiB | 43.6 ms | 68.3 ms | **198.9 ms** |
| 500 certs | 10.44 MiB | 87.1 ms | 141.9 ms | **387.8 ms** |

- **per-peer `Send` = 43.6 ms reproduces the production `HUB: node stream send
  took 40.7 ms` almost exactly.** Of that 43.6 ms, `proto.Size` (9.5) +
  `proto.Marshal` (31.5) = 41 ms is protobuf CPU inside `stream.Send`; ~3 % is
  actual transport. That is why 40 ms shows up on loopback.
- **end-to-end** measures "decoded and queued on `newViewMsgChan`", NOT "new
  view installed". `verifyNewView` (≈1000 Ed25519 verifies for 253 certs) and
  the per-slot install loop run afterwards. So 198.9 ms is a lower bound on
  what the 90 ms timer is actually racing.
- The gap between 68 ms fan-out and 199 ms end-to-end is receiver work per
  node: gRPC decode + `Deliver` → `verifySignature` **re-marshals the whole
  5.28 MiB** (~32 ms) then Ed25519-verifies (~19.8 ms) ≈ 52 ms, plus another
  `proto.Size` (~9.5 ms) for a log line, plus `NewViewFromPB`.

### 4.3 gRPC tuning sweep (253 certs) — nothing helped

| Tuning | per-peer Send | fan-out | end-to-end |
|---|---|---|---|
| baseline (production opts) | 43.6 ms | 68.3 ms | 198.9 ms |
| 1 MiB read/write buffers | 43.8 ms | 68.6 ms | 191.4 ms |
| + 64 MiB windows | 43.8 ms | 69.1 ms | 196.6 ms |
| shared envelope (build once, not per peer) | 42.3 ms | 65.7 ms | 192.6 ms |
| shared + `grpc.PreparedMsg` | 43.2 ms | 45.0 ms* | 170.2 ms* |
| **gzip** | **232 ms** | 261 ms | 510 ms |
| all combined | 42.9 ms | 66.7 ms | 189.8 ms |

\* An earlier version of the harness built the shared envelope outside the
timed region, inflating the shared-envelope win to ~26 ms. Corrected, the win
is ~4.6 ms of fan-out. `PreparedMsg` adds nothing because `Encode` still
marshals per stream.

Conclusions: gRPC write/read buffer size, HTTP/2 window size, and
`PreparedMsg` are no-ops here because the bottleneck never reaches the socket.
**gzip is 5.3x worse** — do not enable it for loopback / LAN.

### 4.4 `buildEnvelope` per-peer IS parallel (correction)

`asyncBroadCast` spawns one goroutine per peer
([`node/node.go:901`](../node/node.go)). On the 64-core box the per-peer encode
work scales well:

| Concurrent encodes | Wall time (5.28 MiB) | vs 1 | Alloc/round |
|---|---|---|---|
| 1 | 60.9 ms | 1.00x | 18.3 MiB |
| 3 | 68.8 ms | 1.13x | 54.8 MiB |
| 8 | 76.1 ms | 1.25x | 146.0 MiB |

Going 1 → 3 concurrent encodes costs **+7.9 ms of wall time**, not 2 × 19.4 ms.
~87 % parallel efficiency; the slope is allocator / GC / memory bandwidth. So
the per-peer `buildEnvelope`/`Size`/`Marshal` cost is CPU-and-allocation ×3
(18 → 55 MiB per broadcast) but latency only ×1.13. It is not on the event
loop's critical path — `asyncBroadCast` spawns and returns.

### 4.5 What is on the critical path

```
newview() on the event loop, STRICTLY SERIAL, node blocked:
   NewViewToPB           22.4 ms
   marshalDeterministic  32.0 ms   (for signing)
   SignMessageEd25519    19.8 ms
                         -------
                         74.2 ms
   asyncBroadCast(...)      ~0     spawns 3 goroutines, returns
   acceptNewViewTimers()            <- 90 ms timer armed HERE
```

Peers physically have the decoded message ~199 ms after broadcast start. The
timer expires at T+90 while delivery lands at T+199. It never fits.

---

## 5. What PBFT says about signing NewView

Sources: Castro & Liskov, "Practical Byzantine Fault Tolerance" (OSDI 1999) and
"…and Proactive Recovery" (ACM TOCS 2002); Stanford CS244b PBFT notes.

- **NewView is always signed by the new primary**: `⟨NEW-VIEW, v+1, V, O⟩_σp`.
- `V` = the 2f+1 VIEW-CHANGE messages the primary collected (full messages,
  each signed by its sender), including the primary's own. Replicas recompute
  `O` from `V` and reject a NewView whose `O` does not match.
- `O` = re-proposed `⟨PRE-PREPARE, v+1, n, d⟩_σp` with **digest `d` only**;
  sequence gaps filled with null pre-prepares.
- The P-set inside each VIEW-CHANGE holds `pre-prepare + 2f prepares` per
  prepared seq — **digests only, no request bodies**. Original PBFT never puts
  the client request / batch bytes into VIEW-CHANGE or NEW-VIEW; a replica
  missing a request fetches it separately.
- OSDI'99 signs every message with RSA. TOCS'02 replaced signatures with MAC
  authenticators on the normal path (pre-prepare / prepare / commit / requests
  / checkpoints) **but kept digital signatures for VIEW-CHANGE and NEW-VIEW**,
  because view change needs transferable authentication (a replica must
  independently verify that 2f+1 others signed valid VIEW-CHANGEs).

Implication: signing NewView is correct and mandatory. The problem is not
"should we sign" — it is that this implementation's signed object carries
`ActualMsg` batch payloads, per-transaction digest lists, and (potentially)
`CheckpointBalances`, making it multi-MB. A textbook NewView covering 250–500
prepared seqs is tens-to-low-hundreds of KB and signs in single-digit ms
(matching the "253-bare-certs" row in §4.1).

---

## 6. Fixes, ranked

### 6.1 Exponential backoff on the view timers — the load-bearing fix

`leaderProgressTimeout` and `newViewTimeout` are compile-time constants
([`node/viewtimers.go:6`](../node/viewtimers.go)) and every arming uses the same
value. Castro-Liskov §4.5.2 doubles the timeout each time a replica times out
on a view before making progress, and resets to base after a successful
executed sequence in the new view. Doubling is *the* liveness mechanism: it
guarantees the timeout eventually exceeds real message delay.

Without it, if installation genuinely needs 200–390 ms (§4.2), every view fails
at 90 ms forever — exactly views 12–26 in the logs. With `90 → 180 → 360 →
720 ms` the storm self-heals in ~3–4 views. This is independent of message
size and is the difference between a transient hiccup and a permanent outage.

Implementation: track a per-node backoff multiplier, bump it in
`handleLeaderProgressTimeout` / `handleNewViewTimeout`, reset it after the first
executed sequence following a new-view install (`resetLeaderProgressTimer` from
`exeLoop`).

### 6.2 Move the perf trigger off the checkpoint boundary

The trigger fires only at `seq % CHECKPOINT_INTERVAL == 0`
([`node/throughputperformance.go:62`](../node/throughputperformance.go)), which
is exactly when the fresh checkpoint is un-stabilized. Options:

- Fire at `seq % CHECKPOINT_INTERVAL == CHECKPOINT_INTERVAL/2`.
- Or in `perfVC`, if there is an executed-but-not-stable checkpoint at
  `lastExecuted`, defer `enterViewChange()` (bounded wait) until it stabilizes.

This does not *create* the expensive case — a crash view change can still land
mid-interval (§2.2) — but it stops the perf path from deterministically
selecting the worst case every single time. Common-path NewView drops to ~3
certs / 55 KB / ~3 ms end-to-end.

### 6.3 Shrink the NewView

Measured reductions at 253 certs (§4.1):

| Change | Wire size | Serial on event loop | End-to-end |
|---|---|---|---|
| baseline | 5.28 MiB | 74.2 ms | 198.9 ms |
| drop `ActualMsg` from O-set + VC certs | 1.35 MiB | 14.2 ms | ~60 ms est. |
| + replace per-txn digest list with a Merkle root | 0.36 MiB | 6.6 ms | ~30 ms est. |

- Dropping `ActualMsg` requires a way to re-fetch the batch on the receiver
  (PBFT fetches missing requests separately). The per-txn digest list
  (`DigestIndividualClientMsgs`, 30 × 32 B per pre-prepare, ~1000 copies in the
  message) is needed to verify a re-fetched batch unless replaced by a Merkle
  root.
- Never let `CheckpointBalances` ride in the signed message (§4.1: 100k
  accounts = 1.25 s). If checkpoint state must transfer, do it out of band.
- Remove `proto.Size(env)` from the timed send path
  ([`node/nodeMessageHub.go:835`](../node/nodeMessageHub.go)) — pure logging
  cost, ~9.5 ms per peer for 5.28 MiB, doubled traversal.

### 6.4 Sign a commitment, not the payload

`marshalDeterministic` (32 ms) + `SignMessageEd25519` (19.8 ms) = 51.8 ms of
the 74 ms serial path exist only to produce one signature over 5.28 MiB.
Ed25519 hashes its whole input with SHA-512 twice; this Xeon has no `sha_ni`.

Sign a compact struct built from digests already in memory:

```go
type NewViewSignPayload struct {
	MsgType     string   // "NEWVIEW" — domain separation, mandatory
	View        int64
	From        int32
	OSetDigest  [32]byte // over each O-set entry: seq ‖ view ‖ DigestClientMsg ‖ Signature
	VCSetDigest [32]byte // over each ViewChangeLog entry: vc.From ‖ vc.Signature
}
```

This is standard (RSA-PSS, ECDSA, Ed25519ph all sign a hash) and safe here
because the bulk is already self-authenticating and independently verified:
`verifyVC` / `verifyPreparedCertsParallel` check every inner pre-prepare and
prepare signature, `verifyNewView` checks every ViewChange signature. The outer
signature's only unique job — "this primary assembled exactly this set" — is
bound equally by a 32-byte commitment. `~51.8 ms → ~0.1 ms` on both sender and
every receiver.

Caveats: domain-separate with `MsgType` + `View`; the commitment must cover
every field the receiver acts on; inner verification must still run — the
compact header is not a licence to skip cert checks. The same treatment applies
to ViewChange in `VC()` ([`node/roundrobin.go:42`](../node/roundrobin.go)),
~18 ms serial there.

Even with this, end-to-end at 253 certs stays above 90 ms — it lowers the
constant so backoff (§6.1) converges in fewer views; it does not remove the
need for backoff.

### 6.5 Node 2's proposal delay

`proposal_delay_node: 2` + `proposal_delay_ms: 2000` + 90 ms progress timer
means node 2 can never be a working primary. If that is deliberate fault
injection, fine — but it guarantees a view change on every one of node 2's
turns. Note it when reading these logs.

---

## 7. Summary

- The immediate cause is a **fixed 90 ms leader-progress timer** vs a **new-view
  install that legitimately takes 200–390 ms** when the P-set carries a full
  checkpoint interval of prepared certs.
- The perf trigger makes it deterministic by firing exactly on the checkpoint
  boundary, so the P-set is always ~250 and the primary is always node 2 or 4.
- The "stale timer fire" theory is ruled out: timing shows fresh 90 ms
  expiries, and the Go 1.23+ timer semantics plus single-goroutine access make
  a stale fire impossible on this build.
- Signing NewView is required by PBFT; the cost comes from this
  implementation's oversized signed object, not from signing per se.
- Fix priority: **exponential backoff (6.1)** > perf-trigger offset (6.2) >
  message shrink (6.3) > commitment signing (6.4).
