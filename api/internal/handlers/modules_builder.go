package handlers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/archetypes"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/packager"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/preview"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/scaffold"
	"github.com/GameplanePanel/gameplane/gp-module/pkg/validator"
)

// BuilderPortDef specifies a network port configuration during scaffolding.
type BuilderPortDef struct {
	Name          string `json:"name"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	Advertise     bool   `json:"advertise"`
}

// BuilderScaffoldRequest carries parameters for module scaffolding.
type BuilderScaffoldRequest struct {
	Name             string           `json:"name"`
	DisplayName      string           `json:"displayName,omitempty"`
	Archetype        string           `json:"archetype,omitempty"`
	Image            string           `json:"image,omitempty"`
	Ports            []BuilderPortDef `json:"ports,omitempty"`
	Categories       []string         `json:"categories,omitempty"`
	Summary          string           `json:"summary,omitempty"`
	StorageSize      string           `json:"storageSize,omitempty"`
	StorageMountPath string           `json:"storageMountPath,omitempty"`
}

// BuilderScaffoldResponse returns the in-memory scaffolded module files.
type BuilderScaffoldResponse struct {
	ModuleYaml   string `json:"moduleYaml"`
	TemplateYaml string `json:"templateYaml"`
	ReadmeMd     string `json:"readmeMd"`
	IconBase64   string `json:"iconBase64,omitempty"`
}

// BuilderValidateRequest carries module and template YAML for offline verification.
type BuilderValidateRequest struct {
	ModuleYaml   string `json:"moduleYaml"`
	TemplateYaml string `json:"templateYaml"`
}

// BuilderValidateResponse returns findings from instant offline verification.
type BuilderValidateResponse struct {
	Clean        bool                `json:"clean"`
	ErrorCount   int                 `json:"errorCount"`
	WarningCount int                 `json:"warningCount"`
	Findings     []validator.Finding `json:"findings"`
}

// BuilderPreviewRequest carries template YAML and simulation inputs.
type BuilderPreviewRequest struct {
	TemplateYaml string            `json:"templateYaml"`
	VersionID    string            `json:"versionId,omitempty"`
	Memory       string            `json:"memory,omitempty"`
	Config       map[string]string `json:"config,omitempty"`
}

// BuilderEffectiveEnvVar represents one computed environment variable with source provenance.
type BuilderEffectiveEnvVar struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Source string `json:"source"`
}

// BuilderPreviewPort represents a port preview.
type BuilderPreviewPort struct {
	Name          string `json:"name"`
	ContainerPort int    `json:"containerPort"`
	Protocol      string `json:"protocol"`
	Advertise     bool   `json:"advertise"`
}

// BuilderPreviewStorage represents storage preview settings.
type BuilderPreviewStorage struct {
	Size      string `json:"size"`
	MountPath string `json:"mountPath"`
}

// BuilderPreviewResponse returns runtime preview and simulation results.
type BuilderPreviewResponse struct {
	VersionID      string                   `json:"versionId,omitempty"`
	ResolvedImage  string                   `json:"resolvedImage"`
	EffectiveEnv   []BuilderEffectiveEnvVar `json:"effectiveEnv"`
	ComputedConfig map[string]string        `json:"computedConfig"`
	Ports          []BuilderPreviewPort     `json:"ports"`
	Storage        BuilderPreviewStorage    `json:"storage"`
}

// BuilderExportRequest carries the finalized module files for packaging or cluster installation.
type BuilderExportRequest struct {
	Name         string `json:"name"`
	ModuleYaml   string `json:"moduleYaml"`
	TemplateYaml string `json:"templateYaml"`
	ReadmeMd     string `json:"readmeMd"`
	IconBase64   string `json:"iconBase64,omitempty"`
	TargetSource string `json:"targetSource,omitempty"`
}

// BuilderExportResponse describes the cluster installation result when targetSource is provided.
type BuilderExportResponse struct {
	Installed  bool   `json:"installed"`
	ModuleName string `json:"moduleName"`
}

// BuilderArchetypesResponse returns the list of available archetypes in canonical order.
type BuilderArchetypesResponse struct {
	Archetypes []archetypes.ArchetypeDefinition `json:"archetypes"`
}

func (h modulesHandler) builderScaffold(w http.ResponseWriter, req *http.Request) {
	var body BuilderScaffoldRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid json request: %w", err))
		return
	}
	if body.Name == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("name is required"))
		return
	}

	ports := make([]archetypes.PortDef, len(body.Ports))
	for i, p := range body.Ports {
		ports[i] = archetypes.PortDef{
			Name:          p.Name,
			ContainerPort: p.ContainerPort,
			Protocol:      p.Protocol,
			Advertise:     p.Advertise,
		}
	}

	opts := scaffold.Options{
		Name:             body.Name,
		DisplayName:      body.DisplayName,
		Archetype:        body.Archetype,
		Image:            body.Image,
		Ports:            ports,
		StorageSize:      body.StorageSize,
		StorageMountPath: body.StorageMountPath,
		Categories:       body.Categories,
		Summary:          body.Summary,
	}

	files, err := scaffold.GenerateFiles(opts)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	resp := BuilderScaffoldResponse{
		ModuleYaml:   files.ModuleYAML,
		TemplateYaml: files.TemplateYAML,
		ReadmeMd:     files.ReadmeMD,
		IconBase64:   files.IconBase64,
	}
	writeJSON(w, resp)
}

func (h modulesHandler) builderValidate(w http.ResponseWriter, req *http.Request) {
	var body BuilderValidateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid json request: %w", err))
		return
	}

	var meta struct {
		Name string `json:"name" yaml:"name"`
	}
	_ = yaml.Unmarshal([]byte(body.ModuleYaml), &meta)
	dirName := meta.Name
	if dirName == "" {
		dirName = "module"
	}

	files := map[string][]byte{
		"module.yaml":   []byte(body.ModuleYaml),
		"template.yaml": []byte(body.TemplateYaml),
		"README.md":     []byte("# " + dirName + "\n"),
		"icon.png":      archetypes.PlaceholderIconBytes(),
	}

	report, err := validator.ValidateFiles(dirName, files, validator.ValidateOptions{
		Strict:  false,
		Offline: true,
	})
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	findings := report.Findings
	if findings == nil {
		findings = []validator.Finding{}
	}

	errorCount := 0
	warningCount := 0
	for _, f := range findings {
		switch f.Level {
		case validator.SeverityError:
			errorCount++
		case validator.SeverityWarn:
			warningCount++
		}
	}

	resp := BuilderValidateResponse{
		Clean:        report.Clean,
		ErrorCount:   errorCount,
		WarningCount: warningCount,
		Findings:     findings,
	}
	writeJSON(w, resp)
}

func (h modulesHandler) builderPreview(w http.ResponseWriter, req *http.Request) {
	var body BuilderPreviewRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid json request: %w", err))
		return
	}

	opts := preview.Options{
		TemplateYAML: []byte(body.TemplateYaml),
		VersionID:    body.VersionID,
		MemoryLimit:  body.Memory,
		UserConfig:   body.Config,
	}

	res, err := preview.GeneratePreview(opts)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	// Map source provenance for environment variables
	configSourceMap := make(map[string]string)
	for _, cf := range res.ConfigFields {
		configSourceMap[cf.Name] = cf.Source
	}

	effectiveEnv := make([]BuilderEffectiveEnvVar, 0, len(res.EffectiveEnv))
	for k, v := range res.EffectiveEnv {
		source := "template"
		if s, ok := configSourceMap[k]; ok && s != "" {
			source = s
		}
		effectiveEnv = append(effectiveEnv, BuilderEffectiveEnvVar{
			Name:   k,
			Value:  v,
			Source: source,
		})
	}

	ports := make([]BuilderPreviewPort, 0, len(res.Ports))
	for _, p := range res.Ports {
		ports = append(ports, BuilderPreviewPort{
			Name:          p.Name,
			ContainerPort: p.ContainerPort,
			Protocol:      p.Protocol,
			Advertise:     p.Advertise,
		})
	}

	computedConfig := res.ComputedConfig
	if computedConfig == nil {
		computedConfig = make(map[string]string)
	}

	resp := BuilderPreviewResponse{
		VersionID:      res.VersionID,
		ResolvedImage:  res.EffectiveImage,
		EffectiveEnv:   effectiveEnv,
		ComputedConfig: computedConfig,
		Ports:          ports,
		Storage: BuilderPreviewStorage{
			Size:      res.Storage.Size,
			MountPath: res.Storage.MountPath,
		},
	}
	writeJSON(w, resp)
}

func (h modulesHandler) builderExport(w http.ResponseWriter, req *http.Request) {
	var body BuilderExportRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid json request: %w", err))
		return
	}

	if body.Name == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("name is required"))
		return
	}
	if body.ModuleYaml == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("moduleYaml is required"))
		return
	}
	if body.TemplateYaml == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("templateYaml is required"))
		return
	}

	var meta struct {
		Name string `json:"name" yaml:"name"`
	}
	if err := yaml.Unmarshal([]byte(body.ModuleYaml), &meta); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("failed to parse module.yaml: %w", err))
		return
	}
	if meta.Name != body.Name {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("module name %q does not match module.yaml name %q", body.Name, meta.Name))
		return
	}

	readme := body.ReadmeMd
	if readme == "" {
		readme = "# " + body.Name + "\n"
	}

	files := map[string][]byte{
		"module.yaml":   []byte(body.ModuleYaml),
		"template.yaml": []byte(body.TemplateYaml),
		"README.md":     []byte(readme),
	}

	if body.IconBase64 != "" {
		decoded, err := base64.StdEncoding.DecodeString(body.IconBase64)
		if err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid base64 in iconBase64: %w", err))
			return
		}
		if len(decoded) == 0 {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("iconBase64 decodes to empty data"))
			return
		}
		files["icon.png"] = decoded
	} else {
		files["icon.png"] = archetypes.PlaceholderIconBytes()
	}

	report, err := validator.ValidateFiles(body.Name, files, validator.ValidateOptions{
		Strict:  false,
		Offline: true,
	})
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("validation failed: %w", err))
		return
	}
	if !report.Clean {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("module validation failed: module contains errors"))
		return
	}

	// Case 1: Direct cluster installation into an upload-type ModuleSource
	if body.TargetSource != "" {
		if err := h.requireUploadSource(req, body.TargetSource); err != nil {
			writeUploadSourceErr(w, req, err)
			return
		}

		cmName := "module-upload-" + body.Name
		cm := &corev1.ConfigMap{
			TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "ConfigMap"},
			ObjectMeta: metav1.ObjectMeta{
				Name:      cmName,
				Namespace: h.namespace,
				Labels: map[string]string{
					labelModuleUpload:     "true",
					labelUploadModuleName: body.Name,
				},
			},
			BinaryData: files,
		}

		cms := h.k.Typed.CoreV1().ConfigMaps(h.namespace)
		if _, err := cms.Create(req.Context(), cm, metav1.CreateOptions{}); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				httperr.Write(w, req, err)
				return
			}
			existing, err := cms.Get(req.Context(), cmName, metav1.GetOptions{})
			if err != nil {
				httperr.Write(w, req, err)
				return
			}
			if existing.Labels[labelModuleUpload] != "true" {
				httperr.WriteCode(w, req, http.StatusConflict,
					fmt.Errorf("configmap %q exists and is not a module upload", cmName))
				return
			}
			existing.Labels[labelUploadModuleName] = body.Name
			existing.BinaryData = files
			existing.Data = nil
			if _, err := cms.Update(req.Context(), existing, metav1.UpdateOptions{}); err != nil {
				httperr.Write(w, req, err)
				return
			}
		}

		writeJSONCreated(w, BuilderExportResponse{
			Installed:  true,
			ModuleName: body.Name,
		})
		return
	}

	// Case 2: Download .tar.gz bundle
	archive, _, err := packager.CreateArchiveFromFiles(files, packager.DefaultPackageLimits)
	if err != nil {
		httperr.WriteCode(w, req, http.StatusInternalServerError, fmt.Errorf("failed to create archive: %w", err))
		return
	}

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", body.Name+".tar.gz"))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(archive)
}

func (h modulesHandler) builderArchetypes(w http.ResponseWriter, _ *http.Request) {
	all := archetypes.AllArchetypes()
	if len(all) == 0 {
		writeJSON(w, BuilderArchetypesResponse{Archetypes: []archetypes.ArchetypeDefinition{}})
		return
	}

	// Define the deterministic order: steamcmd, java, generic, then alphabetical
	orderedKeys := make([]string, 0, len(all))
	order := []string{"steamcmd", "java", "generic"}

	// Add ordered keys first
	for _, key := range order {
		if _, ok := all[key]; ok {
			orderedKeys = append(orderedKeys, key)
		}
	}

	// Collect remaining keys
	remaining := make([]string, 0)
	for key := range all {
		found := false
		for _, orderedKey := range orderedKeys {
			if key == orderedKey {
				found = true
				break
			}
		}
		if !found {
			remaining = append(remaining, key)
		}
	}

	// Sort remaining keys alphabetically
	sort.Strings(remaining)
	orderedKeys = append(orderedKeys, remaining...)

	// Build ordered response
	result := make([]archetypes.ArchetypeDefinition, len(orderedKeys))
	for i, key := range orderedKeys {
		result[i] = all[key]
	}

	writeJSON(w, BuilderArchetypesResponse{Archetypes: result})
}
