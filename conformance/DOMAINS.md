# Domain accounting

This document answers one question for every function and internal kernel in
zerojson's runtime (the zero-dependency root module): **how do we know it is
correct across its whole input domain, and
where does residual risk remain?**

The motivation is concrete. After ~30 fuzzers, the JSONTestSuite corpus,
mutation testing (at the time an ad-hoc pass; now the repeatable, CI-wired
framework in `mutation_test.go`), and 99.8% branch coverage, an external reviewer
still found a `math.MinInt` negation panic in `AppendTime`.
Fuzzers **sample** a domain; for any space of 2^32 or more they essentially
never land on the exact extremes. This suite converts that probabilistic
coverage into **exhaustive** or **argued-complete** coverage:

- **EXHAUSTIVE** / **TOTAL** — every value in the domain is tried.
- **BOUNDARY-COMPLETE** — every boundary (range edges, digit-length
  transitions, per-lane byte values, calendar regimes) is tried in full; the
  interior is argued equivalent or covered by a strided sweep.
- **DIFFERENTIAL+PARTITIONED** — checked against an independent oracle over a
  partition of the domain that covers every distinct code path, with the
  continuous fuzz/property layer covering the interior.

Every enumeration compares against an **independent oracle** — `strconv`,
`encoding/json`, `time`, easyjson, `google/uuid` — never
against zerojson itself.

The enumerations live in `conformance/domains_test.go` (per-commit tier) and
`conformance/domains_exhaustive_test.go` (`-tags exhaustive`, pre-release
tier).

## Standing rule

Any new runtime function, kernel, or bug fix **must add its row to the table
below and its boundary enumeration to `domains_test.go` in the same commit**.
A domain with no row is an unclassified risk; a new fix with no boundary test
is how the last extreme-value bug shipped.

---

## Root module

### numbers.go — integer & numeric-token decode

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `ReadInt8`, `ReadUint8`, `ReadInt16`, `ReadUint16` | every value in width ± out-of-range margin | **TOTAL** | `TestNarrowIntTotalDomain` | none — full domain + both boundaries walked |
| `ReadInt32`, `ReadUint32` | 2^32 | **BOUNDARY-COMPLETE** (all edges + digit transitions ±2 + stride-65537 sweep) normal; **TOTAL** exhaustive | `TestInt32Uint32BoundarySweep`, `TestInt32FullEnumeration`, `TestUint32FullEnumeration` | none — full 2^32 verified in the exhaustive tier |
| `ReadInt64`, `ReadUint64` | 2^64 | **BOUNDARY-COMPLETE** (every power-of-ten & 9-repunit transition ±2, min/max ±2, leading zeros, quoted `,string`) | `TestInt64Uint64BoundaryComplete` | interior beyond boundaries not walked (2^64 infeasible); shared `parseDigitsBounded` kernel is exhaustively verified at 8/16/32-bit widths |
| `ReadInt`, `ReadUint` | platform `int`/`uint` width | **BOUNDARY-COMPLETE** on native 64-bit and the CI 386 execution target; 32-bit tightens to the exhaustively-tested 32-bit kernel | `TestReadIntWidthRangeChecks`, 386 CI runtime suite | none across the two Go integer widths |
| `ReadFloat64` | float64 text | **DIFFERENTIAL** vs `strconv.ParseFloat` (the actual parser) | root `float_test.go`, codec value-space fuzz | `strconv` *is* the implementation; parse correctness inherited |
| `parseDigitsBounded` | digit run + overflow limit | **BOUNDARY-COMPLETE** — the shared kernel exercised at every width's exact limit | all integer tests above | none at the tested widths |
| `readSigned`, `readUnsigned` | signed/unsigned magnitude vs limit | **TOTAL/BOUNDARY** via the width readers | narrow + int32 full + int64 boundary | the `math.MinInt64` negation branch is hit by `-9223372036854775808` |
| `IsEightDigits` | 2^64 word | **BOUNDARY-COMPLETE** (each of 8 lanes × all 256) + **TOTAL** over valid words | `TestEightDigitKernelsBoundary`, `TestParseEightDigitsFull` | non-digit multi-lane combinations argued from per-lane completeness of the SWAR mask |
| `ParseEightDigits` | 10^8 valid words | **TOTAL** (exhaustive) + strided normal | `TestParseEightDigitsFull`, `TestEightDigitKernelsBoundary` | none — every valid 8-digit word verified |
| `readNumericBytes` / `ReadNumericBytes` | quoted-or-bare number token | **DIFFERENTIAL+PARTITIONED** — every int/float/decimal decode routes through it | all numeric tests | valid escaped digits are unquoted before parsing; malformed tokens are rejected |

### writer.go — encode path

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `AppendString` | arbitrary bytes | **TOTAL** over all 1-byte & all 2-byte strings + fuzz | `TestAppendStringByteTotal`, `FuzzAppendStringUTF8` | ≥3-byte rune interactions & SWAR-lane alignment covered by fuzz + `TestAppendStringUTF8MatchesBothOracles` |
| `needEscape`, `zeroBytes`, `safeSet`, `appendEscaped`, `replacementEscape`, `appendNonASCII` | per-byte escape decision | **TOTAL** — every byte value appears as lane content across the 1/2-byte sweep | `TestAppendStringByteTotal` | invalid/valid-UTF8 multi-byte boundaries covered by `FuzzAppendStringUTF8` |
| `AppendTime` | zone offset ∈ full int range; instant | **TOTAL** over [-24h,+24h] + out-of-range classes + named int32/int64 extremes; sub-minute **TOTAL** in ±10min | `TestAppendTimeOffsetTotal`, `TestAppendTimeSubMinuteOffsets` | instant components (year/month/…) not swept on encode — but `append2`/`append4` are exercised by the date decode grid and existing vectors |
| `append2` | 0..99 | **TOTAL** — every pair emitted via time/decimal paths | `TestAppendTime*`, decimal grid | none |
| `append4` | year 0..9999 | **DIFFERENTIAL+PARTITIONED** (year classes) | `TestReadTimeDateGridTotal` (decode side), writer vectors | full 0..9999 encode not swept; arithmetic is `/100,%100` over `append2` (itself total) |
| `AppendUUIDBytes` | 16 bytes → 36 hex | **DIFFERENTIAL** (hex table) via decode round-trip + vectors | `TestReadUUIDPositionComplete`, `FuzzReadUUIDBytesMatchesParseBytes` | encode direction argued from the shared `hexDigits` table + round-trip |
| `AppendFloat64` | 2^64 | **BOUNDARY-COMPLETE** (exponent field, denormals, notation transitions, shortest-repr classics) + 2.1M property fuzz | `TestAppendFloat64BoundaryComplete`, root `float_test.go` | full 2^64 infeasible; `strconv` is the byte-identity target |
| `AppendFloat32` | 2^32 | **TOTAL** (exhaustive) + 16M strided + property fuzz | `TestFloat32FullEnumeration`, `TestAppendFloat32StridedSweep` | none — every bit pattern verified in the exhaustive tier |
| `AppendInt64`, `AppendUint64`, `AppendQuotedInt64`, `AppendQuotedUint64` | 2^64 | **DIFFERENTIAL** vs `strconv.Append{Int,Uint}` + **TOTAL** over narrow ranges | `TestNarrowIntTotalDomain`, `TestInt64Uint64BoundaryComplete` (quoted) | `strconv` is the implementation |
| `AppendBool` | {true,false} | **TOTAL** | existing writer/codec tests | none |
| `AppendStringBytes` | wrapper of `AppendString` | inherits `AppendString` coverage | — | none |
| `AppendInterface` | arbitrary `any` | **DIFFERENTIAL** — delegates to `encoding/json.Marshal` | codec interface tests | it *is* `encoding/json` |

### decimal.go — dependency-free decimal parts

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `AppendDecimalParts` | coeff ∈ int64 × exp ∈ [-37,37] | **GRID-TOTAL** — digit-length transitions × every legal exp; panic asserted ±5 beyond | `TestDecimalPartsGridTotal`, root `decimal_test.go` | none across the grid; panic boundary enumerated |
| `ParseDecimalParts` | plain decimal text | **GRID-TOTAL** round-trip + malformed/range tables | `TestDecimalPartsGridTotal`, root `decimal_test.go` | non-canonical malformed inputs covered by existing tables |
| `absInt64` | int64 incl. `MinInt64` | **BOUNDARY** — `MinInt64` coefficient exercised | `TestDecimalPartsGridTotal` | none |

### strings.go — string decode & unescape

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `unescapeString` | `\uXXXX` code units; surrogate pairs | **TOTAL** over 65536 code units; **TOTAL** 2048×2048 pair grid (exhaustive), representative-complete normal | `TestUnescapeAllCodeUnits`, `TestUnescapeSurrogatePairs`, `TestUnescapeSurrogatePairsFull` | value path is lenient by design on *malformed/truncated* escapes (strictness is `Valid`/`ReadKey`'s job) — noted, not a divergence |
| `parseHex4` | 4 hex chars | **TOTAL** — all 65536 4-hex combinations via `\uXXXX` sweep | `TestUnescapeAllCodeUnits` | non-hex leniency intentional |
| `utf8AppendRune` | rune 0..0x10FFFF | **BOUNDARY-COMPLETE** — all BMP (0..FFFF) + astral via surrogate combine | `TestUnescapeAllCodeUnits`, `TestUnescapeSurrogatePairs*` | none across representable runes |
| `readStringBytes`, `readStringSlow`, `hasByte` | quote/backslash scan (16/8/1 lanes) | **DIFFERENTIAL+PARTITIONED** — every string decode + all-byte scans | all string/escape tests, existing fuzz | lane-boundary positions covered by fuzz |
| `ReadKey` | object key (grammar-strict) | **BOUNDARY-COMPLETE** on escapes via `Valid` | `TestValidEscapeAcceptReject`, existing key tests | full key grammar via `FuzzValidAgainstStdlib` |
| `ReadString`, `ReadInternedString`, `ReadEnumBytes`, `ReadTextBytes` | string value | inherits unescape coverage | `TestUnescape*` | none beyond the lenient-value note |
| `bytesToString` | unsafe view | trivial; N/A | — | none |

### time.go — RFC3339 parse

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `parseRFC3339Z` / `ReadTime` | (month 0..13 × day 0..32) × leap regimes × boundary years | **TOTAL** grid + every digit position × all 256 bytes + fraction counts 1..10 | `TestReadTimeDateGridTotal`, `TestReadTimeDigitPositionMutation`, `TestReadTimeFractionAndSeparators` | offset timestamps fall back to `time.Parse` (the oracle) — parity, not divergence |
| `d2` (the `bad |= hi\|lo` kernel) | 2 bytes per digit slot | **TOTAL** — every position × all 256 bytes | `TestReadTimeDigitPositionMutation` | none — the exact bug class enumerated to death |
| `isLeapYear`, `leapsBeforeYear`, `floorDiv`, `daysSinceUnixEpoch` | year classes incl. below-epoch Hinnant path | **DIFFERENTIAL** vs `time.Parse` over 8 year classes × full month/day grid | `TestReadTimeDateGridTotal` | years outside {0,1,1900,2000,2001,2004,9998,9999} argued from the algorithm's proleptic-Gregorian validity |
| `nsScale`, `monthDays`, `daysBeforeMonth`, `daysBeforeMonthLeap` | index by validated month / frac-count | covered by the grid + fraction tests | as above | none |

### uuid.go

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `ReadUUIDBytes` | 36-byte & 38-byte forms, each position | **POSITION-COMPLETE** — 254/256 bytes at every position (0x22/0x5c excluded: not literal string-content bytes) | `TestReadUUIDPositionComplete`, `FuzzReadUUIDBytesMatchesParseBytes` | escaped-UUID path errors by design; excluded bytes argued |
| `parseUUIDBytes` | urn(45)/braced(38)/32-hex fallback | **POSITION-COMPLETE** (braced) + fuzz (urn, 32-hex) | `TestReadUUIDPositionComplete`, existing fuzz | urn/32-hex length classes covered by the pinned-oracle fuzz |
| `hexVal` (table) | 256 | **TOTAL** — every byte at every hex position | `TestReadUUIDPositionComplete` | none |

### valid.go — grammar validator

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `validEscape`, `validString` | all 256 `\X`; 4 hex positions × 256 | **BOUNDARY-COMPLETE** vs `encoding/json.Valid` | `TestValidEscapeAcceptReject` | none on escapes |
| `Valid`, `validValue`, `validObject`, `validArray`, `validNumber`, `validLiteral`, `skipDigitRun`, `scanStringSpecial`, `hasControlByte`, `validSkipWS`, `isHexDigit` | full RFC 8259 grammar | **DIFFERENTIAL** vs `encoding/json.Valid` | `FuzzValidAgainstStdlib`, JSONTestSuite corpus, `TestValidEscapeAcceptReject` | verdict-parity guaranteed by continuous fuzz + the standard conformance corpus |

### intern.go

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `Intern`, `internHash` | byte tokens ≤ 24 bytes | **ARGUED** — pure FNV-1a selects one of 32 locked maps; equality is resolved by the map; each shard retains at most 4,096 values and then degrades to copying | `TestInternValueContract`, `TestInternHitPathZeroAlloc`, `TestReadInterfaceDoesNotInternObjectKeys`, `TestInternTableRaceHammer` | no external oracle applies; the fixed cap bounds retained attacker-controlled cardinality, while explicit `zerojson:"intern"` remains a caller policy choice |

### lexer.go / interface.go / ordered.go / raw.go / appender.go — control flow

These are structural dispatch (delimiters, object/array framing, ordered
fast-path accessors, `Raw` passthrough, `Appender`/`Reader` interfaces), not
input-domain kernels. They are covered by the generated-decode conformance
suite (`conformance/codec`) round-tripping real structs against easyjson
and `encoding/json`, plus `ReadInterface`'s depth-cap and metamorphic tests.
`ReadInterface` shares the leaf readers tabulated above.

---

## Generator (cmd/zerojsongen) — type-shape domain

The tables above enumerate the runtime's VALUE domains. The generator has
its own input domain — the space of Go **type shapes** it classifies and
emits codecs for — and that domain went unenumerated until an external
review found seven classification/composite-value bugs living entirely in
it (nil slices/maps encoded as `[]`/`{}` instead of `null`; `[]byte` as a
number array instead of base64; unexported embedded structs dropped;
top-level `null` rejected; `map[string]struct{}` accepting non-objects;
interface methods matched by name not signature; invalid tag names passing
through). The **type-shape enumeration matrix** (`conformance/generator/typeshape`)
now accounts for this domain the way the tables above account for the value
domains: a committed generator (`gen.go`) constructs the cross product of
field KIND × tag VARIANT × value STATE, emits a real zerojson codec **and**
an independently generated easyjson twin for each, and the harness
(`typeshape_matrix_test.go`) compares every cell against `encoding/json`
(primary) and easyjson (migration) — byte-for-byte except a tiny
closed-and-asserted allowlist (`allowedReasons`). The matrix independently
re-discovers all seven findings.

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `classify` — field-kind classification | every leaf kind, `[]T`/`[]*T`, `[]byte`/named byte slice, `map[string]T`, `map[string]struct{}`, nested/`*`struct, `any`, `Raw`/`RawMessage`, Marshaler/TextMarshaler/Appender (right **and** wrong signature) | **DIFFERENTIAL+ENUMERATED** — one matrix cell per kind, encode/decode vs both oracles | `TestTypeShapeMatrix`, `TestGeneratorInteractionCompileGuards` | recursive graphs are refused; kinds outside the enumerated set are refused at generate time |
| nil vs empty vs populated **container** encode (finding 1) | slice/map field × {nil, empty-non-nil, populated} × {plain, omitempty} | **ENUMERATED** — nil ⇒ `null`, empty ⇒ `[]`/`{}`, byte-identity vs both oracles | `TestTypeShapeMatrix` (`C_*` cells) | none across the enumerated container kinds |
| `[]byte` base64 (finding 2) | `[]byte`, named byte slice, `*[]byte`, `[][]byte`, `map[string][]byte` | **ENUMERATED** encode+decode; decode rejects the array-of-numbers form (allowlisted vs stdlib) | `TestTypeShapeMatrix` (`C_bytes*`) | `type X []byte` **root** is refused (not a supported root shape) |
| embedding (finding 3) | exported/unexported × value embed × declaration position | **ENUMERATED** — exported-through-unexported promotion vs stdlib; embed-order divergence vs easyjson allowlisted; embedded pointers refused | `TestTypeShapeMatrix` (`E_*`), compile-refusal tests | pointer embeds & method-bearing embeds refused at generate time |
| top-level `null` decode (finding 4) | every generated shape (struct + slice/map roots) | **ENUMERATED** — `null` ⇒ zero-value no-op success; `nullx` ⇒ trailing-data error | `TestTopLevelNull` | none |
| `map[string]struct{}` value strictness (finding 5) | value shapes `{}`, `{unknown}`, `null`, number, array, string, bool | **ENUMERATED** — object/null accepted, others rejected, vs both oracles | `TestSetMapStrictness` | none |
| interface-method **signature** detection (finding 6) | `MarshalJSON`/`MarshalText`/`AppendZJSON`/`ReadZJSON`/`IsZero` at the correct and at a wrong signature | **ENUMERATED** — wrong-signature same-name methods ignored (structural encode), vs both oracles | `TestTypeShapeMatrix` (`M_wrong_*`), `TestGeneratorInteractionCompileGuards` | signatures and `zerojson.Lexer` package identity compared by `go/types` identity |
| generated-method provenance | zerojsongen/easyjson output vs arbitrary generated source containing custom JSON/Text methods | **ENUMERATED** — only known replaceable codec outputs may be superseded; other generated custom methods retain their wire format | `TestReplaceableCodecOutputIsGeneratorSpecific`, `TestTypeShapeMatrix` (`L_text*`) | generator identity is pinned by exact standard header prefixes |
| tag name validation (finding 7) & `,string` | invalid tag names, `,string` on int/uint including bare-token rejection, omitempty on scalar-underlying Marshalers | **ENUMERATED** vs stdlib (invalid ⇒ field-name fallback; easyjson emits verbatim, allowlisted) | `TestTypeShapeMatrix` (`T_*`, `L_*_oe`), `TestStringOptionRequiresQuotedInput` | matches `encoding/json`'s `isValidTag` character classes and quoted-token rule |
| decode-error **oracle direction** | arbitrary fuzz bytes | **DIFFERENTIAL** — zerojson-rejects ⇒ stdlib-rejects, with an explicit (currently empty for these shapes) allowlist | generated `Fuzz*ZJSON` (both directions) | complements the pre-existing stdlib-rejects ⇒ zerojson-rejects value-class check |
| cross-feature interactions | `IsZero` × pointer/scalar/slice, map-value addressability × pointer-only methods, reused target × duplicate composite keys, ordered fallback × callbacks, pooled arena × stateful Reader | **DIFFERENTIAL+STATEFUL** — initialized targets, duplicate members, callback counters, and consecutive pooled decodes against stdlib | `interactions_test.go`, `root_types_test.go` | callback-bearing schemas bypass ordered speculation; arena seen bits distinguish first occurrence from duplicates |

**Matrix size:** 84 type-shape cells × up to 3 value states, plus the
top-level-null and set-map strictness sweeps — 367 subtests. **New findings
it surfaced beyond the seven:** `omitempty` on a scalar-underlying
`Marshaler`/`Appender` value (e.g. a quoted-money `Cents(0)`) was always
emitted, where both `encoding/json` and easyjson omit it — fixed (gated on
the underlying zero value); and a generator interaction where a
wrong-signature same-name `MarshalJSON` blocked auto-discovery of an
otherwise-plain same-package struct — fixed as part of finding 6.

---

## Measured runtimes

### Per-commit tier (`go test ./conformance/`) — added cost ~1.4s total

| Test | Wall-clock | Cases |
|---|---|---|
| `TestNarrowIntTotalDomain` | 0.04s | ~200k (full int8/uint8/int16/uint16 decode + encode) |
| `TestInt32Uint32BoundarySweep` | 0.03s | ~131k (boundaries + stride-65537) |
| `TestInt64Uint64BoundaryComplete` | <0.01s | ~300 |
| `TestAppendTimeOffsetTotal` | 0.04s | ~180k offsets |
| `TestAppendTimeSubMinuteOffsets` | <0.01s | 1,201 |
| `TestReadTimeDateGridTotal` | <0.01s | 8×14×33 = 3,696 |
| `TestReadTimeDigitPositionMutation` | <0.01s | 14×256 = 3,584 |
| `TestReadTimeFractionAndSeparators` | <0.01s | ~1,300 |
| `TestAppendStringByteTotal` | 0.01s | 256 + 65,536 |
| `TestUnescapeAllCodeUnits` | 0.01s | 65,536 |
| `TestUnescapeSurrogatePairs` | <0.01s | 2048×7 |
| `TestValidEscapeAcceptReject` | <0.01s | 256 + 4×256 |
| `TestReadUUIDPositionComplete` | 0.01s | (36+38)×254 |
| `TestAppendFloat32StridedSweep` | 1.10s | ~16M |
| `TestAppendFloat64BoundaryComplete` | <0.01s | ~2,200 |
| `TestDecimalPartsGridTotal` | <0.01s | ~2,800 |
| `TestEightDigitKernelsBoundary` | <0.01s | 2,048 + ~20k strided |

### Pre-release tier (`go test -tags exhaustive -timeout 30m ./conformance/`)

| Test | Wall-clock | Cases |
|---|---|---|
| `TestInt32FullEnumeration` | 89s | 4,294,967,296 |
| `TestUint32FullEnumeration` | 86s | 4,294,967,296 |
| `TestFloat32FullEnumeration` | 181s | 4,294,967,296 |
| `TestUnescapeSurrogatePairsFull` | 1.0s | 2048×2048 = 4,194,304 |
| `TestParseEightDigitsFull` | 1.4s | 100,000,000 |

Total exhaustive tier: **~6 minutes** on the dev machine.

## Full-enumeration feasibility decisions

- **int32 / uint32 total (4.29B each):** FEASIBLE and KEPT. With reused
  buffers the decode loop does zero per-value heap allocation; each full
  sweep runs in ~85–90s. Retained as the pre-release gate rather than reduced
  to a sample.
- **float32 total (4.29B):** FEASIBLE and KEPT at 181s (two `strconv`
  `AppendFloat` calls per finite value dominate the cost). The per-commit
  tier keeps the 16M strided sweep + the existing 2.1M property fuzz so a
  regression is caught quickly; the full sweep gates releases.

## Findings

No divergence surfaced. Every enumeration above passes against its
independent oracle in both tiers. (Two test-harness issues found and fixed
during authoring — feeding quoted `,string` input to `strconv`, and asserting
easyjson/stdlib agree on the `\b`/`\f` short escapes where they deliberately
differ — were mistakes in the *oracle wiring*, not zerojson behavior.)

## Unsafe inventory

The shipped library contains exactly two `unsafe` sites (verify with
`grep -rn unsafe --include='*.go' . | grep -v _test | grep -v cmd/ | grep -v conformance/`).
Each is documented below with the safety argument and what pins it.

The `nounsafe` build tag (`go build -tags nounsafe`) compiles a
provably-unsafe-free build of the **root module** by swapping both sites below
for copying implementations (`unsafe_off.go`).

### 1. `strings.go` — `bytesToString` (`unsafe.String`)

**What.** Returns a `string` that aliases the backing `[]byte` without
copying: `unsafe.String(&b[0], len(b))`.

**Why safe.** Every caller falls into one of three cases:

- **(a) Passed to a stdlib function whose error retains the input.**
  `time.Parse` (time.go) and `strconv.ParseFloat` (interface.go) can wrap
  the input string in their returned error. But zerojson immediately
  stringifies that error through `fmt.Errorf` in `setErr` (lexer.go), so the
  aliased string is copied into the error message and never escapes into
  retained state past the decode call. The `[]byte` is free to be reused.
- **(b) Aliases a freshly-allocated buffer.** In `unescapeStringBytes`
  (strings.go:159) the bytes handed to `bytesToString` were allocated by the
  unescape one line earlier — no caller-owned memory is involved, so the
  resulting string owns its bytes for all practical purposes.
- **(c) Borrow mode is explicit.** `ReadString` only returns an aliased
  string when `l.Borrow` is set (strings.go:344); the default copy mode does
  a real `string(b)` copy, so a borrowed alias only exists when the caller
  opted in to the borrow lifetime contract.

**Pinned by.** The `-race` + `-gcflags=all=-d=checkptr=2` gate
(`run_checkptr_modules` in `scripts/verify.sh`), the borrow-lifetime
fuzzers, and the byte-identity / differential suites.

**Residual risk.** Low. Borrow mode places a lifetime obligation on the
caller (the returned string is valid only while the source buffer is
unchanged); this is a documented API contract, not a latent bug.

### 2. `writer.go` — `AppendString` (`unsafe.Slice(unsafe.StringData(s), len(s))`)

**What.** Constructs a read-only, function-local `[]byte` view over the
input string for SWAR (word-at-a-time) escape scanning.

**Why safe.** The view never escapes the function and is never written —
it is used only to load 8-byte words for the escape-mask scan; all output
goes through `append` to `dst`. This is the canonical safe use of
`unsafe.Slice` over a string.

**Pinned by.** checkptr + the string-encoding differential tests.

**Residual risk.** Negligible.
