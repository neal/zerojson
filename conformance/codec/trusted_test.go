package codec

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/neal/zerojson"
)

// Trusted decoding skips zerojson.Valid, so the generated decoders meet
// malformed input directly. FuzzTrustedDecode hands every input to each
// type's UnmarshalZJSONTrusted: on invalid JSON it may fail or succeed but
// must not panic, and on input Valid accepts it must decode exactly as
// UnmarshalZJSON does. The per-type generated fuzzers run the same check
// alongside their stdlib oracles; this target covers every shape per input.

type trustedDecoder interface {
	UnmarshalZJSON([]byte) error
	UnmarshalZJSONTrusted([]byte) error
}

var trustedTargets = []struct {
	name string
	make func() trustedDecoder
}{
	{"Event", func() trustedDecoder { return new(Event) }},
	{"RawHolder", func() trustedDecoder { return new(RawHolder) }},
	{"OmitZeroCases", func() trustedDecoder { return new(OmitZeroCases) }},
	{"StringOptCases", func() trustedDecoder { return new(StringOptCases) }},
	{"AppenderCases", func() trustedDecoder { return new(AppenderCases) }},
	{"MapFieldsLike", func() trustedDecoder { return new(MapFieldsLike) }},
	{"EventLeg", func() trustedDecoder { return new(EventLeg) }},
	{"EmbedMid", func() trustedDecoder { return new(EmbedMid) }},
	{"EventProjection", func() trustedDecoder { return new(EventProjection) }},
	{"InteractionOmitZero", func() trustedDecoder { return new(InteractionOmitZero) }},
	{"MapPointerMethods", func() trustedDecoder { return new(MapPointerMethods) }},
	{"ReuseSemantics", func() trustedDecoder { return new(ReuseSemantics) }},
	{"OrderedSideEffects", func() trustedDecoder { return new(OrderedSideEffects) }},
	{"PoolAppenderHolder", func() trustedDecoder { return new(PoolAppenderHolder) }},
	{"BytesCases", func() trustedDecoder { return new(BytesCases) }},
	{"EventList", func() trustedDecoder { return new(EventList) }},
	{"LegPtrList", func() trustedDecoder { return new(LegPtrList) }},
	{"LegIndex", func() trustedDecoder { return new(LegIndex) }},
	{"PointerMethodIndex", func() trustedDecoder { return new(PointerMethodIndex) }},
	{"PointerQuotedIndex", func() trustedDecoder { return new(PointerQuotedIndex) }},
	{"RawList", func() trustedDecoder { return new(RawList) }},
	{"RawIndex", func() trustedDecoder { return new(RawIndex) }},
	{"RawHolderList", func() trustedDecoder { return new(RawHolderList) }},
	{"DecimalList", func() trustedDecoder { return new(DecimalList) }},
	{"PointerCollections", func() trustedDecoder { return new(PointerCollections) }},
	{"NumberCases", func() trustedDecoder { return new(NumberCases) }},
	{"NullCases", func() trustedDecoder { return new(NullCases) }},
	{"FoldNames", func() trustedDecoder { return new(FoldNames) }},
	{"FoldUnicodeNames", func() trustedDecoder { return new(FoldUnicodeNames) }},
	{"OrderedLookup", func() trustedDecoder { return new(OrderedLookup) }},
	{"IgnoredOnly", func() trustedDecoder { return new(IgnoredOnly) }},
	{"EventMeta", func() trustedDecoder { return new(EventMeta) }},
	{"ReuseInner", func() trustedDecoder { return new(ReuseInner) }},
}

func FuzzTrustedDecode(f *testing.F) {
	golden, _ := filepath.Glob("testdata/golden/*.json")
	for _, path := range golden {
		data, err := os.ReadFile(path)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
		for _, cut := range []int{1, len(data) / 3, len(data) / 2, len(data) - 1} {
			f.Add(data[:cut])
		}
	}
	for _, v := range variants() {
		if data, err := v.AppendZJSON(nil); err == nil {
			f.Add(data)
			f.Add(data[:len(data)-1])
		}
	}
	for _, s := range []string{`{}`, `[]`, `null`, `{"legs":[{"ratio":1}]}`, `{"a":`, `[{`, `{"bag":{"k":[1,{"x":`} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		valid := zerojson.Valid(data) == nil
		for _, target := range trustedTargets {
			trusted := target.make()
			trustedErr := trusted.UnmarshalZJSONTrusted(data)
			if !valid {
				continue
			}
			checked := target.make()
			checkedErr := checked.UnmarshalZJSON(data)
			if (trustedErr == nil) != (checkedErr == nil) || trustedErr != nil && trustedErr.Error() != checkedErr.Error() {
				t.Fatalf("%s: trusted decode error %v, checked decode error %v, for %q", target.name, trustedErr, checkedErr, data)
			}
			if !reflect.DeepEqual(trusted, checked) {
				t.Fatalf("%s: trusted decode differs for %q", target.name, data)
			}
		}
	})
}
