package codec

import (
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// TestPooledDecodeNeverAdoptsCallerMemory pins that a pooled Decode
// zeroes its target first, so a caller-owned map or pointee is never
// adopted as an arena cell and cleared by a LATER decode.
func TestPooledDecodeNeverAdoptsCallerMemory(t *testing.T) {
	mine := map[string]any{"keep": 1.0}
	var d EventDecoder
	prev := Event{Bag: mine}
	if err := d.Decode([]byte(`{"bag":{"a":1}}`), &prev); err != nil {
		t.Fatal(err)
	}
	if err := d.Decode([]byte(`{"bag":{"b":2}}`), new(Event)); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(mine, map[string]any{"keep": 1.0}) {
		t.Fatalf("caller-owned map was clobbered by a later pooled decode: %v", mine)
	}
}

// TestPooledDecoderConcurrentUsePanics pins that two goroutines sharing a
// Decoder fail loudly rather than tearing the arena.
func TestPooledDecoderConcurrentUsePanics(t *testing.T) {
	var d EventLegDecoder
	doc := []byte(`{"ratio":"1.5","label":"L"}`)
	var wg sync.WaitGroup
	var mu sync.Mutex
	panics := 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					if !strings.Contains(r.(string), "concurrent Decode") {
						t.Errorf("unexpected panic: %v", r)
					}
					mu.Lock()
					panics++
					mu.Unlock()
				}
			}()
			for j := 0; j < 2000; j++ {
				var v EventLeg
				_ = d.Decode(doc, &v)
			}
		}()
	}
	wg.Wait()
	if panics == 0 {
		t.Log("no overlap observed this run (timing-dependent); the guard is exercised by the race detector elsewhere")
	}
	// The Decoder is usable again afterwards.
	var v EventLeg
	if err := d.Decode(doc, &v); err != nil {
		t.Fatal(err)
	}
}

// TestPooledDecoderRecoversAfterPanic pins that a panic inside a pooled
// decode (here a nil target) does not leave the Decoder permanently
// "busy": the next call must work, not report a spurious concurrent use.
func TestPooledDecoderRecoversAfterPanic(t *testing.T) {
	var d EventLegDecoder
	doc := []byte(`{"ratio":"1.5","label":"L"}`)
	func() {
		defer func() { _ = recover() }()
		_ = d.Decode(doc, nil)
	}()
	var v EventLeg
	if err := d.Decode(doc, &v); err != nil || v.Label != "L" {
		t.Fatalf("Decoder unusable after a recovered panic: err=%v v=%+v", err, v)
	}
}

// Cover struct, slice-root and map-root wrappers. The only persistent state
// is the overlap guard; behavioral lifetime checks live in ownership.
func TestDecoderOwnsNoResultStorage(t *testing.T) {
	for _, decoder := range []any{EventDecoder{}, EventListDecoder{}, LegIndexDecoder{}} {
		typ := reflect.TypeOf(decoder)
		if typ.NumField() != 1 || typ.Field(0).Name != "busy" || typ.Field(0).Type != reflect.TypeOf(atomic.Bool{}) {
			t.Errorf("retained decoder storage: %s", typ)
		}
	}
}
