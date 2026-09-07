// Package oracle describes the encoding/json implementation the
// conformance suite is running against. Go 1.27 re-based encoding/json on
// the encoding/json/v2 implementation (GOEXPERIMENT=jsonv2 became the
// default), and that "v1-on-v2" layer differs from classic v1 in a few
// edge cases the suite pins against stdlib. The suite detects the
// implementation by probing behavior, never by Go version, so it also
// does the right thing under GOEXPERIMENT=nojsonv2 on a 1.27+ toolchain.
package oracle

import (
	"bytes"
	"encoding/json"
)

// StdlibJSONV2 reports whether encoding/json is the v2-backed
// implementation. The probe: classic v1 encodes an invalid UTF-8 byte as
// the six-byte backslash-u-f-f-f-d escape; v2 (and the v1-on-v2 layer) emits the raw
// three-byte U+FFFD rune.
// classicEscape is the six ASCII bytes classic v1 (and zerojson, and
// easyjson) write for an invalid UTF-8 input byte — spelled out byte by
// byte so no editor or tool can silently turn it into the raw rune.
var classicEscape = []byte{'\\', 'u', 'f', 'f', 'f', 'd'}

var StdlibJSONV2 = func() bool {
	b, _ := json.Marshal("\x80")
	return !bytes.Contains(b, classicEscape)
}()

// Known behavioral drifts of the v1-on-v2 layer relative to classic v1,
// each pinned where the suite compares against stdlib:
//
//   - Invalid UTF-8 in an encoded string: raw U+FFFD instead of the backslash-u-f-f-f-d escape
//     (zerojson and easyjson keep the classic escape).
//   - A `,string` field accepts a leading '+' inside the quotes ("+00");
//     classic v1, easyjson, and zerojson reject it.
//   - An invalid tag name (one containing a backslash) is truncated at the
//     backslash rather than falling back to the Go field name; zerojson
//     matches classic v1 (documented in the compatibility contract).
//   - The `any`-fallback encode path (interface{} fields delegate to
//     encoding/json) allocates more per call.
//   - An ESCAPED quoted null ("\u006eull") on a `,string` pointer field
//     preserves a non-nil pointer but allocates zero if it was nil.
//     Classic v1 clears it; both clear it for the plain spelling.
//   - A value-typed TextUnmarshaler slice or map is left untouched by
//     null, where classic v1 clears it (pinned by the generator null matrix).
//
// StdlibEscapesInvalidUTF8 is the first of those, named for readability
// at call sites that normalize stdlib output before a byte comparison.
var StdlibEscapesInvalidUTF8 = !StdlibJSONV2

// ClassicUTF8 rewrites stdlib output produced by the v2-backed
// implementation into what classic v1 would have produced for the same
// value: the raw replacement rune becomes the classic escape. It is the
// identity on classic v1 output (which never contains the raw rune for
// an invalid input byte — a valid U+FFFD in the INPUT is also passed
// through raw by classic v1, so callers must only apply this to inputs
// that do not themselves contain U+FFFD).
func ClassicUTF8(stdlibOutput []byte) []byte {
	if !StdlibJSONV2 {
		return stdlibOutput
	}
	return bytes.ReplaceAll(stdlibOutput, []byte("\uFFFD"), classicEscape)
}
