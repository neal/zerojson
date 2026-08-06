package main

import (
	"strings"
	"testing"
)

// These exercise the tag-policy validation directly (parseJSONOptions,
// parseZerojsonTag, validateFieldOptions return errors rather than
// calling log.Fatal, precisely so they're unit-testable — see leaf_test.go
// for the same convention applied to parseLeaf).

func TestParseJSONOptionsAcceptsStandard(t *testing.T) {
	g := &generator{}
	var fi fieldInfo
	if err := g.parseJSONOptions(&fi, []string{"omitempty", "omitzero", "string"}, "T.F"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !fi.omitempty || !fi.omitzero || !fi.stringOpt {
		t.Fatalf("options not recorded: %+v", fi)
	}
}

func TestParseJSONOptionsRejectsUnknown(t *testing.T) {
	g := &generator{}
	var fi fieldInfo
	// "intern" in the json tag is rejected there and pointed at the
	// zerojson tag instead.
	if err := g.parseJSONOptions(&fi, []string{"omitempty", "intern"}, "T.F"); err == nil {
		t.Fatal("expected error for unknown json tag option \"intern\"")
	} else if !containsAll(err.Error(), "T.F", "intern", "zerojson") {
		t.Fatalf("error should name the field, the bad option, and point at the zerojson tag: %v", err)
	}
}

func TestParseZerojsonTagHonorsIntern(t *testing.T) {
	g := &generator{}
	var fi fieldInfo
	if err := g.parseZerojsonTag(&fi, "intern", "T.F"); err != nil {
		t.Fatal(err)
	}
	if !fi.intern {
		t.Fatal("intern not recorded")
	}
}

func TestParseZerojsonTagRejectsUnknown(t *testing.T) {
	g := &generator{}
	var fi fieldInfo
	if err := g.parseZerojsonTag(&fi, "borrow", "T.F"); err == nil {
		t.Fatal("expected error for unknown zerojson tag option")
	} else if !containsAll(err.Error(), "T.F", "borrow") {
		t.Fatalf("error should name the field and the bad option: %v", err)
	}
}

// TestValidateFieldOptionsInternKinds checks the field kinds zerojson:"intern"
// is accepted on: string, named-string enum, and map (where it interns the
// keys). Everything else is rejected.
func TestValidateFieldOptionsInternKinds(t *testing.T) {
	g := &generator{}
	for _, k := range []kind{kString, kEnum, kMap} {
		if err := g.validateFieldOptions(fieldInfo{intern: true, kind: k}, "T.F"); err != nil {
			t.Errorf("intern should be allowed on kind %v: %v", k, err)
		}
	}
	if err := g.validateFieldOptions(fieldInfo{intern: true, kind: kInt}, "T.F"); err == nil {
		t.Error("intern should be rejected on a non-string/enum/map field")
	}
}

func TestParseZerojsonTagEmptyIsNoop(t *testing.T) {
	g := &generator{}
	var fi fieldInfo
	if err := g.parseZerojsonTag(&fi, "", "T.F"); err != nil {
		t.Fatalf("empty zerojson tag should be a no-op: %v", err)
	}
	if fi.intern {
		t.Fatal("intern should not be set")
	}
}

func TestValidateFieldOptionsOmitzero(t *testing.T) {
	tests := []struct {
		name    string
		fi      fieldInfo
		wantErr bool
	}{
		{"int always representable", fieldInfo{omitzero: true, kind: kInt}, false},
		{"string always representable", fieldInfo{omitzero: true, kind: kString}, false},
		{"slice always representable (nil check)", fieldInfo{omitzero: true, kind: kSlice}, false},
		{"byte slice always representable (nil check)", fieldInfo{omitzero: true, kind: kByteSlice}, false},
		{"uuid without IsZero falls back to comparable check", fieldInfo{omitzero: true, kind: kUUID, typeStr: "uuid.UUID", hasIsZero: false}, false},
		{"time has IsZero", fieldInfo{omitzero: true, kind: kTime, hasIsZero: true}, false},
		{"custom leaf (decimal) has IsZero", fieldInfo{omitzero: true, kind: kCustomLeaf, hasIsZero: true}, false},
		{"struct without IsZero errors", fieldInfo{omitzero: true, kind: kStruct, typeStr: "Foo", hasIsZero: false}, true},
		{"struct with IsZero is fine", fieldInfo{omitzero: true, kind: kStruct, typeStr: "Foo", hasIsZero: true}, false},
		{"marshaler without IsZero errors", fieldInfo{omitzero: true, kind: kMarshaler, typeStr: "Foo", hasIsZero: false}, true},
		{"slice-shaped marshaler needs no IsZero (nil check)", fieldInfo{omitzero: true, kind: kMarshaler, marshalerIsSlice: true, hasIsZero: false}, false},
		{"map-shaped marshaler needs no IsZero (nil check)", fieldInfo{omitzero: true, kind: kMarshaler, marshalerIsMap: true, hasIsZero: false}, false},
		{"pointer is always representable regardless of kind", fieldInfo{omitzero: true, ptr: true, kind: kStruct, hasIsZero: false}, false},
	}
	g := &generator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.validateFieldOptions(tt.fi, "T.F")
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestValidateFieldOptionsString(t *testing.T) {
	tests := []struct {
		name    string
		kind    kind
		wantErr bool
	}{
		{"int supported", kInt, false},
		{"uint supported", kUint, false},
		{"float not yet supported", kFloat64, true},
		{"bool not yet supported", kBool, true},
		{"already-quoted type redundant", kQuotedInt, true},
	}
	g := &generator{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := g.validateFieldOptions(fieldInfo{stringOpt: true, kind: tt.kind, typeStr: "T"}, "T.F")
			if tt.wantErr && err == nil {
				t.Fatal("expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
