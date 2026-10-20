// Package scaffold generates schema-compliant starter module directories.
package scaffold

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GameplanePanel/gameplane/gp-module/internal/archetypes"
	"github.com/GameplanePanel/gameplane/gp-module/internal/common"
	"gopkg.in/yaml.v3"
)

// Options defines inputs for module scaffolding.
type Options struct {
	Name             string
	DisplayName      string
	Archetype        string
	Image            string
	Ports            []archetypes.PortDef
	StorageSize      string
	StorageMountPath string
	Categories       []string
	Summary          string
	OutputDir        string
	Overwrite        bool
}

// GeneratedFiles holds the in-memory contents of generated module files.
type GeneratedFiles struct {
	ModuleYAML   string `json:"moduleYaml"`
	TemplateYAML string `json:"templateYaml"`
	ReadmeMD     string `json:"readmeMd"`
	IconBytes    []byte `json:"-"`
	IconBase64   string `json:"iconBase64,omitempty"`
}

// Result holds the result of scaffolding a module to disk.
type Result struct {
	Dir          string
	Files        GeneratedFiles
	CreatedFiles []string
}

// GenerateFiles generates the module files in-memory from options and archetype defaults.
func GenerateFiles(opts Options) (*GeneratedFiles, error) {
	if err := common.ValidateModuleName(opts.Name); err != nil {
		return nil, err
	}

	archetypeID := opts.Archetype
	if archetypeID == "" {
		archetypeID = "generic"
	}
	arch, err := archetypes.GetArchetype(archetypeID)
	if err != nil {
		return nil, err
	}

	displayName := opts.DisplayName
	if displayName == "" {
		displayName = titleize(opts.Name)
	}

	image := opts.Image
	if image == "" {
		image = arch.DefaultImage
	}

	ports := opts.Ports
	if len(ports) == 0 {
		ports = arch.DefaultPorts
	} else {
		seenPorts := make(map[int]string)
		for i := range ports {
			if ports[i].ContainerPort < 1 || ports[i].ContainerPort > 65535 {
				return nil, fmt.Errorf("invalid port number %d: must be 1-65535", ports[i].ContainerPort)
			}
			// Canonicalize protocol to uppercase
			ports[i].Protocol = strings.ToUpper(ports[i].Protocol)
			proto := ports[i].Protocol
			if proto != "TCP" && proto != "UDP" {
				return nil, fmt.Errorf("invalid protocol %q for port %d: must be TCP or UDP", proto, ports[i].ContainerPort)
			}
			if existingName, exists := seenPorts[ports[i].ContainerPort]; exists {
				return nil, fmt.Errorf("duplicate port %d used by %q and %q", ports[i].ContainerPort, existingName, ports[i].Name)
			}
			seenPorts[ports[i].ContainerPort] = ports[i].Name
		}
	}

	storageSize := opts.StorageSize
	if storageSize == "" {
		storageSize = arch.DefaultStorage.Size
	}

	storageMount := opts.StorageMountPath
	if storageMount == "" {
		storageMount = arch.DefaultStorage.MountPath
	}

	categories := opts.Categories
	if len(categories) == 0 {
		categories = arch.DefaultCategories
	}

	summary := opts.Summary
	if summary == "" {
		switch arch.ID {
		case "steamcmd":
			summary = fmt.Sprintf("Dedicated server for %s powered by SteamCMD", displayName)
		case "java":
			summary = fmt.Sprintf("Dedicated server for %s powered by Java/JVM", displayName)
		default:
			summary = fmt.Sprintf("Dedicated server for %s", displayName)
		}
	}

	// 1. Render module.yaml
	moduleDoc := map[string]any{
		"apiVersion":  "gameplane.local/module/v1",
		"name":        opts.Name,
		"displayName": displayName,
		"version":     "1.0.0",
		"game":        opts.Name,
		"categories":  categories,
		"summary":     summary,
		"homepage":    "",
		"license":     "MIT",
		"icon":        "icon.png",
	}

	var moduleBuf strings.Builder
	moduleBuf.WriteString("# yaml-language-server: $schema=../.schema/module.schema.json\n")
	enc := yaml.NewEncoder(&moduleBuf)
	enc.SetIndent(2)
	if err := enc.Encode(moduleDoc); err != nil {
		return nil, fmt.Errorf("failed to encode module.yaml: %w", err)
	}

	// 2. Render template.yaml
	specDoc := map[string]any{
		"displayName": displayName,
		"game":        opts.Name,
		"version":     "1.0.0",
		"categories":  categories,
		"description": fmt.Sprintf("Dedicated game server for %s managed by Gameplane.\n", displayName),
		"image":       image,
		"ports":       ports,
		"storage": map[string]string{
			"size":      storageSize,
			"mountPath": storageMount,
		},
	}

	if len(arch.DefaultEnv) > 0 {
		specDoc["env"] = arch.DefaultEnv
	}
	if len(arch.ConfigSchema) > 0 {
		specDoc["configSchema"] = arch.ConfigSchema
	}
	if len(arch.Capabilities.Lifecycle.Stop) > 0 {
		specDoc["capabilities"] = arch.Capabilities
	}
	if arch.DefaultSecurity != nil {
		specDoc["security"] = arch.DefaultSecurity
	}

	templateDoc := map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata": map[string]any{
			"name": opts.Name,
			"labels": map[string]string{
				"gameplane.local/module": opts.Name,
			},
		},
		"spec": specDoc,
	}

	var templateBuf strings.Builder
	templateBuf.WriteString("# yaml-language-server: $schema=../.schema/gametemplate.schema.json\n")
	encTmpl := yaml.NewEncoder(&templateBuf)
	encTmpl.SetIndent(2)
	if err := encTmpl.Encode(templateDoc); err != nil {
		return nil, fmt.Errorf("failed to encode template.yaml: %w", err)
	}

	// 3. Render README.md
	var readmeBuf strings.Builder
	fmt.Fprintf(&readmeBuf, "# %s\n\n", displayName)
	fmt.Fprintf(&readmeBuf, "%s\n\n", summary)
	readmeBuf.WriteString("## Overview\n\n")
	fmt.Fprintf(&readmeBuf, "This module packages the dedicated server for **%s** as a Gameplane OCI bundle.\n\n", displayName)
	readmeBuf.WriteString("## Networking & Ports\n\n")
	for _, p := range ports {
		adv := "internal only"
		if p.Advertise {
			adv = "publicly advertised"
		}
		fmt.Fprintf(&readmeBuf, "- **%s**: port `%d/%s` (%s)\n", p.Name, p.ContainerPort, p.Protocol, adv)
	}
	readmeBuf.WriteString("\n## Storage\n\n")
	fmt.Fprintf(&readmeBuf, "- Volume size: `%s`\n- Mount path: `%s`\n", storageSize, storageMount)
	if len(arch.DefaultEnv) > 0 {
		readmeBuf.WriteString("\n## Environment Variables\n\n")
		for _, e := range arch.DefaultEnv {
			fmt.Fprintf(&readmeBuf, "- `%s`: default `%s`\n", e.Name, e.Value)
		}
	}
	if sec := arch.DefaultSecurity; sec != nil {
		readmeBuf.WriteString("\n## Container User\n\n")
		fmt.Fprintf(&readmeBuf, "- The game runs as uid `%d`, gid `%d`; the data volume is group-owned by gid `%d` (`spec.security`).\n",
			sec.RunAsUser, sec.RunAsGroup, sec.FSGroup)
		readmeBuf.WriteString("- If you change the image, set `spec.security` to a non-root user that image provides.\n")
	}
	if len(arch.ConfigSchema) > 0 {
		readmeBuf.WriteString("\n## Configuration Parameters\n\n")
		for _, c := range arch.ConfigSchema {
			desc := c.Description
			if desc == "" {
				desc = c.DisplayName
			}
			fmt.Fprintf(&readmeBuf, "- **%s** (`%s`): %s\n", c.Name, c.Type, desc)
		}
	}

	icon := archetypes.PlaceholderIconBytes()

	return &GeneratedFiles{
		ModuleYAML:   moduleBuf.String(),
		TemplateYAML: templateBuf.String(),
		ReadmeMD:     readmeBuf.String(),
		IconBytes:    icon,
		IconBase64:   base64.StdEncoding.EncodeToString(icon),
	}, nil
}

// Scaffold generates and writes module files to target directory.
func Scaffold(opts Options) (*Result, error) {
	outDir := opts.OutputDir
	if outDir == "" {
		outDir = filepath.Join("modules", opts.Name)
	}

	if fi, err := os.Stat(outDir); err == nil && fi.IsDir() {
		if !opts.Overwrite {
			return nil, fmt.Errorf("target directory %q already exists; use --overwrite (-f) to overwrite", outDir)
		}
	}

	files, err := GenerateFiles(opts)
	if err != nil {
		return nil, err
	}

	if err := os.MkdirAll(outDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create directory %q: %w", outDir, err)
	}

	created := []string{}

	modulePath := filepath.Join(outDir, "module.yaml")
	if err := os.WriteFile(modulePath, []byte(files.ModuleYAML), 0600); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", modulePath, err)
	}
	created = append(created, modulePath)

	templatePath := filepath.Join(outDir, "template.yaml")
	if err := os.WriteFile(templatePath, []byte(files.TemplateYAML), 0600); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", templatePath, err)
	}
	created = append(created, templatePath)

	readmePath := filepath.Join(outDir, "README.md")
	if err := os.WriteFile(readmePath, []byte(files.ReadmeMD), 0600); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", readmePath, err)
	}
	created = append(created, readmePath)

	iconPath := filepath.Join(outDir, "icon.png")
	if err := os.WriteFile(iconPath, files.IconBytes, 0600); err != nil {
		return nil, fmt.Errorf("failed to write %s: %w", iconPath, err)
	}
	created = append(created, iconPath)

	return &Result{
		Dir:          outDir,
		Files:        *files,
		CreatedFiles: created,
	}, nil
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
