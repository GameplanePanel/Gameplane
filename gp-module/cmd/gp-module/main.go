// Package main implements the gp-module CLI entrypoint.
package main

import (
	"fmt"
	"os"

	"github.com/GameplanePanel/gameplane/gp-module/internal/validator"
)

// version is the gp-module release, surfaced via --version and compared by
// the validator against a module's declared gameplaneMinVersion.
const version = validator.ToolVersion

func printUsage() {
	fmt.Printf(`gp-module — Gameplane Module Authoring & Building Toolkit (v%s)

Usage:
  gp-module <command> [arguments] [options]

Commands:
  init      Scaffold a new game module directory
  validate  Offline validation and linting of module manifests
  preview   Dry-run manifest rendering and dynamic config preview
  package   Package module directory into an OCI artifact bundle
  i18n      Extract and manage module translations

Options:
  -h, --help     Show this help message
  -v, --version  Show version information

Run 'gp-module <command> --help' for details on a specific command.
`, version)
}

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "-h", "--help", "help":
		printUsage()
		os.Exit(0)
	case "-v", "--version", "version":
		fmt.Printf("gp-module version %s\n", version)
		os.Exit(0)
	case "init", "new", "scaffold":
		runInit(args)
	case "validate", "lint":
		runValidate(args)
	case "preview", "render":
		runPreview(args)
	case "package", "build", "bundle":
		runPackage(args)
	case "i18n":
		runI18n(args)
	default:
		fmt.Fprintf(os.Stderr, "error: unknown command %q\n\n", cmd)
		printUsage()
		os.Exit(2)
	}
}
