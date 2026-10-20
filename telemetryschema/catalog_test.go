package telemetryschema

import (
	"reflect"
	"regexp"
	"testing"
)

// TestIsOfficial checks catalog membership: exact, case-sensitive names only.
func TestIsOfficial(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"minecraft-java", "terraria", "valheim", "7-days-to-die", "v-rising", "cs2"} {
		if !IsOfficial(name) {
			t.Errorf("IsOfficial(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "custom", "other", "Minecraft-Java", "minecraft-java ", " terraria", "minecraft", "minecraft-java2", "my-private-game"} {
		if IsOfficial(name) {
			t.Errorf("IsOfficial(%q) = true, want false", name)
		}
	}
}

// TestCatalog checks the embedded list is well formed and that Catalog hands
// out a copy.
func TestCatalog(t *testing.T) {
	t.Parallel()
	got := Catalog()
	if len(got) == 0 {
		t.Fatal("catalog is empty")
	}
	name := regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
	seen := map[string]bool{}
	for _, n := range got {
		if !name.MatchString(n) {
			t.Errorf("catalog entry %q is not a module name", n)
		}
		if seen[n] {
			t.Errorf("catalog entry %q is duplicated", n)
		}
		seen[n] = true
		if !IsOfficial(n) {
			t.Errorf("IsOfficial(%q) = false for a catalog entry", n)
		}
	}
	for _, n := range []string{"minecraft-java", "terraria", "valheim"} {
		if !seen[n] {
			t.Errorf("catalog is missing %q", n)
		}
	}

	got[0] = "tampered"
	if IsOfficial("tampered") || Catalog()[0] == "tampered" {
		t.Fatal("mutating the result of Catalog changed the catalog")
	}
}

// TestParseCatalog covers blank lines, surrounding whitespace and CRLF input.
func TestParseCatalog(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"empty", "", nil},
		{"only blanks", "\n \n\t\n", nil},
		{"simple", "a\nb\n", []string{"a", "b"}},
		{"no trailing newline", "a\nb", []string{"a", "b"}},
		{"blank lines and spaces", "\n a \n\n\tb\t\n", []string{"a", "b"}},
		{"crlf", "a\r\nb\r\n", []string{"a", "b"}},
	}
	for _, tc := range cases {
		if got := parseCatalog(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: parseCatalog(%q) = %#v, want %#v", tc.name, tc.in, got, tc.want)
		}
	}
}
