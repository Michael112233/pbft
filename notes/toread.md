# To read / open points

## 4. Multi-leader (Mir-BFT / RCC / Ladon) — defer

Biggest throughput story in the literature (10–30×), but it needs a partitioned request
hash space to stop duplication, plus global ordering across instances. At n=4 and
200 tx/s injection you aren't leader-bandwidth-bound, so you'd pay the whole
implementation cost for an arm that never wins in your setup. Revisit only if you scale
n up.

## The thing that will actually block learning

Your agent currently decides on synthetic state.

A reputation policy is only learnable if the state contains something like per-node
participation rate; a latency-aware policy needs RTT spread/variance. If you add
policies without the matching features, the bandit can't tell CrashedNode from Healthy
and every new arm is pure exploration cost. BFTBrain's feature set is a good shopping
list and maps onto what you already compute:

- fast-path ratio
- messages received per slot
- average interval between consecutive leader proposals
- request/reply size
- client send rate

The inter-proposal interval in particular is what separates "slow leader" from "dead
leader" — exactly the ProposalDelay vs. CrashedNode distinction.

## If you take two

Reputation + crashed-node, and hash-random + targeted-attack. They're the cheapest to
build, they win for reasons no existing arm can replicate, and each has a scenario where
it clearly loses too (reputation is pointless in Healthy; hash-random costs you the
fairness of RR), which is what makes them worth learning rather than just dominating.

## Sources

- [Mir-BFT (arXiv:1906.05552)](https://ar5iv.labs.arxiv.org/html/1906.05552)
- [Ladon (EuroSys'25)](https://arxiv.org/html/2409.10954)
- [BFTBrain: Adaptive BFT Consensus with Reinforcement Learning, NSDI'25](https://arxiv.org/html/2408.06432v1)
