// Package modsrc abstracts where module bundles come from. A Fetcher
// indexes one ModuleSource into catalog entries and pulls individual
// bundles; the OCI registry implementation lives here today, with git,
// http, local-dir, and upload sources to follow. The Module and
// ModuleSource controllers depend only on this package — never on a
// transport directly.
package modsrc

import (
	"errors"
	"fmt"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
	"sigs.k8s.io/yaml"
)

// Canonical file names inside a module bundle, regardless of transport:
// OCI layer title annotations, ConfigMap binaryData keys, and files in a
// git/local/archive module directory all use these names. The full
// format is specified in docs/module-authoring.md.
const (
	FileMetadata = "module.yaml"
	FileTemplate = "template.yaml"
	FileReadme   = "README.md"
	FileIcon     = "icon.png"
)

// Metadata mirrors the on-disk module.yaml schema documented in
// docs/module-authoring.md. Fields not listed here are ignored.
type Metadata struct {
	APIVersion  string   `yaml:"apiVersion" json:"apiVersion"`
	Name        string   `yaml:"name" json:"name"`
	DisplayName string   `yaml:"displayName" json:"displayName"`
	Version     string   `yaml:"version" json:"version"`
	Game        string   `yaml:"game" json:"game"`
	Categories  []string `yaml:"categories,omitempty" json:"categories,omitempty"`
	// Category is the legacy singular form, accepted for back-compat with
	// bundles authored before categories became a list. normalizeCategories
	// folds it into Categories and clears it; nothing else reads it.
	Category            string `yaml:"category,omitempty" json:"category,omitempty"`
	Summary             string `yaml:"summary,omitempty" json:"summary,omitempty"`
	Homepage            string `yaml:"homepage,omitempty" json:"homepage,omitempty"`
	License             string `yaml:"license,omitempty" json:"license,omitempty"`
	GameplaneMinVersion string `yaml:"gameplaneMinVersion,omitempty" json:"gameplaneMinVersion,omitempty"`
	Icon                string `yaml:"icon,omitempty" json:"icon,omitempty"`
}

// Bundle is the parsed contents of one module, addressed by a digest
// whose shape depends on the source: OCI manifest digest, git commit
// ("git:<sha>"), or a content hash over the module directory.
type Bundle struct {
	// Digest identifies this exact bundle content. Stamped onto the
	// resulting GameTemplate so a Module reconcile can detect drift even
	// when the version string is reused.
	Digest string

	// Metadata is the parsed module.yaml.
	Metadata Metadata

	// TemplateYAML is the raw GameTemplate manifest YAML (without
	// metadata.name set). The reconciler decodes this into a v1alpha1
	// GameTemplate when materializing the install.
	TemplateYAML []byte

	// Readme is the raw README.md bytes, or nil when the bundle did not
	// ship one.
	Readme []byte

	// Icon is the raw icon bytes when the bundle ships an icon file.
	// Currently surfaced via the API by base64-encoding into a data
	// URI, sized down by the caller.
	Icon []byte

	// Locales holds the module's translation files, keyed by language tag
	// (from "locales/<tag>.json" layers). It is nil for bundles that carry
	// none. The content is unvalidated; the Module reconciler checks it
	// against the ids derived from the template.
	Locales map[string][]byte

	// OversizedLocales lists, sorted, the tags whose locale file was larger
	// than oci.MaxLocaleLayerSize, or did not fit oci.MaxLocaleTotalSize, and
	// was therefore not read. Locales never
	// holds them; the reconciler reports them as LocalesTooLarge.
	OversizedLocales []string
}

// FromFiles assembles and validates a Bundle from its files, keyed by
// the canonical File* names. Unknown keys are ignored so sources can
// carry extra files without breaking older operators.
func FromFiles(digest string, files map[string][]byte) (*Bundle, error) {
	b := &Bundle{Digest: digest}
	meta, ok := files[FileMetadata]
	if !ok {
		return nil, errors.New("bundle missing module.yaml metadata")
	}
	if err := yaml.Unmarshal(meta, &b.Metadata); err != nil {
		return nil, fmt.Errorf("parse module.yaml: %w", err)
	}
	b.Metadata.normalizeCategories()
	if b.Metadata.Name == "" {
		return nil, errors.New("module.yaml missing required field: name")
	}
	tmpl := files[FileTemplate]
	if len(tmpl) == 0 {
		return nil, errors.New("bundle missing template.yaml")
	}
	b.TemplateYAML = tmpl
	b.Readme = files[FileReadme]
	b.Icon = files[FileIcon]
	locales, oversized := oci.SplitLocaleLayers(files)
	if len(locales) > 0 {
		b.Locales = locales
	}
	b.OversizedLocales = oversized
	return b, nil
}

// normalizeCategories folds the legacy scalar `category:` into Categories.
// An explicit `categories:` list always wins; the scalar is cleared either
// way so no caller can read a stale value.
func (m *Metadata) normalizeCategories() {
	if len(m.Categories) == 0 && m.Category != "" {
		m.Categories = []string{m.Category}
	}
	m.Category = ""
}
