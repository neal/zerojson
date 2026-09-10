# Fresh-storage decision evidence

Control: `ba64c77964c2c56e68ccbfe08ce2e5bd565456b7`. The fresh-ordered
prototype changed only the generated Decoder wrapper: a local arena replaces
its persistent field. Encoding and scalar/ordered paths were unchanged.
The implementation additionally removes obsolete recycling bookkeeping.

The amd64 comparison used Go 1.26.5, CGO disabled, Intel Xeon Platinum 8280,
Ubuntu 24.04, one pinned CPU, GOMAXPROCS=1, GOGC=100 and GOMEMLIMIT=1GiB.
Six 250ms samples per benchmark ran in control/candidate/candidate/control
order, three samples per entry, without concurrent compilation or tests.
`amd64-comparison.json` records median ns/op, sample range, bytes and allocations.
Raw logs remain in the maintainer's experiment archive, not in this repository.

| Pooled borrow workload | Control | Prototype | Allocations |
|---|---:|---:|---:|
| Flat EventLeg | 193.85 ns | 193 ns | 0 → 0 |
| Mixed Event | 10.2645 µs | 11.2105 µs | 39 → 55 |
| Payload, 0 elements | 732.9 ns | 934.25 ns | 0 → 3 |
| Payload, 4 elements | 1.7915 µs | 2.4565 µs | 0 → 9 |
| Payload, 64 elements | 15.7575 µs | 19.657 µs | 0 → 17 |

A separate fixed-100,000-decode allocation/GC observation on a 64-element
borrowing payload moved from 0 B/op and no GC to 6,960 B/op and 239–240 GC
cycles; elapsed time moved from 1.62–1.64s to 2.02–2.05s. Aggregate stop-the-world
pause was 2.56–2.60ms, not all GC CPU cost or a production tail-latency estimate.

This cost is deliberately accepted to remove cross-call result ownership.
Flat zero-allocation decoding stays required. The finalized implementation is
compared with this prototype, not silently granted a larger budget. See the
root development plan for gate status and forward acceptance criteria.

Reproduce fixture measurements from conformance with:

```sh
GOMAXPROCS=1 go test ./ownership -run '^$' -bench '^BenchmarkPayload$' -benchmem -count=6 -benchtime=250ms
GOMAXPROCS=1 go test ./codec -run '^$' -bench '^BenchmarkDecodeEvent(Leg)?_ZJSON_PooledBorrow$' -benchmem -count=6 -benchtime=250ms
```

Run both checkouts on the same idle host, alternating their order. Do not
compare absolute timings from different hosts or treat these synthetic shapes
as downstream workload capacity guarantees.
