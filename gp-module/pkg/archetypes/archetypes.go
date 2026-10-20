// Package archetypes provides public archetypes definitions.
package archetypes

import (
	internalarch "github.com/GameplanePanel/gameplane/gp-module/internal/archetypes"
)

// PortDef defines a container network port.
type PortDef = internalarch.PortDef

// StorageDef defines a volume mount requirement.
type StorageDef = internalarch.StorageDef

// ArchetypeDefinition represents a game module starter preset.
type ArchetypeDefinition = internalarch.ArchetypeDefinition

var (
	// GetArchetype returns the archetype definition by ID or error.
	GetArchetype = internalarch.GetArchetype
	// AllArchetypes returns all built-in archetypes.
	AllArchetypes = internalarch.AllArchetypes
	// PlaceholderIconBytes returns default 256x256 PNG icon bytes.
	PlaceholderIconBytes = internalarch.PlaceholderIconBytes
)
