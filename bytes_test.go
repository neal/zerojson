package zerojson

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestReadBytesBase64MatchesEncodingJSONForms(t *testing.T) {
	t.Parallel()
	cases := []string{
		`"AQD/"`, `""`, `[]`, `[0,1,255]`, `[null,2]`, `null`,
		`[256]`, `[-1]`, `[1.5]`, `[1E700]`, `["1"]`, `[true]`, `[1,]`,
		`"not base64"`, `{}`, ` [ 0 , 255 ] `,
	}
	for _, input := range cases {
		input := input
		t.Run(input, func(t *testing.T) {
			var want []byte
			stdErr := json.Unmarshal([]byte(input), &want)

			l := NewLexer([]byte(input), false)
			var got []byte
			if !l.IsNull() {
				got = l.ReadBytesBase64()
			}
			l.Finish()
			if (l.Err() != nil) != (stdErr != nil) {
				t.Fatalf("error mismatch: zerojson=%v encoding/json=%v", l.Err(), stdErr)
			}
			if stdErr == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("value mismatch: zerojson=%#v encoding/json=%#v", got, want)
			}
		})
	}
}
