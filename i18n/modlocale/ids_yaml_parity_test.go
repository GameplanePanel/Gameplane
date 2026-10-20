package modlocale

import (
	"encoding/json"
	"reflect"
	"testing"

	"gopkg.in/yaml.v3"
)

// The operator decodes YAML through a JSON round trip (sigs.k8s.io/yaml) while
// gp-module and i18n-check use yaml.v3 directly. Derive must see the same ids
// either way.
func TestDeriveYAMLDecoderParity(t *testing.T) {
	t.Parallel()
	const moduleYAML = "displayName: Minecraft\nsummary: Java edition\n"
	const templateYAML = `spec:
  displayName: Minecraft
  versions:
    - id: "1.21"
      displayName: "1.21 latest"
  configSchema:
    - name: MAX_PLAYERS
      displayName: Max players
      description: How many
    - name: MODE
      enum: [survival, creative]
`
	decode := func(src string) map[string]any {
		var m map[string]any
		if err := yaml.Unmarshal([]byte(src), &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	roundTrip := func(m map[string]any) map[string]any {
		raw, err := json.Marshal(m)
		if err != nil {
			t.Fatal(err)
		}
		var out map[string]any
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	mod, tmpl := decode(moduleYAML), decode(templateYAML)
	direct := Derive(mod, tmpl)
	viaJSON := Derive(roundTrip(mod), roundTrip(tmpl))
	if len(direct) == 0 {
		t.Fatal("fixture derived no ids")
	}
	if !reflect.DeepEqual(direct, viaJSON) {
		t.Fatalf("decoder mismatch:\n%v\n%v", direct, viaJSON)
	}
}
