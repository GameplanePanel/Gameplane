package validator

import (
	"fmt"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestCheckpointDeclarationMatchesAvailableAdapter(t *testing.T) {
	for _, tc := range []struct {
		game, adapter, extra, rcon string
		valid                      bool
	}{
		{"minecraft-java", "minecraft-save-all", "", "  rcon: {protocol: source}\n", true},
		{"minecraft-java", "minecraft-save-all", "", "  rcon: {}\n", true},
		{"minecraft-java", "minecraft-save-all", "", "  rcon: {protocol: none}\n", false},
		{"minecraft-java", "minecraft-save-all", "", "", false},
		{"ark", "minecraft-save-all", "", "  rcon: {}\n", false},
		{"minecraft-java", "unqualified-command", "", "  rcon: {}\n", false},
		{"minecraft-java", "minecraft-save-all", ", quiesce: {}", "  rcon: {}\n", false},
	} {
		var document yaml.Node
		input := fmt.Sprintf("spec:\n  game: %s\n%s  capabilities: {backupFlush: %s%s}\n", tc.game, tc.rcon, tc.adapter, tc.extra)
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
