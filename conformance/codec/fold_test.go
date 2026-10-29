package codec

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode"
)

// foldLookup is one generated type's case-insensitive field lookup.
type foldLookup struct {
	typ       string
	declared  []string // JSON names in field order
	fieldName func(string) string
	fold      func(string) string
	extra     []string // near misses beyond the declared names' fold variants
}

func foldLookups() []foldLookup {
	return []foldLookup{
		{"FoldNames", jsonNamesOf[FoldNames](), zjFieldName_FoldNames, zjFoldFieldName_FoldNames, []string{
			"", "kk", "siz", "sizes", "s", "S", "\u017f", "orderi", "ORDERIDS", "cafe", "CAFE", "caf",
			"CAF\u00c9S", "x-y-z", "x_y_z", "orderi\u0131", "ORDER\u0130D", "\u212a\u212a",
			"a_declared_name_longer_than_the_other", "a_declared_name_longer_than_the_otherss",
			"A_DECLARED_NAME_LONGER_THAN_THE_OTHERS\u017f", strings.Repeat("A", 64), "\xff", "k\xff", "orderi\xffd",
		}},
		{"FoldUnicodeNames", jsonNamesOf[FoldUnicodeNames](), zjFieldName_FoldUnicodeNames, zjFoldFieldName_FoldUnicodeNames, []string{
			"", "ilk", "ILK", "Ilk", "\u0130lk", "cafe", "CAFE", "caf", "\xff",
		}},
	}
}

// jsonNamesOf lists T's JSON field names in field order.
func jsonNamesOf[T any]() []string {
	t := reflect.TypeFor[T]()
	names := make([]string, t.NumField())
	for i := range names {
		names[i], _, _ = strings.Cut(t.Field(i).Tag.Get("json"), ",")
	}
	return names
}

// referenceFieldName is the lookup zjFieldName_<T> must equal: the key if
// it is a declared name, else the first declared name in field order that
// strings.EqualFold matches, else the key itself.
func referenceFieldName(names []string, key string) string {
	for _, n := range names {
		if key == n {
			return n
		}
	}
	for _, n := range names {
		if strings.EqualFold(key, n) {
			return n
		}
	}
	return key
}

// foldVariants returns spellings of name that strings.EqualFold accepts:
// every combination of its runes' simple-fold orbits for short names, so
// each declared name is reached by ASCII and non-ASCII keys alike, and
// the as-declared, lowercase, and uppercase forms of longer ones.
func foldVariants(name string) []string {
	if len([]rune(name)) > 8 {
		return []string{name, strings.ToLower(name), strings.ToUpper(name)}
	}
	out := []string{""}
	for _, r := range name {
		orbit := []rune{r}
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			orbit = append(orbit, f)
		}
		var next []string
		for _, prefix := range out {
			for _, f := range orbit {
				next = append(next, prefix+string(f))
			}
		}
		out = next
	}
	return out
}

// keys is every fold variant of the declared names plus the near misses:
// wrong lengths, folds that only look alike (dotless and dotted i, an
// unaccented e), keys past the longest name, and invalid UTF-8.
func (l foldLookup) keys() []string {
	var keys []string
	for _, n := range l.declared {
		keys = append(keys, foldVariants(n)...)
	}
	return append(keys, l.extra...)
}

// TestFoldFieldNameMatchesReference compares the generated lookup, whose
// ASCII keys take a lowercase switch instead of the EqualFold chain, with
// the exact-then-EqualFold reference on every fold variant and near miss.
func TestFoldFieldNameMatchesReference(t *testing.T) {
	for _, l := range foldLookups() {
		declared := map[string]bool{}
		for _, n := range l.declared {
			declared[n] = true
		}
		for _, key := range l.keys() {
			want := referenceFieldName(l.declared, key)
			if got := l.fieldName(key); got != want {
				t.Errorf("zjFieldName_%s(%q) = %q, want %q", l.typ, key, got, want)
			}
			if !declared[key] {
				if got := l.fold(key); got != want {
					t.Errorf("zjFoldFieldName_%s(%q) = %q, want %q", l.typ, key, got, want)
				}
			}
		}
	}
	// The cases the ASCII switch must get right by construction.
	for key, want := range map[string]string{
		"K": "\u212a", "k": "k", "SIZE": "\u017fize", "Size": "Size", "ORDERID": "orderID",
		"OrderId": "OrderId", "CAF\u00c9": "caf\u00e9", "CAFE": "CAFE", "X-Y_Z": "x-y_z",
	} {
		if got := zjFieldName_FoldNames(key); got != want {
			t.Errorf("zjFieldName_FoldNames(%q) = %q, want %q", key, got, want)
		}
	}
}

func FuzzFoldFieldName(f *testing.F) {
	lookups := foldLookups()
	for _, l := range lookups {
		for _, key := range l.keys() {
			f.Add(key)
		}
	}
	f.Fuzz(func(t *testing.T, key string) {
		for _, l := range lookups {
			if got, want := l.fieldName(key), referenceFieldName(l.declared, key); got != want {
				t.Fatalf("zjFieldName_%s(%q) = %q, want %q", l.typ, key, got, want)
			}
		}
	})
}

// foldDocs decodes each key alone, every key in one object and again in
// reverse (later members overwrite earlier ones through their folded
// field), and the given extra documents.
func foldDocs(t *testing.T, keys []string, extra ...string) []string {
	var docs []string
	var all, reversed strings.Builder
	all.WriteByte('{')
	reversed.WriteByte('{')
	for i, key := range keys {
		k, err := json.Marshal(key)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, "{"+string(k)+":1}")
		if i > 0 {
			all.WriteByte(',')
			reversed.WriteByte(',')
		}
		all.WriteString(string(k) + ":" + strings.Repeat("1", 1+i%9))
		k, _ = json.Marshal(keys[len(keys)-1-i])
		reversed.WriteString(string(k) + ":" + strings.Repeat("2", 1+i%9))
	}
	return append(append(docs, all.String()+"}", reversed.String()+"}"), extra...)
}

type foldDecoded[T any] interface {
	*T
	UnmarshalZJSON([]byte) error
	UnmarshalZJSONBorrow([]byte) error
}

// checkFoldDecodes decodes each document through the ordered and keyed
// paths in copy, borrow, and pooled modes and compares with encoding/json.
func checkFoldDecodes[T comparable, P foldDecoded[T]](t *testing.T, docs []string, pooled func([]byte, *T) error) {
	t.Helper()
	for _, doc := range docs {
		var want T
		wantErr := json.Unmarshal([]byte(doc), &want)
		for _, mode := range []string{"copy", "borrow", "pooled"} {
			var got T
			var err error
			switch mode {
			case "copy":
				err = P(&got).UnmarshalZJSON([]byte(doc))
			case "borrow":
				err = P(&got).UnmarshalZJSONBorrow([]byte(doc))
			case "pooled":
				err = pooled([]byte(doc), &got)
			}
			if (err != nil) != (wantErr != nil) || got != want {
				t.Errorf("%s %s:\nstdlib:   %+v %v\nzerojson: %+v %v", mode, doc, want, wantErr, got, err)
			}
		}
	}
}

func TestFoldNamesDecodeMatchesStdlib(t *testing.T) {
	lookups := foldLookups()
	var d FoldNamesDecoder
	checkFoldDecodes[FoldNames](t, foldDocs(t, lookups[0].keys(),
		`{"\u212a":1,"k":2}`, `{"\u004b":3}`, `{"\u017fize":4,"Size":5,"SIZE":6}`, `{"S\u0130ZE":7}`,
		`{"orderID":1,"OrderId":2,"ORDERID":3,"orderid":4}`, `{"caf\u00c9":8,"CAFE":9}`), d.Decode)
	var u FoldUnicodeNamesDecoder
	checkFoldDecodes[FoldUnicodeNames](t, foldDocs(t, lookups[1].keys(),
		`{"\u0131LK":1,"caf\u00c9":2}`, `{"ILK":3,"CAFE":4}`), u.Decode)
}

// TestFoldFieldNameDoesNotAllocate pins the ASCII switch to the stack
// buffer: converting it for the switch must not copy it to the heap.
func TestFoldFieldNameDoesNotAllocate(t *testing.T) {
	requireStableAllocationInstrumentation(t)
	for _, l := range foldLookups() {
		for _, key := range []string{"SIZE", "ORDERID", "unknown", strings.Repeat("A", 64), "CAF\u00c9", "\u212a"} {
			if n := testing.AllocsPerRun(100, func() { foldSink = l.fold(key) }); n != 0 {
				t.Errorf("zjFoldFieldName_%s(%q): %v allocations, want 0", l.typ, key, n)
			}
		}
	}
}

var foldSink string
