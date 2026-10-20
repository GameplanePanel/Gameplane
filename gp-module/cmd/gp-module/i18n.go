// Package main implements the gp-module CLI.
package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	gpi18n "github.com/GameplanePanel/gameplane/gp-module/internal/i18n"
)

func runI18n(args []string) {
	os.Exit(i18nCommand(args, os.Stdout, os.Stderr))
}

// i18nCommand runs the i18n subcommands and returns the process exit code:
// 0 on success, 1 on error, 2 on usage errors.
func i18nCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		_, _ = fmt.Fprint(stderr, i18nUsage)
		if len(args) == 0 {
			return 2
		}
		return 0
	}
	switch args[0] {
	case "extract":
		return i18nExtract(args[1:], stdout, stderr)
	case "stamp":
		return i18nStamp(args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "error: unknown i18n subcommand %q\n\n%s", args[0], i18nUsage)
		return 2
	}
}

const i18nUsage = `Usage: gp-module i18n <subcommand> [arguments]

Subcommands:
  extract [MODULE_PATH] [--output <file>]
      Derive the translatable message ids from module.yaml and template.yaml
      and write the English reference pack, with translator descriptions
      (default: <MODULE_PATH>/locales/en.json).
  stamp [MODULE_PATH]
      Record the current English text hash (sourceHash) on every translated
      entry of <MODULE_PATH>/locales/<tag>.json (except en.json) that lacks
      one, so a later English change shows up as needs-review.
`

func i18nExtract(args []string, stdout, stderr io.Writer) int {
	var (
		modulePath = "."
		output     string
		havePath   bool
	)
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(stderr, i18nUsage)
			return 0
		case arg == "--output" || arg == "-output":
			if i+1 < len(args) {
				output = args[i+1]
				i++
				continue
			}
			_, _ = fmt.Fprintln(stderr, "error: --output needs a file path")
			return 2
		case strings.HasPrefix(arg, "--output="):
			output = strings.TrimPrefix(arg, "--output=")
		case strings.HasPrefix(arg, "-"):
			_, _ = fmt.Fprintf(stderr, "error: unknown option %q\n\n%s", arg, i18nUsage)
			return 2
		default:
			if havePath {
				_, _ = fmt.Fprintln(stderr, "error: only one MODULE_PATH may be given")
				return 2
			}
			modulePath, havePath = arg, true
		}
	}
	written, err := gpi18n.WriteExtract(modulePath, output)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "wrote %s\n", written)
	return 0
}

func i18nStamp(args []string, stdout, stderr io.Writer) int {
	modulePath := "."
	havePath := false
	for _, arg := range args {
		switch {
		case arg == "-h" || arg == "--help":
			_, _ = fmt.Fprint(stderr, i18nUsage)
			return 0
		case strings.HasPrefix(arg, "-"):
			_, _ = fmt.Fprintf(stderr, "error: unknown option %q\n\n%s", arg, i18nUsage)
			return 2
		default:
			if havePath {
				_, _ = fmt.Fprintln(stderr, "error: only one MODULE_PATH may be given")
				return 2
			}
			modulePath, havePath = arg, true
		}
	}
	stamped, err := gpi18n.Stamp(modulePath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if len(stamped) == 0 {
		_, _ = fmt.Fprintln(stdout, "nothing to stamp")
		return 0
	}
	for _, f := range stamped {
		_, _ = fmt.Fprintf(stdout, "stamped %s\n", f)
	}
	return 0
}
