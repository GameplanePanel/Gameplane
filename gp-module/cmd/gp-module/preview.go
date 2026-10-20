// Package main implements the gp-module CLI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/preview"
	"gopkg.in/yaml.v3"
)

type configFlags map[string]string

func (c *configFlags) String() string {
	return ""
}

func (c *configFlags) Set(v string) error {
	parts := strings.SplitN(v, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("config parameter must be in KEY=VALUE format, got %q", v)
	}
	(*c)[parts[0]] = parts[1]
	return nil
}

func runPreview(args []string) {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		flagVersionID  string
		flagMemory     string
		flagConfigFile string
		flagJSON       bool
	)
	cfgMap := make(configFlags)

	fs.StringVar(&flagVersionID, "version-id", "", "Select a specific version ID from spec.versions")
	fs.StringVar(&flagMemory, "memory", "4Gi", "Simulated container memory limit (e.g. 4Gi, 2048Mi)")
	fs.StringVar(&flagConfigFile, "config-file", "", "Path to YAML or JSON file containing key-value config overrides")
	fs.Var(&cfgMap, "config", "Provide custom config override in KEY=VAL format. Can be repeated.")
	fs.BoolVar(&flagJSON, "json", false, "Output results in JSON format")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: gp-module preview [MODULE_PATH] [options]

Dry-run manifest rendering and configuration preview.

Options:
  --version-id <id>     Select a specific version entry from spec.versions
  --memory <qty>        Simulated container memory limit (default: 4Gi)
  --config <key=val>    Provide custom configuration value (repeatable)
  --config-file <path>  Path to YAML/JSON configuration file
  --json                Output results in JSON format
  -h, --help            Show this help message
`)
	}

	reordered := reorderPreviewArgs(args)
	if err := fs.Parse(reordered); err != nil {
		os.Exit(2)
	}

	modulePath := "."
	posArgs := fs.Args()
	if len(posArgs) > 0 {
		modulePath = posArgs[0]
	}

	userConfig := make(map[string]string)

	if flagConfigFile != "" {
		data, err := os.ReadFile(filepath.Clean(flagConfigFile))
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading config file %q: %v\n", flagConfigFile, err)
			os.Exit(1)
		}
		var parsed map[string]any
		if err := yaml.Unmarshal(data, &parsed); err != nil {
			if errJSON := json.Unmarshal(data, &parsed); errJSON != nil {
				fmt.Fprintf(os.Stderr, "error parsing config file %q: %v\n", flagConfigFile, err)
				os.Exit(1)
			}
		}
		for k, v := range parsed {
			userConfig[k] = fmt.Sprintf("%v", v)
		}
	}

	for k, v := range cfgMap {
		userConfig[k] = v
	}

	opts := preview.Options{
		ModuleDir:   modulePath,
		VersionID:   flagVersionID,
		MemoryLimit: flagMemory,
		UserConfig:  userConfig,
	}

	res, err := preview.GeneratePreview(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error generating preview for %s: %v\n", modulePath, err)
		os.Exit(1)
	}

	if flagJSON {
		data, err := res.ToJSON()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error serializing preview JSON: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(res.FormatHuman())
	}
}

func reorderPreviewArgs(args []string) []string {
	var flags []string
	var positionals []string
	skipNext := false

	flagsWithValue := map[string]bool{
		"--version-id":  true,
		"-version-id":   true,
		"--memory":      true,
		"-memory":       true,
		"--config-file": true,
		"-config-file":  true,
		"--config":      true,
		"-config":       true,
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
