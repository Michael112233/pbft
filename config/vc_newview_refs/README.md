# vc_newview_refs: live check of the NewView-by-digest change (Phase 2)

## Intention

Check, at n=7 on real runs, that the NewView carrying `(sender, digest)` refs
instead of the 2f+1 full ViewChanges installs every view the old code installed,
with no rejected NewViews, and see how often a replica has to fetch a ViewChange
from the primary.

- **If it works:** view-change latency at n=7 should drop, because the NewView goes from about 3.5 MiB to about 514 KiB.
- **If it fails:** any `NEWVIEW REJECTED`, or a view that installs on `viewid` but stalls here, is a bug to fix before Phase 1's remaining work, which drops the per-txn lists.

## Compared against

The same three runs on branch `viewid` (commit `56cf434`), which still embeds the full ViewChanges in the NewView.

- **Configs:** `vcrefs_base_*` is byte-identical to `vcrefs_*` except for `_note`. Separate names keep the results in separate `results/` directories.
- **Ignored key:** `viewid` doesn't know `view_change_fetch_delay_ms` and ignores it.

## What changes, what is held fixed

The base is `config/run2new.json`, with these changes:

| key | value | why |
|---|---|---|
| `node_num` | 7 | The size problem appears at n=7 (2f+1 = 5 ViewChanges per NewView). |
| `nodes_dead`, `nodes_in_dark` | 7 entries; nodes 2 and 3 dead only in `*_prr_crash2` | f=2 crashed nodes leaves exactly 2f+1 live ViewChanges, so every ref must resolve. Dead nodes are crashed from the start outside scenario mode (`node/node.go`). |
| `default_action` | `PerformanceRoundRobin` or `PeriodicElection` | Fixed would rarely change view. Perf rotates every few seconds at n=7. Election exercises the NewView sent after 2f+1 GrantVotes. |
| `view_change_fetch_delay_ms` | 20 | The default, written out. It is how long a replica waits for a missing ViewChange before asking the primary. |

Everything else is held fixed:
- epoch, scenario and oracle modes are off;
- `carry_state: false`;
- batch 50, `max_inflight_seq` 40, `inject_speed` 200;
- throttle and timers are as in `run2new.json`: Fixed/Perf at 150 ms, Periodic at 10 s.

## Configs

| file | branch | action | crashed |
|---|---|---|---|
| `vcrefs_n7_prr.json` | vc-compact-certs | PerformanceRoundRobin | none |
| `vcrefs_n7_pe.json` | vc-compact-certs | PeriodicElection | none |
| `vcrefs_n7_prr_crash2.json` | vc-compact-certs | PerformanceRoundRobin | 2, 3 |
| `vcrefs_base_n7_prr.json` | viewid | PerformanceRoundRobin | none |
| `vcrefs_base_n7_pe.json` | viewid | PeriodicElection | none |
| `vcrefs_base_n7_prr_crash2.json` | viewid | PerformanceRoundRobin | 2, 3 |

## How to run

Each run is 300 s, so about 16 min per branch including start and stop. Results land in `results/<config name>/`.

```bash
git switch vc-compact-certs
./run_experiments.sh 300 config/vc_newview_refs/vcrefs_n7_prr.json \
    config/vc_newview_refs/vcrefs_n7_pe.json config/vc_newview_refs/vcrefs_n7_prr_crash2.json

git switch viewid      # the configs are untracked on viewid; copy the folder over if it is missing
./run_experiments.sh 300 config/vc_newview_refs/vcrefs_base_n7_prr.json \
    config/vc_newview_refs/vcrefs_base_n7_pe.json config/vc_newview_refs/vcrefs_base_n7_prr_crash2.json
```

If you use `scripts/run_isolated.sh`, note that its worktree checks out a commit. Move it to the branch's commit first.

## How to analyse

```bash
for r in results/vcrefs_*n7_*; do
  echo "== $r"
  for p in "NEWVIEW REJECTED" "NEWVIEW PENDING" "NEWVIEW RESOLVED" "VC FETCH sent" "VC FETCH served" \
           "VC EQUIVOCATION" "Failed to verify view change" "signature verification failed" \
           "New view timer expired" "some req for batch not found" "Assertion failed" "VC CERTS"; do
    printf "  %-32s %s\n" "$p" "$(cat $r/node_?.log | grep -c "$p")"
  done
  printf "  %-32s %s\n" "views installed per node" "$(for f in $r/node_?.log; do grep -c 'processing completed' $f; done | paste -sd' ')"
  grep -h "processing completed in" $r/node_?.log \
    | sed -E 's/.*processing completed in ([0-9.]+)(µs|ms|s)$/\1 \2/' \
    | awk '{v=$1; if($2=="µs")v/=1000; else if($2=="s")v*=1000; print v}' | sort -n \
    | awk '{a[NR]=$1} END{if(NR) printf "  NewView install ms: n=%d median=%s p95=%s max=%s\n", NR, a[int(NR/2)+1], a[int(NR*0.95)+1], a[NR]}'
done
```

Compare each `vcrefs_*` run with its `vcrefs_base_*` twin.

**Hit rate of the already-verified skip (Phase 3, `vcrefs_*` only):**

```bash
grep -h "VC CERTS" results/vcrefs_n7_prr/node_?.log \
  | sed -E 's/.*cached=([0-9]+) to_verify=([0-9]+).*took=(.*)$/\1 \2 \3/' \
  | awk '{c+=$1; v+=$2} END{printf "cached=%d to_verify=%d hit=%.1f%%\n", c, v, 100*c/(c+v)}'
```

The benchmark's warm case is 100%. Misses are prepares a node never received, and
certs from older views.

**On `vcrefs_*`, these should be 0:**
- `NEWVIEW REJECTED`, `Failed to verify view change`, `signature verification failed`, `some req for batch not found`.

**`NEWVIEW PENDING` should be rare,** and almost every one should have a matching `NEWVIEW RESOLVED`. A PENDING with no fetch means the ViewChange was just behind the NewView. `VC FETCH served` should be near 0 when no node is crashed.

**Against the twin, look at:**
- views installed per node;
- `New view timer expired`;
- NewView install time;
- view rate.

In `*_crash2`, the twin's numbers are the bar. Dead primaries cause new-view timeouts on both branches.

**The `HUB: Received ViewChange ... size_bytes` log is now the signed payload size,** not the envelope size. It's smaller by tens of bytes.

## Expected outcome and caveats

- **Expected:** views install as on `viewid` or better, with no rejections.
- **Possibly better on `vcrefs`:** fewer `New view timer expired` at n=7, because the 150 ms timers no longer race a 3.5 MiB NewView.
- **Branch confounder:** the `vc-compact-certs` branch also carries Phase 1a (compact cert prepares, certs capped at 2f, a vote-sender check) and Phase 3 (the already-verified skip in `verifyVC`). Those changes are part of what this compares.
- **Phase 3 effect:** fewer `New view timer expired` and faster view changes at n=7 are expected mostly from Phase 3, not from the NewView size. On 16 CPUs, timer need fell from about 290 ms to about 34 ms (`docs/newview-timer-budget.md`).
- **Run length:** 300 s gives about 100 Perf view changes. That's enough for counts, but only a rough latency distribution.
- **Not covered: Byzantine behaviour.** That includes an equivocating ViewChange and a forged ref. Unit tests cover it: `node/newviewrefs_test.go`.

## Provenance

- Written 2026-10-14, against `vc-compact-certs`: rebased on `viewid` `56cf434`, plus Phase 1a and the uncommitted Phase 2 work.
- Request: "give me a plan for phase 2 … implement phase 2 first", with this live check from the approved plan.
