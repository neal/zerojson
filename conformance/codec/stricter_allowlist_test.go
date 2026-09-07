package codec

import "testing"

// TestStricterAllowlistIsCausalAndScoped pins the generated helpers behind
// the Go 1.27 `,string` "+00" exemption, negatively and
// toolchain-independently (the zjStdlibJSONV2 gate is applied separately
// in zjDecodeStricterAllowed<T>):
//
//   - zjStripStringOptPlus<T> touches only a `,string` field of THAT type,
//     as an object member at the depth where the field lives;
//   - zjStricterCausedByPlus<T> grants the exemption only when removing
//     those '+' bytes alone makes zerojson accept the document — presence
//     of a "+" member is not enough, the '+' must be the cause.
//
// Two independent reviews found, in turn, a file-wide list that matched
// `{"unknown":{"count":"+00"},"origin":123}` for every fuzz target, and a
// per-type list that still exempted documents zerojson rejected for
// unrelated reasons.
func TestStricterAllowlistIsCausalAndScoped(t *testing.T) {
	for _, doc := range []string{
		`{"unknown":{"count":"+00"},"origin":123}`, // count nested one level too deep
		`{"count":"+00"}`,                          // Event has no ,string field named count
		`{"origin":"+1 555"}`,                      // an ordinary string value beginning with '+'
		`{"seq":"+5"}`,                             // a quoted-int64 marker field, not ,string
	} {
		if _, ok := zjStripStringOptPlusEvent([]byte(doc)); ok {
			t.Errorf("Event: %s wrongly matched a ,string site", doc)
		}
		if zjStricterCausedByPlusEvent([]byte(doc)) {
			t.Errorf("Event: %s wrongly exempted", doc)
		}
	}

	// StringOptCases declares count and amount with ,string at depth 1.
	strip := []struct{ in, want string }{
		{`{"count":"+00"}`, `{"count":"00"}`},
		{`{"amount":"+1","count":"+2"}`, `{"amount":"1","count":"2"}`},
		{`{ "count" : "+00" }`, `{ "count" : "00" }`},
		{`{"x":[1,2],"count":"+00"}`, `{"x":[1,2],"count":"00"}`},
		{`{"c\u006funt":"+00"}`, `{"c\u006funt":"00"}`}, // escaped key still names the field
	}
	for _, c := range strip {
		got, ok := zjStripStringOptPlusStringOptCases([]byte(c.in))
		if !ok || string(got) != c.want {
			t.Errorf("StringOptCases strip %s: ok=%v got %s want %s", c.in, ok, got, c.want)
		}
		if !zjStricterCausedByPlusStringOptCases([]byte(c.in)) {
			t.Errorf("StringOptCases: %s should be exempt (the '+' is the sole cause)", c.in)
		}
	}
	for _, doc := range []string{
		`{"unknown":{"count":"+00"}}`, // wrong depth: not a site, and zerojson accepts it anyway
		`{"count":"5"}`,               // no '+'
		`{"count":5}`,                 // zerojson rejects (bare on ,string) but no '+' to blame
		`{"other":"+00"}`,
		`{"count`, // malformed: never exempt
		// Presence without causality: a '+' site exists, but zerojson
		// rejects the stripped document too, so the exemption must NOT
		// apply and the stdlib comparison must run.
		`{"count":"+00","amount":"x"}`,
		`{"count":"+00","amount":5}`,
		`{"count":"+00"} trailing`,
		`{"count":"+abc"}`,
	} {
		if zjStricterCausedByPlusStringOptCases([]byte(doc)) {
			t.Errorf("StringOptCases: %s wrongly exempted", doc)
		}
	}
}
