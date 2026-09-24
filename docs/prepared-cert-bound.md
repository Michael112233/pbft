# How many prepared certs a ViewChange can carry (2026-09-23)

This note derives the maximum size of a ViewChange P-set (the prepared certs it
carries) from the code: the checkpoint interval, the watermarks and the in-flight
window. It also covers what that bound means for the NewView. The motivation was a
view-change cascade in a `PerformanceRoundRobin` run at `max_batch_size: 50`,
where VCs carrying 187–224 certs could not be installed inside the 150 ms new-view
timer. See [newview-cost-and-viewchange-storm.md](newview-cost-and-viewchange-storm.md)
for why message size turns into latency.

**Short answer:** the hard ceiling is **500 certs per VC** (`2 × CHECKPOINT_INTERVAL`,
enforced by the high watermark). In steady state the practical ceiling is **~290**
(`CHECKPOINT_INTERVAL + max_inflight_seq`, plus a few slots of checkpoint
stabilization lag).

---

## 1. What goes into the P-set

`createVCContent` ([`node/view.go:81`](../node/view.go)) walks every seq from the
node's last stable checkpoint `s` up to `maxSeqNum`, and includes each slot that
has a `preparedProof`:

```go
for seq := lastStableCheckpointSeq + 1; seq <= n.consensusLog.maxSeqNum; seq++ {
    slot, exists := n.consensusLog.GetLogEntry(seq)
    if !exists || slot.preparedProof == nil {
        continue
    }
    preparedCerts[seq] = slot.preparedProof
}
```

So committed and even executed slots are carried as long as they are above the
last **stable** checkpoint. `preparedProof` is cleared only by `GCLog` when a
checkpoint stabilizes. The question is how far above `s` a slot can become
prepared.

## 2. The three constraints

Notation: `K` = `CHECKPOINT_INTERVAL` = 250 ([`node/node.go:27`](../node/node.go)),
`W` = `max_inflight_seq` = 40 (config), `s` = the node's last stable checkpoint,
`δ` = slots executed while the next checkpoint waits for 2f+1 votes.

| constraint | code | bound on a prepared seq |
|---|---|---|
| **High watermark** | `GCLog` sets `low = s+1`, `high = low + 2K − 1 = s + 2K` ([`node/consensuslog.go:165-166`](../node/consensuslog.go)). PrePrepare, Prepare and Commit above `high` are dropped ([`node/node.go:505`](../node/node.go), [`679`](../node/node.go), [`818`](../node/node.go)), and the leader will not propose past it ([`node/node.go:395`](../node/node.go)). | `seq ≤ s + 2K` |
| **In-flight window** | The leader proposes only while `sequenceNumber − lastExecuted < max_inflight_seq` ([`node/node.go:390-391`](../node/node.go)). | `seq ≤ lastExecuted_leader + W` |
| **Checkpoint interval** | A local checkpoint is taken at every `lastExecuted % K == 0` ([`node/execution.go:80`](../node/execution.go)). It becomes stable (and GC raises `s`) only after 2f+1 matching CHECKPOINT messages. | `lastExecuted − s < K + δ` while stabilization keeps up |

Combining them:

```
certs per VC ≤ min( 2K ,  (lastExecuted − s) + W )
             ≤ min( 500,  K + δ + W )
```

## 3. The three regimes

| case | max certs | when |
|---|---|---|
| **Steady state** | **≈ 250 + 40 + δ ≈ 290–295** | Execution sits just before checkpoint `s + K`, with a full in-flight window of prepared-but-not-executed slots on top. At ~163 slots/s and ~17 ms stabilization, δ ≈ 3. |
| **Stabilization stalls** | **500** (hard ceiling) | Checkpoint `s + K` never collects 2f+1 votes, so execution continues until the leader hits `high = s + 2K`. The in-flight window no longer binds. |
| **Lagging replica** | **≤ 500** | Its `s` is stale, but it also drops everything above its own `high`, so the 2K bound still holds. In practice it carries *fewer* certs, since it prepared little. |

Where in the cycle a view change lands is effectively random for the timed perf
trigger (1 s sampling, unrelated to checkpoint boundaries), so each view change
draws a cert count between 0 and ~290. The 09-06 storm doc describes the older
sequence-driven trigger, which fired exactly on a checkpoint boundary and so always
drew the maximum.

### Two details

- **`W` only limits an honest leader.** Replicas accept any PrePrepare in
  `[low, high]` without checking the in-flight window, so only the watermark is a
  protocol-level guarantee.
- **Certs retained across views are still covered.** `RemoveLogEntriesAboveSeq`
  ([`node/consensuslog.go:180`](../node/consensuslog.go)) keeps prepared slots after
  a new-view install, but they stay in `(s, s + 2K]`, so the bound holds.

## 4. What it means for the NewView

- **V:** 2f+1 = 3 VCs, each up to 500 certs, so at most 1,500 certs.
- **O:** `createO` ([`node/view.go:659`](../node/view.go)) sets `minS` = highest
  stable checkpoint among the VCs + 1 (lines 664/686; `createOReplica` mirrors this
  at 898/920) and `maxS` = highest prepared seq in any VC. That makes O at most
  `maxS − minS + 1 ≤ 2K = 500` re-proposals, with gaps filled by null requests.

## 5. Observed

Two runs, `PerformanceRoundRobin`, `max_batch_size: 50`, 150 ms new-view timer, 4
nodes on one box.

| | `carry_state: true` (22:10 run) | `carry_state: false` (23:40 run) |
|---|---|---|
| VC bytes per cert | ~7.4 KB (50 request bodies + digests + sigs) | ~2.07 KB (digests + sigs) |
| largest P-set seen | 224 | 249 |
| cert count where installs stop fitting 150 ms | ~155 (157 was 2 ms late on one node) | not reached |
| 2f+1 → NewView at ~250 certs | cascade | 40–54 ms |

Both runs stayed inside the steady-state regime (≤ 290). The 500 case needs
checkpoint stabilization to stall and was not observed.

Extrapolating the `carry_state: false` run at ~0.2 ms per cert puts a 500-cert
view change at ~100 ms against the 150 ms timer; the `newview_cost_test.go`
harness measured 89 ms end-to-end for that shape. It fits, but with a thin margin
under CPU contention. That is why exponential backoff on the new-view timer
(storm doc §6.1) is still worth adding as the safety net: shrinking the message
moves the typical case far below the threshold, while backoff guarantees recovery
whenever something pushes a view change over it.
