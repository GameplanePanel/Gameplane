package controller

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ValidateNodeDiagnosticsNamespace applies the same namespace boundary at
// startup and reconciliation. Empty diagnostics disables the deployment opt-in;
// an enabled diagnostic namespace requires explicit, bounded protected names.
func ValidateNodeDiagnosticsNamespace(namespace string, protected []string) error {
	if namespace == "" {
		return nil
	}
	if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
		return fmt.Errorf("node diagnostics namespace must be an explicit DNS-label namespace: %s", strings.Join(problems, "; "))
	}
	if len(protected) == 0 {
		return fmt.Errorf("node diagnostics require an explicit nonempty protected namespace set")
	}
	for _, name := range protected {
		if problems := validation.IsDNS1123Label(name); len(problems) != 0 {
			return fmt.Errorf("protected namespace %q must be an explicit DNS-label namespace: %s", name, strings.Join(problems, "; "))
		}
		if namespace == name {
			return fmt.Errorf("node diagnostics namespace must be separate from all game and control-plane namespaces")
		}
	}
	return nil
}
