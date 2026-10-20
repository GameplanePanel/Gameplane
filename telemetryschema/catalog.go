package telemetryschema

import (
	_ "embed"
	"slices"
	"strings"
)

// catalogText is catalog.txt, generated from charts/gameplane/values.yaml
// defaultModuleSource.oci.modules; hack/check-telemetry-catalog.sh fails when
// the two differ.
//
//go:embed catalog.txt
var catalogText string

// officialModules is catalogText split into names, in file order.
var officialModules = parseCatalog(catalogText)

// parseCatalog splits catalog text into module names, skipping blank lines.
func parseCatalog(text string) []string {
	var names []string
	for _, line := range strings.Split(text, "\n") {
		if name := strings.TrimSpace(line); name != "" {
			names = append(names, name)
		}
	}
	return names
}

// IsOfficial reports whether name is an official module in the embedded
// catalog.
func IsOfficial(name string) bool {
	return slices.Contains(officialModules, name)
}

// Catalog returns a copy of the official module names, in catalog order.
func Catalog() []string {
	return slices.Clone(officialModules)
}
