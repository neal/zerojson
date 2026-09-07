// easyjson_oracle.go defines EventEJ, a type with the exact same
// underlying struct layout as Event (same fields, same tags, same field
// types — so every field-type-level custom Marshaler/TextMarshaler
// (Tags, IDList, StrInt64, YMD) is picked up identically), annotated for
// real easyjson code generation. This gives the value-space fuzzer
// (value_fuzz_test.go) a genuine, independently generated easyjson
// oracle for Event's shape, rather than relying only on encoding/json
// (which, per this project's contract, is the decode oracle — encode's
// primary contract is byte-identity with easyjson specifically).
package codec

//go:generate go run github.com/mailru/easyjson/easyjson -all easyjson_oracle.go

//easyjson:json
type EventEJ Event

// EmbedMidEJ is EmbedMid's easyjson-generated twin (the type itself is
// declared in types.go — deliberately NOT in this file, because the
// -all flag above generates methods for every struct in this file, and
// giving EmbedMid itself an easyjson MarshalJSON would hijack
// embed_order_test.go's json.Marshal oracle call). See EmbedMid's doc
// for the promoted-field-ordering divergence this pair pins.
//
//easyjson:json
type EmbedMidEJ EmbedMid
