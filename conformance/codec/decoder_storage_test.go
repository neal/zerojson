package codec

import (
	"reflect"
	"sync/atomic"
	"testing"
)

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
