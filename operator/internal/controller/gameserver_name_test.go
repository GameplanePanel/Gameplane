package controller

import (
	"strings"
	"testing"
)

func TestValidateServerName(t *testing.T) {
	valid := []string{"sdtd", "mc-1", "seven-days", strings.Repeat("a", 57)}
	for _, name := range valid {
		if err := validateServerName(name); err != nil {
			t.Errorf("validateServerName(%q) = %v, want nil", name, err)
		}
	}
	invalid := []string{"7d2d", "1", "my.server", "-mc", strings.Repeat("a", 58)}
	for _, name := range invalid {
		if err := validateServerName(name); err == nil {
			t.Errorf("validateServerName(%q) = nil, want an error", name)
		}
	}
}
