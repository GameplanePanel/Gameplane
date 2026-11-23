package handlers

import (
	"strings"
	"testing"
)

func TestValidServerName(t *testing.T) {
	for _, name := range []string{"sdtd", "mc-1", strings.Repeat("a", 57)} {
		if !validServerName(name) {
			t.Errorf("validServerName(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "7d2d", "my.server", "MC", strings.Repeat("a", 58)} {
		if validServerName(name) {
			t.Errorf("validServerName(%q) = true, want false", name)
		}
	}
}
