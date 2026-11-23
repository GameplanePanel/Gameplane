package controller

import (
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation"
)

// validateServerName checks that a GameServer name can name its Services.
// The operator creates Services "<name>" and "<name>-agent", and a Service
// name must be a DNS-1035 label: a lowercase letter first, then lowercase
// letters, digits and dashes, at most 63 characters. The apiserver accepts
// any DNS-1123 subdomain as a GameServer name (for example "7d2d"), so
// without this check the Service create fails on every pass.
func validateServerName(name string) error {
	if len(validation.IsDNS1035Label(name+"-agent")) == 0 {
		return nil
	}
	return fmt.Errorf("server name %q cannot be used: Kubernetes Service names must start with a lowercase letter, contain only lowercase letters, digits and dashes, and the server's %q Service must fit in 63 characters; delete this server and create it again with a name that starts with a letter and has at most 57 characters", name, name+"-agent")
}
