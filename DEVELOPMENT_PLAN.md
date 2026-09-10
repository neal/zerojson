# Decoder simplification and confidence plan

Status: architecture experiments completed; production implementation has
not changed. The preferred design is provisional pending the performance
decision below. This is an active maintainer plan, not a release guarantee.

## Objective

Reach a decoder design whose important correctness properties are tractable
to explain and test, while retaining measured performance advantages.
Prefer removing unnecessary state over accumulating guards around it.
Ordinary behavior should follow encoding/json; deliberate extension
contracts must stay explicit and tested.

Success is not another empty review, a fuzz execution total, or a green
regression count. Success requires an explicit architecture decision,
credible discovery tests, an acceptable performance/allocation budget, and
verification of the finalized implementation.

## Current decision and evidence

- Frozen code control: `ba64c77964c2c56e68ccbfe08ce2e5bd565456b7`.
  Later planning-only commits do not change this experimental control.
- Five isolated prototypes compared speculation, persistent arenas, and
  ordinary-container stdlib delegation. Encoding was unchanged: all 35
  generated encoder functions in the codec fixture had matching normalized
  AST hashes.
- Preferred candidate: fresh per-call storage, retaining generated scalar
  decoding and the existing eligible ordered path. This removes persistent
  decoder-owned arena state, not within-call aliasing or callback obligations.
- Reject blanket per-container stdlib delegation in the tested design: its
  pooled collection workloads took about 5–6 times the control time.
- Do not discard ordered decoding indiscriminately: the measured cost was
  significant on flat and ordinary collection shapes.
- Local M4 Pro / Go 1.26.5 measurements of the preferred candidate: flat
  decoding essentially unchanged with zero allocations; mixed Event pooled
  borrowing about 8% slower with 39 → 55 allocations; synthetic collection
  workloads about 23–37% slower with additional allocations. These are not
  amd64 production capacity estimates.
- Bounded discovery fixtures passed 155,520 storage checks and 1,008 callback
  checks on each candidate, including race/checkptr runs. Counts include
  equivalent states and are not coverage percentages.
- The same enumerations rediscovered defects in `3df9255` and `cc905b2`
  without a list of historical JSON reproducers. This was retrospective,
  informed by known bug classes, not a blind holdout.
- No experimental design has passed a complete release gate or long soak.

The reproducible experiment bundle is currently outside the repository;
its location is in the project continuity note. It includes source fixtures,
candidate patches, rebuild/benchmark scripts, raw logs and summaries. Before
implementing the selected design, bring the relevant evidence and tests into
a reviewable development branch; do not depend on temporary directories.

## Gates, in order

### G0 — Freeze and compare architectures [complete]

Control and prototypes are isolated, encoding is unchanged, behavioral
challenges ran, and forward/reverse local benchmarks identify tradeoffs.
The resulting recommendation is the fresh-storage candidate above.

### G1 — Accept a performance and allocation budget [next]

1. Reproduce the preferred candidate/control comparison on amd64 in an
   approved, isolated environment. Do not load a shared host or incur paid
   infrastructure/CI usage without appropriate authorization.
2. Include flat, mixed, and collection-heavy shapes; reused and reset targets;
   copying and borrowing; latency, bytes/op, and allocations/op. Measure
   representative sustained allocation/GC cost, not latency alone.
3. Set explicit acceptable limits with the maintainer. No limits have yet
   been agreed; do not invent acceptance thresholds after seeing results.
4. Record adopt/reject/revise and why. If the cost is unacceptable, isolate
   the particular optimization worth retaining and its invariants; do not
   restore all the previous complexity by default.

Exit: recorded budget and architecture decision supported by repeatable
evidence. Until then, production decoder behavior remains unchanged.

### G2 — Implement the selected ownership design [pending G1]

- Write the within-call ownership/publication rules before refactoring.
- Implement the accepted design in a separate branch from the frozen control.
- Remove obsolete cross-call bookkeeping instead of leaving a prototype
  substitution surrounded by unused state-management logic.
- Keep encoding, full-document validation, scalar readers, and explicit
  registered overrides unless evidence supports a separate decision.
- Update decoder lifetime documentation and allocation expectations honestly.
  Do not add another public API merely to carry the experiment.

Exit: reviewable implementation whose remaining state and transitions are
documented, with equivalent behavior or explicit approved differences.

### G3 — Establish a credible fast discovery suite [with G2]

- Preserve the independent bounded enumeration, not just exact bug seeds.
- Inventory representable operations and observable outcomes. Explicitly
  cover or exclude roots, fields, pointer fields, hidden slice storage,
  null/empty/grow/shrink, duplicate members, caller aliases, callbacks,
  failures/recovery, and registered overrides.
- Use an independent stdlib oracle for ordinary behavior and independently
  specified expectations for deliberate overrides. Do not share production
  classification logic with the oracle's exemptions.
- Calibrate observers and replay representative defective revisions. Distinguish
  retrospective rediscovery from genuinely held-out evidence.
- Propagate race/checkptr into generated child test processes explicitly.
- Report exercised transitions and missing combinations, not just counts.

Exit: a small repeatable suite that distinguishes representative broken
versions from the candidate, plus a clear list of untested interactions.
Avoid claiming exhaustive coverage of arbitrary Go types or callbacks.

### G4 — Verify one stable candidate [pending G2/G3]

During development, run affected invariants and mutants first. Once stable:

- Freeze an exact candidate revision and run the full release gate once.
- Verify supported toolchains, generated-file reproducibility, instrumentation,
  and performance/allocation budgets. Record all failures and reruns.
- Run bounded longer fuzz/resource campaigns on the meaningful state/input
  model; do not substitute soak duration for missing operations.
- Review the architecture delta and residual risks independently where possible.

Exit: complete evidence tied to a revision, no unexplained failures, and
explicit remaining limitations. A code change invalidates relevant prior
results; reuse evidence only when its dependency scope is established.

### G5 — Release decision and continued maintenance [pending G4]

Make an explicit release decision from compatibility, complexity, performance,
and residual-risk evidence. Preserve minimized regressions and the fast
discovery suite. Keep expensive campaigns bounded/on-demand; do not restore
daily CI spending by default. Push/release remains separately authorized.

## Working rules and stop conditions

- At the start of a turn, read this plan and inspect live state. Work on the
  next gate rather than restarting a broad review.
- At a checkpoint, record gate, revision, evidence, decision and next action.
  Do not call a gate complete while required tests are still running.
- For a new defect, record the violated invariant, why discovery missed it,
  and which neighboring cases become testable. A regression seed alone is
  not closure of the defect class.
- If findings contradict an accepted design assumption, pause that gate and
  revisit the decision. Do not silently add another exception or widen an
  oracle allowlist to regain green tests.
- No unrelated optimizations, OMS-specific schemas, or downstream changes.
- Do not benchmark alongside tests/builds. Do not start full certification
  while still exploring fixes, or keep certifying a superseded candidate.

## Latest checkpoint

2026-09-09: G0 complete. Production code remains at the frozen control;
the preferred prototype is not merged. Next action is G1: preserve/replay
the experiment on amd64 and agree on the performance/allocation budget
before production implementation. No background job or scheduled task is
implied by this plan.
