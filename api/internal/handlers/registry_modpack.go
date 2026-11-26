package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
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

// Phases written by the operator (status.modpack.phase). "Pending" is also
// synthesised by modpackPhase for a change the operator has not seen yet.
const (
	modpackPhasePending      = "Pending"
	modpackPhaseBackingUp    = "BackingUp"
	modpackPhaseApplying     = "Applying"
	modpackPhaseBackupFailed = "BackupFailed"
)

// blockedCurseForgeKeyNotShared is providerInfo.modpacksBlockedReason when the
// selected version's image has no built-in CurseForge key and sharing is off.
const blockedCurseForgeKeyNotShared = "curseforge_key_not_shared"

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
	// ServerVersion is an optional template version id (spec 035 FR-014). When
	// it differs from the server's current version, the install switches the
	// server to it in the same write as the pack.
	ServerVersion string `json:"serverVersion"`
}

// modpackRemoveRequest is the JSON stored in the modpack-remove annotation.
type modpackRemoveRequest struct {
	RequestID   string `json:"requestId"`
	BackupFirst bool   `json:"backupFirst"`
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

// modpackMissingFile is one row of MODS_NEED_DOWNLOAD.txt (parsed in modpack_missing.go).
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
	// BackupDestination is the name of the backup destination the pre-change
	// backup goes to (alphabetically first when several exist); empty when none.
	BackupDestination string `json:"backupDestination,omitempty"`
}

// getModpack serves GET /servers/{name}/modpack. A legacy server (pack env in
// spec.env, no spec.modpack) is adopted first (modpack_adopt.go, research R6).
func (h *registryHandler) getModpack(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	gs := h.adoptLegacy(req.Context(), target)
	writeJSON(w, h.modpackViewFor(req, target, gs, true))
}

// modpackViewFor builds the GET body from a server object (the one just
// written, for POST/DELETE responses). withMissing reads MODS_NEED_DOWNLOAD.txt
// through the agent (GET only; POST/DELETE skip the agent round trip).
func (h *registryHandler) modpackViewFor(req *http.Request, target *modTarget, gs *unstructured.Unstructured, withMissing bool) modpackView {
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
	view.Phase = modpackPhase(gs)
	view.Message, _, _ = unstructured.NestedString(st, "message")
	view.BackupName, _, _ = unstructured.NestedString(st, "backupName")
	view.KeyShared, _, _ = unstructured.NestedBool(st, "keyShared")
	view.BackupDestination = h.backupDestinationName(req, target)
	view.BackupAvailable = view.BackupDestination != ""
	if withMissing {
		h.fillMissingFiles(req, target, gs, &view)
	}
	return view
}

// modpackPhase is status.modpack.phase, except that a change the operator has
// not picked up yet (spec.modpack differs from status.modpack.applied, or a
// removal is requested while a pack is still applied) reads "Pending". The one
// exception: BackupFailed for the request that is current (same request id and
// the target it started from still desired) stays visible, because the
// operator leaves spec unequal to applied after refusing it; an unrelated spec
// edit does not hide it.
func modpackPhase(gs *unstructured.Unstructured) string {
	st, _, _ := unstructured.NestedMap(gs.Object, "status", "modpack")
	phase, _, _ := unstructured.NestedString(st, "phase")
	spec, hasSpec, _ := unstructured.NestedMap(gs.Object, "spec", "modpack")
	applied, hasApplied, _ := unstructured.NestedMap(st, "applied")
	removing := gs.GetAnnotations()[modpackRemoveAnnotation] != ""
	waiting := (hasSpec && !samePack(spec, applied, hasApplied)) || (!hasSpec && hasApplied && removing)
	if !waiting {
		return phase
	}
	if phase == modpackPhaseBackupFailed {
		target, hasTarget, _ := unstructured.NestedMap(st, "target")
		requestID, _, _ := unstructured.NestedString(st, "requestId")
		sameTarget := (hasSpec && samePack(spec, target, hasTarget)) || (!hasSpec && !hasTarget)
		if sameTarget && requestID == currentModpackRequestID(gs) {
			return phase
		}
	}
	return modpackPhasePending
}

// currentModpackRequestID mirrors the operator's modpackRequestID: the removal
// request id when a removal is requested, else spec.modpack.requestId.
func currentModpackRequestID(gs *unstructured.Unstructured) string {
	ann := gs.GetAnnotations()
	if raw := ann[modpackRemoveAnnotation]; raw != "" {
		var req modpackRemoveRequest
		if err := json.Unmarshal([]byte(raw), &req); err != nil {
			return raw
		}
		return req.RequestID
	}
	id, _, _ := unstructured.NestedString(gs.Object, "spec", "modpack", "requestId")
	return id
}

// modpackChangeInProgress reports that a previous change has not finished:
// POST and DELETE answer 409 server.modpack.change_in_progress meanwhile.
// Applied, BackupFailed (for the current request) and "never managed" are
// all free to change.
func modpackChangeInProgress(gs *unstructured.Unstructured) bool {
	switch modpackPhase(gs) {
	case modpackPhasePending, modpackPhaseBackingUp, modpackPhaseApplying:
		return true
	}
	return false
}

// currentModpack is the pack the server has now: spec.modpack, else the
// applied pack of a removal still pending. Nil when none.
func currentModpack(gs *unstructured.Unstructured) *modpackActive {
	if spec, ok, _ := unstructured.NestedMap(gs.Object, "spec", "modpack"); ok {
		return activeFrom(spec, false)
	}
	if applied, ok, _ := unstructured.NestedMap(gs.Object, "status", "modpack", "applied"); ok {
		return activeFrom(applied, true)
	}
	return nil
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

// backupDestinationName returns the name of the backup destination in the
// server's namespace (a Secret labelled gameplane.local/backup-destination=true,
// the same discovery the /backup-destinations routes use, destinations.go),
// or "" when there is none. When several exist the alphabetically first name
// wins, matching the operator's choice (gameserver_modpack.go).
func (h *registryHandler) backupDestinationName(req *http.Request, target *modTarget) string {
	if target.k == nil || target.k.Typed == nil {
		return ""
	}
	list, err := target.k.Typed.CoreV1().Secrets(target.namespace).List(req.Context(), metav1.ListOptions{
		LabelSelector: destinationLabel + "=true",
	})
	if err != nil || len(list.Items) == 0 {
		return ""
	}
	names := make([]string, 0, len(list.Items))
	for _, s := range list.Items {
		names = append(names, s.Name)
	}
	slices.Sort(names)
	return names[0]
}

// modpacksBlockedReason is providerInfo.modpacksBlockedReason: non-empty when
// the provider declares needsKeyImageTags, the server's selected version image
// carries one of those tags, and the administrator has not enabled key
// sharing. The Secret is read only when the first two hold.
func (h *registryHandler) modpacksBlockedReason(ctx context.Context, target *modTarget, p providerCfg) string {
	selected, _, _ := unstructured.NestedString(target.server.Object, "spec", "version")
	return h.blockedReasonFor(ctx, target, p, activeVersionEntry(target.template, selected))
}

// blockedReasonFor is modpacksBlockedReason for the server version entry the
// pack would run on (the image decides whether the CurseForge key is needed).
func (h *registryHandler) blockedReasonFor(ctx context.Context, target *modTarget, p providerCfg, entry map[string]any) string {
	if p.modpacks == nil || len(p.modpacks.needsKeyImageTags) == 0 {
		return ""
	}
	if entry == nil {
		return ""
	}
	image, _ := entry["image"].(string)
	tag := imageTag(image)
	if tag == "" || !slices.Contains(p.modpacks.needsKeyImageTags, tag) {
		return ""
	}
	// The share flag lives on the management cluster's Secret; a remote
	// cluster's operator reads its own namespace and never gets the key, so
	// it counts as not shared there.
	if target.cluster == scope.DefaultCluster && h.keyShared(ctx) {
		return ""
	}
	return blockedCurseForgeKeyNotShared
}

// keyShared reads the share annotation of the managed CurseForge key Secret
// (registry_secret.go:curseforgeShareState). False with no home client.
func (h *registryHandler) keyShared(ctx context.Context) bool {
	share, _ := curseforgeShareState(ctx, h.home, h.controlNS)
	return share
}

// imageTag returns the tag of an image reference: the part after the last ":"
// of the last path element and before "@digest". "" when there is no tag.
func imageTag(image string) string {
	if i := strings.IndexByte(image, '@'); i >= 0 {
		image = image[:i]
	}
	slash := strings.LastIndexByte(image, '/')
	colon := strings.LastIndexByte(image, ':')
	if colon > slash {
		return image[colon+1:]
	}
	return ""
}

// removeModpack serves DELETE /servers/{name}/modpack?backupFirst=true|false.
// It deletes spec.modpack and asks the operator (annotation) to remove the pack,
// wipe the mods folder and optionally back up first. Allowed when spec.modpack is
// set, when a removal failed (BackupFailed, annotation still present: the retry
// replaces the annotation) or when only status.modpack.applied is left. 404 only
// when neither spec.modpack nor status.modpack.applied is set.
func (h *registryHandler) removeModpack(w http.ResponseWriter, req *http.Request) {
	target, ok := loadModTarget(w, req, h.clients, h.k)
	if !ok {
		return
	}
	backupFirst := false
	if raw := req.URL.Query().Get("backupFirst"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, errModpackBackupFirst)
			return
		}
		backupFirst = v
	}
	gs := target.server
	spec, hasSpec, _ := unstructured.NestedMap(gs.Object, "spec", "modpack")
	applied, hasApplied, _ := unstructured.NestedMap(gs.Object, "status", "modpack", "applied")
	if !hasSpec && !hasApplied {
		httperr.WriteMessage(w, req, messages.New("server.modpack.not_found", nil))
		return
	}
	if backupFirst && h.backupDestinationName(req, target) == "" {
		httperr.WriteMessage(w, req, messages.New("server.modpack.backup_unavailable", nil))
		return
	}
	// A failed removal is retried by calling DELETE again; every other
	// unfinished change blocks.
	retry := !hasSpec && gs.GetAnnotations()[modpackRemoveAnnotation] != "" && modpackPhase(gs) == modpackPhaseBackupFailed
	if !retry && modpackChangeInProgress(gs) {
		httperr.WriteMessage(w, req, messages.New("server.modpack.change_in_progress", nil))
		return
	}
	removed := activeFrom(applied, false)
	if hasSpec {
		removed = activeFrom(spec, false)
	}
	requestID, err := newModpackRequestID()
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	marker, err := json.Marshal(modpackRemoveRequest{RequestID: requestID, BackupFirst: backupFirst})
	if err != nil {
		httperr.Write(w, req, fmt.Errorf("encode modpack removal request: %w", err))
		return
	}
	updated, err := updateModTarget(req.Context(), target, func(current *unstructured.Unstructured) {
		unstructured.RemoveNestedField(current.Object, "spec", "modpack")
		ann := current.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann[modpackRemoveAnnotation] = string(marker)
		current.SetAnnotations(ann)
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	h.auditModpack(req, gs.GetName(), "remove", removed.Provider, removed.Project, removed.Version, "")
	writeJSONStatus(w, http.StatusAccepted, h.modpackViewFor(req, target, updated, false))
}

// newModpackRequestID returns a random 128-bit hex id for a removal request.
func newModpackRequestID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate modpack request id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

// auditModpack writes the explicit audit row of a modpack write (action is
// install, change or remove). Provider, project and version are validated
// slugs; the CurseForge key never reaches this code. A failed audit write is
// logged and the generic audit middleware row stays as the fallback.
// serverSwitch is "<old>-><new>" when the install also switched the server
// version (FR-014), else "".
func (h *registryHandler) auditModpack(req *http.Request, serverName, action, provider, project, version, serverSwitch string) {
	if h.audit == nil {
		return
	}
	reason := fmt.Sprintf("server.modpack.%s provider=%s project=%s version=%s", action, provider, project, version)
	if serverSwitch != "" {
		reason += " server_version=" + serverSwitch
	}
	if err := h.audit.WriteSync(req.Context(), req.Method, req.URL.Path, serverName, reason, http.StatusAccepted); err != nil {
		slog.Warn("modpack audit write failed", "server", serverName, "action", action, "err", err)
		return
	}
	audit.MarkRecorded(req.Context())
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
