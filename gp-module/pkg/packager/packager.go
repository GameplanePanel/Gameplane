// Package packager provides public packager exports.
package packager

import (
	internalpack "github.com/GameplanePanel/gameplane/gp-module/internal/packager"
)

// PackageLimits defines resource thresholds for packager operations.
type PackageLimits = internalpack.PackageLimits

// PackageWarning represents non-fatal warnings during packaging.
type PackageWarning = internalpack.PackageWarning

// PackageOptions configures archive creation or OCI pushing.
type PackageOptions = internalpack.PackageOptions

var (
	// DefaultPackageLimits provides standard size limits.
	DefaultPackageLimits = internalpack.DefaultPackageLimits
	// CreateArchiveFromFiles creates a tar.gz archive from in-memory files.
	CreateArchiveFromFiles = internalpack.CreateArchiveFromFiles
	// CreateArchiveFromDir creates a tar.gz archive from a directory.
	CreateArchiveFromDir = internalpack.CreateArchiveFromDir
	// ExportArchiveToFile writes an archive to target path.
	ExportArchiveToFile = internalpack.ExportArchiveToFile
	// PushOCI pushes a module directory to an OCI registry via ORAS.
	PushOCI = internalpack.PushOCI
	// ReadModuleFiles reads module files from a directory into memory.
	ReadModuleFiles = internalpack.ReadModuleFiles
)
