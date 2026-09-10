# Decoder simplification and confidence plan

Status: fresh per-call storage implemented and verified on
`codex/fresh-decoder-storage`. G0–G4 are complete within their stated bounds;
independent review and the maintainer's release decision are next.
This is an active maintainer plan, not a release guarantee.

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
  ordinary-container stdlib delegation. Encoding was unchanged: all 29
  generated encoder functions in the codec fixture had matching normalized
  AST hashes.
- Adopted design: fresh per-call storage, retaining generated scalar
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
- The finalized implementation passed the full release gate and bounded
  two-message fuzz campaigns. No new hours-long soak was run.

The reproducible experiment bundle is currently outside the repository;
its location is in the project continuity note. It includes source fixtures,
candidate patches, rebuild/benchmark scripts, raw logs and summaries. Before
implementing the selected design, bring the relevant evidence and tests into
a reviewable development branch; do not depend on temporary directories.
The independent fixtures and amd64 summary are now checked in under
`conformance/ownership`; raw experimental logs remain in the external archive.

The encoder fingerprint was refined to select `AppendZJSON` methods exactly:
the earlier 35-function count also included six decoder/name-lookup functions
whose type names contained Appender. All 29 actual encoders match control,
prototype and implementation (SHA-256
`bc91e7c6d5d794f80b28494325b38a9e00c8bf7374e40641d12bacf361e202e9`).

## Gates, in order

### G0 — Freeze and compare architectures [complete]

Control and prototypes are isolated, encoding is unchanged, behavioral
challenges ran, and forward/reverse local benchmarks identify tradeoffs.
The resulting recommendation is the fresh-storage candidate above.

### G1 — Accept a performance and allocation budget [complete]

Decision: adopt fresh per-call storage, retaining eligible ordered and scalar
paths. The maintainer delegated this decision after the amd64 comparison.
Accept the measured mixed-workload 9.2% time increase and 39-to-55 allocations,
and synthetic collection increases up to 37.1% with up to 17 allocations.
This is an explicit tradeoff decision, not a retroactively passed predefined
budget. Implementation must preserve flat zero-allocation decoding and must
not materially regress beyond the fresh-ordered prototype; investigate any
repeatable additional slowdown above 5% or additional allocation per operation
on those same fixtures before G4 can close. Production GC/tail-latency capacity
remains unproven by this microbenchmark evidence.

1. Reproduce the preferred candidate/control comparison on amd64 in an
   approved, isolated environment. Do not load a shared host or incur paid
   infrastructure/CI usage without appropriate authorization.
2. Include flat, mixed, and collection-heavy shapes; reused and reset targets;
   copying and borrowing; latency, bytes/op, and allocations/op. Measure
   representative sustained allocation/GC cost, not latency alone.
3. Record explicit acceptable limits with the maintainer; do not describe a
   decision made from measurements as passing a predefined threshold.
4. Record adopt/reject/revise and why. If the cost is unacceptable, isolate
   the particular optimization worth retaining and its invariants; do not
   restore all the previous complexity by default.

Exit: recorded budget and architecture decision supported by repeatable
evidence. Until then, production decoder behavior remains unchanged.

### G2 — Implement the selected ownership design [complete]

Within-call ownership rules:

- A Decoder retains no arena or result storage between calls. Each call resets
  its destination, validates the whole input, then creates its own arena.
- Current destination pointers and container headers are authoritative, including
  replacements made by callbacks. Detached storage is never recycled.
- A published pointer cell cannot be republished after detachment within a call;
  retain pointer publication guards even though cross-call bookkeeping disappears.
- Ordinary reused slices preserve stdlib slot/null semantics. Registered overrides
  keep their explicitly tested reset semantics; these are not interchangeable.
- Borrowed strings still depend on the input buffer, independent of Decoder life.
- Ordered fallback must obey the existing no-callback-replay eligibility rules.

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

### G3 — Establish a credible fast discovery suite [complete, bounded]

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

### G4 — Verify one stable candidate [complete, bounded]

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

### G5 — Release decision and continued maintenance [next]

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

2026-09-09: G0 complete; the measurement portion of G1 is complete. The user
authorized a temporary DigitalOcean c-2 / sfo3 VM (599231920), exposing Intel
Xeon Platinum 8280 / amd64. It was deleted after collecting results, and
its absence was verified through the API. No extra volumes or backups remain.

Control and fresh-ordered binaries used Go 1.26.5, CGO disabled. Both passed
the bounded correctness checks. Six samples per benchmark ran in ABBA order
on one pinned CPU. Flat decoding was essentially unchanged; fresh ordered
increased mixed pooled Event time by 9.2%, synthetic pooled time by 25–37%,
and allocations. Non-pooled copy/borrow paths were essentially unchanged.

For 100,000 decodes of a separate 64-element borrowing payload, control
allocated zero bytes and performed no GC; fresh ordered allocated 6,960
bytes/decode and performed 239–240 GC cycles in 2.02–2.05 seconds. This
is a real allocation/throughput tradeoff, not evidence of acceptable
production tail latency. Detailed evidence is in the experiment bundle's
DO_RUN.md, do-summary.json and do-evidence directory.

The maintainer subsequently delegated the decision: G1 is accepted as above.
Implementation starts from `ad80ff0` on `codex/fresh-decoder-storage`.
Next action: remove persistent decoder arenas and obsolete bookkeeping, port
the bounded discovery fixtures, update ownership documentation and mutation
anchors, then run targeted verification before freezing a G4 candidate.
No background benchmark, VM, or recurring scheduled task remains running.

Implementation checkpoint: per-call arenas replace persistent Decoder fields;
removed beginDecode, slice/map ownership flags, root reset flags, and redundant
first-use pointer clearing. Pointer publication guards remain. SameBacking and
SameMap remain available for compatibility, but generated decoders no longer
use them. Documentation and allocation ceilings reflect the new lifetime.

Development verification (working tree based on ad80ff0):

- Root and generator module tests passed on Go 1.26.5; codec and ownership
  package tests passed on Go 1.26.5 and Go 1.27.0.
- Independent challenge, scalar reuse matrix, generator behavior matrix and
  generated-schema campaign passed. Independent challenge and scalar matrix
  also passed with explicit race/checkptr instrumentation in their children.
- Ownership enumerations, retained-result tests and affected codec tests passed
  with race/checkptr. All generated fixtures regenerated successfully.
- First mutation run: 4 of 45 survived. Three detectors only reached now-removed
  cross-call behavior; moved them to duplicate/registered-override slice cases.
  The fourth removed redundant zeroing of an unpublished fresh pointer cell;
  removed that production code and obsolete mutant. Revised run: 44/44 killed
  for their intended diagnostics, after clean detector baselines.
- Same-host ABBA comparison against the accepted prototype: all five pooled
  borrowing medians within 2%, same allocation counts, fewer allocated bytes
  for collection shapes. Flat decoding remains zero-allocation. Individual
  measurements are in ownership/implementation-comparison.json. This is local
  implementation/prototype evidence, not a new amd64 production capacity test.

G2/G3 are complete within the documented finite inventory, not universal proof.
Next: commit this candidate, then run its full release gate without code edits.
G4 still needs full-gate, supported-toolchain and bounded campaign evidence.

First frozen full-gate attempt at `a863be9` stopped in generator tests: one
embedded shape-validation fixture still asserted pre-simplification allocation
budgets (0/2/4 rather than the measured 5). Updated that test-only budget and
its stale name/comment. No production code changed; prior behavioral,
instrumentation, mutation and performance evidence remains applicable.

Final frozen candidate: `9ce7187a56e834b19212eed9348098099b6f880f` (production
implementation from `a863be9`). Both commits are SSH-signed and verified.

- `GOTOOLCHAIN=go1.26.5 GOMAXPROCS=2 scripts/verify.sh full` passed end to end:
  all three modules' tests/vet/race/checkptr, nounsafe build/tests, clean fixture
  regeneration including ownership, exhaustive domains (393.375s), synthetic
  shadow, and all 44 mutation detectors (137.89s).
- Go 1.27.0: `go test ./... -count=1` passed in all three modules.
- `FuzzPoolPair`, one worker, `-fuzztime=1000000x`, five-minute timeout:
  Go 1.26.5 passed 1,000,022 executions in 47.681s; Go 1.27.0 passed 1,000,000
  in 54.809s. These check fresh-decoder agreement and retained first results;
  the separate ownership enumeration supplies the independent stdlib oracle.
- No uncommitted generated drift, background run, new paid infrastructure,
  push, or release remains. Subsequent verification-record changes are docs only.

Remaining limits: bounded schemas and callback programs, not universal coverage;
retrospective mutation calibration, not a blind holdout; no hours-long soak or
production tail-latency guarantee. The amd64 experiment measured the prototype;
the final simplification was compared with it on arm64, not re-timed on amd64.
No second reviewer has independently reviewed this final architecture delta.

Concrete next action: independent review of the frozen ownership delta and
test-oracle boundaries, followed by the maintainer's merge/release decision.
Do not restart an open-ended review/fuzz loop or repeat the full gate without
new evidence or a relevant code change.
