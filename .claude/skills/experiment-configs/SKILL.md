---
name: experiment-configs
description: Write a set of PBFT experiment configs (a pilot, a sweep, a rerun, a comparison batch) into config/<folder>/ together with a README.md that states the intention of the batch. Use whenever creating more than one experiment config, a pilot, a parameter sweep, or a batch of runs to compare against earlier results — even when the request is just "make configs for X" or "run a few pilots".
---

# Experiment configs: one folder, one README

Every batch of experiment configs lives in its own folder under `config/`, and that
folder has a `README.md` saying why the batch exists. Configs outlive the
conversation that made them; six weeks later `config/prep_sweep_max/` means nothing
without its intent, and the results in `results/` are only interpretable against it.

**Do not run the experiments.** The user runs them on a separate machine. Write the
configs and the README, show them, and give the exact run command. Do not start
`alt_run_project.sh`, `run_experiments.sh`, `run_long_experiment.sh` or
`scripts/run_isolated*.sh`, not even a short smoke run, unless the user asks for
that run in this conversation.

## Steps

1. **Pick the base.** Start from `config/run2new.json` (the base config, per
   CLAUDE.md), or, for a rerun or comparison, from the earlier run's own
   `results/<run>/config.json`, so only the intended variable changes.
2. **Diff against run2new.** List keys present in `config/run2new.json` but missing
   from the base you copied, and decide each one: an absent key takes the default
   in `config/*.go`, which may not be what the earlier run did.
3. **Check what each value actually does in the current code**, not what its name
   says. Grep for the reader before relying on a knob; several are dead
   (`max_batch_delay`, `proposal_delay_ms`, `nodes_in_dark`). Known trap: the
   Fixed/Perf timeouts changed twice. Before 2026-10-09 `fixed_trigger_timeout_ms`
   applied everywhere. From 2026-10-09 to the `timer-model` branch (2026-10-16), in
   scenario mode it applied only to NetworkDelay and NetworkDelayFCrash, and every
   other scenario used `relaxed_fixed_trigger_timeout_ms`. On `timer-model` that key
   is rejected: `fixed_trigger_timeout_ms` is the progress timeout everywhere and
   `fixed_new_view_timeout_ms` the new-view one (`fixed_timers_shared` uses the larger
   of the two for both). ProposalDelay is `proposal_delay_interval_ms`: a spacing
   between a slow leader's proposals with the first one of a tenure free, no longer a
   sleep before every proposal (`proposal_delay_sleep_ms` is rejected).
4. **Write the configs** to `config/<folder>/<name>.json`. Give the files names that
   are unique across `results/`: `run_experiments.sh` saves to
   `results/<config basename>` and deletes an existing directory of that name first.
   Put a `_note` in each config saying what that one run is.
5. **Write `config/<folder>/README.md`** with the sections below.
6. **Show the user a table of the configs** (scenario, action, the varied
   parameter, the values that matter) and the run command. Stop there.

## README.md sections

- **Intention** — the question the batch answers, in one or two sentences, and
  what result would change a decision.
- **Compared against** — the earlier results (path under `results/`) or the other
  arm of the comparison, and why it is comparable.
- **What changes, what is held fixed** — the varied parameter(s) and every value
  that differs from the base, each with the reason. Call out values set only to
  reproduce an earlier run (such as the timer key above).
- **Configs** — a table: file, scenario, action, varied value.
- **How to run** — the exact command, duration per run, expected total time,
  where results land, and anything the host needs (`client_num` > 1 needs the
  committed harness; isolated worktrees must be moved to the right commit).
- **How to analyse** — the scripts to run on the results (for example
  `scripts/scenario_pilot_summary.py`) and which numbers to compare.
- **Expected outcome / caveats** — what we expect and why, plus known confounders
  (code commits between the old and new runs, different host or CPU pinning,
  short runs).
- **Provenance** — date, git commit the configs were written against, and the
  conversation's request in one line.

Keep it short: a reader should know in a minute why these configs exist and how to
use them.
