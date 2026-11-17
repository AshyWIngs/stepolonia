package content

import (
	"encoding/json"
	"testing"
)

func TestPublicProjectionCodecs(t *testing.T) {
	metadata := []byte(`{"price":{"kind":"decimal","scope":"shared"},"count":{"kind":"integer","scope":"shared"},"featured":{"kind":"boolean","scope":"shared"}}`)
	values, err := tagPublicValues([]byte(`{"price":"123456789012.123456","count":"9223372036854775807","featured":false}`), metadata, "shared")
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]struct {
		Kind  string
		Value json.RawMessage
	}
	if err = json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if string(decoded["price"].Value) != `"123456789012.123456"` || string(decoded["count"].Value) != `"9223372036854775807"` || string(decoded["featured"].Value) != "false" {
		t.Fatalf("lossy tagged values: %s", encoded)
	}
	for _, input := range []string{`{"private":"secret"}`, `{"featured":"false"}`, `{"featured":null}`, `{"price":123}`, `{"count":null}`} {
		if _, err = tagPublicValues([]byte(input), metadata, "shared"); err == nil {
			t.Fatalf("unsafe projection accepted: %s", input)
		}
	}
	if _, err = tagPublicValues([]byte(`{"price":"1"}`), metadata, "localized"); err == nil {
		t.Fatal("wrong scope accepted")
	}
}
