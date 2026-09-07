# Historical benchmark results

[Current benchmark commands](README.md#benchmarking) · [Project overview](../README.md)

The following are **historical measurements, not benchmarks of the current
branch**. They predate changes to depth checks, null handling, pointer reuse,
and pooled target resetting. Re-run the benchmarks on your target architecture
before using these numbers to estimate current performance.

They were measured on an Apple M4 Pro (arm64, Go 1.26.4), with
`-benchtime 2s -count 6` and the median reported. The stdlib rows were measured
separately on the same machine with `-benchtime 1s -count 3`.
`Event` mixes direct leaves, custom methods, containers, and interface fields;
`EventLeg` is a leaf-only shape.

**Encode** (`Event`, 35 fields, ns/op · B/op · allocs/op — median of six):

| | ns/op | B/op | allocs/op |
|---|--:|--:|--:|
| `AppendZJSON`, fresh buffer | 1798 | 3905 | 31 |
| `AppendZJSON`, reused buffer | **1478** | 584 | 22 |
| `easyjson.Marshal` | 2464 | 3290 | 49 |
| `encoding/json.Marshal` | 3810 | 2194 | 44 |

**Decode** (`Event`):

| | ns/op | B/op | allocs/op |
|---|--:|--:|--:|
| `UnmarshalZJSON` (copy) | 3030 | 2403 | 54 |
| `UnmarshalZJSONBorrow` | 2976 | 2366 | 48 |
| pooled `Decode` (copy) | 2746 | 1610 | 42 |
| pooled `DecodeBorrow` | **2729** | 1612 | 36 |
| `easyjson.Unmarshal` | 3840 | 2477 | 56 |
| `encoding/json.Unmarshal` | 9265 | 3776 | 94 |

`encoding/json` reflects `Event` structurally: it has no `MarshalJSON`
wrapper routing back through zerojson. In this historical sample, reused-buffer
encode was about 2.6× faster and pooled-borrow decode about 3.4× faster.

The leaf-only `EventLeg` sample isolates the allocation-free path:

| `EventLeg` | ns/op | B/op | allocs/op |
|---|--:|--:|--:|
| `AppendZJSON`, reused buffer | 22.0 | **0** | **0** |
| `UnmarshalZJSONBorrow` | 30.8 | **0** | **0** |
| pooled `DecodeBorrow` | 30.8 | **0** | **0** |

**Ordered fast path vs keyed fallback** is benchmarked on callback-free
`EventLeg` using identical bytes and a fresh arena. Callback-bearing schemas
use the keyed path directly so user decode methods are never replayed.

**Other** (median of six):

| | ns/op | throughput | allocs/op |
|---|--:|--:|--:|
| Projection (`EventProjection` over full `Event` bytes) | 771 | — | 1 |
| `Valid`, `record_insert` (golden) | 140 | 2.1 GB/s | 0 |
| `Valid`, `record_update` (golden) | 82 | 2.2 GB/s | 0 |
| `Valid`, `summary_update` (golden) | 125 | 1.9 GB/s | 0 |
| Golden batch replay (3 records: pooled borrow decode + reused-buffer re-encode) | 8800 | 82 MB/s | 193 |

Allocation budgets are pinned separately in
[allocation_test.go](codec/allocation_test.go). Those assertions
track the current code; the timing tables above do not.
