--------------------------- MODULE AwareSchedule ---------------------------
(***************************************************************************)
(* The leader-schedule layer of PBFT with an Aware-style policy.           *)
(*                                                                         *)
(* What is modelled                                                        *)
(*   - Generations g = 1..MaxGen. The leader of view (g, c) is computed    *)
(*     locally from a member set C_g: members in id order, index           *)
(*     (g + c) mod |C_g|.                                                  *)
(*   - How a node obtains C_g, in two variants:                            *)
(*       "pilot"     : the current code. A fixed aggregator sends each     *)
(*                     node an aggregate; a node that missed it adopts the *)
(*                     rows carried by the single ViewChange / NewView     *)
(*                     that pulled it into the generation.                 *)
(*       "certified" : a coordination consensus decides one value per      *)
(*                     epoch with a transferable commit certificate; a     *)
(*                     node enters g+1 only holding the certificate for    *)
(*                     epoch g, and catch-up messages carry it.            *)
(*   - Scores are not modelled: the adversary picks ANY member set with at *)
(*     least Floor members, every time a set is produced. This is the      *)
(*     strongest score manipulation possible, so what holds here holds for *)
(*     every real scoring rule that respects the floor.                    *)
(*   - Data-plane views are abstracted after GST by a view synchronizer:   *)
(*     the honest nodes of a generation try views together; a view        *)
(*     succeeds iff every honest node is in that generation, they all      *)
(*     compute the same leader, and that leader is Good (honest and fast   *)
(*     enough for the trigger). Otherwise the adversary can make it fail   *)
(*     (with n = 3f+1 a view needs all 2f+1 honest nodes when the f        *)
(*     Byzantine ones withhold). Before GST any view may fail.             *)
(*                                                                         *)
(* What is not modelled: PBFT safety (prepared certificates, O-sets), the  *)
(* coordination consensus internals (assumed: at most one certified value *)
(* per epoch; after GST it decides), message contents, timers.            *)
(***************************************************************************)
EXTENDS Integers, FiniteSets

CONSTANTS
    N,            \* number of nodes, n = 3f + 1
    Byz,          \* Byzantine nodes
    Slow,         \* honest nodes too slow for the trigger's timeout
    Floor,        \* minimum member-set size
    MaxGen,       \* generations explored
    CtrMod,       \* view counters are kept mod CtrMod (lcm of the possible |C|)
    PreSyncLimit, \* failed views allowed before a generation synchronizes are
                  \* limited to ctr < PreSyncLimit. Only ctr mod |C| matters, so
                  \* PreSyncLimit >= N still covers every start position; set it
                  \* to CtrMod for no limit.
    Variant,      \* "certified" or "pilot"
    Aggregator,   \* pilot: the fixed epoch aggregator (node 4 in the code)
    ForgedCatchUp,\* pilot: a Byzantine ViewChange / NewView sender can hand a
                  \* lagging node made-up rows (FALSE isolates other failures)
    StartAtGST    \* TRUE: start after GST with all honest nodes synchronized

Nodes   == 1..N
Honest  == Nodes \ Byz
Good    == Honest \ Slow
Gens    == 1..MaxGen
Epochs  == 1..(MaxGen - 1)   \* epoch g's value feeds generation g + 1
NoSet   == {}                  \* "nothing received"; as a schedule: all nodes
BadBound == Cardinality(Byz \cup Slow)

ValidSets == {C \in SUBSET Nodes : Cardinality(C) >= Floor}

\* A member set as used by the leader function: an empty set means the pilot's
\* fallback, every node in id order.
Eff(S) == IF S = NoSet THEN Nodes ELSE S

\* Leader of view (g, c): the member whose rank in id order is (g + c) mod k.
Leader(S, g, c) ==
    LET C == Eff(S)
        k == Cardinality(C)
    IN  CHOOSE x \in C : Cardinality({y \in C : y < x}) = (g + c) % k

VARIABLES
    dec,        \* certified: dec[e] = the certified value of epoch e, or NoSet
    agg,        \* pilot: agg[i][e] = the aggregate of epoch e node i received
    aggVal,     \* pilot, honest aggregator: the one value it sends for epoch e
    gen,        \* gen[i]: node i's generation
    sched,      \* sched[i]: the member set node i uses in gen[i]
    ctr,        \* ctr[g]: the view counter (mod CtrMod) the synchronizer is at in g
    installed,  \* installed[g]: a Good leader was installed in g
    run,        \* ghost: failed views in g since every honest node was in g after
                \* GST (-1 before that point), capped at BadBound + 1
    gst

vars == <<dec, agg, aggVal, gen, sched, ctr, installed, run, gst>>

\* Start counting failed views in every generation that all honest nodes occupy
\* after GST. Applied to the post-state of every action that can cause it.
SyncAfter(gen2, gst2) ==
    [g \in Gens |->
        IF run[g] = -1 /\ gst2 /\ \A i \in Honest : gen2[i] = g
        THEN 0 ELSE run[g]]

Init ==
    /\ dec = [e \in Epochs |-> NoSet]
    /\ agg = [i \in Honest |-> [e \in Epochs |-> NoSet]]
    /\ aggVal = [e \in Epochs |-> NoSet]
    /\ gen = [i \in Honest |-> 1]
    \* Generation 1's set is agreed (genesis config) but adversarial.
    /\ \E C1 \in ValidSets : sched = [i \in Honest |-> C1]
    /\ \E c0 \in 0..(N - 1) :
          ctr = [g \in Gens |-> IF g = 1 THEN c0 ELSE 0]
    /\ installed = [g \in Gens |-> FALSE]
    /\ gst = StartAtGST
    /\ run = [g \in Gens |-> IF g = 1 /\ StartAtGST THEN 0 ELSE -1]

-----------------------------------------------------------------------------
(* Coordination: produce epoch e's value.                                  *)

\* certified: the coordination consensus decides one value for epoch e.
CPDecide(e) ==
    /\ Variant = "certified"
    /\ dec[e] = NoSet
    /\ \E C \in ValidSets : dec' = [dec EXCEPT ![e] = C]
    /\ UNCHANGED <<agg, aggVal, gen, sched, ctr, installed, run, gst>>

\* pilot: the aggregator's aggregate for epoch e reaches honest node i, which
\* keeps it only while it is still in generation e (the code ignores an
\* aggregate for another generation). A Byzantine aggregator can send each node
\* a different valid aggregate; an honest one sends the same to everybody.
AggDeliver(i, e) ==
    /\ Variant = "pilot"
    /\ gen[i] = e
    /\ agg[i][e] = NoSet
    /\ IF Aggregator \in Byz
         THEN \E C \in ValidSets : agg' = [agg EXCEPT ![i][e] = C]
                /\ UNCHANGED aggVal
         ELSE IF aggVal[e] = NoSet
                THEN \E C \in ValidSets :
                        /\ aggVal' = [aggVal EXCEPT ![e] = C]
                        /\ agg' = [agg EXCEPT ![i][e] = C]
                ELSE /\ agg' = [agg EXCEPT ![i][e] = aggVal[e]]
                     /\ UNCHANGED aggVal
    /\ UNCHANGED <<dec, gen, sched, ctr, installed, run, gst>>

-----------------------------------------------------------------------------
(* Generation switches.                                                    *)

\* After GST a generation lasts long enough (epoch length assumption): nobody
\* leaves g before a Good leader was installed in it or BadBound + 1 views
\* were tried. Before GST anyone can leave at any time.
MayLeave(g) ==
    \/ ~gst
    \/ installed[g]
    \/ run[g] > BadBound

\* Node i's own switch (epoch timer fired, decision arrived).
Switch(i) ==
    LET g == gen[i] IN
    /\ g < MaxGen
    /\ MayLeave(g)
    /\ IF Variant = "certified"
         THEN /\ dec[g] # NoSet                    \* needs the certificate
              /\ sched' = [sched EXCEPT ![i] = dec[g]]
         ELSE /\ agg[i][g] # NoSet                 \* the decision needs it
              /\ sched' = [sched EXCEPT ![i] = agg[i][g]]
    /\ gen' = [gen EXCEPT ![i] = g + 1]
    /\ run' = SyncAfter(gen', gst)
    /\ UNCHANGED <<dec, agg, aggVal, ctr, installed, gst>>

\* Node i is pulled into gen[i] + 1 by f+1 ViewChanges or a NewView. That needs
\* at least one honest node already there (f Byzantine messages are not f+1).
\* certified: the message carries epoch g's certificate, so i adopts dec[g].
\* pilot: if i has epoch g's aggregate it uses it; otherwise it adopts the rows
\* of the one message that pulled it in: an honest sender's set, or anything a
\* Byzantine sender makes up.
CatchUp(i) ==
    LET g == gen[i] IN
    /\ g < MaxGen
    /\ \E j \in Honest : gen[j] > g
    /\ IF Variant = "certified"
         THEN /\ dec[g] # NoSet
              /\ sched' = [sched EXCEPT ![i] = dec[g]]
         ELSE IF agg[i][g] # NoSet
                THEN sched' = [sched EXCEPT ![i] = agg[i][g]]
                ELSE \/ \E j \in Honest :
                          /\ gen[j] = g + 1
                          /\ sched' = [sched EXCEPT ![i] = sched[j]]
                     \/ /\ ForgedCatchUp /\ Byz # {}
                        /\ \E C \in ValidSets :
                              sched' = [sched EXCEPT ![i] = C]
    /\ gen' = [gen EXCEPT ![i] = g + 1]
    /\ run' = SyncAfter(gen', gst)
    /\ UNCHANGED <<dec, agg, aggVal, ctr, installed, gst>>

-----------------------------------------------------------------------------
(* Data-plane views (synchronizer abstraction).                            *)

InG(g) == {i \in Honest : gen[i] = g}

\* The view at ctr[g] can succeed: every honest node is in g, they agree on
\* the leader, and it is Good.
CanSucceed(g) ==
    /\ InG(g) = Honest
    /\ \E l \in Good : \A i \in Honest : Leader(sched[i], g, ctr[g]) = l

ViewSucceeds(g) ==
    /\ ~installed[g]
    /\ CanSucceed(g)
    /\ installed' = [installed EXCEPT ![g] = TRUE]
    /\ UNCHANGED <<dec, agg, aggVal, gen, sched, ctr, run, gst>>

\* The view fails and the honest nodes in g move to the next counter. After GST
\* the adversary cannot fail a view that can succeed.
ViewFails(g) ==
    /\ ~installed[g]
    /\ InG(g) # {}
    /\ gst => ~CanSucceed(g)
    /\ run[g] = -1 => ctr[g] < PreSyncLimit
    /\ ctr' = [ctr EXCEPT ![g] = (ctr[g] + 1) % CtrMod]
    /\ run' = [run EXCEPT ![g] = IF run[g] = -1 THEN -1
                                   ELSE IF run[g] > BadBound THEN run[g]
                                   ELSE run[g] + 1]
    /\ UNCHANGED <<dec, agg, aggVal, gen, sched, installed, gst>>

GST ==
    /\ ~gst
    /\ gst' = TRUE
    /\ run' = SyncAfter(gen, TRUE)
    /\ UNCHANGED <<dec, agg, aggVal, gen, sched, ctr, installed>>

Next ==
    \/ \E e \in Epochs : CPDecide(e)
    \/ \E i \in Honest, e \in Epochs : AggDeliver(i, e)
    \/ \E i \in Honest : Switch(i) \/ CatchUp(i)
    \/ \E g \in Gens : ViewSucceeds(g) \/ ViewFails(g)
    \/ GST

\* Fairness after GST: views of every generation keep being tried, lagging nodes
\* catch up, the coordination layer decides.
Fairness ==
    /\ \A g \in Gens : WF_vars(ViewSucceeds(g)) /\ WF_vars(ViewFails(g))
    /\ \A i \in Honest : WF_vars(CatchUp(i)) /\ WF_vars(Switch(i))
    /\ \A e \in Epochs : WF_vars(CPDecide(e))
    /\ \A i \in Honest, e \in Epochs : WF_vars(AggDeliver(i, e))
    /\ WF_vars(GST)

Spec == Init /\ [][Next]_vars /\ Fairness

-----------------------------------------------------------------------------
(* Properties.                                                             *)

TypeOK ==
    /\ gen \in [Honest -> Gens]
    /\ \A i \in Honest : sched[i] = NoSet \/ sched[i] \in ValidSets
    /\ ctr \in [Gens -> 0..(CtrMod - 1)]
    /\ run \in [Gens -> -1..(BadBound + 1)]

\* Every honest node's member set respects the floor (the pilot's fallback,
\* every node, trivially does).
FloorOK == \A i \in Honest : Cardinality(Eff(sched[i])) >= Floor

\* Schedule agreement: honest nodes in the same generation compute the same
\* leader for every view.
Agreement ==
    \A i, j \in Honest :
        gen[i] = gen[j] => Eff(sched[i]) = Eff(sched[j])

\* Bounded bad run: after GST, once every honest node is in g, a Good leader is
\* installed within BadBound failed views (BadBound = |Byz \cup Slow|).
BoundedBadRun == \A g \in Gens : run[g] <= BadBound

\* Liveness: after GST the system reaches a generation that every honest node
\* is in and that has a Good leader installed.
EventuallyGoodLeader ==
    <>(\E g \in Gens : installed[g] /\ InG(g) = Honest)

=============================================================================
