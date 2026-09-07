# Domain accounting

This inventory records runtime value-domain tests, generator type-shape tests,
and the limits of each strategy.

- **TOTAL / EXHAUSTIVE:** every value in the stated finite domain is tested.
  For numeric readers this usually means canonical encodings, not every
  possible input byte string.
- **BOUNDARY-COMPLETE:** the listed boundaries are enumerated; equivalence of
  untested interiors is an assumption, not a proof.
- **DIFFERENTIAL / PARTITIONED:** selected classes are compared against an
  oracle, with fuzzing for additional combinations.

Runtime enumerations live in `conformance/runtime/domains_test.go` and
`domains_exhaustive_test.go`. Most use `strconv`, `encoding/json`, `time`,
easyjson, or `google/uuid`; ownership and interning tests assert API invariants.

When changing a runtime kernel, update its row and add boundary tests.
Passing an enumeration does not cover arbitrary malformed encodings, schema
interactions, or caller lifetime violations unless stated explicitly.

## Root module

### numbers.go — integer & numeric-token decode

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `ReadInt8`, `ReadUint8`, `ReadInt16`, `ReadUint16` | every value in width ± out-of-range margin | **TOTAL** | `TestNarrowIntTotalDomain` | canonical values and tested overflow margins; other malformed spellings are sampled |
| `ReadInt32`, `ReadUint32` | 2^32 | **BOUNDARY-COMPLETE** (all edges + digit transitions ±2 + stride-65537 sweep) normal; **TOTAL** exhaustive | `TestInt32Uint32BoundarySweep`, `TestInt32FullEnumeration`, `TestUint32FullEnumeration` | canonical encodings cover all 2^32 values; malformed spellings remain sampled |
| `ReadInt64`, `ReadUint64` | 2^64 | **BOUNDARY-COMPLETE** (every power-of-ten & 9-repunit transition ±2, min/max ±2, leading zeros, quoted `,string`) | `TestInt64Uint64BoundaryComplete` | interior beyond boundaries not walked (2^64 infeasible); shared `parseDigitsBounded` kernel is exhaustively verified at 8/16/32-bit widths |
| `ReadInt`, `ReadUint` | platform `int`/`uint` width | **BOUNDARY-COMPLETE** for the running architecture's integer width | `TestReadIntWidthRangeChecks`; run on both 32-bit and 64-bit hosts | ordinary amd64 CI does not exercise 32-bit execution |
| `ReadFloat64` | float64 text | **DIFFERENTIAL** vs `strconv.ParseFloat` (the actual parser) | root `float_test.go`, codec value-space fuzz | `strconv` *is* the implementation; parse correctness inherited |
| `parseDigitsBounded` | digit run + overflow limit | **BOUNDARY-COMPLETE** — the shared kernel exercised at every width's exact limit | all integer tests above | non-canonical digit runs and delimiter interactions are not exhaustive |
| `readSigned`, `readUnsigned` | signed/unsigned magnitude vs limit | **TOTAL/BOUNDARY** via the width readers | narrow + int32 full + int64 boundary | the `math.MinInt64` negation branch is hit by `-9223372036854775808` |
| `IsEightDigits` | 2^64 word | **BOUNDARY-COMPLETE** (each of 8 lanes × all 256) + **TOTAL** over valid words | `TestEightDigitKernelsBoundary`, `TestParseEightDigitsFull` | non-digit multi-lane combinations argued from per-lane completeness of the SWAR mask |
| `ParseEightDigits` | 10^8 valid words | **TOTAL** (exhaustive) + strided normal | `TestParseEightDigitsFull`, `TestEightDigitKernelsBoundary` | none — every valid 8-digit word verified |
| `readBareNumericBytes` (plain int/uint/float fields, `any` numbers) | bare number token only | **DIFFERENTIAL+PARTITIONED** — every plain numeric decode routes through it | all numeric tests, `TestInt64Uint64BoundaryComplete`'s bare-rejects-quoted check | a string token, a leading `+`, `.5`, or `1.` is rejected like `encoding/json`; a leading zero is the one documented leniency |
| `readNumericBytes` / `ReadNumericBytes` (custom leaves) | quoted-or-bare number token, returned raw | **DIFFERENTIAL+PARTITIONED** — decimal leaf decodes route through it | decimal tests | valid escaped digits are unquoted; the LEAF's own parser owns grammar rejection (a bare `+1` reaches it) |
| `ReadQuotedOrBareInt64` (quoted-int64 marker types) | quoted token with optional leading `+`, or a strict bare number | **DIFFERENTIAL+PARTITIONED** — `StrInt64` decodes route through it | quoted-int64 tests, `TestReadIntWidthRangeChecks` | bare `+5` rejected; quoted `"+5"` accepted like the type's own `strconv`-based `UnmarshalJSON` |
| `ReadFloat32` | bare float token at 32-bit precision | **DIFFERENTIAL** — every `float32` field decode | `TestReadIntWidthRangeChecks` float32 cases, `Event.Ratio32` fuzzers | out-of-range (`1e39`) is a decode error like `encoding/json`, never a silent `±Inf` |

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
| `unescapeString` | `\uXXXX` code units; surrogate pairs | **TOTAL** over 65536 code units; **TOTAL** 2048×2048 pair grid (exhaustive), representative-complete normal | `TestUnescapeAllCodeUnits`, `TestUnescapeSurrogatePairs`, `TestUnescapeSurrogatePairsFull` | value path is lenient by design on *malformed/truncated* escapes (strictness is `Valid`/`ReadKey`'s job) — documented decode divergence |
| `parseHex4` | 4 hex chars | **TOTAL** — all 65536 4-hex combinations via `\uXXXX` sweep | `TestUnescapeAllCodeUnits` | non-hex leniency intentional |
| `utf8AppendRune` | rune 0..0x10FFFF | **BOUNDARY-COMPLETE** — all BMP (0..FFFF) + astral via surrogate combine | `TestUnescapeAllCodeUnits`, `TestUnescapeSurrogatePairs*` | none across representable runes |
| `readStringBytes`, `readStringSlow`, `hasByte` | quote/backslash scan (16/8/1 lanes) | **DIFFERENTIAL+PARTITIONED** — every string decode + all-byte scans | all string/escape tests, existing fuzz | lane-boundary positions covered by fuzz |
| `ReadKey` | object key (grammar-strict) | **BOUNDARY-COMPLETE** on escapes via `Valid` | `TestValidEscapeAcceptReject`, existing key tests | full key grammar via `FuzzValidAgainstStdlib` |
| `ReadString`, `ReadInternedString`, `ReadEnumBytes` | string value | inherits unescape coverage | `TestUnescape*`, string/enum codec tests | longer malformed strings and interning interactions are sampled |
| `ReadTextBytes` | strict JSON string token before a Text callback | **DIFFERENTIAL** plus malformed-token regressions | root `text_test.go`, `FuzzReadTextBytes` | callback semantics remain the custom type's responsibility |
| `bytesToString` | potentially borrowed byte storage | ownership checks and checkptr | see unsafe inventory below | caller lifetime obligations in borrow mode |

### time.go — RFC3339 parse

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `parseRFC3339Z` / `ReadTime` | (month 0..13 × day 0..32) × leap regimes × boundary years | **TOTAL** grid + every digit position × all 256 bytes + fraction counts 1..10 | `TestReadTimeDateGridTotal`, `TestReadTimeDigitPositionMutation`, `TestReadTimeFractionAndSeparators` | offset timestamps fall back to `time.Parse` (the oracle) — parity, not divergence |
| `d2` (the `bad |= hi\|lo` kernel) | 2 bytes per digit slot | **TOTAL** — every position × all 256 bytes | `TestReadTimeDigitPositionMutation` | all single-byte position mutations tested; multi-byte combinations are sampled |
| `isLeapYear`, `leapsBeforeYear`, `floorDiv`, `daysSinceUnixEpoch` | year classes incl. below-epoch Hinnant path | **DIFFERENTIAL** vs `time.Parse` over 8 year classes × full month/day grid | `TestReadTimeDateGridTotal` | years outside {0,1,1900,2000,2001,2004,9998,9999} argued from the algorithm's proleptic-Gregorian validity |
| `nsScale`, `monthDays`, `daysBeforeMonth`, `daysBeforeMonthLeap` | index by validated month / frac-count | covered by the grid + fraction tests | as above | none |

### uuid.go

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `ReadUUIDBytes` | 36-byte & 38-byte forms, each position | **POSITION-COMPLETE** — 254/256 bytes at every position (0x22/0x5c excluded: not literal string-content bytes) | `TestReadUUIDPositionComplete`, `FuzzReadUUIDBytesMatchesParseBytes` | valid escaped spellings are unescaped before parsing; multi-position mutations are sampled |
| `parseUUIDBytes` | urn(45)/braced(38)/32-hex fallback | **POSITION-COMPLETE** (braced) + fuzz (urn, 32-hex) | `TestReadUUIDPositionComplete`, existing fuzz | urn/32-hex length classes covered by the pinned-oracle fuzz |
| `hexVal` (table) | 256 | **TOTAL** — every byte at every hex position | `TestReadUUIDPositionComplete` | none |

### valid.go — grammar validator

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `validEscape`, `validString` | all 256 `\X`; 4 hex positions × 256 | **BOUNDARY-COMPLETE** vs `encoding/json.Valid` | `TestValidEscapeAcceptReject` | none on escapes |
| `Valid`, `validValue`, `validObject`, `validArray`, `validNumber`, `validLiteral`, `skipDigitRun`, `scanStringSpecial`, `hasControlByte`, `validSkipWS`, `isHexDigit` | full RFC 8259 grammar | **DIFFERENTIAL** vs `encoding/json.Valid` | `FuzzValidAgainstStdlib`, JSONTestSuite corpus, `TestValidEscapeAcceptReject` | arbitrary-length inputs are sampled, not exhausted |

### intern.go

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `Intern`, `internHash` | byte tokens ≤ 24 bytes | **ARGUED** — pure FNV-1a selects one of 32 locked maps; equality is resolved by the map; each shard retains at most 4,096 values and then resets to a new generation | `TestInternValueContract`, `TestInternHitPathZeroAlloc`, `TestReadInterfaceDoesNotInternObjectKeys`, `TestInternTableRaceHammer` | no external oracle applies; the fixed cap bounds retained attacker-controlled cardinality, while explicit `zerojson:"intern"` remains a caller policy choice |

### lexer.go / interface.go / ordered.go / raw.go / appender.go — control flow

Structural dispatch has its own tests: depth boundaries, malformed keys/values,
callback gating, error paths, pointer identity, borrowed Raw storage, and pooled
reuse. See root `depth_test.go`, `raw_test.go`, `conformance/codec`, and
the generator's stateful schema campaign. `ReadInterface` shares the leaf
readers above; its container framing also needs these structural checks.

### Additional value paths

| Function / kernel | Strategy | Tests | Residual risk |
|---|---|---|---|
| `AppendBytesBase64`, `ReadBytesBase64` | base64 and numeric arrays, malformed/range inputs, allocation scaling | root `bytes_test.go`, codec resource tests, generated byte-slice seeds | unusual combinations are sampled |
| `ReadJSONNumber`, `AppendJSONNumber` | valid/invalid literals, quoted input, null and empty values | `conformance/codec/json_number_test.go` | arbitrary-length literals are sampled |
| `Raw`, `AppendRawChecked` | grammar validation plus copy/borrow ownership and reuse | root `raw_test.go`, codec `raw_test.go`, generated Raw oracle mutants | callers must preserve borrowed storage |

## Generator (cmd/zerojsongen) — type-shape domain

The generator's input domain is Go type graphs, not just JSON bytes.
`conformance/generator/typeshape` generates a fixed field-kind × tag × value-state
matrix with real zerojson and easyjson codecs and compares against stdlib.
Separate compile fixtures and generated-schema campaigns cover method sets,
visibility, aliases, package names, and stateful interactions. No finite matrix
exhausts this domain.

| Function / kernel | Input domain | Strategy | Test(s) | Residual risk |
|---|---|---|---|---|
| `classify` — field-kind classification | every leaf kind, `[]T`/`[]*T`, `[]byte`/named byte slice, `map[string]T`, `map[string]struct{}`, nested/`*`struct, `any`, `Raw`/`RawMessage`, Marshaler/TextMarshaler/Appender (right **and** wrong signature) | **DIFFERENTIAL+ENUMERATED** — one matrix cell per kind, encode/decode vs both oracles | `TestTypeShapeMatrix`, `TestGeneratorInteractionCompileGuards` | recursive graphs are refused; kinds outside the enumerated set are refused at generate time |
| nil vs empty vs populated **container** encode | slice/map field × {nil, empty-non-nil, populated} × {plain, omitempty} | **ENUMERATED** — nil ⇒ `null`, empty ⇒ `[]`/`{}`, byte-identity vs both oracles | `TestTypeShapeMatrix` (`C_*` cells) | none across the enumerated container kinds |
| `[]byte` base64 | `[]byte`, named byte slice, `*[]byte`, `[][]byte`, `map[string][]byte` | **ENUMERATED** encode+decode; decode also accepts stdlib's array-of-numbers form (seeded by the generated fuzzers) | `TestTypeShapeMatrix` (`C_bytes*`), generated `Fuzz*ZJSON` seeds | `type X []byte` **root** is refused (not a supported root shape) |
| embedding | exported/unexported × value embed × declaration position | **ENUMERATED** — exported-through-unexported promotion vs stdlib; embed-order divergence vs easyjson allowlisted; embedded pointers refused | `TestTypeShapeMatrix` (`E_*`), compile-refusal tests | pointer embeds & method-bearing embeds refused at generate time |
| top-level `null` decode | every generated shape (struct + slice/map roots) | **ENUMERATED** — `null` leaves struct roots unchanged and clears slice/map roots; `nullx` is an error | `TestTopLevelNull` | none |
| `map[string]struct{}` value strictness | value shapes `{}`, `{unknown}`, `null`, number, array, string, bool | **ENUMERATED** — object/null accepted, others rejected, vs both oracles | `TestSetMapStrictness` | none |
| interface-method **signature** detection | `MarshalJSON`/`MarshalText`/`AppendZJSON`/`ReadZJSON`/`IsZero` at the correct and at a wrong signature | **ENUMERATED** — wrong-signature same-name methods ignored (structural encode), vs both oracles | `TestTypeShapeMatrix` (`M_wrong_*`), `TestGeneratorInteractionCompileGuards` | signatures and `zerojson.Lexer` package identity compared by `go/types` identity |
| generated-method provenance | zerojsongen/easyjson output vs arbitrary generated source containing custom JSON/Text methods | **ENUMERATED** — only known replaceable codec outputs may be superseded; other generated custom methods retain their wire format | `TestReplaceableCodecOutputIsGeneratorSpecific`, `TestTypeShapeMatrix` (`L_text*`) | generator identity is pinned by exact standard header prefixes |
| tag name validation & `,string` | invalid tag names, `,string` on int/uint including bare-token rejection, omitempty on scalar-underlying Marshalers | **ENUMERATED** vs stdlib (invalid ⇒ field-name fallback; easyjson emits verbatim, allowlisted) | `TestTypeShapeMatrix` (`T_*`, `L_*_oe`), `TestStringOptionRequiresQuotedInput` | matches `encoding/json`'s `isValidTag` character classes and quoted-token rule |
| decode-error **oracle direction** | arbitrary fuzz bytes | **DIFFERENTIAL** — zerojson-rejects ⇒ stdlib-rejects, with one exemption: Go 1.27's v2-backed `encoding/json` accepting a leading `+` in a `,string` value, granted per type only when stripping those `+` bytes alone makes zerojson accept (`zjStricterCausedByPlus<T>`) | generated `Fuzz*ZJSON` (both directions), `codec/fuzz_allowlist_test.go` | complements the pre-existing stdlib-rejects ⇒ zerojson-rejects value-class check |
| cross-feature interactions | `IsZero` × pointer/scalar/slice, map-value addressability × pointer-only methods, reused target × duplicate composite keys, ordered fallback × callbacks, pooled arena × stateful Reader | **DIFFERENTIAL+STATEFUL** — initialized targets, duplicate members, callback counters, and consecutive pooled decodes against stdlib | `interactions_test.go`, `root_types_test.go` | callback-bearing schemas bypass ordered speculation; arena seen bits distinguish first occurrence from duplicates |

## Running the enumerations

From the repository root:

```sh
go test ./conformance/runtime -count=1
go test -tags exhaustive -timeout 30m ./conformance/runtime -count=1
```

The exhaustive tier includes all 2^32 int32, uint32, and float32 values,
2048×2048 surrogate pairs, and 10^8 valid eight-digit words. Plan for several
minutes on a developer machine; timing depends on hardware and load.
`scripts/verify.sh full` includes this tier.

## Unsafe inventory

The root module's two unsafe conversions live in `unsafe_on.go`.
The `nounsafe` build tag selects copying implementations in `unsafe_off.go`.
Check the compiled file set, not just a text search, when verifying that mode.

### `bytesToString` (`unsafe.String`)

The returned string aliases the input byte slice. Current uses fall into four
classes:

- Numeric/time parsers consume the view during the call. Their errors are
  formatted into owned strings by `Lexer.setErr` before returning.
- Unescaping creates fresh storage that is then exposed as a string.
- `ReadString` returns a borrowed view only when borrow mode was requested.
- `AppendStringBytes` uses the view synchronously while copying/escaping into
  the output buffer.

Tests cover copy-mode input scribbling, borrowed lifetimes, and error ownership,
plus race/checkptr builds. Borrow mode still requires callers to keep input
bytes unchanged while any result refers to them.

### `stringToBytes` (`unsafe.Slice` / `unsafe.StringData`)

`AppendString` uses this read-only view to scan string bytes. It does not write
through the view or return it to callers; output is appended separately.
String differential tests and checkptr exercise this path. Callers must not
use an unsafe writable alias of the source string as the output buffer.
