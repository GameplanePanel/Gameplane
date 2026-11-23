package main

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

type templateHostnameMap map[string]string

func (hosts templateHostnameMap) set(value string) error {
	template, hostname, ok := strings.Cut(value, "=")
	if !ok || len(validation.IsDNS1123Subdomain(template)) != 0 {
		return fmt.Errorf("expected <template>=<hostname> with a valid GameTemplate name, got %q", value)
	}
	if _, exists := hosts[template]; exists {
		return fmt.Errorf("template %q already has a default hostname", template)
	}
	// Match the length and DNS-label rules for spec.networking.hostname.
	if len(hostname) > validation.DNS1123SubdomainMaxLength {
		return fmt.Errorf("hostname for template %q exceeds 253 characters", template)
	}
	for label := range strings.SplitSeq(hostname, ".") {
		if problems := validation.IsDNS1123Label(label); len(problems) != 0 {
			return fmt.Errorf("invalid hostname %q for template %q: %s", hostname, template, strings.Join(problems, "; "))
		}
	}
	hosts[template] = hostname
	return nil
}
