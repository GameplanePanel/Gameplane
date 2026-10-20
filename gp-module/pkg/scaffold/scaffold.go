// Package scaffold provides public scaffold exports.
package scaffold

import (
	internalscaffold "github.com/GameplanePanel/gameplane/gp-module/internal/scaffold"
)

// Options defines inputs for module scaffolding.
type Options = internalscaffold.Options

// GeneratedFiles holds the in-memory contents of generated module files.
type GeneratedFiles = internalscaffold.GeneratedFiles

// Result holds the result of scaffolding a module to disk.
type Result = internalscaffold.Result

var (
	// GenerateFiles generates the module files in-memory from options and archetype defaults.
	GenerateFiles = internalscaffold.GenerateFiles
	// Scaffold generates and writes module files to target directory.
	Scaffold = internalscaffold.Scaffold
)
