package codec

import (
	"encoding/json"
	"testing"
)

// int_overflow_test.go pins integer-overflow decode behavior: a numeric
// value that exceeds a field's integer width must be a decode error,
// exactly as encoding/json rejects it — never a silently wrapped or
// truncated value (e.g. 2147483648 must not become a negative int32 with a
// nil error).
//
// Each case is asserted for error-agreement against encoding/json (the
// decode contract oracle) through both the copy and borrow entry points
// and, where the type has a pooled Decoder, the pooled path too.
func TestDecodeIntWidthOverflowMatchesStdlib(t *testing.T) {
	// Event carries Shard int32, Flags uint16, Count int (64-bit).
	eventCases := []struct {
		name string
		json string
	}{
		{"shard_int32_in_range", `{"shard":2147483647}`},
		{"shard_int32_overflow", `{"shard":2147483648}`},
		{"shard_int32_underflow", `{"shard":-2147483649}`}, // neighbor
		{"flags_uint16_in_range", `{"flags":65535}`},
		{"flags_uint16_overflow", `{"flags":65536}`},
		{"flags_uint16_negative", `{"flags":-1}`},
		{"count_int64_overflow", `{"count":9223372036854775808}`},
		{"count_int64_underflow", `{"count":-9223372036854775809}`}, // neighbor
		{"count_int64_boundary_ok", `{"count":9223372036854775807}`},
	}
	for _, tc := range eventCases {
		t.Run("event/"+tc.name, func(t *testing.T) {
			wantErr := json.Unmarshal([]byte(tc.json), new(Event)) != nil

			var a Event
			if (a.UnmarshalZJSON([]byte(tc.json)) != nil) != wantErr {
				t.Errorf("UnmarshalZJSON: stdlib wantErr=%v disagreed", wantErr)
			}
			var b Event
			if (b.UnmarshalZJSONBorrow([]byte(tc.json)) != nil) != wantErr {
				t.Errorf("UnmarshalZJSONBorrow: stdlib wantErr=%v disagreed", wantErr)
			}
			var dec EventDecoder
			var c Event
			if (dec.Decode([]byte(tc.json), &c) != nil) != wantErr {
				t.Errorf("EventDecoder.Decode: stdlib wantErr=%v disagreed", wantErr)
			}
		})
	}

	// StringOptCases exercises the quoted (,string) integer path: Count
	// uint32, Amount int64. The range check must apply after stripping the
	// quotes exactly as it does to a bare number.
	stringOptCases := []struct {
		name string
		json string
	}{
		{"count_uint32_overflow_quoted", `{"count":"4294967296","amount":"0"}`},
		{"count_uint32_ok_quoted", `{"count":"4294967295","amount":"0"}`},
		{"amount_int64_overflow_quoted", `{"count":"0","amount":"9223372036854775808"}`},
		{"amount_int64_ok_quoted", `{"count":"0","amount":"9223372036854775807"}`},
	}
	for _, tc := range stringOptCases {
		t.Run("stringopt/"+tc.name, func(t *testing.T) {
			wantErr := json.Unmarshal([]byte(tc.json), new(StringOptCases)) != nil
			var a StringOptCases
			if (a.UnmarshalZJSON([]byte(tc.json)) != nil) != wantErr {
				t.Errorf("UnmarshalZJSON: stdlib wantErr=%v disagreed", wantErr)
			}
		})
	}
}
