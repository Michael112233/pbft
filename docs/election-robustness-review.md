# Election-path robustness review

## Executive summary

The election design has a reasonable overall shape, but the current implementation is not yet robust. It can permanently stop during view change, repeatedly split votes without electing anyone, fail to recover when timers are disabled, and accept a `NewView` from a node that did not prove it won the election.

The happy path can elect a leader:

1. A timeout or `f+1` view-change messages enters view change.
2. At `2f+1` view-change messages, nodes start their VRF/VDF work.
3. A node votes for the first acceptable candidate, or for itself when its VDF completes.
4. A candidate that collects `2f+1` grants broadcasts `NewView`.
5. Nodes accept the new leader and resume normal progress.

The intended recovery approach is also directionally correct: keep a recovery timer running after voting so a split election or failed winner eventually causes another view change. The implementation details described below prevent that guarantee from holding in all relevant cases.

This review assumes honest nodes send each protocol message once, as intended. Duplicate-message handling is still discussed because duplicates can also come from retries, reconnects, restored state, or future transport changes rather than a deliberate attack.

## Current implementation flow

The current election path is approximately:

1. [`enterViewChange`](../node/view.go) stops existing timers, marks view change as running, increments `forView`, and sends the node's view-change message.
2. [`HandleViewChange`](../node/roundrobin.go) collects view-change messages. At `f+1` messages, a node may enter the next view. At a quorum, it invokes election logic.
3. [`ElectionLogic`](../node/election.go) requires the target view to equal `forView` and requires the view-change count to equal `QuorumSize()`. It processes one buffered `RequestVote`, if present, or starts a local VRF/VDF.
4. The VDF worker sends an `electionVDFResult` back to the node event loop.
5. [`handleElectionVDFResult`](../node/election.go) self-votes, starts the new-view recovery timer, and broadcasts a signed `RequestVote`.
6. [`HandleRequestVoteMsg`](../node/election.go) verifies an acceptable candidate, records the vote, sends a signed `GrantVote`, and starts the recovery timer.
7. [`HandleGrantVoteMsg`](../node/election.go) collects unique grant senders. A candidate that reaches a quorum calls `newview`.
8. [`newview`](../node/view.go) makes the candidate the local leader, constructs and broadcasts `NewView`, and changes its timers to accepted-new-view mode.
9. [`HandleNewView`](../node/view.go) verifies the message, updates the current view and leader, and stops the election recovery timer.

## Liveness failures and hard-stuck paths

### No recovery timer immediately after entering view change

`enterViewChange` stops the existing timers and sends a view-change message, but it does not immediately start another timeout. The election recovery timer starts only after the node votes:

- after its own VDF completes successfully; or
- after it accepts another node's `RequestVote`.

This creates permanent-stall paths. A node may have no active timeout if:

- it never obtains `2f+1` view-change messages;
- VRF proof creation fails;
- the private VRF key is missing;
- the VDF returns an error;
- the VDF computation never completes;
- it never receives a valid `RequestVote`; or
- it is several views behind and cannot catch up.

The per-view recovery/pacemaker timer should start immediately when entering view change. It should remain active through view-change collection, VDF execution, voting, grant collection, and `NewView` delivery. It should stop only after the node accepts a valid `NewView`.

### Timers can be disabled by configuration

The timer-starting methods in [`viewtimers.go`](../node/viewtimers.go) return without starting timers unless `n.cfg.Fixed` is true. Leader-progress timeout handling also returns without recovery when `PeakTpsTest` is true.

The checked [`run2new.json`](../config/run2new.json) configuration currently includes:

```json
"fixed": false,
"peak_tps_test": true
```

With those settings, a split vote may never retry, a failed elected leader may never be replaced, and a failed initial leader may leave the system permanently stuck.

Pacemaker and recovery timers should not depend on whether leader selection is fixed, round-robin, or election-based. Until those concerns are separated, an election configuration would at minimum need `fixed: true` and `peak_tps_test: false` for the current timer code to operate.

### No initial leader-progress timer

`Node.Start` starts the event loop and message hub, but does not start a leader-progress timer. The timer is normally reset only after executing a request or accepting a new view.

If the initial leader fails before any request is executed, replicas may have no timeout that can begin view change. The initial leader-progress timer should be started as part of node startup.

### Nodes cannot catch up by more than one view

Future-view handling enters a new view only when:

```go
viewChange.ViewNumber == n.forView+1
```

For example, a node at `forView == 3` can receive enough view-change messages for view 6 and still only buffer them. If it missed the `NewView` messages for views 4 and 5, it may never advance.

A node receiving `f+1` valid messages for any higher view should be able to advance to that target view, subject to the protocol's view catch-up rules. Advancing by exactly one view is too restrictive for an asynchronous network and recovering nodes.

### VDF failure has no independent recovery path

Old VDF results are safely ignored after the node moves to a newer view, but the VDF computation does not have an independent election timeout. A context can make a VDF loop cooperatively cancellable, but cancellation must be checked inside the repeated-squaring loop. A context alone cannot forcibly kill computation that never checks it.

Even if old computations are allowed to finish, entering the view must start a timer so a failed or excessively slow VDF cannot freeze election progress.

## Split votes and repeated elections

A split vote does not necessarily hard-stop the system if every recovery timer is active. Nodes can time out, enter the next view, and try again. The present VDF race can nevertheless split repeatedly.

Each node currently:

1. starts its own VDF;
2. votes for itself immediately when its VDF finishes; and
3. refuses all later candidates for that view.

The current delay is derived in the range of roughly 100 to 1000 repeated squarings. That work can finish faster than a `RequestVote` can travel through the network and event loops. Multiple nodes can therefore finish locally and self-vote before hearing from the fastest candidate.

The result is:

- every node has one self-vote;
- no candidate obtains `2f+1` grants;
- the recovery timer advances the view, if it is enabled; and
- the same timing pattern can repeat in every subsequent view.

Randomness provides a chance of eventually electing a node, but the implementation does not provide a strong liveness guarantee. Stable network and scheduling patterns can create an indefinite livelock.

Possible approaches include:

- increasing and measuring the VDF delay spread so the fastest proof can reach peers before most local VDFs finish;
- adding a short collection window and voting for the best valid candidate received during that window;
- using a deterministic ranking rule instead of irrevocably voting for the first local completion; or
- using a common-randomness leader ranking protocol designed for this purpose.

The election timeout should exceed:

```text
maximum expected VDF duration
+ RequestVote network delay
+ GrantVote network delay
+ NewView network delay
+ scheduling and safety margin
```

The timeout should generally increase after consecutive failed views. A fixed 90 ms timeout can cause perpetual view changes whenever normal network or processing latency exceeds the timeout.

Nodes also do not necessarily begin their VDFs simultaneously. They start after individually observing the view-change quorum, so message-delivery order gives some nodes a head start independent of the randomized VDF delay. That may be acceptable for the simulation, but it means the mechanism is not a pure synchronized random race.

## Node-failure scenarios

| Scenario | Current likely outcome |
| --- | --- |
| Up to `f` nodes fail before view change | Recovery is possible only if all remaining `2f+1` nodes enter view change and the relevant timers are active. |
| One candidate fails while running its VDF | Other candidates can still finish and request votes. |
| A candidate fails after receiving some grants | The election is split; recovery depends entirely on voters' new-view recovery timers. |
| A candidate obtains quorum and fails before broadcasting `NewView` | Voters retry only if their recovery timers are active. |
| A candidate sends `NewView` to only some nodes and fails | Nodes become view-split. Active leader-progress and recovery timers may eventually reunify them in a later view. |
| The initial leader fails before any request executes | There is a risk of permanent stall because no initial progress timer is started. |
| Exactly `f` nodes are down and the live votes split | Nobody can win the current view because every remaining live vote is needed for `2f+1`. A timed retry is mandatory. |
| A node falls multiple views behind | It can buffer higher-view messages without entering their view, potentially forever. |

With `N = 3f+1`, exactly `2f+1` nodes remain when `f` nodes have failed. In that condition, the candidate needs every live vote. One live node voting for a different candidate makes that view unwinnable.

## Quorum and buffer bugs

### RequestVote is allowed before a view-change quorum

`HandleRequestVoteMsg` buffers candidates while:

```go
len(n.viewChangeMsgsLog[view]) <= n.fNodes+1
```

That is not equivalent to waiting for `2f+1` messages. With seven nodes:

```text
f = 2
f+1 = 3
2f+1 = 5
```

The current condition begins processing candidates at four view-change messages, before the required quorum of five.

The condition should use the unique count and actual quorum:

```go
if n.uniqueViewChangeCount(view) < n.QuorumSize() {
    // Buffer the candidate.
    return
}
```

### Buffered RequestVote processing can panic or starve valid candidates

`ElectionLogic` tests whether `reqVoteBuffer[view]` is non-nil and then removes index zero. This is unsafe because a slice may be non-nil but empty.

The current behavior also:

- tries only one buffered candidate;
- can leave other valid candidates unprocessed if the first is invalid;
- retains a non-nil empty slice in the map; and
- can panic on a later `[0]` access.

It should test `len(buffer) > 0`, process candidates until one is accepted or the buffer is empty, and delete the map entry after draining it.

### Exact equality checks are brittle

Several paths use:

```go
count == n.QuorumSize()
```

Quorum conditions should normally use:

```go
count >= n.QuorumSize()
```

and be paired with per-view one-shot flags such as:

```go
electionStarted[view]
newViewSent[view]
```

Exact equality depends on perfectly unique and sequential delivery. It becomes fragile with buffered messages, retries, recovered state, or accidental duplicates.

### View-change deduplication is disabled

The deduplication body in `appendViewChangeIfNew` is commented out. A focused existing test, `TestViewChangeSenderIsCountedOnce`, currently fails because a repeated message is accepted.

This is not required to demonstrate the liveness failures under the stated send-once assumption, but robust quorum accounting should always count unique authenticated senders. Exactly-once network delivery should not be a protocol correctness requirement.

## Election safety and authorization problems

### NewView does not prove that its sender won

This is the most important election-safety problem.

A candidate collects signed grants in `HandleGrantVoteMsg`, but:

- `GrantVoteMsg` does not identify the candidate receiving the vote;
- grant signatures are not retained as an election certificate;
- `NewView` does not carry the grant certificate; and
- `verifyNewView` does not verify that its sender obtained `2f+1` election votes.

Any node with the view-change quorum can therefore attempt to construct and send a `NewView`, even if it did not win the VDF race. Under a Byzantine fault model, grants can also be reused or sent to multiple candidates because the signed content does not bind the vote to its intended candidate.

The grant should explicitly name its candidate:

```go
type GrantVoteMsg struct {
    From        int
    CandidateID int
    ViewNumber  int64
}
```

The candidate should retain the signed `GrantVoteMsgSig` messages and include at least `2f+1` of them in `NewView`. A replica accepting `NewView` should verify:

- every grant signature;
- unique grant senders;
- every grant has the same view;
- every grant names `NewView.From` as the candidate; and
- the certificate contains at least `2f+1` valid grants.

Honest-node one-vote behavior gives useful quorum-intersection properties, but it does not replace a verifiable election certificate in the message that installs the leader.

### Another NewView can replace the leader in the same view

`HandleNewView` rejects only messages whose view is less than the current view. It may accept another `NewView` for the current view and overwrite the selected leader.

It should normally reject `NewViewNumber <= n.view`. If same-view replay needs to be accepted for retransmission, it should be idempotent and require the same leader and the same election/view-change certificates.

### Embedded view-change messages are not fully bound to NewView

`verifyNewView` verifies signatures and unique view-change senders, but should also explicitly require each embedded view-change message to have:

```go
vc.ViewNumber == newView.NewViewNumber
```

The quorum certificate must be for the exact view being installed.

### O-set verification can accept omissions

`verifyOSet` checks that entries supplied by the leader match the expected set, but it does not ensure that every required expected entry is present. A leader may be able to omit required prepared operations.

Verification should compare both directions or compare canonicalized complete sets, ensuring there are no invalid additions and no required omissions.

## VRF, seed, and VDF concerns

### The receiver trusts the candidate-provided seed

`VerifyVRFVDF` verifies the VRF and VDF using `reqVote.Seed`, but does not independently reconstruct or validate the seed expected for `reqVote.ViewNumber`.

A Byzantine candidate can try different seeds and publish only the seed that produces a favorable delay. The seed must be canonical and bound to the view.

For the current temporary seed, validation can reconstruct it locally:

```go
expectedSeed := []byte(fmt.Sprintf("view-%d", reqVote.ViewNumber))
if !bytes.Equal(reqVote.Seed, expectedSeed) {
    return false
}
```

When the seed is replaced with a threshold signature, every node should verify the threshold signature and derive the exact same seed deterministically from the signed view/domain-separated message.

### The current modulus has known factors

`VDFLargeModulus` multiplies two public, known Mersenne primes. All nodes do obtain the same modulus, which is useful for simulation and verification, but its factorization is also public.

For a real unknown-order RSA-group VDF, knowing the factors of `N` can allow a Byzantine participant to shortcut the supposedly sequential computation. A secure deployment needs an RSA modulus whose factorization is unknown to all participants, typically generated by a trusted setup, a multiparty ceremony, or replaced by a transparent VDF construction that does not require trusted modulus generation.

The present modulus is suitable for functional simulation, not an adversarial security claim.

## Timer behavior after candidate failure

Starting a new-view recovery timer after a node votes is a good part of the design. It handles these cases:

- votes are split and no candidate reaches quorum;
- the winning candidate fails before `NewView`;
- the winning candidate sends `NewView` to only part of the cluster; or
- `NewView` is delayed or rejected.

The candidate should also keep recovery/progress protection after it locally declares itself leader. If broadcasting `NewView` fails or followers do not install it, the candidate must eventually leave that view rather than believe it is leader forever.

Stopping the election recovery timer after accepting a valid `NewView` is correct. At that point it should be replaced by/reset as the normal leader-progress timer, not leave the node without any liveness timer.

## Recommended election lifecycle

A more robust state machine is:

1. On leader-progress timeout or `f+1` valid messages for any higher view, enter that target view.
2. Stop old-view work and immediately start a per-view recovery timer.
3. Send exactly one signed view-change message for the target view.
4. At `>=2f+1` unique view-change messages, start election work exactly once.
5. Validate buffered candidates and start the local VDF if the node remains unvoted.
6. Vote exactly once using a clearly defined candidate-selection rule.
7. Keep the recovery timer running after voting.
8. Make every grant explicitly name the candidate and view.
9. Have the candidate collect and retain `>=2f+1` signed grants.
10. Include both the view-change certificate and election certificate in `NewView`.
11. Fully verify both certificates before accepting the leader.
12. After accepting `NewView`, stop election recovery and start/reset leader-progress tracking.
13. If any stage times out, advance to a higher view and increase the timeout.
14. Cancel or harmlessly ignore old-view VDF work, and garbage-collect old per-view maps and buffers.

The per-view state should include explicit flags instead of relying on exact counts:

```go
type electionViewState struct {
    entered          bool
    electionStarted  bool
    voted            bool
    votedFor         int
    newViewSent      bool
    newViewAccepted  bool
}
```

## Priority order

### P0: Required for liveness

- Start an election recovery timer immediately upon entering view change.
- Start the initial leader-progress timer when the node starts.
- Remove the `Fixed`/`PeakTpsTest` behavior that disables election recovery.
- Permit catch-up to higher views rather than only `forView+1`.
- Fix the RequestVote VC threshold to `QuorumSize()`.
- Drain buffered candidates safely.
- Use `>= quorum` plus explicit one-shot state.
- Calibrate the VDF race and use timeout backoff to avoid persistent self-vote splits.

### P0: Required for Byzantine election safety

- Bind every grant to a candidate and view.
- Retain signed grant messages.
- Include a `2f+1` election certificate in `NewView`.
- Verify that certificate before installing the leader.
- Prevent conflicting same-view `NewView` replacement.

### P1: Required for cryptographic integrity

- Reconstruct and verify the canonical view seed locally.
- Verify threshold-signature-derived seeds when that mechanism is introduced.
- Replace the known-factor modulus for any adversarial or production use.
- Verify embedded VC view numbers and complete O-set equality.

### P2: Robustness and maintenance

- Restore unique-sender deduplication.
- Make VDF computation cooperatively cancellable.
- Garbage-collect old per-view election state.
- Add end-to-end tests for split votes, candidate failure, partial `NewView`, multiple consecutive view changes, and multi-view catch-up.

## Test coverage observations

- The focused VDF completion test passes, showing that the worker can return its result through the event loop.
- The duplicate view-change sender test currently fails because deduplication is disabled.
- A message-hub RequestVote test can block when its manually constructed test node has no election channel; production construction initializes the channel, but the fixture needs updating.
- Some timer tests construct a node without configuration and panic when timer code dereferences `n.cfg`.
- There is no complete election-liveness test that intentionally creates a split vote and proves the cluster advances and eventually installs one leader.

The most valuable new test would run `3f+1` nodes, force every live node to self-vote in one view, verify that no candidate wins that view, let the recovery timer expire, and then verify that exactly one candidate is installed in a later view. Variants should fail the winning candidate before `NewView`, deliver `NewView` to only a subset, and hold one node several views behind.

## Final assessment

The conceptual protocol is close to a workable retrying election: enter view change, gather a VC quorum, race, vote once, keep a recovery timer active, and retry after a split or failed winner. The current implementation does not yet guarantee those transitions.

The most immediate causes of getting stuck are missing or disabled timers, failure to catch up across multiple views, and recovery beginning only after a successful vote. The most likely cause of repeated livelock is that the VDF race is much faster than message propagation, causing many nodes to self-vote. The most serious safety gap is that `NewView` carries no verifiable proof that its sender won `2f+1` candidate-bound votes.

Fixing those areas will make the implementation much closer to the intended behavior under both consecutive view changes and leader failures.
