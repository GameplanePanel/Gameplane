package handlers

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
)

// Spec 035: GameServer.spec.modpack is the only place the API records a pack;
// the operator renders the env and reports progress in status.modpack. The
// wire shapes follow specs/035-curseforge-modpacks/contracts/api-modpack.md.

// modpackRemoveAnnotation mirrors v1alpha1.ModpackRemoveAnnotation (the API
// reads unstructured objects and does not import the operator types).
const modpackRemoveAnnotation = "gameplane.local/modpack-remove"

// modpackProjectRe and modpackVersionRe mirror the GameServer CRD patterns of
// spec.modpack.project and spec.modpack.version, so a bad value is a 400 from
// us instead of an apiserver 422.
var (
	modpackProjectRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	modpackVersionRe = regexp.MustCompile(`^[A-Za-z0-9._-]{0,64}$`)
)

// modpackInstallBody is the POST /servers/{name}/modpack request. Ref is the
// pre-035 body ({"ref": "<slug>"} plus ?provider=); it is accepted as
// project with no pinned version so the current dashboard keeps working until
// the web tasks switch to the new body.
type modpackInstallBody struct {
	Provider    string `json:"provider"`
	Project     string `json:"project"`
	Version     string `json:"version"`
	Title       string `json:"title"`
	VersionName string `json:"versionName"`
	BackupFirst bool   `json:"backupFirst"`
	Ref         string `json:"ref"`
}

type modpackActive struct {
	Provider       string `json:"provider"`
	Project        string `json:"project"`
	Version        string `json:"version"`
	Title          string `json:"title"`
	VersionName    string `json:"versionName"`
	Pinned         bool   `json:"pinned"`
	PendingRemoval bool   `json:"pendingRemoval,omitempty"`
}

// modpackMissingFile is one row of MODS_NEED_DOWNLOAD.txt (filled by a later task).
type modpackMissingFile struct {
	Mod          string `json:"mod"`
	VersionName  string `json:"versionName"`
	Filename     string `json:"filename"`
	DownloadPage string `json:"downloadPage"`
}

type modpackView struct {
	Active              *modpackActive       `json:"active"`
	Phase               string               `json:"phase"`
	Message             string               `json:"message"`
	BackupName          string               `json:"backupName"`
	KeyShared           bool                 `json:"keyShared"`
	MissingFiles        []modpackMissingFile `json:"missingFiles"`
	MissingFilesUnknown bool                 `json:"missingFilesUnknown"`
	DownloadsPath       string               `json:"downloadsPath"`
	BackupAvailable     bool                 `json:"backupAvailable"`
}

// getModpack serves GET /servers/{name}/modpack. Missing files stay empty
// until the missing-files task reads them through the agent.
func (h *registryHandler) getModpack(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	writeJSON(w, h.modpackViewFor(req, target, target.server))
}

// modpackViewFor builds the GET body from a server object (the one just
// written, for POST/DELETE responses).
func (h *registryHandler) modpackViewFor(req *http.Request, target *modTarget, gs *unstructured.Unstructured) modpackView {
	view := modpackView{MissingFiles: []modpackMissingFile{}}
	spec, hasSpec, _ := unstructured.NestedMap(gs.Object, "spec", "modpack")
	st, _, _ := unstructured.NestedMap(gs.Object, "status", "modpack")
	applied, hasApplied, _ := unstructured.NestedMap(st, "applied")
	removing := gs.GetAnnotations()[modpackRemoveAnnotation] != ""

	switch {
	case hasSpec:
		view.Active = activeFrom(spec, false)
	case hasApplied && removing:
		view.Active = activeFrom(applied, true)
	}
	view.Phase, _, _ = unstructured.NestedString(st, "phase")
	view.Message, _, _ = unstructured.NestedString(st, "message")
	view.BackupName, _, _ = unstructured.NestedString(st, "backupName")
	view.KeyShared, _, _ = unstructured.NestedBool(st, "keyShared")
	// Written but not yet picked up by the operator: spec and applied differ.
	if (hasSpec && !samePack(spec, applied, hasApplied)) || (!hasSpec && hasApplied && removing) {
		view.Phase = "Pending"
	}
	view.BackupAvailable = h.hasBackupDestination(req, target)
	return view
}

func activeFrom(m map[string]any, pendingRemoval bool) *modpackActive {
	str := func(k string) string { s, _ := m[k].(string); return s }
	a := &modpackActive{
		Provider: str("provider"), Project: str("project"), Version: str("version"),
		Title: str("title"), VersionName: str("versionName"), PendingRemoval: pendingRemoval,
	}
	a.Pinned = a.Version != ""
	return a
}

func samePack(spec, applied map[string]any, hasApplied bool) bool {
	if !hasApplied {
		return false
	}
	for _, k := range []string{"provider", "project", "version", "title", "versionName"} {
		a, _ := spec[k].(string)
		b, _ := applied[k].(string)
		if a != b {
			return false
		}
	}
	return true
}

// hasBackupDestination reports whether the server's namespace holds a backup
// destination (a Secret labelled gameplane.local/backup-destination=true, the
// same discovery the /backup-destinations routes use, destinations.go).
func (h *registryHandler) hasBackupDestination(req *http.Request, target *modTarget) bool {
	if target.k == nil || target.k.Typed == nil {
		return false
	}
	list, err := target.k.Typed.CoreV1().Secrets(target.namespace).List(req.Context(), metav1.ListOptions{
		LabelSelector: destinationLabel + "=true",
		Limit:         1,
	})
	return err == nil && len(list.Items) > 0
}

// modpackSpecFrom turns a validated install body into the spec.modpack map.
func modpackSpecFrom(provider string, b modpackInstallBody) map[string]any {
	m := map[string]any{"provider": provider, "project": b.Project}
	if b.Version != "" {
		m["version"] = b.Version
	}
	if b.Title != "" {
		m["title"] = truncate(b.Title, 128)
	}
	if b.VersionName != "" {
		m["versionName"] = truncate(b.VersionName, 128)
	}
	if b.BackupFirst {
		m["backupFirst"] = true
	}
	return m
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// normalizeModpackBody resolves the legacy {"ref"} body and validates the
// fields against the CRD patterns. ok is false after writing a 400.
func normalizeModpackBody(w http.ResponseWriter, req *http.Request, b *modpackInstallBody) bool {
	b.Project = strings.TrimSpace(b.Project)
	if b.Project == "" {
		b.Project = strings.TrimSpace(b.Ref)
	}
	b.Version = strings.TrimSpace(b.Version)
	if !modpackProjectRe.MatchString(b.Project) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errModpackProject)
		return false
	}
	if !modpackVersionRe.MatchString(b.Version) {
		httperr.WriteCode(w, req, http.StatusBadRequest, errModpackVersion)
		return false
	}
	return true
}

// writeJSONStatus is writeJSON with an explicit status. Content-Type is set
// before WriteHeader, which snapshots the headers.
func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
