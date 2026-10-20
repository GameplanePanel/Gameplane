// Package preview provides public preview exports.
package preview

import (
	internalprev "github.com/GameplanePanel/gameplane/gp-module/internal/preview"
)

// Options defines inputs for generating a dry-run preview.
type Options = internalprev.Options

// PortPreview represents an exposed network port.
type PortPreview = internalprev.PortPreview

// StoragePreview represents module storage settings.
type StoragePreview = internalprev.StoragePreview

// ConfigFieldPreview describes a resolved configuration field with its provenance.
type ConfigFieldPreview = internalprev.ConfigFieldPreview

// Result holds the synthesized runtime configuration.
type Result = internalprev.Result

var (
	// GeneratePreview evaluates template.yaml against inputs and resolves effective runtime config.
	GeneratePreview = internalprev.GeneratePreview
	// CalculateAutoMemory determines automatic memory allocation.
	CalculateAutoMemory = internalprev.CalculateAutoMemory
)
