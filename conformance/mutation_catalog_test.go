package conformance

// Curated faults and their independent behavioral detectors.
func mutationCatalog() []mutationCase {
	const (
		validDirect = `g.p("%sif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)`
		validNested = `g.p("%s\t\tif err := zerojson.Valid(raw); err != nil { return dst, err }", ind)`
	)
	return []mutationCase{
		{
			// A type whose only custom codec is a hand-written
			// MarshalEasyJSON must be refused, not encoded by its Go
			// structure (which silently changes a migrated wire format).
			name: "easyjson_only_type_is_encoded_structurally", tier: "quick", file: "cmd/zerojsongen/load.go",
			old:       `if m := g.handwrittenEasyjsonMethod(named); m != "" && !(hasM && hasU) {`,
			new:       `if m := g.handwrittenEasyjsonMethod(named); false && m != "" && !(hasM && hasU) {`,
			wantCount: 1,
			detector:  generatorDetector(`^TestGeneratorBehaviorMatrix/easyjson_only_field_refused$`), wantOutput: "want substring",
		},
		{
			// A same-package named map whose methods are generated output must
			// get the inline map codec; delegating to the methods makes
			// regeneration change the codec.
			name: "generated_named_map_methods_delegated", tier: "quick", file: "cmd/zerojsongen/load.go",
			old:       "generatedContainer = true\n\t\t\t\thasM, hasU = false, false",
			new:       "generatedContainer = false",
			wantCount: 1,
			detector:  generatorDetector(`^TestGeneratedNamedMapMethodsAreReplaced$`), wantOutput: "delegates to v.Set",
		},
		{
			// A member-boundary deviation hands the rest of the object to the
			// keyed member loop; restarting re-decodes the ordered prefix.
			name: "ordered_deviation_restarts_object", tier: "quick", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\t\\t\\t\\tl.SetPos(memberPos)\")\n\tg.p(\"\\t\\t\\t\\t%s(l, v, a)\", membersDecodeName(n))\n\tg.p(\"\\t\\t\\t\\treturn l.Err() == nil\")",
			new:        "g.p(\"\\t\\t\\t\\t_ = memberPos\")\n\tg.p(\"\\t\\t\\t\\treturn false\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedHandoffDecodesPrefixOnce$`), wantOutput: "want 2",
		},
		{
			// The keyed loop must resume at the deviating member's key.
			name: "ordered_handoff_skips_rewind", tier: "quick", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\t\\t\\t\\tl.SetPos(memberPos)\")",
			new:        "g.p(\"\\t\\t\\t\\t_ = memberPos\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedHandoffMatchesStdlib$`), wantOutput: "zerojson err=",
		},
		{
			// A plain declared key out of order hands the rest of the object
			// to the keyed member loop too, not just a key ReadKey had to read.
			name: "ordered_lookup_handoff_restarts_object", tier: "quick", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\t\\t\\t%s(l, v, a)\", membersDecodeName(n))\n\tg.p(\"\\t\\t\\treturn l.Err() == nil\")\n\tg.p(\"\\t\\tdefault:\")",
			new:        "g.p(\"\\t\\t\\treturn false\")\n\tg.p(\"\\t\\tdefault:\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedHandoffDecodesPrefixOnce$`), wantOutput: "want 2",
		},
		{
			// The lookup must not match a field before the cursor: a repeated
			// or out-of-order name belongs to the keyed loop.
			name: "ordered_lookup_ignores_cursor", tier: "quick", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\tif k >= from {\")",
			new:        "g.p(\"\\tif k >= 0 {\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestSeekOrderedKeyMatchesReference$`), wantOutput: "; want -2 at",
		},
		{
			// A plain key that names a field only under case folding is
			// declared, not unknown.
			name: "ordered_lookup_skips_folded_names", tier: "quick", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\t\\tif name := string(key); %s(name) != name {\", foldFieldNameFunc(n))",
			new:        "g.p(\"\\t\\tif name := string(key); false && %s(name) != name {\", foldFieldNameFunc(n))",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedHandoffMatchesStdlib$`), wantOutput: "zerojson {Origin:acme Active:false}",
		},
		{
			// A declared name behind the cursor is a duplicate or out of
			// order; skipping it as unknown loses the later value.
			name: "ordered_lookup_skips_declared_names", tier: "quick", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\tif k >= 0 {\")\n\tg.p(\"\\t\\treturn -2\")",
			new:        "g.p(\"\\tif k >= 0 {\")\n\tg.p(\"\\t\\tl.SetPos(next)\")\n\tg.p(\"\\t\\treturn -3\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedHandoffMatchesStdlib$`), wantOutput: "zerojson {Origin:acme Active:true}, stdlib {Origin:beta Active:true}",
		},
		{
			// The fold is skipped only for keys longer than every name.
			name: "ordered_lookup_fold_guard_too_short", tier: "full", file: "cmd/zerojsongen/ordered.go",
			old:        "g.p(\"\\tif len(key) <= %d {\", maxLen)",
			new:        "g.p(\"\\tif len(key) < %d {\", maxLen)",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestOrderedLookupDecodeMatchesStdlib$`), wantOutput: `copy {"A_DECLARED_NAME_LONGER_THAN_THIRTY_TWO":8}`,
		},
		{
			// Near the end of input the byte loop must stop at a raw control byte too.
			name: "valid_fast_tail_admits_control", tier: "quick", file: "valid.go",
			old:       "for j < n && data[j] != '\"' && data[j] != '\\\\' && data[j] >= 0x20 {",
			new:       "for j < n && data[j] != '\"' && data[j] != '\\\\' {",
			wantCount: 1,
			detector:  rootDetector(`^TestValidFastMatchesExact$`), wantOutput: "validFast accepted invalid",
		},
		{
			// The word mask must mark raw control bytes, not only quotes and
			// backslashes.
			name: "valid_fast_word_admits_control", tier: "full", file: "valid.go",
			old:       "q := v ^ 0x0202020202020202\n\tb := v ^ 0x5c5c5c5c5c5c5c5c\n\treturn ((q-0x21*lo)&^q | (b-lo)&^b) & hi",
			new:       "q := v ^ 0x2222222222222222\n\tb := v ^ 0x5c5c5c5c5c5c5c5c\n\treturn ((q-lo)&^q | (b-lo)&^b) & hi",
			wantCount: 1,
			detector:  rootDetector(`^TestValidFastMatchesExact$`), wantOutput: "validFast accepted invalid",
		},
		{
			// A container closes only with its own bracket.
			name: "valid_fast_ignores_closer_kind", tier: "quick", file: "valid.go",
			old:       "case c == closer:",
			new:       "case c == '}' || c == ']':",
			wantCount: 1,
			detector:  rootDetector(`^TestValidFastMatchesExact$`), wantOutput: "validFast accepted invalid",
		},
		{
			// A comma separates members only inside a container.
			name: "valid_fast_top_level_comma", tier: "quick", file: "valid.go",
			old:       "case c == ',' && closer > 0:",
			new:       "case c == ',':",
			wantCount: 1,
			detector:  rootDetector(`^TestValidFastMatchesExact$`), wantOutput: "validFast accepted invalid",
		},
		{
			// Past its stack the fast pass must hand the document to validExact.
			name: "valid_fast_depth_unchecked", tier: "full", file: "valid.go",
			old:       "if depth == validFastDepth {",
			new:       "if depth == 4*validFastDepth {",
			wantCount: 1,
			detector:  rootDetector(`^TestValidFastDepthBoundary$`), wantOutput: "validFast accepted depth 257",
		},
		{
			// Strings are the common case; sending them to validExact silently gives up the fast pass.
			name: "valid_fast_strings_fall_back", tier: "quick", file: "valid.go",
			old:       "case '\"':\n\t\tgoto str",
			new:       "case '\"':\n\t\tif false {\n\t\t\tgoto str\n\t\t}\n\t\treturn false",
			wantCount: 1,
			detector:  rootDetector(`^TestValidFastMatchesExact$`), wantOutput: "validFast rejected valid",
		},
		{
			// A key followed by whitespace before its colon is not plain.
			name: "plain_key_ignores_colon", tier: "quick", file: "ordered.go",
			old:       "if end+1 >= len(data) || data[end] != '\"' || data[end+1] != ':' {",
			new:       "if end+1 >= len(data) || data[end] != '\"' {",
			wantCount: 1,
			detector:  rootDetector(`^TestPlainKey$`), wantOutput: `PlainKey("\"a\" :1" at 0)`,
		},
		{
			// ReadKey normalizes invalid UTF-8, so a plain key is ASCII only.
			name: "plain_key_admits_non_ascii", tier: "full", file: "ordered.go",
			old:       "if high&0x8080808080808080 != 0 {",
			new:       "if false && high&0x8080808080808080 != 0 {",
			wantCount: 1,
			detector:  rootDetector(`^TestPlainKey$`), wantOutput: `, 8, true; want "", 0, false`,
		},
		{
			// Nested structs must share the decode's cell block once it
			// exists; otherwise every nested type pays its own allocation.
			name: "nested_cells_not_shared", tier: "quick", file: "cmd/zerojsongen/decode.go",
			old:        `g.p("%s\ta.a%s.zjBlock = &a.zjBlock.s%s", ind, f.cellName, f.cellName)`,
			new:        `g.p("%s\t_ = a", ind)`,
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestLazyCellBlockAllocations$`), wantOutput: "want 1",
		},
		{
			// AppendInterface may copy Marshaler output verbatim only when
			// encoding/json would; HTML-sensitive bytes must be escaped.
			name: "append_interface_verbatim_skips_html_escape", tier: "quick", file: "writer.go",
			old:       "if c >= utf8.RuneSelf || c == '<' || c == '>' || c == '&' || c == '\\\\' {",
			new:       "if c >= utf8.RuneSelf || c == '\\\\' {",
			wantCount: 1,
			detector:  rootDetector(`^TestAppendInterfaceMatchesMarshal$`), wantOutput: "AppendInterface:",
		},
		{
			// Output needing post-processing is replayed into json.Marshal,
			// not re-encoded from the value, so the method runs once.
			name: "append_interface_calls_marshaler_twice", tier: "quick", file: "writer.go",
			old:       "return appendReplayed(dst, v, replayMarshaler{b, err})",
			new:       "return appendStdlib(dst, v)",
			wantCount: 1,
			detector:  rootDetector(`^TestAppendInterfaceCallsMethodsOnce$`), wantOutput: "method called 2 times",
		},
		{
			// A replayed Marshaler error must name v's type, not the stand-in.
			name: "append_interface_error_names_stand_in", tier: "full", file: "writer.go",
			old:       "retyped.Type = vt",
			new:       "retyped.Type = st",
			wantCount: 1,
			detector:  rootDetector(`^TestAppendInterfaceMatchesMarshal$`), wantOutput: "errors differ",
		},
		{
			// Appender slice elements and map values must call AppendZJSON;
			// emitting nothing would produce "[,]".
			name: "appender_element_encodes_nothing", tier: "quick", file: "cmd/zerojsongen/encode.go",
			old:        `g.p("%sdst, err = %s.AppendZJSON(dst)", ind, expr)`,
			new:        `g.p("%s_ = %s", ind, expr)`,
			occurrence: 1, wantCount: 2,
			detector: generatorDetector(`^TestGeneratorShapeValidation/slice,_pointer_slice,_and_map_of_hand-written_Appender_struct$`), wantOutput: "encode: got",
		},
		{
			name: "empty_slice_keeps_previous_capacity", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%sif len(%s) == 0 {", ind, cell)`,
			new: `g.p("%sif %s == nil {", ind, cell)`, wantCount: 1,
			regenerate: true, detector: codecDetector(`^TestOverrideSliceStorage$`), wantOutput: "empty override slice retained capacity:",
		},
		{
			// Cross-call hidden-capacity clearing no longer exists. Prevent
			// reintroducing retained result storage instead; keep the old
			// hidden-capacity behavioral tests in TestScalarReuseKinds.
			name: "decoder_retains_arena", tier: "quick", file: "cmd/zerojsongen/pool.go",
			old: `g.p("type %s struct {", dec)`,
			new: `g.p("type %s struct {", dec); g.p("\ta %s", arenaName(n))`, wantCount: 1,
			regenerate: true, detector: codecDetector(`^TestDecoderOwnsNoResultStorage$`), wantOutput: "retained decoder storage",
		},
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
			name: "ordered_replays_shared_collections", tier: "full", file: "cmd/zerojsongen/ordered.go",
			old: `if f.ptr || f.kind == kInterface || f.kind == kSlice || f.kind == kLeafSlice || f.kind == kByteSlice || f.kind == kMap {`,
			new: `if f.ptr || f.kind == kInterface {`, wantCount: 1,
			detector: generatorDetector(`^TestSharedStorageReplay$`), wantOutput: "shared storage state=",
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
			// An ASCII key reaches a declared name with a Kelvin sign or long
			// s only through the ASCII switch; dropping such names from it
			// sends "K" to the later field "k".
			name: "ascii_fold_drops_unicode_names", tier: "quick", file: "cmd/zerojsongen/decode.go",
			old: `c, ok := asciiFoldMember(r)`, new: `c, ok := byte(r), r < utf8.RuneSelf`, wantCount: 1,
			regenerate: true,
			detector:   codecDetector(`^TestFoldFieldNameMatchesReference$`), wantOutput: `zjFieldName_FoldNames("K") = "k"`,
		},
		{
			// Folded ties go to the first field in declaration order.
			name: "ascii_fold_tie_takes_last_field", tier: "quick", file: "cmd/zerojsongen/decode.go",
			old:        "if !ok || seen[key] {\n\t\t\tcontinue\n\t\t}",
			new:        "if !ok {\n\t\t\tcontinue\n\t\t}\n\t\tif seen[key] {\n\t\t\tfor i := range cases {\n\t\t\t\tif cases[i].key == key {\n\t\t\t\t\tcases[i].name = f.jsonName\n\t\t\t\t}\n\t\t\t}\n\t\t\tcontinue\n\t\t}",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestFoldFieldNameMatchesReference$`), wantOutput: `zjFieldName_FoldNames("ORDERID") = "OrderId"`,
		},
		{
			name: "ascii_fold_skips_lowercasing", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("\t\t\tif 'A' <= c && c <= 'Z' { c += 'a' - 'A' }")`, new: `g.p("\t\t\t_ = 'A'")`, wantCount: 1,
			regenerate: true,
			detector:   codecDetector(`^TestFoldFieldNameMatchesReference$`), wantOutput: `zjFieldName_FoldNames("SIZE") = "SIZE"`,
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
			old: `g.p("%s\t%s = %s(l, nil)", ind, cell, read)`, new: `g.p("%s\tif %s != nil { clear(%s); %s = %s(l, %s) } else { %s = %s(l, nil) }", ind, cell, cell, cell, read, cell, cell, read)`, wantCount: 1,
			detector: generatorDetector(`^TestGeneratorBehaviorMatrix/publication_map_ordered$`), wantOutput: "publication pooled=",
		},
		{
			name: "publication_slice", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%s\t%s = nil", ind, cell)`, new: `g.p("%s\t// incorrectly keep detached storage", ind)`, wantCount: 1,
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
			name: "zero_method_mutates_map_value", tier: "full", file: "cmd/zerojsongen/encode.go",
			old: `if !addressable && !f.ptr && f.omitzero && f.hasIsZero && !f.zeroOnValue {`,
			new: `if false && !addressable && !f.ptr && f.omitzero && f.hasIsZero && !f.zeroOnValue {`, wantCount: 1,
			detector: generatorDetector(`^TestGeneratorBehaviorMatrix/zero_method_true_map_emptyfalse$`), wantOutput: "IsZero receiver:",
		},
		{
			name: "state_campaign_observes_retained_maps", tier: "full", file: "cmd/zerojsongen/decode.go",
			old: `g.p("%s\t%s = %s(l, nil)", ind, cell, read)`,
			// Recycle detached storage to model clearing an observable map.
			new:        `g.p("%s\tif %s != nil { clear(%s); %s = %s(l, %s) } else { %s = %s(l, nil) }", ind, cell, cell, cell, read, cell, cell, read)`,
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
			old:        "k := string(l.ReadKey())",
			new:        "k := Intern(l.ReadKey())",
			occurrence: 0,
			wantCount:  2,
			detector:   rootDetector(`^TestReadInterfaceDoesNotInternObjectKeys$`),
			wantOutput: "retained an arbitrary object key (member 0)",
		},
		{
			name:       "read_interface_spill_keys_are_interned",
			tier:       "quick",
			file:       "interface.go",
			old:        "k := string(l.ReadKey())",
			new:        "k := Intern(l.ReadKey())",
			occurrence: 1,
			wantCount:  2,
			detector:   rootDetector(`^TestReadInterfaceDoesNotInternObjectKeys$`),
			wantOutput: "retained an arbitrary object key (member 9)",
		},
		{
			// An error past the stack slots must not publish the members
			// buffered so far as a partial object.
			name: "read_interface_spill_error_returns_partial_object", tier: "quick", file: "interface.go",
			old:       "\t\tif l.err != nil {\n\t\t\treturn nil\n\t\t}\n\t\tif m != nil {",
			new:       "\t\tif l.err != nil {\n\t\t\tbreak\n\t\t}\n\t\tif m != nil {",
			wantCount: 1,
			detector:  rootDetector(`^TestReadInterfaceObjectErrorIsNil$`), wantOutput: "want nil and an error",
		},
		{
			// The spill buffer must hand over to direct insertion when full.
			name: "read_interface_spill_overruns_buffer", tier: "quick", file: "interface.go",
			old:       "} else if n < len(kv) {",
			new:       "} else if n <= len(kv) {",
			wantCount: 1,
			detector:  rootDetector(`^TestReadInterfaceObjectSizes$`), wantOutput: "index out of range",
		},
		{
			// Members buffered before the hand-over to direct insertion
			// must reach the map.
			name: "read_interface_spill_drops_buffered_members", tier: "quick", file: "interface.go",
			old:       "m = make(map[string]any, 2*len(kv))\n\t\t\tfor _, e := range kv[:n] {",
			new:       "m = make(map[string]any, 2*len(kv))\n\t\t\tfor _, e := range kv[:0] {",
			wantCount: 1,
			detector:  rootDetector(`^TestReadInterfaceObjectSizes$`), wantOutput: "n=40 dup=0: err=<nil>",
		},
		{
			name:       "map_intern_opt_in_condition_is_inverted",
			tier:       "quick",
			file:       "cmd/zerojsongen/maps.go",
			old:        "if intern {\n\t\tg.p(\"%smk := %s(zerojson.Intern(l.ReadKey()))\", ind, keyTypeStr)",
			new:        "if !intern {\n\t\tg.p(\"%smk := %s(zerojson.Intern(l.ReadKey()))\", ind, keyTypeStr)",
			occurrence: 0,
			wantCount:  1,
			detector:   generatorDetector(`^TestMapKeyInterningOptIn$`),
			wantOutput: "tagged map key is not interned",
		},
		{
			// The member that overflows the stack slots is handed to the
			// spill helper and must be kept.
			name: "map_spill_drops_handed_member", tier: "quick", file: "cmd/zerojsongen/maps.go",
			old:        "g.p(\"\\tkv[n] = %s{mk, mv}\", entry)\n\tg.p(\"\\tn++\")",
			new:        "g.p(\"\\t_, _ = mk, mv\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestMapSizesMatchStdlib$`), wantOutput: "mismatch for",
		},
		{
			// An existing map is added to; buffering is only for new maps.
			name: "map_existing_target_is_buffered", tier: "quick", file: "cmd/zerojsongen/maps.go",
			old:        "g.p(\"\\t\\t\\tif m != nil {\")",
			new:        "g.p(\"\\t\\t\\tif false {\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestMapSizesMatchStdlib$`), wantOutput: "mismatch for",
		},
		{
			// Members buffered before the spill helper switches to direct
			// insertion must reach the map.
			name: "map_spill_drops_buffered_members", tier: "quick", file: "cmd/zerojsongen/maps.go",
			old:        "g.p(\"\\t\\t\\tfor _, e := range kv[:n] { m[e.k] = e.v }\")",
			new:        "g.p(\"\\t\\t\\tfor _, e := range kv[:0] { m[e.k] = e.v }\")",
			wantCount:  1,
			regenerate: true,
			detector:   codecDetector(`^TestMapSizesMatchStdlib$`), wantOutput: "mismatch for",
		},
		{
			// A buffered map must be made at its final size, not grown.
			name: "map_spill_drops_size_hint", tier: "quick", file: "cmd/zerojsongen/maps.go",
			old:        "g.p(\"\\t\\tm = make(%s, n)\", typeStr)",
			new:        "g.p(\"\\t\\tm = make(%s)\", typeStr)",
			occurrence: 1,
			wantCount:  2,
			regenerate: true,
			detector:   codecDetector(`^TestMapDecodeMakesMapOnce$`), wantOutput: "decode allocations=",
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
			name:       "duplicate_slice_is_not_reset",
			tier:       "quick",
			file:       "cmd/zerojsongen/decode.go",
			old:        `g.p("%s%s = %s[:0]", ind, cell, cell)`,
			new:        `g.p("%s_ = %s", ind, cell)`,
			occurrence: 0,
			wantCount:  3,
			regenerate: true,
			detector:   codecDetector(`^TestPooledDecoderNestedInSliceNoLeak$`),
			wantOutput: "nested-in-slice leak on duplicate member:",
		}, {
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
			detector:   codecDetector(`^TestOverrideSliceStorage$`),
			wantOutput: "reused override root length:",
		}, {
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
