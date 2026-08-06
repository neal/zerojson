# conformance

Differential and byte-identity test suites that need external oracles
(easyjson, `google/uuid`, and `go-json-experiment/json` as a third decode
oracle). Its own module so the root `zerojson` package
stays dependency-free; see the top-level README's "Module layout" for
the full picture.

- `runtime/` — external-oracle checks for the dependency-free runtime:
  exhaustive/boundary domain accounting, JSONTestSuite, strings, and UUIDs.
- `codec/` — exercises every field kind the generator supports
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
  campaigns, plus `compat/` wrapper tests and the exhaustive `typeshape/`
  classification matrix.
- `mutation_test.go` — repository-wide curated mutation testing.

## Testing story

What's continuously verified, and against which oracles:

| Property | Oracle(s) | Where |
|---|---|---|
| Encode byte-identity | easyjson (real generated code) | `codec/event_codec_test.go` (`TestByteIdenticalToStdlib`* against stdlib too, since the two agree for the fixture), `codec/valuefuzz_test.go`'s `FuzzEventValueSpaceEncode` (value-space, not JSON-text-space — reaches NaN/Inf, invalid UTF-8, extreme decimals) |
| Decode semantics | `encoding/json` v1 (primary contract) | `codec/event_codec_test.go`'s `FuzzDecodeAgainstStdlib`, `TestRoundTripAgainstStdlib` |
| Raw decoded Go-value semantics | `encoding/json` v1, compared before re-encoding can normalize differences | `codec/string_behavior_fuzz_test.go` plus each generated per-type fuzzer; opaque custom-codec fields are excluded field-by-field |
| Generator method/addressability semantics | `encoding/json` over an oracle alias in a freshly generated fixture package | `generator/behavior_test.go`; systematically crosses JSON/Text, value/pointer receivers, and direct/slice/map positions |
| Generated-method provenance | generator-specific source headers plus stdlib byte identity | `cmd/zerojsongen`'s `TestReplaceableCodecOutputIsGeneratorSpecific` and `generator/typeshape`'s `L_text*` cells |
| Generated schema reachability | `encoding/json` acceptance plus the per-type differential properties | `codec/zerojson_gen_fuzz_test.go`; every declared field receives a correctly-shaped seed, containers are non-empty, and escaped-string, invalid-UTF-8, and both `[]byte` forms are seeded |
| Multi-feature schema interactions | `encoding/json` over reproducible generated temporary packages | `generator/schema_campaign_test.go`; deterministic multi-field schemas controlled by `ZEROJSON_SCHEMA_SEED` / `ZEROJSON_SCHEMA_COUNT` |
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
modules (root, `cmd/zerojsongen`, `conformance`); `full` adds
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

## `Valid` — no new divergence from `encoding/json.Valid`

Unlike the decode-semantics table above (which documents genuine,
deliberate divergences from stdlib), `zerojson.Valid`'s contract *is*
verdict parity with `encoding/json.Valid` — so this section records what
was checked, not a divergence list, because none was found or introduced:

- **Invalid UTF-8 inside strings is accepted**, matching v1's
  byte-oriented scanner (which never decodes UTF-8, only rejects raw
  control bytes and validates escape-sequence grammar). This was a
  design decision going in, not a discovered divergence — a stricter
  opt-in `ValidUTF8` variant (matching `encoding/json/v2`'s default) is a
  possible future addition, not built since no current caller needs it.
- **`maxNestingDepth` is 10000**, verified empirically against a live
  `encoding/json.Valid` call at the 9999/10000/10001 boundary (`Valid`
  accepts 10000 nested containers, rejects 10001) before wiring
  `Valid`'s own depth cap to the same constant this package's
  `ReadInterface` already used.
- **Lone/unpaired `\u`-escaped surrogates are grammar-valid**: four hex
  digits is the whole escape-syntax requirement; neither `Valid` nor
  stdlib's scanner checks surrogate *pairing* at the grammar level (only
  a value-level unescape step — which `Valid` never performs — replaces
  an unpaired one with U+FFFD).
- The full-payload verdict-parity fuzzer (`FuzzValidAgainstStdlib`) ran
  clean for 71s / 7.2M execs before landing, seeded from every corpus
  this package's other suites already assembled (`variants()`, the
  golden fixtures, the truncation corpus, the ordered-decoder trap
  seeds) plus hand-picked grammar corners (leading zeros, bare/trailing
  exponent signs, incomplete `\u` escapes, and so on).

## `encoding/json/v2` as a third oracle

`jsonv2_test.go` uses `github.com/go-json-experiment/json` — the actual
reference implementation the real stdlib `encoding/json/v2` package is
staged from — rather than the stdlib package itself, which only exists
when built with `GOEXPERIMENT=jsonv2` (checked directly against this
repo's toolchain: `go doc encoding/json/v2` fails to resolve without
that env var). Depending on the reference module instead means the
ordinary `go test ./...` invocation across this repo never needs a
non-default experimental build flag.

Three genuine v1-vs-v2 behavioral divergences were found by probing both
packages directly (not assumed from documentation) and are pinned by a
dedicated test each, in addition to the differential fuzzer
(`FuzzDecodeAgainstJSONV2`) that canonicalizes fuzzer input through v1
first (removing the ambiguity these divergences create) and then
requires zerojson, v1, and v2 to agree on the rest:

| # | Divergence | v1 (`encoding/json`) | v2 (`encoding/json/v2`) | zerojson sides with | Test |
|---|---|---|---|---|---|
| 1 | Key matching case-sensitivity | Case-insensitive fallback when no exact match | Case-sensitive only (opt-in `MatchCaseInsensitiveNames` restores v1) | **v2** (documented in the top-level README's compatibility contract) | `TestKeyMatchingCaseSensitivityDivergesFromV1MatchesV2` |
| 2 | Duplicate object member names | Last occurrence wins | Rejected outright (`jsontext: duplicate object member name ...`) | **v1** (documented in `ordered_test.go`'s `orderedTrapSeeds`) | `TestDuplicateKeyLastWinsDivergesFromV2MatchesV1` |
| 3 | `time.Duration` representation | None — it's a plain named `int64`, decodes as a bare number like any other integer | No default representation at all by design (open question upstream, [go.dev/issue/71631](https://go.dev/issue/71631)); requires an explicit format tag or a custom `Unmarshalers` override | **v1** (bare number, no special-casing) | `TestDurationRepresentationDivergesFromV1RequiresExplicitFormatInV2` |

No divergence in generic *number* handling beyond error-path cosmetics
was found: both packages reject the same malformed numeric literals
(integer overflow, negative-into-unsigned, leading zero/plus/hex
digits, bare `NaN`/`Infinity` tokens, six-plus-digit exponents) and both
decode a JSON number into an `any`-typed field as `float64` with
identical precision loss beyond 2^53. (One error-path-only cosmetic
difference was noted but not pinned as a test, since neither oracle's
*success* contract depends on it: on a float64 overflow error, v2
leaves the destination holding `+Inf` where v1 leaves it at the zero
value.)

`FuzzDecodeAgainstJSONV2` runs `Unmarshal` through v2 with a
`WithUnmarshalers` override restoring v1's bare-number `time.Duration`
representation (divergence 3), since `Event` (the fuzzed type) has a
`Timeout time.Duration` field and every canonical input would otherwise
fail identically on that one field, which would just be re-testing
divergence 3 on every single fuzz execution instead of exploring the
rest of `Event`'s shape.
