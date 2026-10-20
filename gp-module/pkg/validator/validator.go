// Package validator provides public validator exports.
package validator

import (
	internalval "github.com/GameplanePanel/gameplane/gp-module/internal/validator"
)

// Severity indicates finding severity level.
type Severity = internalval.Severity

// Finding severity level constants.
const (
	SeverityError = internalval.SeverityError
	SeverityWarn  = internalval.SeverityWarn
)

// ValidateOptions defines inputs for module validation.
type ValidateOptions = internalval.ValidateOptions

// ModuleReport holds validation findings for a single module.
type ModuleReport = internalval.ModuleReport

// Finding represents an issue discovered during validation.
type Finding = internalval.Finding

// ValidationReport holds findings across validated modules.
type ValidationReport = internalval.ValidationReport

var (
	// ValidateDirectory validates a module located in a filesystem directory.
	ValidateDirectory = internalval.ValidateDirectory
	// ValidateFiles validates in-memory module files.
	ValidateFiles = internalval.ValidateFiles
)
