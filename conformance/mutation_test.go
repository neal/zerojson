package conformance

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestMutationDetectors proves that the regression suite kills realistic,
// compile-valid faults in the runtime and generator. It is opt-in because it
// copies the repository, regenerates fixtures, and launches nested go tests.
// Use scripts/mutation-test.sh; quick is the developer tier and full is the
// broader pre-release tier.
func TestMutationDetectors(t *testing.T) {
	mode := os.Getenv("ZEROJSON_MUTATION")
	if mode == "" {
		t.Skip("run scripts/mutation-test.sh [quick|full]")
	}
	if mode != "quick" && mode != "full" {
		t.Fatalf("ZEROJSON_MUTATION=%q, want quick or full", mode)
	}

	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	mutantRoot := filepath.Join(t.TempDir(), "zerojson-mutant")
	if err := copyMutationTree(repoRoot, mutantRoot); err != nil {
		t.Fatalf("copy repository: %v", err)
	}

	mutations := mutationCatalog()
	var selected []mutationCase
	seenNames := make(map[string]bool)
	for _, m := range mutations {
		if m.name == "" || seenNames[m.name] {
			t.Fatalf("mutation name %q is empty or duplicated", m.name)
		}
		seenNames[m.name] = true
		if m.tier != "quick" && m.tier != "full" {
			t.Fatalf("mutation %s has invalid tier %q", m.name, m.tier)
		}
		if m.old == m.new {
			t.Fatalf("mutation %s does not change its source anchor", m.name)
		}
		if m.tier == "quick" || mode == "full" {
			selected = append(selected, m)
		}
	}
	t.Logf("validating %d of %d curated mutants (%s tier)", len(selected), len(mutations), mode)

	// A pre-existing detector failure must never count as a killed mutant.
	// Run every distinct detector once against the pristine temporary tree.
	baselines := make(map[string]commandSpec)
	for _, m := range selected {
		baselines[m.detector.key()] = m.detector
	}
	for _, detector := range baselines {
		if out, err := runMutationCommand(mutantRoot, detector); err != nil {
			t.Fatalf("baseline detector %s failed: %v\n%s", detector, err, out)
		}
	}

	for _, m := range selected {
		m := m
		t.Run(m.name, func(t *testing.T) {
			t.Logf("injecting %s mutant in %s", m.tier, m.file)
			restore, err := applyMutation(mutantRoot, m)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := restore(); err != nil {
					t.Errorf("restore mutant: %v", err)
				}
			})

			if m.regenerate {
				if out, err := runMutationCommand(mutantRoot, commandSpec{
					dir:  "conformance",
					args: []string{"go", "generate", "./codec"},
				}); err != nil {
					t.Fatalf("mutant did not regenerate cleanly: %v\n%s", err, out)
				}
			}

			out, err := runMutationCommand(mutantRoot, m.detector)
			if err == nil {
				t.Fatalf("MUTANT SURVIVED: detector %s passed", m.detector)
			}
			if !strings.Contains(out, m.wantOutput) {
				t.Fatalf("mutant died for the wrong reason; want detector output containing %q\ncommand: %s\nerror: %v\n%s", m.wantOutput, m.detector, err, out)
			}
		})
	}
}

type mutationCase struct {
	name       string
	tier       string
	file       string
	old        string
	new        string
	occurrence int // zero-based; -1 replaces all occurrences
	wantCount  int
	regenerate bool
	detector   commandSpec
	wantOutput string
}

type commandSpec struct {
	dir  string
	args []string
}

func (c commandSpec) key() string { return c.dir + "\x00" + strings.Join(c.args, "\x00") }

func (c commandSpec) String() string {
	return fmt.Sprintf("(cd %s && %s)", c.dir, strings.Join(c.args, " "))
}

func codecDetector(pattern string) commandSpec {
	return commandSpec{
		dir:  "conformance",
		args: []string{"go", "test", "./codec", "-run", pattern, "-count=1"},
	}
}

func rootDetector(pattern string) commandSpec {
	return commandSpec{
		dir:  ".",
		args: []string{"go", "test", "./...", "-run", pattern, "-count=1"},
	}
}

func generatorDetector(pattern string) commandSpec {
	return commandSpec{
		dir:  "conformance",
		args: []string{"go", "test", "./generator", "-run", pattern, "-count=1"},
	}
}

func mutationCatalog() []mutationCase {
	const (
		validDirect = `g.p("%sif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)`
		validNested = `g.p("%s\t\tif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)`
	)
	return []mutationCase{
		{
			name: "scalar_root_reuse_rejects_null", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%sif l.IsNull() { %s = nil } else if l.EnterArray() {", ind, target)`,
			new: `g.p("%sif l.EnterArray() {", ind)`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "scalar root capacity:",
		},
		{
			name: "scalar_reuse_clears_existing_element", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%s\t\tif !l.IsNull() { zjSlice[zjIndex] = %s }", ind, g.leafReadExpr(f.valInfo))`,
			new: `g.p("%s\t\tvar zjReset %s; zjSlice[zjIndex] = zjReset; if !l.IsNull() { zjSlice[zjIndex] = %s }", ind, f.valInfo.typeStr, g.leafReadExpr(f.valInfo))`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "scalar slice capacity:",
		},
		{
			name: "custom_container_private_publication", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `delegate := stdlibMethodContainer(f) || (collection && g.stdlibValue(f, map[string]bool{}) && g.fieldHasReplayCallbacks(f, map[string]bool{}))`,
			new: `delegate := false && (stdlibMethodContainer(f) || (collection && g.stdlibValue(f, map[string]bool{}) && g.fieldHasReplayCallbacks(f, map[string]bool{})))`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "callback-visible slice lengths:",
		},
		{
			name: "root_slice_drops_caller_alias", tier: "full", file: "cmd/zerojsongen/root.go",
			old: `g.p("\tif *v != nil {")`, new: `g.p("\t*v = nil; if *v != nil {")`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "root slice alias:",
		},
		{
			name: "reused_slice_drops_existing_elements", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%sif %s != nil {", ind, target)`, new: `g.p("%s%s = nil; if %s != nil {", ind, target, target)`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "ordinary compatibility",
		},
		{
			name: "field_lookup_drops_case_folding", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("\tif %s.EqualFold(name, %q) { return %q }", q, f.jsonName, f.jsonName)`, new: `g.p("\tif false && %s.EqualFold(name, %q) { return %q }", q, f.jsonName, f.jsonName)`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "ordinary compatibility",
		},
		{
			name: "publication_pointer", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%s\tif a.published%s {", ind, f.cellName)`, new: `g.p("%s\tif false {", ind)`, wantCount: 1,
			detector: generatorDetector(`^TestGeneratorBehaviorMatrix/publication_scalar_ordered$`), wantOutput: "publication pooled=",
		},
		{
			name: "publication_map", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: ` || a.mapPublished%s {", ind, cell, f.cellName, f.cellName)`, new: ` {", ind, cell, f.cellName)`, wantCount: 1,
			detector: generatorDetector(`^TestGeneratorBehaviorMatrix/publication_map_ordered$`), wantOutput: "publication pooled=",
		},
		{
			name: "publication_slice", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%sa.slicePublished%s = true", ind, f.cellName)`, new: `g.p("%sa.slicePublished%s = false", ind, f.cellName)`, wantCount: 1,
			detector: generatorDetector(`^TestGeneratorBehaviorMatrix/publication_slice_ordered$`), wantOutput: "publication pooled=",
		},
		{
			name: "byte_method_policy", tier: "full", file: "cmd/zerojsongen/load.go",
			old: `g.classify(&element, s.Elem(), ctx+"[elem]")`, new: `_ = element`, wantCount: 1,
			detector: generatorDetector(`^TestGeneratorBehaviorMatrix/byte_methods_02_slice$`), wantOutput: "generator error:",
		},
		{
			name: "predeclared_import_policy", tier: "full", file: "cmd/zerojsongen/main.go",
			old: `generatedIdents[name] || types.Universe.Lookup(name) != nil`, new: `generatedIdents[name]`, wantCount: 1,
			detector: commandSpec{dir: "cmd/zerojsongen", args: []string{"go", "test", ".", "-run", "^TestImportAliasesAvoidPredeclaredNames$", "-count=1"}}, wantOutput: "predeclared import qualifier:",
		},
		{
			name: "state_campaign_observes_retained_maps", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%s\t} else if %s == nil || !a.owned%s || a.mapPublished%s {", ind, cell, f.cellName, f.cellName)`,
			// Both guards now prevent same-document reuse. Remove both to
			// model the original fault of clearing an observable map.
			new:        `g.p("%s\t} else if %s == nil {", ind, cell)`,
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestGeneratedSchemaCampaign$`), wantOutput: "state/retained-pointee mismatch",
		},
		{
			name: "state_campaign_observes_retained_pointers", tier: "full", file: "cmd/zerojsongen/decode.go",
			old:        `g.p("%s*%s = %s", ind, target, readExpr)`,
			new:        `g.p("%s%s = %s; %s = &%s", ind, cell, readExpr, target, cell)`,
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestGeneratedSchemaCampaign$`), wantOutput: "state/retained-pointee mismatch",
		},
		{
			name: "state_campaign_observes_borrowed_raw", tier: "full", file: "raw.go",
			old: "*r = bytes.Clone(data)", new: "*r = append((*r)[:0], data...)",
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestGeneratedSchemaCampaign$`), wantOutput: "overwrote borrowed buffer",
		},
		{
			name: "state_campaign_observes_dynamic_callbacks", tier: "full", file: "cmd/zerojsongen/interface.go",
			old: "rv.Kind() == %s.Pointer && !rv.IsNil()", new: "rv.Kind() == %s.Pointer && rv.IsNil()",
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestGeneratedSchemaCampaign$`), wantOutput: "callbacks=",
		},
		{
			name: "raw_unmarshal_reuses_borrowed_storage", tier: "full", file: "raw.go",
			old: "*r = bytes.Clone(data)", new: "*r = append((*r)[:0], data...)",
			occurrence: 0, wantCount: 1,
			detector: rootDetector(`^TestRawUnmarshalDetachesBorrowedStorage$`), wantOutput: "borrowed input overwritten",
		},
		{
			name: "empty_struct_map_bypasses_methods", tier: "full", file: "cmd/zerojsongen/load.go",
			old: `!named && isEmptyStruct(m.Elem())`, new: `(!named || named) && isEmptyStruct(m.Elem())`,
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestTypeShapeBehavior$`), wantOutput: "TestEmptyStructMethods",
		},
		{
			name: "tagged_hidden_embed_is_dropped", tier: "full", file: "cmd/zerojsongen/load.go",
			old: "if !f.Exported() {", new: "if !f.Exported() && tag != \"\" { continue }; if !f.Exported() {",
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestTypeShapeBehavior$`), wantOutput: "TestTaggedHiddenEmbeds",
		},
		{
			name: "byte_element_alias_hides_methods", tier: "full", file: "cmd/zerojsongen/load.go",
			old: "types.Unalias(s.Elem()).(*types.Named)", new: "s.Elem().(*types.Named)",
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestTypeShapeBehavior$`), wantOutput: "TestDefinedByteSlices",
		},
		{
			name: "array_emptiness_is_not_recorded", tier: "full", file: "cmd/zerojsongen/load.go",
			old: `fi.scalarEmpty = "array"`, new: `fi.scalarEmpty = ""`,
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestTypeShapeBehavior$`), wantOutput: "TestArrayEmptinessAndNull",
		},
		{
			name: "existing_pointer_is_replaced", tier: "full", file: "cmd/zerojsongen/decode.go",
			old:        `g.p("%s*%s = %s", ind, target, readExpr)`,
			new:        `g.p("%s%s = %s; %s = &%s", ind, cell, readExpr, target, cell)`,
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestTypeShapeBehavior$`), wantOutput: "TestSharedPointers",
		},
		{
			name: "interface_concrete_receiver_is_ignored", tier: "full", file: "cmd/zerojsongen/interface.go",
			old: "rv.Kind() == %s.Pointer && !rv.IsNil()", new: "rv.Kind() == %s.Pointer && rv.IsNil()",
			occurrence: 0, wantCount: 1,
			detector: generatorDetector(`^TestTypeShapeBehavior$`), wantOutput: "TestInterfaceReceivers",
		},
		{
			name: "root_entry_skips_syntax_preflight", tier: "full", file: "cmd/zerojsongen/root.go",
			old: `g.p("\tif err := zerojson.Valid(data); err != nil { return err }")`, new: `g.p("\t// syntax preflight omitted")`, wantCount: 1,
			detector: generatorDetector(`^TestIndependentChallenge$`), wantOutput: "root syntax preflight",
		},
		{
			name:       "read_interface_keys_are_interned",
			tier:       "quick",
			file:       "interface.go",
			old:        "m[string(l.ReadKey())] = l.ReadInterface()",
			new:        "m[Intern(l.ReadKey())] = l.ReadInterface()",
			occurrence: 0,
			wantCount:  1,
			detector:   rootDetector(`^TestReadInterfaceDoesNotInternObjectKeys$`),
			wantOutput: "retained an arbitrary object key",
		},
		{
			name:       "map_intern_opt_in_condition_is_inverted",
			tier:       "quick",
			file:       "cmd/zerojsongen/decode.go",
			old:        "if f.intern {\n\t\t\tg.p(\"%s\\tmk := %s(zerojson.Intern(l.ReadKey()))\", ind, f.keyTypeStr)",
			new:        "if !f.intern {\n\t\t\tg.p(\"%s\\tmk := %s(zerojson.Intern(l.ReadKey()))\", ind, f.keyTypeStr)",
			occurrence: 0,
			wantCount:  1,
			detector:   generatorDetector(`^TestMapKeyInterningOptIn$`),
			wantOutput: "tagged map key is not interned",
		},
		{
			name:       "enum_intern_opt_in_condition_is_inverted",
			tier:       "quick",
			file:       "cmd/zerojsongen/decode.go",
			old:        "unknown := \"string(b)\"\n\t\tif f.intern {",
			new:        "unknown := \"string(b)\"\n\t\tif !f.intern {",
			occurrence: 0,
			wantCount:  1,
			detector:   generatorDetector(`^TestMapKeyInterningOptIn$`),
			wantOutput: "tagged enum unknown-value fallback is not interned",
		},
		{
			name:       "raw_copy_aliases_input",
			tier:       "quick",
			file:       "raw.go",
			old:        "*r = bytes.Clone(b)",
			new:        "*r = b\n\t_ = bytes.Clone",
			occurrence: 1, // ReadRaw; occurrence 0 is ReadRawNull, declared above it
			wantCount:  2,
			detector:   codecDetector(`^TestRawHolderCopyModeDoesNotAliasInputBuffer$`),
			wantOutput: "aliased input",
		},
		{
			name:       "finish_accepts_trailing_data",
			tier:       "quick",
			file:       "lexer.go",
			old:        "if l.pos < len(l.data) {",
			new:        "if false && l.pos < len(l.data) {",
			occurrence: 2,
			wantCount:  3,
			detector:   commandSpec{dir: ".", args: []string{"go", "test", ".", "-run", "^TestLexerFinishRejectsTrailingData$", "-count=1"}},
			wantOutput: "Finish accepted trailing data",
		},
		{
			name:       "custom_decode_errors_are_dropped",
			tier:       "quick",
			file:       "lexer.go",
			old:        "l.err = err",
			new:        "_ = err",
			occurrence: 0,
			wantCount:  2,
			detector:   generatorDetector(`^TestGeneratorBehaviorMatrix/json_decode_mutates_then_errors$`),
			wantOutput: "decode errors:",
		},
		{
			name:       "pooled_slice_is_not_reset",
			tier:       "quick",
			file:       "cmd/zerojsongen/decode.go",
			old:        `g.p("%s%s = %s[:0]", ind, cell, cell)`,
			new:        `g.p("%s_ = %s", ind, cell)`,
			occurrence: 0,
			wantCount:  3,
			regenerate: true,
			detector:   codecDetector(`^TestPooledDecoderSliceReuseNoGhosts$`),
			wantOutput: "pooled slice reuse leaked state",
		},
		{
			name:       "pooled_map_is_not_cleared",
			tier:       "quick",
			file:       "cmd/zerojsongen/decode.go",
			old:        `g.p("%s\t\tclear(%s)", ind, cell)`,
			new:        `g.p("%s\t\t_ = %s", ind, cell)`,
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestPooledDecoderMapReuseNoGhosts$`),
			wantOutput: "pooled map reuse leaked state",
		},
		{
			name:       "ordered_fallback_replays_callbacks",
			tier:       "quick",
			file:       "cmd/zerojsongen/ordered.go",
			old:        "return g.namesOrderable(ti.name, map[string]bool{}) && !g.typeHasReplayCallbacks(ti.name, map[string]bool{})",
			new:        "return g.namesOrderable(ti.name, map[string]bool{})",
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedFallbackDoesNotReplayCallbacks$`),
			wantOutput: "callback count:",
		},
		{
			name:       "direct_marshaler_output_is_not_validated",
			tier:       "quick",
			file:       "cmd/zerojsongen/encode.go",
			old:        validDirect,
			new:        "",
			occurrence: 0,
			wantCount:  1,
			detector:   generatorDetector(`^TestGeneratorBehaviorMatrix/malformed_json_marshaler_rejected_in_every_shape$`),
			wantOutput: "direct malformed output errors:",
		},
		{
			name:       "pooled_pointer_cell_is_not_zeroed",
			tier:       "full",
			file:       "cmd/zerojsongen/decode.go",
			old:        `g.p("%s\t%s = zjZero", ind, cell)`,
			new:        `g.p("%s\t_ = zjZero", ind)`,
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestPooledDecoderPtrStructReuseNoSubfieldGhosts$`),
			wantOutput: "pooled ptr-struct reuse leaked state",
		},
		{
			name:       "pooled_entry_points_skip_syntax_preflight",
			tier:       "full",
			file:       "cmd/zerojsongen/pool.go",
			old:        `g.p("\tif err := zerojson.Valid(data); err != nil { return err }")`,
			new:        `g.p("\t// syntax preflight omitted")`,
			occurrence: -1, // one emission site now serves both Decode and DecodeBorrow
			wantCount:  1,
			detector:   generatorDetector(`^TestIndependentChallenge$`),
			wantOutput: "syntax preflight",
		},
		{
			name:       "struct_entry_points_skip_syntax_preflight",
			tier:       "full",
			file:       "cmd/zerojsongen/decode.go",
			old:        `g.p("\tif err := zerojson.Valid(data); err != nil { return err }")`,
			new:        `g.p("\t// syntax preflight omitted")`,
			occurrence: 0,
			wantCount:  1,
			detector:   generatorDetector(`^TestIndependentChallenge$`),
			wantOutput: "syntax preflight",
		},
		{
			name:       "root_slice_is_not_reset",
			tier:       "full",
			file:       "cmd/zerojsongen/root.go",
			old:        `g.p("%s%s = %s[:0]", ind, cell, cell)`,
			new:        `g.p("%s_ = %s", ind, cell)`,
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestRootPooledDecode/EventList_reuse$`),
			wantOutput: "reused-cell len",
		},
		{
			name:       "root_map_is_not_cleared",
			tier:       "full",
			file:       "cmd/zerojsongen/root.go",
			old:        `g.p("%s\t\tclear(%s)", ind, cell)`,
			new:        `g.p("%s\t\t_ = %s", ind, cell)`,
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestRootPooledDecode/LegIndex_reuse$`),
			wantOutput: "reused-table len",
		},
		{
			name:       "reused_slice_slot_is_not_zeroed",
			tier:       "full",
			file:       "cmd/zerojsongen/decode.go",
			old:        `g.p("%s\t\t%s[len(%s)-1] = %s", ind, cell, cell, zero)`,
			new:        `g.p("%s\t\tif false { %s[len(%s)-1] = %s }", ind, cell, cell, zero)`,
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestPooledDecoderNestedInSliceNoLeak$`),
			wantOutput: "nested-in-slice leak",
		},
		{
			name:       "slice_marshaler_output_is_not_validated",
			tier:       "full",
			file:       "cmd/zerojsongen/encode.go",
			old:        validNested,
			new:        "",
			occurrence: 0,
			wantCount:  2,
			detector:   generatorDetector(`^TestGeneratorBehaviorMatrix/malformed_json_marshaler_rejected_in_every_shape$`),
			wantOutput: "slice field malformed output errors:",
		},
		{
			name:       "map_marshaler_output_is_not_validated",
			tier:       "full",
			file:       "cmd/zerojsongen/encode.go",
			old:        validNested,
			new:        "",
			occurrence: 1,
			wantCount:  2,
			detector:   generatorDetector(`^TestGeneratorBehaviorMatrix/malformed_json_marshaler_rejected_in_every_shape$`),
			wantOutput: "map field malformed output errors:",
		},
		{
			name:       "slice_root_marshaler_output_is_not_validated",
			tier:       "full",
			file:       "cmd/zerojsongen/root.go",
			old:        validDirect,
			new:        "",
			occurrence: 0,
			wantCount:  2,
			detector:   generatorDetector(`^TestGeneratorBehaviorMatrix/malformed_json_marshaler_rejected_in_every_shape$`),
			wantOutput: "slice root malformed output errors:",
		},
		{
			name:       "map_root_marshaler_output_is_not_validated",
			tier:       "full",
			file:       "cmd/zerojsongen/root.go",
			old:        validDirect,
			new:        "",
			occurrence: 1,
			wantCount:  2,
			detector:   generatorDetector(`^TestGeneratorBehaviorMatrix/malformed_json_marshaler_rejected_in_every_shape$`),
			wantOutput: "map root malformed output errors:",
		},
		{
			name:       "interned_string_field_is_consumed_but_not_assigned",
			tier:       "full",
			file:       "cmd/zerojsongen/decode.go",
			old:        `assign("l.ReadInternedString()")`,
			new:        `g.p("%s_ = l.ReadInternedString()", ind)`,
			occurrence: 0,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestProductionCorpusShadowReplay/testdata_golden_record_insert.json/original$`),
			wantOutput: "plain copy semantic mismatch",
		},
		{
			name:       "synthetic_model_catches_dropped_custom_map_error",
			tier:       "full",
			file:       "lexer.go",
			old:        "l.err = err",
			new:        "_ = err",
			occurrence: 0,
			wantCount:  2,
			detector:   codecDetector(`^TestSyntheticShadowPairwise/op_null__set_string_wrong__.*$`),
			wantOutput: "acceptance mismatch",
		},
	}
}

func applyMutation(root string, m mutationCase) (func() error, error) {
	path := filepath.Join(root, filepath.FromSlash(m.file))
	original, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", m.file, err)
	}
	text := string(original)
	if count := strings.Count(text, m.old); count != m.wantCount {
		return nil, fmt.Errorf("%s anchor count = %d, want %d; mutation catalog is stale", m.file, count, m.wantCount)
	}
	mutated, err := replaceOccurrence(text, m.old, m.new, m.occurrence)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", m.name, err)
	}

	generated := []string{
		"conformance/codec/zerojson_gen.go",
		"conformance/codec/zerojson_gen_fuzz_test.go",
	}
	snapshots := map[string][]byte{path: original}
	if m.regenerate {
		for _, name := range generated {
			p := filepath.Join(root, filepath.FromSlash(name))
			b, err := os.ReadFile(p)
			if err != nil {
				return nil, fmt.Errorf("snapshot %s: %w", name, err)
			}
			snapshots[p] = b
		}
	}
	if err := os.WriteFile(path, []byte(mutated), 0o644); err != nil {
		return nil, fmt.Errorf("write mutant %s: %w", m.file, err)
	}
	return func() error {
		for p, b := range snapshots {
			if err := os.WriteFile(p, b, 0o644); err != nil {
				return err
			}
		}
		return nil
	}, nil
}

func replaceOccurrence(s, old, replacement string, occurrence int) (string, error) {
	if occurrence == -1 {
		return strings.ReplaceAll(s, old, replacement), nil
	}
	start := 0
	for i := 0; i <= occurrence; i++ {
		rel := strings.Index(s[start:], old)
		if rel < 0 {
			return "", fmt.Errorf("occurrence %d not found", occurrence)
		}
		at := start + rel
		if i == occurrence {
			return s[:at] + replacement + s[at+len(old):], nil
		}
		start = at + len(old)
	}
	panic("unreachable")
}

func runMutationCommand(root string, spec commandSpec) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, spec.args[0], spec.args[1:]...)
	cmd.Dir = filepath.Join(root, filepath.FromSlash(spec.dir))
	cmd.Env = append(os.Environ(), "GOWORK=off")
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return string(out), fmt.Errorf("%s: %w", spec, ctx.Err())
	}
	return string(out), err
}

func copyMutationTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return os.MkdirAll(dst, 0o755)
		}
		if d.IsDir() && (d.Name() == ".git" || d.Name() == ".codex" || d.Name() == ".agents") {
			return filepath.SkipDir
		}
		outPath := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(outPath, info.Mode().Perm())
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(target, outPath)
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(outPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}
