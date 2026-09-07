# conformance

Differential and byte-identity test suites that need external oracles
(easyjson, `google/uuid`, and `go-json-experiment/json` as a third decode
oracle). Its own module so the root `zerojson` package
stays dependency-free; see [repository layout](../CONTRIBUTING.md#repository-layout).

- `runtime/` — external-oracle checks for the dependency-free runtime:
  exhaustive/boundary domain accounting, JSONTestSuite, strings, and UUIDs.
- `codec/` — exercises supported field kinds
  (`Event` and its sibling types in `types.go`) against `encoding/json`
  (the decode oracle) and easyjson (the encode byte-identity target,
  including a genuinely easyjson-generated `EventEJ` oracle in
  `easyjson_oracle.go`, not a hand-simulated comparison). Named slice/map
  ROOT types (`EventList []Event`, `LegPtrList []*EventLeg`, `LegIndex
  map[string]EventLeg`) cover the generator's root support in
  `root_types_test.go` (encode byte-identity, round-trip, null/empty rules,
  pooled reuse) and the generated root fuzzers. `benchmark_test.go` holds the
  in-repo benchmark suite (encode/decode vs easyjson, ordered-vs-fallback,
  projection, `Valid`, and a batch replay over the golden
  corpus).
- `generator/` — generator compile, behavioral, and deterministic schema
  campaigns, plus `compat/` wrapper tests and the finite `typeshape/`
  classification matrix.
- `mutation_test.go` — repository-wide curated mutation testing.

## Which `encoding/json` is the oracle

Go 1.27 made `encoding/json` a compatibility layer over the v2
implementation. `internal/oracle` probes (never version-checks) which one
is loaded and the suite accounts for the drifts documented in
[the compatibility guide](../docs/compatibility.md#which-encodingjson-go-127-and-later): invalid-UTF-8
output is normalized to the classic `\ufffd` escape before byte
comparison, the generated fuzzers allow zerojson to reject the `"+00"`
that v1-on-v2 accepts in a `,string` field, the type-shape matrix skips
its stdlib comparison for the invalid-tag-name cell, and the `Event`
encode allocation budget carries a figure for each implementation.
Escaped quoted nulls on `,string` pointers and nulls on value-typed
TextUnmarshaler containers are pinned separately, including the retained
map entries after a null followed by another value. Outside the documented
divergences, the normal value and byte-identity comparisons still apply.

## Testing story

Coverage and its oracles (extended campaigns run only when requested):

| Property | Oracle(s) | Where |
|---|---|---|
| Encode byte-identity | easyjson (real generated code) | `codec/event_codec_test.go` (`TestByteIdenticalToStdlib`* against stdlib too, since the two agree for the fixture), `codec/value_fuzz_test.go`'s `FuzzEventValueSpaceEncode` (value-space, not JSON-text-space — reaches NaN/Inf, invalid UTF-8, extreme decimals) |
| Decode semantics | `encoding/json` v1 (primary contract) | `codec/event_codec_test.go`'s `FuzzDecodeAgainstStdlib`, `TestRoundTripAgainstStdlib` |
| Raw decoded Go-value semantics | `encoding/json` v1, compared before re-encoding can normalize differences | `codec/string_behavior_fuzz_test.go` plus each generated per-type fuzzer; opaque custom-codec fields are excluded field-by-field |
| Generator method/addressability semantics | `encoding/json` over an oracle alias in a freshly generated fixture package | `generator/behavior_test.go`; systematically crosses JSON/Text, value/pointer receivers, and direct/slice/map positions |
| Type and package boundaries | compile/run fixtures plus stdlib behavior | `generator/shape_validation_test.go`, `type_shape_behavior_test.go`, `package_layout_test.go`, and `regeneration_test.go` |
| Pool safety and callback/error behavior | retained caller memory, panic recovery, and stdlib parity | `codec/pool_safety_test.go`, `callback_test.go`, `error_path_test.go`, and `generator/error_path_test.go` |
| Named-container null semantics | `encoding/json`, with the v1-on-v2 TextUnmarshaler drift pinned | `generator/null_policy_test.go`; 700 cases crossing underlying kinds, method sets, pointers, duplicate keys, reused targets, and copy/borrow/pooled modes |
| Raw fuzz-oracle sensitivity | deliberately wrong generated decoders must fail with the expected diagnostic | `generator/raw_oracle_test.go`; 15 mutants across struct, slice, map, and nested Raw graphs, checking values and both error directions |
| Generated-method provenance | generator-specific source headers plus stdlib byte identity | `cmd/zerojsongen`'s `TestReplaceableCodecOutputIsGeneratorSpecific` and `generator/typeshape`'s `L_text*` cells |
| Generated schema reachability | `encoding/json` acceptance plus the per-type differential properties | `codec/zerojson_gen_fuzz_test.go`; every declared field receives a correctly-shaped seed, containers are non-empty, and escaped-string, invalid-UTF-8, and both `[]byte` forms are seeded |
| Multi-feature schema interactions | `encoding/json` over reproducible generated temporary packages | `generator/schema_campaign_test.go`; random multi-field schemas plus 32 type/container combinations, both generation modes; controlled by `ZEROJSON_SCHEMA_SEED` / `ZEROJSON_SCHEMA_COUNT` |
| Stateful caller-owned alias graphs | `encoding/json`, pointer topology, callback counts, and retained input bytes | schema campaign's `testdata/schema_campaign` fixture; 512 operation pairs plus opt-in `ZEROJSON_SCHEMA_FUZZTIME` sequence fuzzing; failed packages/corpora retained under `testdata/schema-failures` |
| Decode semantics, 3rd opinion | `encoding/json/v2` (via `go-json-experiment/json`) | `codec/jsonv2_test.go`'s `FuzzDecodeAgainstJSONV2` and the three divergence-pinning tests below |
| Ordered-vs-fallback decode agreement | zerojson's own two internal paths | `codec/ordered_test.go`'s `FuzzOrderedVsFallback` |
| Truncation safety | none needed (error-not-panic is enumerable) | `codec/truncation_test.go` |
| Resource scaling and amplification | N-vs-4N time/allocated-byte ratios plus `encoding/json` allocation baseline | `codec/linearity_test.go`, `codec/resource_scaling_test.go` |
| Copy-mode non-aliasing | independent stdlib snapshot | `codec/aliasing_test.go` |
| Pooled-decoder non-leakage | fresh (non-pooled) zerojson decode | `codec/pool_test.go` (hand-picked), `codec/pool_fuzz_test.go`, and generated four-operation `Fuzz<Type>ZJSONSequence` state machines for every explicit pooled root with mixed copy/borrow modes and copy-input scribbling |
| Test-suite sensitivity | curated compile-valid runtime/generator mutants | `mutation_test.go` via `scripts/mutation-test.sh` (`quick` developer tier, `full` pre-release) |
| Production-shaped shadow replay | `encoding/json` semantic oracle plus four zerojson decode modes | `codec/corpus_shadow_test.go`; checked-in synthetic JSON/JSONL plus optional sanitized captures via `scripts/shadow-corpus.sh` |
| Modeled wire possibilities | exhaustive pairwise/three-wise covering arrays plus bounded adversarial shapes | `codec/synthetic_shadow_test.go`, `codec/SHADOW_MODEL.md`, and `scripts/synthetic-shadow.sh` |
| Float formatting | `strconv.AppendFloat` (root module, dependency-free) | root `float_test.go` |
| `-race` safety under shared intern tables | Go's race detector | `codec/race_test.go` |
| `Valid` verdict parity | `encoding/json.Valid` | root `valid_test.go` (table + `TestValidAllocs`), `codec/valid_test.go`'s `FuzzValidAgainstStdlib`, `TestValidTruncationSweepMatchesStdlib`, `TestValidAdversarial` |
| Trailing-data rejection | `encoding/json.Unmarshal` / easyjson's `jlexer.Lexer.Consumed` | `codec/trailing_test.go`, `generator/compat/trailing_test.go` (including the `IsStart()`-guard nested-field regression) |
| Strict skip (unknown-field grammar) | `encoding/json.Unmarshal` (unknown field genuinely skipped, not materialized) | `codec/strict_skip_test.go` |
| Decode-implies-valid composition | `encoding/json.Valid` | `codec/decode_implies_valid_test.go`'s `FuzzDecodeImpliesValid`, `TestDecodeImpliesValidHandPicked` |

`scripts/verify.sh race` runs tests, vet, race, and checkptr across all three
modules (root, `cmd/zerojsongen`, `conformance`), plus the root `nounsafe`
build; `full` adds
regeneration cleanliness, exhaustive domains, synthetic shadowing, and mutation
testing. Run larger count- or time-based fuzz campaigns
locally when changing parser, generator, ownership, or pooling behavior.
`scripts/fuzz-all.sh` discovers all targets across all modules dynamically and
supports modulo sharding through `FUZZ_SHARD_INDEX` / `FUZZ_SHARD_COUNT`.
Focused campaigns can set `FUZZ_TARGET_PATTERN` to a Bash regular expression;
for example, `FUZZ_TARGET_PATTERN='ZJSONSequence$'` runs only the generated
pooled-decoder state machines. Sharding is applied after this filter so focused
campaigns remain evenly distributed across workers.

The suite's own sensitivity is checked by `scripts/mutation-test.sh`. Its
`quick` tier injects compile-valid faults in core ownership, grammar-boundary,
callback-error, pooled-state, ordered-replay, and custom-Marshaler paths and
requires a specific detector to fail for each one. The
`scripts/mutation-test.sh full` tier adds the remaining container/root call
sites and reset mechanisms for a pre-release gate. Mutations run in
a temporary repository copy; the harness rejects compile failures or unrelated
test failures as invalid kills.

`codec/corpus_shadow_test.go` replays the checked-in synthetic record corpus
through `encoding/json` and zerojson's plain/pooled copy/borrow entry points,
then repeats the comparison after deterministic whitespace, key-order,
unknown-field, duplicate-key, omission, null, escaped-key, and scalar
mutations. `FuzzProductionCorpusShadow` feeds the same records into the
long-running raw-byte fuzz campaign. To add local production evidence without
committing payloads, put sanitized `MapFieldsLike`-shaped `.json`, `.jsonl`, or
`.ndjson` files in a directory and run
`scripts/shadow-corpus.sh /path/to/corpus`. Records are
limited to 16 MiB, must contain at least one exact `op`, `set`, or `where` key,
and raw payload bytes are never included in assertion messages. Each `.json`
file is one record; JSONL/NDJSON files contribute one record per non-empty line.

When captures are unavailable, `codec/synthetic_shadow_test.go` generates the
possibility space documented in `codec/SHADOW_MODEL.md`. Pairwise coverage runs in
normal CI. `scripts/synthetic-shadow.sh full` expands this to every three-way
interaction plus bounded deep, wide, long, whitespace-heavy, and
duplicate-heavy records. The covering-array verifier is separate from the row
generator, and representative rows also seed `FuzzProductionCorpusShadow`.

## Validation coverage

`Valid` targets `encoding/json.Valid` acceptance, including invalid UTF-8,
unpaired Unicode escapes, and the 10,000-container depth limit. Boundary tests,
JSONTestSuite, truncation sweeps, and `FuzzValidAgainstStdlib` check that
contract. Decode-implies-valid tests cover keys, skipped values, and trailing
data; ordinary value-reader leniency is outside that property.

## `encoding/json/v2` as a third oracle

`codec/jsonv2_test.go` uses `github.com/go-json-experiment/json` as a
separately pinned oracle, without requiring an experimental build flag on the
minimum toolchain. The following tests pin selected differences; this is not
an exhaustive inventory of v1/v2 behavior.

| # | Divergence | v1 (`encoding/json`) | v2 (`encoding/json/v2`) | zerojson sides with | Test |
|---|---|---|---|---|---|
| 1 | Key matching case-sensitivity | Case-insensitive fallback when no exact match | Case-sensitive only (opt-in `MatchCaseInsensitiveNames` restores v1) | **v2** (documented in the top-level README's compatibility contract) | `TestKeyMatchingCaseSensitivityDivergesFromV1MatchesV2` |
| 2 | Duplicate object member names | Last occurrence wins | Rejected outright (`jsontext: duplicate object member name ...`) | **v1** (documented in `ordered_test.go`'s `orderedTrapSeeds`) | `TestDuplicateKeyLastWinsDivergesFromV2MatchesV1` |
| 3 | `time.Duration` representation | None — it's a plain named `int64`, decodes as a bare number like any other integer | No default representation at all by design (open question upstream, [go.dev/issue/71631](https://go.dev/issue/71631)); requires an explicit format tag or a custom `Unmarshalers` override | **v1** (bare number, no special-casing) | `TestDurationRepresentationDivergesFromV1RequiresExplicitFormatInV2` |

The differential fuzzer canonicalizes input through v1 before comparison, so
it checks supported semantic values rather than agreement on arbitrary malformed
JSON. Dedicated tests cover rejection behavior.

`FuzzDecodeAgainstJSONV2` runs `Unmarshal` through v2 with a
`WithUnmarshalers` override restoring v1's bare-number `time.Duration`
representation (divergence 3), since `Event` (the fuzzed type) has a
`Timeout time.Duration` field and every canonical input would otherwise
fail identically on that one field, which would just be re-testing
divergence 3 on every single fuzz execution instead of exploring the
rest of `Event`'s shape.

## Generated schema and stateful campaigns

`TestGeneratedSchemaCampaign` creates a temporary package with 40 random
multi-field structs and a 32-cell type/container matrix, then generates and
tests both ordered and `-no-ordered` codecs. The matrix crosses eight base
types with direct/pointer/slice/map positions. Seeds also vary aliases, named
containers, hidden embeds, and omission tags.

A stateful fixture exercises 512 operation pairs across copy/borrow modes.
It checks values, retained pointers, alias topology, callback counts, borrowed
input integrity, and decoder reuse after errors. Failed targets need not match
stdlib's partial state: a detached, alias-preserving copy of zerojson's
post-error graph initializes the next operation's oracle. Successful results
are never normalized this way.

From the repository root:

```sh
GOMAXPROCS=2 ZEROJSON_SCHEMA_SEED=1 ZEROJSON_SCHEMA_COUNT=128 \
  ZEROJSON_SCHEMA_FUZZTIME=100000x \
  go test ./conformance/generator -run '^TestGeneratedSchemaCampaign$' -count=1 -v
```

- `ZEROJSON_SCHEMA_SEED` reproduces the package.
- `ZEROJSON_SCHEMA_COUNT` selects 1–500 random schemas, in addition to the matrix.
- `ZEROJSON_SCHEMA_FUZZTIME` enables sequence fuzzing with one worker per
  generation mode. Execution counts avoid the Go fuzz wall-clock deadline race.
- `ZEROJSON_SCHEMA_RACE=1` and `ZEROJSON_CHECKPTR=1` instrument generated
  tests and the optional sequence fuzzing.

Failed source packages and minimized fuzz corpora are retained under
`conformance/testdata/schema-failures/`, with replay instructions. This directory
is git-ignored and excluded from `./...` discovery. Go minimizes operation
sequences; automated schema minimization is not implemented.

## Longer fuzz campaigns

```sh
FUZZTIME=100000x scripts/fuzz-all.sh
FUZZTIME=100000x FUZZ_TARGET_PATTERN='ZJSONSequence$' scripts/fuzz-all.sh
```

The script defaults to 60 seconds per target, continues after individual
failures, and reports all failing targets. `FUZZMINIMIZETIME` defaults to five
seconds; failing corpora remain available for a separate minimization run.
Use `FUZZ_SHARD_INDEX` and `FUZZ_SHARD_COUNT` to distribute targets across
workers. These commands are on demand, not scheduled CI jobs.

## Benchmarking

[Historical results](BENCHMARKS.md) are retained separately from current
benchmark instructions. Re-measure before publishing performance claims.

Run codec benchmarks from `conformance/codec`:

```sh
go test -run '^$' -bench . -benchmem
```

To compare the bounded interner with the experimental `unique` backend:

```sh
BENCH_COUNT=10 BENCH_TIME=750ms BENCH_CPUS=1,8,32 \
  scripts/benchmark-intern.sh
```

Run the script from the repository root. It copies the working tree into two
temporary directories, replaces only the candidate's `intern.go`, and measures
hits, contention, cardinality, GC epochs, and generated decoding. Results,
metadata, resource measurements, and `benchstat` output go under `$TMPDIR`.
`BENCH_OUT`, `BENCH_GOGC`, and `BENCH_BACKENDS` override output location,
GC target, and execution order.

Compare backends sequentially on the same quiet host and architecture.
Separate shared CI runners are unsuitable for judging small timing differences.
Allocation budgets are tested separately from timing.

Resource-scaling tests compare N and 4N payloads and enforce allocation
amplification limits against stdlib. Allocation checks run normally;
wall-clock checks require `ZEROJSON_LINEARITY=1` (set by the full gate).
