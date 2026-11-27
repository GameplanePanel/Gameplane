package handlers

import (
	"testing"

	"sigs.k8s.io/yaml"
)

func TestYamlNameRoundTrips(t *testing.T) {
	for _, name := range []string{"alice", "alice@example.com", "Alice: admin", "yes", "NULL", "123", "-x", "a\"b\\c", "#tag", "名前"} {
		var got struct {
			User string `json:"user"`
		}
		if err := yaml.Unmarshal([]byte("user: "+yamlName(name)+"\n"), &got); err != nil {
			t.Fatalf("%q: %v", name, err)
		}
		if got.User != name {
			t.Fatalf("%q round-tripped as %q", name, got.User)
		}
	}
	if yamlName("alice") != "alice" {
		t.Fatalf("plain name was quoted: %s", yamlName("alice"))
	}
}
