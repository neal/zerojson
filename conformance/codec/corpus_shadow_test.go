package codec

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/neal/zerojson"
)

// corpus_shadow_test.go is the production-shaped replay layer. Checked-in
// synthetic records always run; ZEROJSON_CORPUS_DIR may add sanitized .json,
// .jsonl, or .ndjson captures without copying them into the repository.
// Every record is transformed deterministically and shadow-decoded through
// encoding/json plus plain/pooled zerojson in both copy and borrow modes.

const maxShadowRecordBytes = 16 << 20

type shadowRecord struct {
	name string
	data []byte
}

func TestProductionCorpusShadowReplay(t *testing.T) {
	records := loadShadowCorpus(t)
	if len(records) < 6 {
		t.Fatalf("shadow corpus has %d records, want at least 6", len(records))
	}

	var pooledCopy, pooledBorrow MapFieldsLikeDecoder
	mutations := 0
	for _, record := range records {
		record := record
		t.Run(shadowTestName(record.name), func(t *testing.T) {
			assertShadowSchema(t, record)
			seenVariants := make(map[string]bool)
			for _, variant := range shadowVariants(t, record) {
				variant := variant
				if variant.name == "" || seenVariants[variant.name] {
					t.Fatalf("empty or duplicate variant name %q", variant.name)
				}
				seenVariants[variant.name] = true
				mutations++
				t.Run(variant.name, func(t *testing.T) {
					shadowCompare(t, variant, &pooledCopy, &pooledBorrow)
				})
			}
		})
	}
	t.Logf("shadow-replayed %d records across %d deterministic variants", len(records), mutations)
}

func FuzzProductionCorpusShadow(f *testing.F) {
	for _, record := range loadShadowCorpus(f) {
		f.Add(record.data)
	}
	for _, seed := range syntheticFuzzSeeds() {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > maxShadowRecordBytes || hugeExponent(data) || !json.Valid(data) {
			return
		}
		if ok, _ := shadowSchemaAdmission(data); !ok {
			return
		}
		var pooledCopy, pooledBorrow MapFieldsLikeDecoder
		shadowCompare(t, shadowVariant{name: "fuzz", data: data}, &pooledCopy, &pooledBorrow)
	})
}

type shadowVariant struct {
	name string
	data []byte
}

func shadowCompare(t *testing.T, variant shadowVariant, pooledCopy, pooledBorrow *MapFieldsLikeDecoder) {
	t.Helper()
	if len(variant.data) > maxShadowRecordBytes {
		t.Fatalf("variant exceeds %d-byte safety limit", maxShadowRecordBytes)
	}
	if !json.Valid(variant.data) {
		t.Fatal("structure-aware corpus mutator produced invalid JSON")
	}
	if err := zerojson.Valid(variant.data); err != nil {
		t.Fatalf("zerojson.Valid rejected stdlib-valid variant: %v", err)
	}

	var std MapFieldsLike
	stdErr := json.Unmarshal(variant.data, &std)

	inputCopy := bytes.Clone(variant.data)
	var plainCopy MapFieldsLike
	plainCopyErr := plainCopy.UnmarshalZJSON(inputCopy)
	assertShadowErrorParity(t, "plain copy", stdErr, plainCopyErr)

	inputBorrow := bytes.Clone(variant.data)
	var plainBorrow MapFieldsLike
	plainBorrowErr := plainBorrow.UnmarshalZJSONBorrow(inputBorrow)
	assertShadowErrorParity(t, "plain borrow", stdErr, plainBorrowErr)

	pooledInputCopy := bytes.Clone(variant.data)
	var viaPooledCopy MapFieldsLike
	pooledCopyErr := pooledCopy.Decode(pooledInputCopy, &viaPooledCopy)
	assertShadowErrorParity(t, "pooled copy", stdErr, pooledCopyErr)

	pooledInputBorrow := bytes.Clone(variant.data)
	var viaPooledBorrow MapFieldsLike
	pooledBorrowErr := pooledBorrow.DecodeBorrow(pooledInputBorrow, &viaPooledBorrow)
	assertShadowErrorParity(t, "pooled borrow", stdErr, pooledBorrowErr)

	if stdErr != nil {
		return
	}

	// Copy modes must own all decoded data even when the input comes from a
	// synthetic/error-recovery sequence. Borrow modes retain their inputs.
	scribble(inputCopy)
	assertMapFieldsLikeEqual(t, "plain copy", std, plainCopy)
	assertMapFieldsLikeEqual(t, "plain borrow", std, plainBorrow)
	scribble(pooledInputCopy)
	assertMapFieldsLikeEqual(t, "pooled copy", std, viaPooledCopy)
	assertMapFieldsLikeEqual(t, "pooled borrow", std, viaPooledBorrow)

	// Shadow the generated encoder as well. Compare AFTER each encoder has
	// applied its own documented lossy rules (notably omitempty turning a
	// non-nil empty map into an omitted field that re-decodes as nil); comparing
	// either round-trip directly with std would incorrectly flag that shared
	// stdlib/zerojson behavior as a codec mismatch. Bytes may differ in map
	// order, so the oracle remains semantic.
	stdEncoded, err := json.Marshal(&std)
	if err != nil {
		t.Fatalf("encoding/json re-encode failed: %v", err)
	}
	var wantRoundTrip MapFieldsLike
	if err := json.Unmarshal(stdEncoded, &wantRoundTrip); err != nil {
		t.Fatalf("encoding/json rejected its own output: %v", err)
	}

	encoded, err := plainCopy.AppendZJSON(nil)
	if err != nil {
		t.Fatalf("zerojson re-encode failed: %v", err)
	}
	if err := zerojson.Valid(encoded); err != nil {
		t.Fatalf("zerojson emitted invalid JSON: %v", err)
	}
	var stdRoundTrip MapFieldsLike
	if err := json.Unmarshal(encoded, &stdRoundTrip); err != nil {
		t.Fatalf("encoding/json rejected zerojson output: %v", err)
	}
	assertMapFieldsLikeEqual(t, "zerojson encode -> stdlib decode", wantRoundTrip, stdRoundTrip)
}

func assertShadowErrorParity(t *testing.T, path string, stdErr, zeroErr error) {
	t.Helper()
	if (stdErr != nil) != (zeroErr != nil) {
		t.Fatalf("%s acceptance mismatch: encoding/json error=%v zerojson error=%v", path, stdErr != nil, zeroErr != nil)
	}
}

func assertMapFieldsLikeEqual(t *testing.T, path string, want, got MapFieldsLike) {
	t.Helper()
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("%s semantic mismatch", path)
	}
}

// assertShadowSchema prevents an unrelated JSON object from yielding a false
// green by decoding to an all-zero MapFieldsLike through unknown-field skips.
// Production captures for this harness must be top-level objects containing at
// least one exact declared key; case-fold-only keys are rejected explicitly.
func assertShadowSchema(t *testing.T, record shadowRecord) {
	t.Helper()
	ok, reason := shadowSchemaAdmission(record.data)
	if !ok {
		t.Fatal(reason)
	}
}

func shadowSchemaAdmission(data []byte) (bool, string) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(data, &object); err != nil {
		return false, fmt.Sprintf("corpus record is not a JSON object: %v", err)
	}
	hasDeclared := false
	for key := range object {
		switch key {
		case "op", "set", "where":
			hasDeclared = true
		default:
			lower := strings.ToLower(key)
			if lower == "op" || lower == "set" || lower == "where" {
				return false, fmt.Sprintf("corpus record uses case-fold-only declared key %q", key)
			}
		}
	}
	if !hasDeclared {
		return false, "corpus record has no exact op, set, or where key"
	}
	return true, ""
}

func loadShadowCorpus(tb testing.TB) []shadowRecord {
	tb.Helper()
	var records []shadowRecord
	for _, dir := range []string{filepath.Join("testdata", "golden"), filepath.Join("testdata", "shadow")} {
		records = append(records, readShadowCorpusDir(tb, dir, dir)...)
	}
	if external := os.Getenv("ZEROJSON_CORPUS_DIR"); external != "" {
		externalRecords := readShadowCorpusDir(tb, external, "external")
		if len(externalRecords) == 0 {
			tb.Fatalf("external shadow corpus %s contains no supported non-empty records", external)
		}
		records = append(records, externalRecords...)
	}
	return records
}

func TestShadowVariantsDeterministic(t *testing.T) {
	for _, record := range loadShadowCorpus(t) {
		first := shadowVariants(t, record)
		second := shadowVariants(t, record)
		if len(first) != len(second) {
			t.Fatalf("%s: variant count changed: %d then %d", record.name, len(first), len(second))
		}
		for i := range first {
			if first[i].name != second[i].name || !bytes.Equal(first[i].data, second[i].data) {
				t.Fatalf("%s: variant %d is not reproducible", record.name, i)
			}
		}
	}
}

func readShadowCorpusDir(tb testing.TB, dir, namespace string) []shadowRecord {
	tb.Helper()
	var records []shadowRecord
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(filepath.Join(namespace, rel))
		switch strings.ToLower(filepath.Ext(path)) {
		case ".json":
			if info.Size() > maxShadowRecordBytes {
				return fmt.Errorf("%s exceeds %d-byte record limit", name, maxShadowRecordBytes)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			data = bytes.TrimSpace(data)
			if len(data) == 0 {
				return fmt.Errorf("%s is empty", name)
			}
			records = append(records, shadowRecord{name: name, data: data})
		case ".jsonl", ".ndjson":
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			scanner := bufio.NewScanner(file)
			scanner.Buffer(make([]byte, 64<<10), maxShadowRecordBytes)
			line := 0
			for scanner.Scan() {
				line++
				data := bytes.TrimSpace(bytes.Clone(scanner.Bytes()))
				if len(data) == 0 {
					continue
				}
				records = append(records, shadowRecord{name: fmt.Sprintf("%s#%d", name, line), data: data})
			}
			scanErr := scanner.Err()
			closeErr := file.Close()
			if scanErr != nil {
				return fmt.Errorf("scan %s: %w", name, scanErr)
			}
			if closeErr != nil {
				return closeErr
			}
		}
		return nil
	})
	if err != nil {
		tb.Fatalf("load shadow corpus %s: %v", dir, err)
	}
	return records
}

func shadowVariants(t *testing.T, record shadowRecord) []shadowVariant {
	t.Helper()
	base, ok := parseJSONTree(record.data)
	if !ok || base.kind != 'o' {
		t.Fatal("admitted corpus record did not parse as an object tree")
	}
	seedHash := sha256.Sum256(record.data)
	seed := int64(binary.LittleEndian.Uint64(seedHash[:8]))
	plainWS := &wsSource{inject: false}
	variants := []shadowVariant{{name: "original", data: bytes.Clone(record.data)}}

	// Equivalent spellings exercise whitespace and ordered/fallback behavior.
	withWhitespace := serialize(nil, base, &wsSource{r: rand.New(rand.NewSource(seed)), inject: true}, nil)
	variants = append(variants, shadowVariant{name: "whitespace", data: withWhitespace})
	permuted := serialize(nil, base, plainWS, rand.New(rand.NewSource(seed^0x5a5a5a5a)))
	variants = append(variants, shadowVariant{name: "key_order", data: permuted})

	// Unknown nested content drives strict Skip over realistic composite values.
	unknownTree := mustShadowTree(t, record.data)
	unknownValue := mustShadowTree(t, []byte(`{"trace":{"ids":[1,2,3],"sampled":true},"notes":[null,"x",{"n":-1.25e3}]}`))
	unknownTree.memb = append(unknownTree.memb, jmember{keyRaw: []byte(`"__zerojson_shadow"`), val: unknownValue})
	variants = append(variants, shadowVariant{name: "unknown_nested", data: serialize(nil, unknownTree, plainWS, nil)})

	// Same-value duplicate keys are semantically invariant but force duplicate
	// handling and ordered-path fallback without needing an oracle allowlist.
	if len(base.memb) > 0 {
		duplicate := mustShadowTree(t, record.data)
		duplicate.memb = append(duplicate.memb, duplicate.memb[0])
		variants = append(variants, shadowVariant{name: "duplicate_same", data: serialize(nil, duplicate, plainWS, nil)})
	}

	limit := min(len(base.memb), 16)
	for i := 0; i < limit; i++ {
		keyName := shadowKeyName(base.memb[i].keyRaw)

		omitted := mustShadowTree(t, record.data)
		omitted.memb = append(omitted.memb[:i], omitted.memb[i+1:]...)
		variants = append(variants, shadowVariant{name: fmt.Sprintf("omit_%02d_%s", i, keyName), data: serialize(nil, omitted, plainWS, nil)})

		nulled := mustShadowTree(t, record.data)
		nulled.memb[i].val = &jnode{kind: 's', raw: []byte("null")}
		variants = append(variants, shadowVariant{name: fmt.Sprintf("null_%02d_%s", i, keyName), data: serialize(nil, nulled, plainWS, nil)})

		if escaped := escapedDeclaredKey(base.memb[i].keyRaw); escaped != nil {
			escapedTree := mustShadowTree(t, record.data)
			escapedTree.memb[i].keyRaw = escaped
			variants = append(variants, shadowVariant{name: fmt.Sprintf("escaped_key_%02d_%s", i, keyName), data: serialize(nil, escapedTree, plainWS, nil)})
		}
	}

	// Mutate up to 16 scalar values while keeping the document valid. Unlike
	// the transformations above these change semantics, so the shadow oracle
	// compares both decoders on the resulting document rather than to the base.
	for i := 0; i < 16; i++ {
		mutated := mustShadowTree(t, record.data)
		var scalars []*jnode
		collectShadowScalars(mutated, &scalars)
		if i >= len(scalars) {
			break
		}
		alt := differentScalar(scalars[i])
		if alt == nil || bytes.Equal(alt.raw, scalars[i].raw) {
			continue
		}
		*scalars[i] = *alt
		variants = append(variants, shadowVariant{name: fmt.Sprintf("scalar_%02d", i), data: serialize(nil, mutated, plainWS, nil)})
	}

	return variants
}

func mustShadowTree(t *testing.T, data []byte) *jnode {
	t.Helper()
	tree, ok := parseJSONTree(data)
	if !ok {
		t.Fatal("shadow corpus tree parse failed")
	}
	return tree
}

func collectShadowScalars(node *jnode, out *[]*jnode) {
	switch node.kind {
	case 's':
		*out = append(*out, node)
	case 'o':
		for _, member := range node.memb {
			collectShadowScalars(member.val, out)
		}
	case 'a':
		for _, elem := range node.elems {
			collectShadowScalars(elem, out)
		}
	}
}

func escapedDeclaredKey(raw []byte) []byte {
	var key string
	if json.Unmarshal(raw, &key) != nil {
		return nil
	}
	switch key {
	case "op":
		return []byte(`"o\u0070"`)
	case "set":
		return []byte(`"s\u0065t"`)
	case "where":
		return []byte(`"w\u0068ere"`)
	}
	return nil
}

func shadowKeyName(raw []byte) string {
	var key string
	if json.Unmarshal(raw, &key) != nil {
		return "key"
	}
	return shadowTestName(key)
}

func shadowTestName(name string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-', r == '.', r == '#':
			return r
		default:
			return '_'
		}
	}, name)
}
