// Package main implements the gp-module CLI.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/archetypes"
	"github.com/GameplanePanel/gameplane/gp-module/internal/common"
	"github.com/GameplanePanel/gameplane/gp-module/internal/scaffold"
)

type stringSlice []string

func (s *stringSlice) String() string {
	return strings.Join(*s, ", ")
}

func (s *stringSlice) Set(v string) error {
	parts := strings.Split(v, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			*s = append(*s, p)
		}
	}
	return nil
}

func runInit(args []string) {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var (
		flagName           string
		flagDisplayName    string
		flagArchetype      string
		flagImage          string
		flagPorts          stringSlice
		flagCategories     stringSlice
		flagSummary        string
		flagOutputDir      string
		flagNonInteractive bool
		flagOverwrite      bool
	)

	fs.StringVar(&flagName, "name", "", "Module slug name (RFC 1123 DNS label)")
	fs.StringVar(&flagDisplayName, "display-name", "", "Human-readable display title")
	fs.StringVar(&flagArchetype, "archetype", "generic", "Starter archetype preset (steamcmd, java, generic)")
	fs.StringVar(&flagImage, "image", "", "Server container image reference")
	fs.Var(&flagPorts, "port", "Primary port declaration (e.g. 25565/tcp, 27015/udp). Can be repeated or comma-separated.")
	fs.Var(&flagPorts, "ports", "Alias for --port")
	fs.Var(&flagCategories, "category", "Catalog category (e.g. Survival, Sandbox). Can be repeated or comma-separated.")
	fs.Var(&flagCategories, "categories", "Alias for --category")
	fs.StringVar(&flagSummary, "summary", "", "One-line description for catalog card")
	fs.StringVar(&flagOutputDir, "output-dir", "", "Target output directory (default: modules/<name>)")
	fs.StringVar(&flagOutputDir, "output", "", "Alias for --output-dir")
	fs.BoolVar(&flagNonInteractive, "non-interactive", false, "Run non-interactively without terminal prompts")
	fs.BoolVar(&flagNonInteractive, "y", false, "Alias for --non-interactive")
	fs.BoolVar(&flagOverwrite, "overwrite", false, "Overwrite existing directory")
	fs.BoolVar(&flagOverwrite, "f", false, "Alias for --overwrite")

	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, `Usage: gp-module init [NAME] [options]

Scaffold a new game module directory pre-populated with module.yaml, template.yaml,
README.md, and a placeholder icon asset.

Options:
`)
		fs.PrintDefaults()
	}

	reordered := reorderInitArgs(args)
	if err := fs.Parse(reordered); err != nil {
		os.Exit(2)
	}

	name := flagName
	if name == "" && fs.NArg() > 0 {
		name = fs.Arg(0)
	}

	reader := bufio.NewReader(os.Stdin)

	// Interactive mode if not non-interactive and missing name
	if !flagNonInteractive && name == "" {
		fmt.Print("Enter module name (e.g. my-game): ")
		input, _ := reader.ReadString('\n')
		name = strings.TrimSpace(input)
	}

	if name == "" {
		fmt.Fprintf(os.Stderr, "error: module name is required\n")
		fs.Usage()
		os.Exit(2)
	}

	if err := common.ValidateModuleName(name); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	displayName := flagDisplayName
	archetypeID := flagArchetype
	image := flagImage
	summary := flagSummary
	var storageSize string
	var storageMountPath string

	if !flagNonInteractive {
		if displayName == "" {
			defaultTitle := titleize(name)
			fmt.Printf("Enter display name [%s]: ", defaultTitle)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				displayName = input
			} else {
				displayName = defaultTitle
			}
		}

		if archetypeID == "generic" && !hasFlag(args, "--archetype") {
			fmt.Print("Select archetype [1] steamcmd, [2] java, [3] generic [3]: ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			switch input {
			case "1", "steamcmd":
				archetypeID = "steamcmd"
			case "2", "java":
				archetypeID = "java"
			case "3", "generic", "":
				archetypeID = "generic"
			default:
				archetypeID = input
			}
		}
	}

	arch, err := archetypes.GetArchetype(archetypeID)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Continue with interactive prompts for remaining fields
	if !flagNonInteractive {
		if image == "" {
			defaultImage := arch.DefaultImage
			fmt.Printf("Enter container image [%s]: ", defaultImage)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				image = input
			} else {
				image = defaultImage
			}
		}

		if len(flagPorts) == 0 {
			defaultPortStr := ""
			if len(arch.DefaultPorts) > 0 {
				p := arch.DefaultPorts[0]
				defaultPortStr = fmt.Sprintf("%d/%s", p.ContainerPort, strings.ToLower(p.Protocol))
			}
			fmt.Printf("Enter game port [%s] (format: PORT/PROTOCOL): ", defaultPortStr)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				pDef, err := parsePortDef(input)
				if err != nil {
					fmt.Fprintf(os.Stderr, "warning: invalid port %q, using archetype default\n", input)
				} else {
					flagPorts = append(flagPorts, fmt.Sprintf("%d/%s", pDef.ContainerPort, strings.ToLower(pDef.Protocol)))
				}
			}
		}

		if len(flagCategories) == 0 {
			defaultCats := strings.Join(arch.DefaultCategories, ", ")
			fmt.Printf("Enter categories [%s] (comma-separated): ", defaultCats)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				flagCategories = stringSlice(strings.Split(input, ","))
				for i := range flagCategories {
					flagCategories[i] = strings.TrimSpace(flagCategories[i])
				}
			} else {
				flagCategories = stringSlice(arch.DefaultCategories)
			}
		}

		if summary == "" {
			fmt.Print("Enter one-line summary: ")
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				summary = input
			}
		}

		if storageSize == "" {
			fmt.Printf("Enter storage size [%s]: ", arch.DefaultStorage.Size)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				storageSize = input
			} else {
				storageSize = arch.DefaultStorage.Size
			}
		}

		if storageMountPath == "" {
			fmt.Printf("Enter storage mount path [%s]: ", arch.DefaultStorage.MountPath)
			input, _ := reader.ReadString('\n')
			input = strings.TrimSpace(input)
			if input != "" {
				storageMountPath = input
			} else {
				storageMountPath = arch.DefaultStorage.MountPath
			}
		}
	}

	// Parse custom ports if provided
	var ports []archetypes.PortDef
	for _, pStr := range flagPorts {
		pDef, err := parsePortDef(pStr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: invalid port %q: %v\n", pStr, err)
			os.Exit(1)
		}
		ports = append(ports, *pDef)
	}

	outDir := flagOutputDir
	if outDir == "" {
		outDir = filepath.Join("modules", name)
	}

	opts := scaffold.Options{
		Name:             name,
		DisplayName:      displayName,
		Archetype:        arch.ID,
		Image:            image,
		Ports:            ports,
		StorageSize:      storageSize,
		StorageMountPath: storageMountPath,
		Categories:       flagCategories,
		Summary:          summary,
		OutputDir:        outDir,
		Overwrite:        flagOverwrite,
	}

	res, err := scaffold.Scaffold(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Successfully scaffolded module %q in %s/\n\n", name, res.Dir)
	fmt.Println("Created files:")
	for _, f := range res.CreatedFiles {
		fmt.Printf("  - %s\n", f)
	}
	fmt.Printf("\nNext steps:\n")
	fmt.Printf("  1. Inspect and customize %s/template.yaml\n", res.Dir)
	fmt.Printf("  2. Validate your module offline: gp-module validate %s\n", res.Dir)
	fmt.Printf("  3. Preview runtime config: gp-module preview %s\n", res.Dir)
}

func parsePortDef(s string) (*archetypes.PortDef, error) {
	parts := strings.Split(s, "/")
	if len(parts) > 2 {
		return nil, fmt.Errorf("port declaration %q must use PORT or PORT/PROTOCOL", s)
	}
	portNum, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid port number %q", parts[0])
	}
	if portNum < 1 || portNum > 65535 {
		return nil, fmt.Errorf("port number %d outside valid range 1-65535", portNum)
	}

	proto := "TCP"
	if len(parts) > 1 {
		proto = strings.ToUpper(parts[1])
	}
	if proto != "TCP" && proto != "UDP" {
		return nil, fmt.Errorf("protocol %q must be TCP or UDP", proto)
	}

	return &archetypes.PortDef{
		Name:          fmt.Sprintf("port-%d", portNum),
		ContainerPort: portNum,
		Protocol:      proto,
		Advertise:     true,
	}, nil
}

func hasFlag(args []string, flagName string) bool {
	for _, a := range args {
		if a == flagName || strings.HasPrefix(a, flagName+"=") {
			return true
		}
	}
	return false
}

func titleize(slug string) string {
	parts := strings.Split(slug, "-")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + strings.ToLower(p[1:])
		}
	}
	return strings.Join(parts, " ")
}

func reorderInitArgs(args []string) []string {
	var flags []string
	var positionals []string
	skipNext := false

	boolFlags := map[string]bool{
		"--non-interactive": true, "-non-interactive": true,
		"-y": true, "--y": true,
		"--overwrite": true, "-overwrite": true,
		"-f": true, "--f": true,
		"-h": true, "--help": true, "-help": true,
	}

	for i, arg := range args {
		if skipNext {
			flags = append(flags, arg)
			skipNext = false
			continue
		}
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if !strings.Contains(arg, "=") && !boolFlags[arg] && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				skipNext = true
			}
		} else {
			positionals = append(positionals, arg)
		}
	}
	return append(flags, positionals...)
}
