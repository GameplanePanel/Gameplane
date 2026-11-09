package controller

import (
	"errors"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// ValidateNodeDiagnosticsNamespace applies the same namespace boundary at
// startup and reconciliation. Empty diagnostics disables the deployment opt-in;
// an enabled diagnostic namespace requires explicit, bounded protected names.
func ValidateNodeDiagnosticsNamespace(namespace string, protected []string) error {
	if failure := nodeDiagnosticsNamespaceFailure(namespace, protected); failure != "" {
		return errors.New(failure)
	}
	return nil
}

// Reconciliation reports an invalid deployment scope as a terminal capture
// failure. Startup reports the same policy violation as a configuration error.
func nodeDiagnosticsNamespaceFailure(namespace string, protected []string) string {
	if namespace == "" {
		return ""
	}
	if problems := validation.IsDNS1123Label(namespace); len(problems) != 0 {
		return fmt.Sprintf("node diagnostics namespace must be an explicit DNS-label namespace: %s", strings.Join(problems, "; "))
	}
	if len(protected) == 0 {
		return "node diagnostics require an explicit nonempty protected namespace set"
	}
	for _, name := range protected {
		if problems := validation.IsDNS1123Label(name); len(problems) != 0 {
			return fmt.Sprintf("protected namespace %q must be an explicit DNS-label namespace: %s", name, strings.Join(problems, "; "))
		}
		if namespace == name {
			return "node diagnostics namespace must be separate from all game and control-plane namespaces"
		}
	}
	return ""
}
