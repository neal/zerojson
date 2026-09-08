package generator

import (
	_ "embed"
	"fmt"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

//go:embed testdata/schema_campaign/publication_test.go
var publicationCampaignTests string

func TestPublicationCampaignSource(t *testing.T) {
	a, at, names := publicationCampaignSource(7)
	b, bt, _ := publicationCampaignSource(7)
	c, _, _ := publicationCampaignSource(8)
	if a != b || at != bt || a == c || len(names) != 6 {
		t.Fatal("publication source is not reproducibly varied")
	}
	for _, src := range []string{a, at, publicationCampaignTests} {
		if _, err := parser.ParseFile(token.NewFileSet(), "campaign.go", src, parser.AllErrors); err != nil {
			t.Fatal(err)
		}
	}
}

// Seeded source variation is separate from the fuzzed operation stream. Every
// campaign includes plain, JSON and Text leaves, direct and nested payloads;
// names and field order change with the seed. Keep compilation cost bounded.
func publicationCampaignSource(seed uint64) (string, string, []string) {
	rng := schemaRNG(seed ^ 0x7075626c69636174)
	var src, tests strings.Builder
	src.WriteString(`package p
import "encoding/json"
var publicationCallback func(int) error
var publicationReceiver func(any)
type PublicationHook int
func(PublicationHook) MarshalJSON()([]byte,error){return []byte("0"),nil}
func(*PublicationHook) UnmarshalJSON(b []byte)error{var n int;if err:=json.Unmarshal(b,&n);err!=nil{return err};if publicationCallback!=nil{return publicationCallback(n)};return nil}
`)
	tests.WriteString("package p\nimport \"testing\"\nvar publicationRunners=[]func(*testing.T,[]byte){\n")
	var names []string
	for i := 0; i < 6; i++ {
		name, leaf := fmt.Sprintf("Publication%d", i), fmt.Sprintf("PublicationLeaf%d", i)
		key := fmt.Sprintf("value_%x", rng.next()&0xffff)
		nested := i%2 != 0
		method := i % 3
		fmt.Fprintf(&src, "type %s int\n", leaf)
		switch method {
		case 1:
			fmt.Fprintf(&src, `func(v %s) MarshalJSON()([]byte,error){return json.Marshal(int(v))}
func(v *%s) UnmarshalJSON(b []byte)error{if publicationReceiver!=nil{publicationReceiver(v)};var n int;if err:=json.Unmarshal(b,&n);err!=nil{return err};*v=%s(n);return nil}
`, leaf, leaf, leaf)
		case 2:
			fmt.Fprintf(&src, `func(v %s) MarshalText()([]byte,error){return json.Marshal(int(v))}
func(v *%s) UnmarshalText(b []byte)error{if publicationReceiver!=nil{publicationReceiver(v)};var n int;if err:=json.Unmarshal(b,&n);err!=nil{return err};*v=%s(n);return nil}
`, leaf, leaf, leaf)
		}
		fields := []string{fmt.Sprintf("P *%s `json:\"p\"`", leaf), fmt.Sprintf("Q *%s `json:\"q\"`", leaf), fmt.Sprintf("M map[string]%s `json:\"m\"`", leaf), fmt.Sprintf("N map[string]%s `json:\"n\"`", leaf), fmt.Sprintf("S []%s `json:\"s\"`", leaf), fmt.Sprintf("T []%s `json:\"t\"`", leaf)}
		for j := len(fields) - 1; j > 0; j-- {
			k := int(rng.next() % uint64(j+1))
			fields[j], fields[k] = fields[k], fields[j]
		}
		payload := strings.Join(fields, ";")
		access := ""
		if nested {
			fmt.Fprintf(&src, "type %sPayload struct {%s}\n", name, payload)
			payload = fmt.Sprintf("Data %sPayload `json:%q`", name, key)
			access = "Data"
		}
		fmt.Fprintf(&src, "type %s struct {%s; Hook PublicationHook `json:\"hook\"`}\n", name, payload)
		fmt.Fprintf(&tests, `func(t *testing.T,p []byte){var d %sDecoder;runPublicationCampaign[%s](t,p,%q,%q,%v,func(b []byte,v *%s,pooled,borrow bool)error{if pooled{if borrow{return d.DecodeBorrow(b,v)};return d.Decode(b,v)};if borrow{return v.UnmarshalZJSONBorrow(b)};return v.UnmarshalZJSON(b)})},
`, name, name, access, key, method == 2, name)
		names = append(names, name)
	}
	tests.WriteString("}\n")
	return src.String(), tests.String(), names
}
