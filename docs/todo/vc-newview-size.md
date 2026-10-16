# TODO: cut ViewChange and NewView size further

Status: not started. Written 2026-10-15, on branch `vc-compact-certs` after Phases 1a, 2
and 3 (`e44ed6b`, `baff766`, `c9ee701`). Three independent cuts, in the order I'd do them:

1. finish Phase 1: drop the per-txn digest lists
2. a smaller checkpoint interval
3. BLS-aggregated prepare signatures

## Where the size is today

Measured from the `VC sent` / `NEWVIEW sent` log lines (`size_bytes`, logged since
`1edd6cc`) in `results/ckpt_fetch_n7`: n = 7, batch 50, `max_inflight_seq` 40.

- **ViewChange ≈ 2.07 KiB per prepared cert, NewView ≈ 1.78 KiB per O-set slot.**
- **Largest seen:** 285 certs, so **VC 588 KiB, NV 506 KiB**. Medians are 200–450 KiB
  (VC) and 170–390 KiB (NV), depending on the arm.
- **Cert count:** at most `CHECKPOINT_INTERVAL + δ + max_inflight_seq` ≈ 250 + δ + 40
  (`docs/prepared-cert-bound.md`). δ is the few slots executed while a checkpoint waits
  for its 2f+1 votes.

One cert on the wire (`PreparedCert`, `proto/pbft_transport.proto`):

| Part | Bytes | Share |
|---|---|---|
| per-txn digest list, `digest_individual_client_msgs` (50 × 34 B) | ~1,700 | ~80% |
| 2f = 4 prepares × (from + 64 B Ed25519 signature) | ~280 | ~13% |
| pre-prepare signature | ~66 | ~3% |
| batch digest, view, seq, framing | ~60 | ~3% |
| **total** | **~2,110** | |

**Why it matters:**
- **TCP first flight.** On a 1500-MTU link, TCP's initial and restart window (10 segments)
  is 14 KiB. A 590 KiB VC needs ~6 round trips from a cold window, which is ~1.9 s under
  the 170 ms NetworkDelay scenario. On `lo` (64 KB segments) the same VC went in one
  flight. Cluster runs currently work around this with `scripts/cluster/setup.sh
  --tune-tcp-lan` (`docs/multi-host.md`). Smaller messages are the real fix, and the one
  that holds on a WAN.
- **Cost per view change.** Every VC goes to n − 1 peers and every NV to n − 1 replicas.
- **Production comparison.** Production BFT systems send view-change messages of
  hundreds of bytes to a few tens of KB (PBFT's ⟨seq, digest, view⟩ claims, BFT-SMaRt's
  last and next decision, HotStuff's single QC).

## Cut 1: finish Phase 1, drop the per-txn digest lists

**What:** with `carry_state: false`, send certs and O-set entries without
`DigestIndividualClientMsgs`. Each node fills the list in from its own pool.

**Why it's possible:**
- No signature covers the list. Pre-prepares are signed over (view, seq, batch digest),
  and prepares and commits over the batch digest.
- The list is only used locally, after agreement, in three places: execution
  (`pool.GetBatch` / `MarkExecuted`), intake seeding, and the `carry_state` branch of
  `buildPreparedCert`.
- Every node that can execute a batch already got the list in the original PrePrepare,
  together with the bodies (`AddBatchIfNewer`).

**Changes:**
- **Pool index:** a batch index in `node/pool.go`, keyed by batch digest and garbage-collected
  with `GCUpTo`.
- **Strip:** `buildPreparedCert` strips the list when `!carry_state`. `createO` and
  `buildOSet` copy from the certs, so they follow without further change.
- **Refill:** `resolveBatchDigests` fills the list back in the install loops (`newview`,
  `HandleNewView`) and in `resetPendingForNewView`.
- **Execution guard:** a batch whose list can't be found stalls, like today's
  `some req for batch not found`. It is never executed as empty, which would make that
  node diverge.
- **`carry_state: true` is unchanged:** there the O-set is the source of the bodies.

The full step-by-step plan, with call sites and tests, was written on 2026-10-14 in the
Claude plan "Rest of Phase 1" (`~/.claude/plans/ok-lets-plan-for-shimmering-quokka.md`
on the development host).

**Trust note:** at a view change each replica now uses the list from the PrePrepare it got
from the original leader, not the new primary's copy. That's the same trust as the normal
path, which already accepts the leader's list unchecked: the batch-digest check in
`HandlePrePrepare` is commented out. Turning that check on would close the gap on both
paths.

**Expected sizes** (`node/viewchange_size_test.go`, from `docs/newview-timer-budget.md`):

| n | VC today → without lists | NV today → without lists |
|---|---|---|
| 4 | 556 → 74 KiB | 514 → 32 KiB |
| 7 | 596 → **114 KiB** | 514 → **32 KiB** |
| 10 | 635 → 153 KiB | 514 → 32 KiB |

After this, a cert is ~400 B, **~85% of it signatures**, and the VC still grows with n
(2f prepares per cert). Cuts 2 and 3 target what's left.

## Cut 2: a smaller checkpoint interval

**What:** lower `CHECKPOINT_INTERVAL`, now a Go constant of 250 (`node/node.go:28`). Both
messages scale with the number of slots above the last stable checkpoint, which is
bounded by `K + δ + W`.

| K | max slots (K + W, W = 40) | VC, today's certs (~2.1 KiB) | VC after Cut 1 (~400 B) | NV after Cut 1 (~113 B/slot) |
|---|---|---|---|---|
| 250 (today) | ~290 | ~590 KiB | ~114 KiB | ~32 KiB |
| 128 | ~170 | ~350 KiB | ~66 KiB | ~19 KiB |
| 64 | ~105 | ~215 KiB | ~41 KiB | ~12 KiB |
| 32 | ~70 | ~145 KiB | ~28 KiB | ~8 KiB |

These sizes are estimates, scaled from the measured per-cert sizes.

**Constraints and costs:**
- **The high watermark must stay well above the pipeline.** The leader stops proposing at
  `s + 2K` (`docs/prepared-cert-bound.md`), so `2K` has to clear `K + W + δ`, i.e.
  `K > W + δ`. With W = 40, K = 64 is the practical floor. K = 32 would let the
  watermark throttle the leader whenever a checkpoint is slow to stabilize.
- **More checkpoint rounds:** 250 / K times as many: a state digest of the 26-account
  state, n² small CHECKPOINT messages, and a GC pass. All are cheap here, but measure the
  CPU.
- **Checkpoint fetch:** the stranding that `checkpoint_fetch` repairs (a node leaving a
  view just below a checkpoint) gets more chances to happen with more checkpoints.
  Re-check the `CKPT FETCH` counts (`scripts/ckpt_fetch_summary.py`).
- **Make it a config key, e.g. `checkpoint_interval`, default 250**, added to
  `config/run2new.json` in the same change as the rule in `CLAUDE.md` requires. That lets
  old runs reproduce and new ones sweep it.
- **Docs that assume K = 250:** `docs/prepared-cert-bound.md` and
  `docs/newview-timer-budget.md`.

## Cut 3: BLS-aggregated prepare signatures

**What:** a cert carries 2f individual Ed25519 prepare signatures, ~280 B at n = 7. All
prepares in a cert sign the same message (view, seq, batch digest). With BLS signatures
on prepares, the 2f of them aggregate into **one 48-byte signature plus a signer bitmap**,
and verifying that takes two pairings, whatever the size of f.

| | per cert | VC at 290 certs | VC at K = 64 (~105 certs) |
|---|---|---|---|
| after Cut 1 | ~400 B | ~114 KiB | ~41 KiB |
| after Cut 1 + BLS | ~175 B (pre-prepare Ed25519 66 + aggregate ~50 + bitmap + digest/view/seq) | ~50 KiB | **~18 KiB** |

The VC per cert also **stops growing with n**. The NV is unaffected, because its O-set
entries carry no prepares.

**Costs and open questions:**
- **CPU.** BLS verification is pairing-based, roughly a millisecond, against ~50–100 µs
  for Ed25519 (`docs/newview-timer-budget.md` measured ~100 µs including marshal). These
  are rough figures, to be measured. Two designs:
  - **(a) Prepares signed with BLS only:** the normal path pays BLS on every prepare
    (n − 1 verifies per slot per node).
  - **(b) Prepares dual-signed:** Ed25519 for the normal path and a BLS share for certs.
    Signing costs double, but BLS is verified only at view changes.

  Batch verification and the many cores per node (one machine per node on a cluster) help.
- **The Phase 3 cache still works.** Aggregation is deterministic (a sum of points), so
  a receiver that holds the same signers' prepares can rebuild the aggregate from its own
  verified shares and compare bytes, with no pairing. In design (b) the held BLS share
  must have been verified once, or a Byzantine sender could pair a valid Ed25519
  signature with a bogus share. Fall back to a pairing check on a mismatch.
- **Rogue-key attacks.** Aggregate verification over same-message signatures needs proof
  of possession for each public key. `setup_crypto/crypto_main.go` would generate it,
  and the key store would check it at load.
- **Library.** Pick and benchmark one, e.g. `blst`'s Go bindings
  (`github.com/supranational/blst/bindings/go`) or `go.dedis.ch/kyber`. This adds a cgo
  or crypto dependency.
- **Wire format.** `CertPrepare` becomes (signer bitmap, aggregate). This is a proto
  change, so plan it first.

## All three together (n = 7, batch 50)

| | VC | NV |
|---|---|---|
| today | ~590 KiB | ~510 KiB |
| Cut 1 | ~114 KiB | ~32 KiB |
| Cut 1 + 2 (K = 64) | ~41 KiB | ~12 KiB |
| Cut 1 + 3 (BLS) | ~50 KiB | ~32 KiB |
| all three | **~18 KiB** | **~12 KiB** |

All three together would put both messages at or close to one default 1500-MTU first
flight (14 KiB), so `--tune-tcp-lan` would no longer matter for them.

## How to measure each step

- **Sizes, offline:** `go test ./node -run 'TestViewChangeWireSize|TestNewViewWireSize' -v`
  (`node/viewchange_size_test.go`).
- **Sizes, in runs:** `grep -h "VC sent\|NEWVIEW sent" results/<run>/node_*.log`; the
  `certs=` and `size_bytes=` fields.
- **Delivery time:** pair each `VC sent` (sender, view, size) with every node's
  `HUB: Received ViewChange message from node S for view {g c}. size_bytes=B`, and the same
  for NewViews. In a NetworkDelay generation, one flight is ~170–195 ms. The 2026-10-15
  numbers: VCs of 450–700 KiB had a median of 186–192 ms on `lo`, with a tail up to
  ~1.55 s (four extra round trips) in `ndelay_prr`.
- **Cache hits:** the `VC CERTS ... cached=… to_verify=…` lines. On 2026-10-15,
  receivers already held 91.6–99.9% of the carried prepares, but 25–37% of NetworkDelay
  VCs missed at least one cert.

## Considered and not pursued

- **Claims for all slots, full certs only for the newest ones, fetch the rest on a miss.**
  25–37% of NetworkDelay VCs miss a cert at the receiver, and each miss outside the
  carried tail would cost a fetch round trip (340 ms under the delay).
- **MAC-based PBFT view change** (⟨seq, digest, view⟩ claims plus VIEW-CHANGE-ACKs, as in
  Castro–Liskov TOCS 2002). This gives the smallest messages, but it is the largest
  redesign and adds an ack step, i.e. one more message delay, to every view change.
