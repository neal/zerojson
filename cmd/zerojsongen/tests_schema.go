package main

import (
	"fmt"
	"strings"
)

// The oracle sees the same exact-name members as zerojson, while zerojson
// still reads the original bytes. Keep dictionaries and opaque codecs intact.
func (g *generator) oracleShape(f fieldInfo) string {
	switch f.kind {
	case kStruct:
		return "zjOracleShape" + f.elem
	case kSlice:
		return "&zjOracleShape{elem: zjOracleShape" + f.elem + "}"
	case kLeafSlice, kMap:
		if f.valInfo != nil {
			return fmt.Sprintf("&zjOracleShape{object: %v, elem: %s}", f.kind == kMap, g.oracleShape(*f.valInfo))
		}
	}
	return "nil"
}

func (g *generator) emitOracleShapes(b *strings.Builder) {
	b.WriteString(oracleProjectionSource)
	for _, ti := range g.types {
		if ti.rootField != nil {
			fmt.Fprintf(b, "var zjOracleShape%s = %s\n", ti.name, g.oracleShape(*ti.rootField))
			continue
		}
		fmt.Fprintf(b, "var zjOracleShape%s = &zjOracleShape{object: true, fields: map[string]*zjOracleShape{\n", ti.name)
		for _, f := range ti.fields {
			fmt.Fprintf(b, "%q: %s,\n", f.jsonName, g.oracleShape(f))
		}
		b.WriteString("}}\n")
	}
}

const oracleProjectionSource = `
// Only struct members that stdlib would match by case folding are removed.
// Exact members (including duplicates), unknown values, dictionary keys and
// custom codecs retain their semantics. This is not a blanket exemption:
// unrelated value errors and all comparable exact fields remain checked.
type zjOracleShape struct {
 object bool
 fields map[string]*zjOracleShape
 elem *zjOracleShape
}

func zjOracleData(data []byte, shape *zjOracleShape) []byte {
 if !json.Valid(data) { return data }
 projected, err := zjProjectOracle(data, shape)
 if err != nil { return data }
 return projected
}

func zjProjectOracle(data []byte, shape *zjOracleShape) ([]byte, error) {
 if shape == nil { return data, nil }
 trimmed := bytes.TrimSpace(data)
 open, close := byte('['), byte(']')
 if shape.object { open, close = '{', '}' }
 if len(trimmed) == 0 || trimmed[0] != open { return data, nil }
 dec := json.NewDecoder(bytes.NewReader(trimmed))
 if _, err := dec.Token(); err != nil { return nil, err }
 out := []byte{open}
 first := true
 for dec.More() {
  var key string
  child := shape.elem
  drop := false
  if shape.object {
   token, err := dec.Token(); if err != nil { return nil, err }
   key = token.(string)
   if shape.fields != nil {
    var exact bool
    child, exact = shape.fields[key]
    if !exact {
     for name := range shape.fields {
      if strings.EqualFold(name, key) { drop = true; break }
     }
    }
   }
  }
  var raw json.RawMessage
  if err := dec.Decode(&raw); err != nil { return nil, err }
  if drop { continue }
  value, err := zjProjectOracle(raw, child); if err != nil { return nil, err }
  if !first { out = append(out, ',') }; first = false
  if shape.object {
   name, err := json.Marshal(key); if err != nil { return nil, err }
   out = append(out, name...); out = append(out, ':')
  }
  out = append(out, value...)
 }
 if _, err := dec.Token(); err != nil { return nil, err }
 return append(out, close), nil
}

`
