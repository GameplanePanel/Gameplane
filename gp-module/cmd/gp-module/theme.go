// Package main implements the gp-module CLI.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/GameplanePanel/gameplane/gp-module/internal/theme"
)

// Exit codes of the theme command group (contracts/gp-module-theme-cli.md):
// 0 success, 1 validation or runtime failure, 2 usage error.
const (
	exitOK       = 0
	exitFailure  = 1
	exitUsage    = 2
	themeFlagTag = "theme"
)

func printThemeUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, `Usage: gp-module theme <command> [arguments] [options]

Theme packages (spec 043): colors plus an optional sanitized CSS overlay.

Commands:
  init <name>             Scaffold <name>/theme.yaml and an empty theme.css
  from-export <file>      Turn a spec 016 gameplane-theme export into <name>/
  validate [dir]          Check a theme folder against every package rule
  preview <dir>           Serve a local sample page styled by the theme
  package <dir>           Write an OCI layout (--out) or push it (--push)

Run 'gp-module theme <command> --help' for the options of one command.
`)
}

// runTheme dispatches the theme command group.
func runTheme(args []string) {
	if len(args) == 0 {
		printThemeUsage(os.Stderr)
		os.Exit(exitUsage)
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "-h", "--help", "help":
		printThemeUsage(os.Stdout)
		os.Exit(exitOK)
	case "init":
		runThemeInit(rest)
	case "from-export":
		runThemeFromExport(rest)
	case "validate":
		runThemeValidate(rest)
	case "preview":
		runThemePreview(rest)
	case "package":
		runThemePackage(rest)
	default:
		_, _ = fmt.Fprintf(os.Stderr, "error: unknown theme command %q\n\n", sub)
		printThemeUsage(os.Stderr)
		os.Exit(exitUsage)
	}
}

// newThemeFlagSet creates a flag set that reports parse errors on stderr.
func newThemeFlagSet(name string, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(themeFlagTag+" "+name, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(os.Stderr, usage)
		fs.PrintDefaults()
	}
	return fs
}

// splitThemeArgs moves flags ahead of positional arguments, so flags may follow
// the path, as the other gp-module commands allow. Names in valueFlags take the
// next argument as their value.
func splitThemeArgs(args []string, valueFlags map[string]bool) []string {
	var flags, positionals []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positionals = append(positionals, args[i+1:]...)
			break
		}
		if !strings.HasPrefix(a, "-") || a == "-" {
			positionals = append(positionals, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if !strings.Contains(name, "=") && valueFlags[name] && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	return append(flags, positionals...)
}

// parseThemeFlags parses args into fs and returns the positional arguments.
// A parse error exits with the usage code.
func parseThemeFlags(fs *flag.FlagSet, args []string, valueFlags map[string]bool) []string {
	if err := fs.Parse(splitThemeArgs(args, valueFlags)); err != nil {
		os.Exit(exitUsage)
	}
	return fs.Args()
}

// failTheme prints a runtime error and exits with the failure code.
func failTheme(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(exitFailure)
}

func runThemeInit(args []string) {
	const usage = `Usage: gp-module theme init <name> [--dark] [--light] [--from-preset pink|legacy]

Scaffolds <name>/theme.yaml with the tokens of a built-in preset and an empty
theme.css. With neither --dark nor --light both modes are written.

`
	fs := newThemeFlagSet("init", usage)
	var dark, light bool
	var preset string
	fs.BoolVar(&dark, "dark", false, "Write the dark token mode")
	fs.BoolVar(&light, "light", false, "Write the light token mode")
	fs.StringVar(&preset, "from-preset", theme.PresetPink, "Built-in preset to copy tokens from (pink or legacy)")
	pos := parseThemeFlags(fs, args, map[string]bool{"from-preset": true})
	if len(pos) != 1 {
		fs.Usage()
		os.Exit(exitUsage)
	}
	var modes []string
	switch {
	case dark && !light:
		modes = []string{"dark"}
	case light && !dark:
		modes = []string{"light"}
	}
	if preset != theme.PresetPink && preset != theme.PresetLegacy {
		_, _ = fmt.Fprintf(os.Stderr, "unknown --from-preset %q (want %s or %s)\n", preset, theme.PresetPink, theme.PresetLegacy)
		os.Exit(exitUsage)
	}
	name := pos[0]
	if err := theme.Init(name, name, theme.InitOptions{Modes: modes, Preset: preset}); err != nil {
		failTheme(err)
	}
	fmt.Printf("Created %s/theme.yaml and %s/theme.css\n", name, name)
	fmt.Printf("Next: fill in author and license, add previews/, then run gp-module theme validate %s\n", name)
}

func runThemeFromExport(args []string) {
	const usage = `Usage: gp-module theme from-export <export.json> --name <name> [--version 0.1.0]

Turns a spec 016 gameplane-theme v1 export into <name>/. Custom colors are
expanded to the 14 tokens with the dashboard's derivation; custom CSS is
written to theme.css when the export has it enabled.

`
	fs := newThemeFlagSet("from-export", usage)
	var name, version string
	fs.StringVar(&name, "name", "", "Theme name (DNS-1123 label); also the folder")
	fs.StringVar(&version, "version", theme.DefaultVersion, "Package version (MAJOR.MINOR.PATCH)")
	pos := parseThemeFlags(fs, args, map[string]bool{"name": true, "version": true})
	if len(pos) != 1 || name == "" {
		fs.Usage()
		os.Exit(exitUsage)
	}
	res, err := theme.FromExport(pos[0], name, name, version)
	if err != nil {
		failTheme(err)
	}
	fmt.Printf("Created %s/theme.yaml with %s tokens", name, res.Mode)
	if res.WroteCSS {
		fmt.Printf(" and %s/theme.css", name)
	}
	fmt.Println()
	for _, n := range res.Notes {
		fmt.Printf("note: %s\n", n)
	}
	fmt.Printf("Next: fill in author and license, add previews/, then run gp-module theme validate %s\n", name)
}

func runThemeValidate(args []string) {
	const usage = `Usage: gp-module theme validate [dir] [--json]

Runs every rule of contracts/theme-package.md and prints each failure with its
rule id and line. lowContrast is printed as a warning and does not fail.
Exit 0 when valid, 1 when not.

`
	fs := newThemeFlagSet("validate", usage)
	var asJSON bool
	fs.BoolVar(&asJSON, "json", false, "Print the result as JSON for CI")
	pos := parseThemeFlags(fs, args, nil)
	if len(pos) > 1 {
		fs.Usage()
		os.Exit(exitUsage)
	}
	dir := "."
	if len(pos) == 1 {
		dir = pos[0]
	}
	pkg, err := theme.Inspect(dir)
	if err != nil {
		failTheme(err)
	}
	report := pkg.Report()
	if asJSON {
		data, err := report.JSON()
		if err != nil {
			failTheme(fmt.Errorf("encode report: %w", err))
		}
		fmt.Println(string(data))
	} else {
		fmt.Print(report.Human())
	}
	if !report.Valid {
		os.Exit(exitFailure)
	}
	os.Exit(exitOK)
}

func runThemePreview(args []string) {
	const usage = `Usage: gp-module theme preview <dir> [--port 8787] [--mode light|dark]

Validates the package, then serves a local sample page styled with its tokens
and theme.css on 127.0.0.1. Nothing is published. Exit 1 when validation
fails; otherwise runs until Ctrl+C.

`
	fs := newThemeFlagSet("preview", usage)
	var port int
	var mode string
	fs.IntVar(&port, "port", theme.DefaultPreviewPort, "Local port to serve on")
	fs.StringVar(&mode, "mode", "", "Mode the page opens in (light or dark); default light when present")
	pos := parseThemeFlags(fs, args, map[string]bool{"port": true, "mode": true})
	if len(pos) != 1 {
		fs.Usage()
		os.Exit(exitUsage)
	}
	if mode != "" && mode != "light" && mode != "dark" {
		_, _ = fmt.Fprintf(os.Stderr, "error: --mode must be light or dark, got %q\n", mode)
		os.Exit(exitUsage)
	}
	if port < 1 || port > 65535 {
		_, _ = fmt.Fprintf(os.Stderr, "error: --port must be between 1 and 65535, got %d\n", port)
		os.Exit(exitUsage)
	}
	pkg, err := theme.Inspect(pos[0])
	if err != nil {
		failTheme(err)
	}
	if !pkg.Valid() {
		fmt.Print(pkg.Report().Human())
		os.Exit(exitFailure)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := theme.Serve(ctx, pkg, port, mode, os.Stdout); err != nil {
		failTheme(err)
	}
	os.Exit(exitOK)
}

func runThemePackage(args []string) {
	const usage = `Usage: gp-module theme package <dir> [--out <oci-layout-dir>] [--push <registry-prefix>] [--plain-http]

Validates the package, then writes an OCI image layout (--out) or pushes it
(--push) to <registry-prefix>/<name>:<version>. Pushing uses ORAS and the registry
credentials from the Docker config. Sign the pushed artifact with cosign sign.

`
	fs := newThemeFlagSet("package", usage)
	var out, push string
	var plainHTTP bool
	fs.StringVar(&out, "out", "", "Write an OCI image layout to this directory")
	fs.StringVar(&push, "push", "", "Registry prefix to push to, for example ghcr.io/org/themes")
	fs.BoolVar(&plainHTTP, "plain-http", false, "Allow plain HTTP registry connections")
	pos := parseThemeFlags(fs, args, map[string]bool{"out": true, "push": true})
	if len(pos) != 1 || (out == "" && push == "") {
		fs.Usage()
		os.Exit(exitUsage)
	}
	pkg, err := theme.Inspect(pos[0])
	if err != nil {
		failTheme(err)
	}
	if !pkg.Valid() {
		fmt.Print(pkg.Report().Human())
		os.Exit(exitFailure)
	}
	bundle, err := theme.BuildBundle(pkg)
	if err != nil {
		failTheme(err)
	}
	if out != "" {
		digest, err := theme.WriteOCILayout(bundle, out)
		if err != nil {
			failTheme(err)
		}
		fmt.Printf("Wrote OCI layout %s for %s %s (manifest %s)\n", out, bundle.Name, bundle.Version, digest)
	}
	if push != "" {
		ref, err := theme.Push(bundle, pos[0], push, theme.PushOptions{PlainHTTP: plainHTTP})
		if err != nil {
			failTheme(err)
		}
		fmt.Printf("Pushed %s\n", ref)
	}
	os.Exit(exitOK)
}
