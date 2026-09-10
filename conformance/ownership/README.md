# Decoder ownership checks

These bounded tests compare ordinary decoding with independent `encoding/json`
decodes. Reset-first wrappers reset the oracle before each call; ordinary
unmarshal methods do not. Inputs and oracle backing storage are separate.

- `TestTransitions`: slice capacities 0–8, all initial lengths, warm/short/grow
  sizes 0–5, null/value elements, duplicate fields, copy/borrow and reset/reuse.
  Observes final values, retained caller backing arrays, input integrity, and
  zero-capacity empty arrays. 155,520 checks, including equivalent states.
- `TestCallbackTransitions`: capacities 0–6, all initial lengths, sizes 0–8,
  four modes. Compares callback-visible enclosing slice contents (1,008 checks).
- `TestResultLifetime`: retains prior pointer/slice/map-bearing results through
  later calls, malformed input, and recovery on the same Decoder.
- `TestResultStorageIsolation`: eight copy/borrow and success/semantic-error
  combinations. Mutates equal-valued results in both directions to expose
  hidden sharing, including storage published by a failed decode.
- `TestResultStorageSurvivesCallbackPanic`: retains partial callback-visible
  slice storage across panic recovery and a later decode in both modes.
- `BenchmarkPayload`: sizes 0, 4, and 64, comparing target reuse and reset-first
  decoding in copy and borrow modes against stdlib.

Run `go test ./ownership` from conformance; add `-race -gcflags=all=-d=checkptr=2`
for instrumented checks. These tests do not launch child processes.

To measure the fixture from conformance:

```sh
GOMAXPROCS=1 go test ./ownership -run '^$' -bench '^BenchmarkPayload$' -benchmem -count=6 -benchtime=250ms
```

Compare revisions on the same idle host, alternating their order. Synthetic
measurements are not production capacity or tail-latency guarantees.

This is not exhaustive: arbitrary callback mutations, pointer graphs, named
container roots, registered overrides, and overlapping calls are covered by
other generator/codec tests, not enumerated here. Callback sequences with
errors are not exhaustively combined across those dimensions. Partial error
results are snapshotted for isolation, not compared with stdlib's partial values.

The complementary checks are `generator/TestIndependentChallenge` (root and
overlapping aliases, callback replacement, error/panic recovery),
`generator/TestScalarReuseKinds` (scalar fields, pointer fields and roots),
`generator/publication_test.go` (within-call publication), and
`codec/TestOverrideSliceStorage` (registered-leaf container length/capacity).
The independent challenge and scalar matrix require
`ZEROJSON_CHALLENGE_RACE=1` to instrument their generated child processes;
an outer `go test -race` alone does not do that.
