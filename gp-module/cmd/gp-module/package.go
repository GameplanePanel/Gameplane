// Package main implements the gp-module CLI.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/packager"
)

func runPackage(args []string) {
	fs := flag.NewFlagSet("package", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		flagOutput        string
		flagRegistry      string
		flagTag           string
		flagTagLatest     bool
		flagPlainHTTP     bool
		flagInsecure      bool
		flagMaxIconSize   int64
		flagMaxBundleSize int64
	)

	fs.StringVar(&flagOutput, "output", "", "Build a local .tar.gz bundle archive at path")
	fs.StringVar(&flagRegistry, "registry", "", "Registry destination prefix (e.g. localhost:5001 or ghcr.io/org/repo)")
	fs.StringVar(&flagTag, "tag", "", "OCI tag to push (defaults to version in module.yaml)")
	fs.BoolVar(&flagTagLatest, "tag-latest", false, "Also tag :latest in addition to version")
	fs.BoolVar(&flagPlainHTTP, "plain-http", false, "Allow plain HTTP registry connections")
	fs.BoolVar(&flagInsecure, "insecure", false, "Skip TLS verification")
	fs.Int64Var(&flagMaxIconSize, "max-icon-size", packager.DefaultMaxIconSize, "Maximum icon size in bytes before warning")
	fs.Int64Var(&flagMaxBundleSize, "max-bundle-size", packager.DefaultMaxBundleSize, "Maximum bundle size in bytes before warning")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: gp-module package [MODULE_PATH] [options]

Package module directory into an OCI artifact or local .tar.gz bundle.

Options:
  --output <path>            Write a local .tar.gz archive instead of pushing
  --registry <ref>           Registry destination (e.g. ghcr.io/org/repo)
  --tag <str>                OCI tag to push (default: module.yaml#version)
  --tag-latest               Also push :latest tag
  --plain-http               Allow plain HTTP connections (e.g. local Kind)
  --insecure                 Skip TLS verification
  --max-icon-size <bytes>    Warning threshold for icon size (default: 512 KiB)
  --max-bundle-size <bytes>  Warning threshold for bundle size (default: 1 MiB)
  -h, --help                 Show this help message
`)
	}

	reordered := reorderPackageArgs(args)
	if err := fs.Parse(reordered); err != nil {
		os.Exit(2)
	}

	modulePath := "."
	posArgs := fs.Args()
	if len(posArgs) > 0 {
		modulePath = posArgs[0]
	}

	if flagOutput == "" && flagRegistry == "" {
		fmt.Fprintln(os.Stderr, "error: either --output <path> or --registry <ref> must be specified")
		fs.Usage()
		os.Exit(2)
	}

	limits := packager.PackageLimits{
		MaxIconSize:   flagMaxIconSize,
		MaxBundleSize: flagMaxBundleSize,
	}

	if flagOutput != "" {
		warnings, err := packager.ExportArchiveToFile(modulePath, flagOutput, limits)
		for _, w := range warnings {
			fmt.Fprintf(os.Stderr, "WARN [%s]: %s\n", w.File, w.Message)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "error packaging module: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Successfully packaged %s into %s\n", modulePath, flagOutput)
		os.Exit(0)
	}

	// Registry push mode
	opts := packager.PackageOptions{
		ModuleDir: modulePath,
		Registry:  flagRegistry,
		Tag:       flagTag,
		TagLatest: flagTagLatest,
		PlainHTTP: flagPlainHTTP,
		Insecure:  flagInsecure,
		Limits:    limits,
	}

	warnings, err := packager.PushOCI(opts)
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "WARN [%s]: %s\n", w.File, w.Message)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "error pushing module: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Successfully pushed module %s to %s\n", modulePath, flagRegistry)
}

func reorderPackageArgs(args []string) []string {
	var flags []string
	var positionals []string
	skipNext := false

	flagsWithValue := map[string]bool{
		"--output":          true,
		"-output":           true,
		"--registry":        true,
		"-registry":         true,
		"--tag":             true,
		"-tag":              true,
		"--max-icon-size":   true,
		"-max-icon-size":    true,
		"--max-bundle-size": true,
		"-max-bundle-size":  true,
	}

	for i, arg := range args {
		if skipNext {
			flags = append(flags, arg)
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if flagsWithValue[arg] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	return append(flags, positionals...)
}
