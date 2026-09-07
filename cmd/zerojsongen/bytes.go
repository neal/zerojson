package main

import "fmt"

// Go permits []byte <-> a named []byte, but not []byte <-> []DefinedByte.
// Only that uncommon element shape needs a copying conversion. Keep the
// usual []byte path unchanged, and do not introduce unsafe reinterpretation.
func byteSliceEncodeExpr(f fieldInfo, expr string) string {
	if f.byteElem == "" {
		return fmt.Sprintf("[]byte(%s)", expr)
	}
	return fmt.Sprintf("func(src %s) []byte { if src == nil { return nil }; out := make([]byte, len(src)); for i, b := range src { out[i] = byte(b) }; return out }(%s)", f.typeStr, expr)
}

func byteSliceReadExpr(f fieldInfo) string {
	if f.byteElem == "" {
		return fmt.Sprintf("%s(l.ReadBytesBase64())", f.typeStr)
	}
	return fmt.Sprintf("func() %s { src := l.ReadBytesBase64(); if src == nil { return nil }; out := make(%s, len(src)); for i, b := range src { out[i] = %s(b) }; return out }()", f.typeStr, f.typeStr, f.byteElem)
}
