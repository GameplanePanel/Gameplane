// Package main implements the gp-module CLI.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/validator"
)

func runValidate(args []string) {
	fs := flag.NewFlagSet("validate", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		flagOffline bool
		flagStrict  bool
		flagJSON    bool
		flagRules   string
	)

	fs.BoolVar(&flagOffline, "offline", true, "Enforce offline validation")
	fs.BoolVar(&flagStrict, "strict", false, "Treat warnings as blocking errors")
	fs.BoolVar(&flagJSON, "json", false, "Output results in JSON format")
	fs.StringVar(&flagRules, "rules", "", "Comma-separated list of rule IDs to evaluate")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: gp-module validate [MODULE_PATH...] [options]

Performs static validation and linting of module manifests.

Options:
  --offline       Enforce pure offline validation without contacting registries (default: true)
  --strict        Treat warnings as blocking errors (fail with code 1)
  --json          Output diagnostic report in machine-readable JSON format
  --rules <list>  Comma-separated list of rule IDs to evaluate (default: all)
  -h, --help      Show this help message
`)
	}

	reorderedArgs := reorderArgs(args)
	if err := fs.Parse(reorderedArgs); err != nil {
		os.Exit(2)
	}

	targetPaths := fs.Args()
	if len(targetPaths) == 0 {
		// Auto-discover
		targetPaths = discoverModulePaths()
		if len(targetPaths) == 0 {
			fmt.Fprintln(os.Stderr, "error: no module directories found to validate")
			os.Exit(1)
		}
	}

	allowedRules := make(map[string]bool)
	if flagRules != "" {
		for _, r := range strings.Split(flagRules, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				allowedRules[r] = true
			}
		}
	}

	opts := validator.ValidateOptions{
		Strict:  flagStrict,
		Offline: flagOffline,
	}

	var moduleReports []validator.ModuleReport

	for _, p := range targetPaths {
		rep, err := validator.ValidateDirectory(p, opts)
		if err != nil {
			// Record file/directory access error
			moduleReports = append(moduleReports, validator.ModuleReport{
				Name:  filepath.Base(p),
				Path:  p,
				Clean: false,
				Findings: []validator.Finding{
					{
						Level:       validator.SeverityError,
						RuleID:      validator.RuleMissingRequiredFile,
						File:        p,
						Line:        0,
						Message:     err.Error(),
						Remediation: "Ensure module directory exists and is accessible.",
					},
				},
			})
			continue
		}

		if len(allowedRules) > 0 {
			var filtered []validator.Finding
			for _, f := range rep.Findings {
				if allowedRules[f.RuleID] {
					filtered = append(filtered, f)
				}
			}
			rep.Findings = filtered
			rep.Clean = true
			for _, f := range rep.Findings {
				if f.Level == validator.SeverityError || (flagStrict && f.Level == validator.SeverityWarn) {
					rep.Clean = false
					break
				}
			}
		}

		moduleReports = append(moduleReports, *rep)
	}

	aggReport := validator.NewValidationReport(moduleReports)

	if flagJSON {
		data, err := aggReport.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error serializing validation report: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(aggReport.FormatHuman())
	}

	if !aggReport.Clean {
		os.Exit(1)
	}
	os.Exit(0)
}

func discoverModulePaths() []string {
	// 1. Current directory has module.yaml or template.yaml
	if _, err := os.Stat("module.yaml"); err == nil {
		return []string{"."}
	}
	if _, err := os.Stat("template.yaml"); err == nil {
		return []string{"."}
	}

	// 2. Check ./modules
	checkDirs := []string{"modules", "../modules", "../../modules"}
	for _, cd := range checkDirs {
		entries, err := os.ReadDir(cd)
		if err == nil {
			var res []string
			for _, e := range entries {
				if e.IsDir() {
					modPath := filepath.Join(cd, e.Name())
					if _, err := os.Stat(filepath.Join(modPath, "template.yaml")); err == nil {
						res = append(res, modPath)
					}
				}
			}
			if len(res) > 0 {
				return res
			}
		}
	}

	return nil
}

func reorderArgs(args []string) []string {
	var flags []string
	var positionals []string
	skipNext := false

	for i, arg := range args {
		if skipNext {
			flags = append(flags, arg)
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if (arg == "--rules" || arg == "-rules") && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	return append(flags, positionals...)
}
