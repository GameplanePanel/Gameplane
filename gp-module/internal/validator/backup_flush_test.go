package validator

import (
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCheckpointDeclarationMatchesAvailableAdapter(t *testing.T) {
	for _, tc := range []struct {
		game, adapter, extra string
		valid                bool
	}{
		{"minecraft-java", "minecraft-save-all", "", true},
		{"ark", "minecraft-save-all", "", false},
		{"minecraft-java", "unqualified-command", "", false},
		{"minecraft-java", "minecraft-save-all", ", quiesce: {}", false},
	} {
		var document yaml.Node
		input := fmt.Sprintf("spec:\n  game: %s\n  capabilities: {backupFlush: %s%s}\n", tc.game, tc.adapter, tc.extra)
		if err := yaml.Unmarshal([]byte(input), &document); err != nil {
			t.Fatal(err)
		}
		valid := true
		for _, finding := range validateTemplateSchema(&document) {
			if finding.Field == "spec.capabilities.backupFlush" {
				valid = false
			}
		}
		if valid != tc.valid {
			t.Errorf("checkpoint %s: accepted=%t", input, valid)
		}
	}
}
