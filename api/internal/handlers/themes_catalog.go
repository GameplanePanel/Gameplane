package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/themepkg"
)

// themeStatusValid is the catalog status of an entry that passed validation
// and signature checks. Only valid entries expose tokens, CSS and previews.
const themeStatusValid = "valid"

// genericSourceError is the catalog text of a failing source for callers
// without themes:manage. The raw last error names source URLs and dial targets.
const genericSourceError = "This source could not be refreshed."

// themeCatalogHandler serves the read-only theme catalog (contracts/themes-api.md,
// "Catalog"). Every read is answered from the database cache that the indexer
// fills; no request contacts a registry.
type themeCatalogHandler struct {
	store *db.Store
}

// themeInstalledRef is the caller's installed copy of a catalog theme.
type themeInstalledRef struct {
	Version string `json:"version"`
}

// themeCatalogItem is one row of GET /themes/catalog and the head of the
// detail response.
type themeCatalogItem struct {
	ID              int64              `json:"id"`
	Source          string             `json:"source"`
	Name            string             `json:"name"`
	DisplayName     string             `json:"displayName"`
	Author          string             `json:"author"`
	Description     string             `json:"description"`
	Tags            []string           `json:"tags"`
	License         string             `json:"license"`
	Version         string             `json:"version"`
	Modes           []string           `json:"modes"`
	Status          string             `json:"status"`
	LowContrast     bool               `json:"lowContrast"`
	Installed       *themeInstalledRef `json:"installed,omitempty"`
	UpdateAvailable bool               `json:"updateAvailable"`
}

// themeCatalogSourceError names an enabled source whose last refresh failed,
// so the dashboard can say why some themes may be stale (US1.4).
type themeCatalogSourceError struct {
	Name      string `json:"name"`
	LastError string `json:"lastError"`
}

// themeCatalogResp is the GET /themes/catalog body.
type themeCatalogResp struct {
	Themes  []themeCatalogItem        `json:"themes"`
	Sources []themeCatalogSourceError `json:"sources"`
}

// themeCatalogDetail is the GET /themes/catalog/{id} body. Tokens and CSS are
// present only for valid entries; the dashboard renders them from here.
type themeCatalogDetail struct {
	themeCatalogItem
	Changelog string          `json:"changelog"`
	Homepage  string          `json:"homepage"`
	Problems  json.RawMessage `json:"problems"`
	Tokens    json.RawMessage `json:"tokens,omitempty"`
	CSS       *string         `json:"css,omitempty"`
}

// themeMetaView is the list-facing part of the stored metadata JSON, written
// by the indexer.
type themeMetaView struct {
	DisplayName string   `json:"displayName"`
	Author      string   `json:"author"`
	Description string   `json:"description"`
	Tags        []string `json:"tags"`
	License     string   `json:"license"`
	Homepage    string   `json:"homepage"`
	Changelog   string   `json:"changelog"`
}

// installedKey identifies an installed theme by source id and theme name,
// never by source name (data-model.md).
type installedKey struct {
	sourceID int64
	name     string
}

// list answers GET /themes/catalog?q=&tag=&source=. q matches the theme name or
// display name, tag matches one of the manifest tags, both case-insensitively.
func (h *themeCatalogHandler) list(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	query := req.URL.Query()
	entries, err := h.store.ListThemeCatalog(ctx, db.ThemeCatalogFilter{Source: query.Get("source")})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	installed, err := h.installedIndex(ctx, userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	search := strings.ToLower(strings.TrimSpace(query.Get("q")))
	tag := strings.ToLower(strings.TrimSpace(query.Get("tag")))

	items := []themeCatalogItem{}
	for _, e := range entries {
		item, meta := catalogItemFor(e, installed)
		if search != "" && !strings.Contains(strings.ToLower(e.Name), search) &&
			!strings.Contains(strings.ToLower(meta.DisplayName), search) {
			continue
		}
		if tag != "" && !hasTag(meta.Tags, tag) {
			continue
		}
		items = append(items, item)
	}

	sources, err := h.store.ListThemeSources(ctx)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	// Only a holder of themes:manage (a control-plane permission) sees the raw
	// last error. Everyone else gets the generic text.
	canManage := auth.UserFromContext(req.Context()).Can("themes:manage", false, "", "")
	failing := []themeCatalogSourceError{}
	for _, src := range sources {
		if src.Enabled && src.LastError != nil {
			msg := genericSourceError
			if canManage {
				msg = *src.LastError
			}
			failing = append(failing, themeCatalogSourceError{Name: src.Name, LastError: msg})
		}
	}
	writeJSON(w, themeCatalogResp{Themes: items, Sources: failing})
}

// get answers GET /themes/catalog/{id} with the entry, its changelog and
// homepage, its problems, and tokens and CSS when the entry is valid.
func (h *themeCatalogHandler) get(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	entry, ok := h.visibleEntry(w, req)
	if !ok {
		return
	}
	installed, err := h.installedIndex(ctx, userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	item, meta := catalogItemFor(entry, installed)
	detail := themeCatalogDetail{
		themeCatalogItem: item,
		Changelog:        meta.Changelog,
		Homepage:         meta.Homepage,
		Problems:         rawJSONOrDefault(entry.Problems, "[]"),
	}
	if entry.Status == themeStatusValid {
		detail.Tokens = rawJSONOrDefault(entry.Tokens, "{}")
		detail.CSS = entry.CSS
	}
	writeJSON(w, detail)
}

// preview answers GET /themes/catalog/{id}/preview/{light|dark} with the stored
// image. Previews come only from valid entries, so invalid or unverified themes
// have none.
func (h *themeCatalogHandler) preview(w http.ResponseWriter, req *http.Request) {
	if _, ok := callerID(w, req); !ok {
		return
	}
	entry, ok := h.visibleEntry(w, req)
	if !ok {
		return
	}
	if entry.Status != themeStatusValid {
		httperr.WriteMessage(w, req, messages.New("server.themes.not_found", nil))
		return
	}
	data, err := h.store.GetThemeCatalogPreview(req.Context(), entry.ID, chi.URLParam(req, "mode"))
	if errors.Is(err, db.ErrThemeNotFound) {
		httperr.WriteMessage(w, req, messages.New("server.themes.not_found", nil))
		return
	}
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	contentType, _, _, err := themepkg.PreviewInfo(data)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	hdr := w.Header()
	hdr.Set("Content-Type", contentType)
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "private, max-age=3600")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// visibleEntry loads the catalog entry named by the {id} path parameter. An
// unparsable id, an unknown entry and an entry of a disabled source all answer
// 404, and it reports false after writing that response.
func (h *themeCatalogHandler) visibleEntry(w http.ResponseWriter, req *http.Request) (db.ThemeCatalogEntry, bool) {
	ctx := req.Context()
	notFound := func() {
		httperr.WriteMessage(w, req, messages.New("server.themes.not_found", nil))
	}
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil || id <= 0 {
		notFound()
		return db.ThemeCatalogEntry{}, false
	}
	entry, err := h.store.GetThemeCatalogEntry(ctx, id)
	if errors.Is(err, db.ErrThemeNotFound) {
		notFound()
		return db.ThemeCatalogEntry{}, false
	}
	if err != nil {
		httperr.Write(w, req, err)
		return db.ThemeCatalogEntry{}, false
	}
	src, err := h.store.GetThemeSourceByID(ctx, entry.SourceID)
	if errors.Is(err, db.ErrThemeNotFound) || (err == nil && !src.Enabled) {
		notFound()
		return db.ThemeCatalogEntry{}, false
	}
	if err != nil {
		httperr.Write(w, req, err)
		return db.ThemeCatalogEntry{}, false
	}
	return entry, true
}

// installedIndex maps the caller's installed themes by source id and name. Rows
// whose source was removed have no source id and are left out.
func (h *themeCatalogHandler) installedIndex(ctx context.Context, userID int64) (map[installedKey]db.InstalledTheme, error) {
	rows, err := h.store.ListInstalledThemes(ctx, userID)
	if err != nil {
		return nil, err
	}
	idx := make(map[installedKey]db.InstalledTheme, len(rows))
	for _, t := range rows {
		if t.SourceID != nil {
			idx[installedKey{sourceID: *t.SourceID, name: t.ThemeName}] = t
		}
	}
	return idx, nil
}

// catalogItemFor builds the list shape of one entry for the caller, and returns
// the decoded metadata for the callers that need the detail fields. An update
// is offered only when the newer entry is valid and the installed copy is from
// the same source.
func catalogItemFor(e db.ThemeCatalogEntry, installed map[installedKey]db.InstalledTheme) (themeCatalogItem, themeMetaView) {
	var meta themeMetaView
	// The metadata is written by the indexer; an undecodable row shows as an
	// entry without descriptive fields rather than failing the whole catalog.
	_ = json.Unmarshal([]byte(e.Metadata), &meta)
	item := themeCatalogItem{
		ID:          e.ID,
		Source:      e.SourceName,
		Name:        e.Name,
		DisplayName: meta.DisplayName,
		Author:      meta.Author,
		Description: meta.Description,
		Tags:        nonNilStrings(meta.Tags),
		License:     meta.License,
		Version:     e.Version,
		Modes:       themeModes(e.Tokens),
		Status:      e.Status,
		LowContrast: e.LowContrast,
	}
	if inst, ok := installed[installedKey{sourceID: e.SourceID, name: e.Name}]; ok {
		item.Installed = &themeInstalledRef{Version: inst.Version}
		item.UpdateAvailable = e.Status == themeStatusValid && semverDescending(e.Version, inst.Version)
	}
	return item, meta
}

// themeModes lists the modes a token document carries, in the order light,
// dark. Unparsable tokens offer no mode.
func themeModes(tokens string) []string {
	modes := []string{}
	var parsed map[string]json.RawMessage
	if err := json.Unmarshal([]byte(tokens), &parsed); err != nil {
		return modes
	}
	for _, mode := range []string{"light", "dark"} {
		if _, ok := parsed[mode]; ok {
			modes = append(modes, mode)
		}
	}
	return modes
}

// hasTag reports whether tag (already lower case) matches one of tags.
func hasTag(tags []string, tag string) bool {
	for _, t := range tags {
		if strings.ToLower(t) == tag {
			return true
		}
	}
	return false
}

// nonNilStrings keeps a JSON array an array, never null.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// rawJSONOrDefault returns a stored JSON document, or def when it is empty.
func rawJSONOrDefault(stored, def string) json.RawMessage {
	if strings.TrimSpace(stored) == "" {
		stored = def
	}
	return json.RawMessage(stored)
}
