package main

// Compare JSON values independently of map order, escaping and equivalent
// number spellings. Keeping decimal coefficients exact avoids float64's loss
// of precision; keeping exponents symbolic avoids expanding enormous powers.
const jsonValueOracleSource = `
func zjAssertJSONEqual(t *testing.T, expected, actual []byte) {
 t.Helper()
 decode := func(b []byte) any {
  d := json.NewDecoder(bytes.NewReader(b)); d.UseNumber()
  var v any
  if err := d.Decode(&v); err != nil { t.Fatalf("encode semantic oracle: invalid JSON %q: %v", b, err) }
  if !json.Valid(b) { t.Fatalf("encode semantic oracle: invalid JSON %q", b) }
  return v
 }
 if !zjJSONValueEqual(decode(expected), decode(actual)) {
  t.Fatalf("encode value divergence\nexpected: %s\nactual: %s", expected, actual)
 }
}

func zjJSONValueEqual(a, b any) bool {
 switch x := a.(type) {
 case nil: return b == nil
 case bool: y, ok := b.(bool); return ok && x == y
 case string: y, ok := b.(string); return ok && x == y
 case json.Number:
  y, ok := b.(json.Number)
  if !ok { return false }
  xc, xe := zjJSONNumberParts(string(x)); yc, ye := zjJSONNumberParts(string(y))
  return xc == yc && xe.Cmp(ye) == 0
 case []any:
  y, ok := b.([]any); if !ok || len(x) != len(y) { return false }
  for i := range x { if !zjJSONValueEqual(x[i],y[i]) { return false } }; return true
 case map[string]any:
  y, ok := b.(map[string]any); if !ok || len(x) != len(y) { return false }
  for k, v := range x { w, ok := y[k]; if !ok || !zjJSONValueEqual(v,w) { return false } }; return true
 }
 return false
}

func zjJSONNumberParts(s string) (string, *big.Int) {
 exponent := new(big.Int)
 if i := strings.IndexAny(s,"eE"); i >= 0 { exponent.SetString(s[i+1:],10); s=s[:i] }
 negative := strings.HasPrefix(s,"-"); s=strings.TrimPrefix(s,"-")
 if i := strings.IndexByte(s,'.'); i>=0 {
  exponent.Sub(exponent,big.NewInt(int64(len(s)-i-1))); s=s[:i]+s[i+1:]
 }
 s=strings.TrimLeft(s,"0")
 if s=="" { return "0",new(big.Int) }
 trimmed:=strings.TrimRight(s,"0")
 exponent.Add(exponent,big.NewInt(int64(len(s)-len(trimmed))))
 if negative { trimmed="-"+trimmed }
 return trimmed,exponent
}

`
