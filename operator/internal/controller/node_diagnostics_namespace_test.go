package controller

import "testing"

func TestNodeDiagnosticsNamespaceRequiresExplicitSeparateScope(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		diagnostic string
		protected  []string
		wantError  bool
	}{
		{"disabled", "", nil, false},
		{"separate", "diagnostics", []string{"games", "games-other", "control", "ca", "modules", "operator-pod"}, false},
		{"duplicate-protected", "diagnostics", []string{"games", "control", "control"}, false},
		{"other-game-namespace", "games-other", []string{"games", "games-other", "control"}, true},
		{"control-plane", "control", []string{"games", "control"}, true},
		{"agent-ca", "ca", []string{"games", "control", "ca"}, true},
		{"module-storage", "modules", []string{"games", "control", "modules"}, true},
		{"operator-deployment", "operator-pod", []string{"games", "control", "operator-pod"}, true},
		{"no-protected-scope", "diagnostics", nil, true},
		{"empty-protected-scope", "diagnostics", []string{}, true},
		{"all-namespaces", "diagnostics", []string{""}, true},
		{"wildcard", "diagnostics", []string{"games", "*"}, true},
		{"empty-entry", "diagnostics", []string{"games", ""}, true},
		{"unparsed-list", "diagnostics", []string{"games,other"}, true},
		{"whitespace", "diagnostics", []string{" games"}, true},
		{"wildcard-diagnostics", "*", []string{"games"}, true},
		{"malformed-diagnostics", "diag/other", []string{"games"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := ValidateNodeDiagnosticsNamespace(test.diagnostic, test.protected); (err != nil) != test.wantError {
				t.Fatalf("namespace validation error = %v, want error %t", err, test.wantError)
			}
		})
	}
}
