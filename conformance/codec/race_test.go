package codec

import (
	"strconv"
	"sync"
	"testing"
)

// race_test.go is a race hammer for the shared, process-wide intern table:
// every decode of an `intern`-tagged field, from any goroutine, any pooled or
// non-pooled Decoder, hits the same sharded table. Run under `go test -race`,
// this is the test that would catch a data race in concurrent interning.

const (
	raceGoroutines = 64
	raceIters      = 500
)

// internPool is a small set of repeated tokens shared across every
// goroutine below, forcing genuine contention: many goroutines racing
// to canonicalize (and, on the very first hits, populate) the SAME
// entries concurrently — the case most likely to expose a race, as
// opposed to each goroutine only ever touching its own private keys.
var internPool = []string{"acme", "globex", "initech", "umbrella", "wonka", "soylent"}

// TestInternTableRaceHammer decodes Event (interned Origin field) and
// MapFieldsLike (interned Op field — a second, independent call site) from
// many goroutines concurrently, mixing overlapping tokens (from
// internPool, the contention case) with per-goroutine-unique tokens
// (the table-growth case), alternating copy and borrow decode modes,
// and checks every decode still produces the correct value regardless
// of what any other goroutine is doing to the shared table at the same
// moment.
func TestInternTableRaceHammer(t *testing.T) {
	var wg sync.WaitGroup
	for g := range raceGoroutines {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := range raceIters {
				var origin, op string
				if i%2 == 0 {
					// Overlapping: many goroutines intern-or-look-up the
					// exact same bytes at the same time.
					origin = internPool[i%len(internPool)]
					op = internPool[(i+1)%len(internPool)]
				} else {
					// Distinct: every goroutine/iteration mints a token no
					// other goroutine will ever produce, exercising the
					// table-growth (allocate-and-insert) path instead of
					// the lookup-hit path.
					origin = "src-" + strconv.Itoa(g) + "-" + strconv.Itoa(i)
					op = "op-" + strconv.Itoa(g) + "-" + strconv.Itoa(i)
				}

				borrow := i%3 == 0

				eventData := []byte(`{"origin":"` + origin + `","active":true}`)
				var tr Event
				var err error
				if borrow {
					err = tr.UnmarshalZJSONBorrow(eventData)
				} else {
					err = tr.UnmarshalZJSON(eventData)
				}
				if err != nil {
					t.Errorf("goroutine %d iter %d: event decode error: %v", g, i, err)
					continue
				}
				if tr.Origin != origin {
					t.Errorf("goroutine %d iter %d: origin mismatch: got %q want %q", g, i, tr.Origin, origin)
				}

				dbopData := []byte(`{"op":"` + op + `","set":{"a":1}}`)
				var d MapFieldsLike
				if borrow {
					err = d.UnmarshalZJSONBorrow(dbopData)
				} else {
					err = d.UnmarshalZJSON(dbopData)
				}
				if err != nil {
					t.Errorf("goroutine %d iter %d: dbop decode error: %v", g, i, err)
					continue
				}
				if d.Op != op {
					t.Errorf("goroutine %d iter %d: op mismatch: got %q want %q", g, i, d.Op, op)
				}
			}
		}(g)
	}
	wg.Wait()
}
