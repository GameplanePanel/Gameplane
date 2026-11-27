package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
)

// themeInstalledHandler serves the caller's installed themes (spec 043,
// contracts/themes-api.md, "Installed themes"). Every route acts on the caller's
// own rows: another user's id answers 404, the same as an unknown id.
type themeInstalledHandler struct {
	store *db.Store
	svc   *themes.Service
}

// installedThemeItem is one row of GET /users/me/themes and the head of the
// detail response. SourceRemoved and UpdateAvailable are derived per request
// from the enabled catalog (data-model.md), never stored.
type installedThemeItem struct {
	ID              int64    `json:"id"`
	Source          string   `json:"source"`
	Name            string   `json:"name"`
	DisplayName     string   `json:"displayName"`
	Version         string   `json:"version"`
	Modes           []string `json:"modes"`
	UpdateAvailable bool     `json:"updateAvailable"`
	SourceRemoved   bool     `json:"sourceRemoved"`
	Selected        bool     `json:"selected"`
}

// installedThemeDetail is the body of one installed theme, as the dashboard
// renders it: the list shape plus the tokens and the sanitized CSS.
type installedThemeDetail struct {
	installedThemeItem
	Tokens json.RawMessage `json:"tokens"`
	CSS    *string         `json:"css"`
}

// installBody is the POST /users/me/themes request.
type installBody struct {
	CatalogID int64 `json:"catalogId"`
	Select    bool  `json:"select"`
}

// MountInstalledThemes registers the per-user theme routes under
// /users/me/themes. It is separate from MountThemes so the catalog and the
// installs can be wired by different callers, and so the install service (which
// holds the store and the indexer) is passed in once.
func MountInstalledThemes(r chi.Router, store *db.Store, svc *themes.Service) {
	h := &themeInstalledHandler{store: store, svc: svc}
	r.Route("/users/me/themes", func(r chi.Router) {
		r.Get("/", h.list)
		r.Post("/", h.create)
		r.Get("/{id}", h.get)
		r.Post("/{id}/update", h.update)
		r.Delete("/{id}", h.remove)
	})
}

// list answers GET /users/me/themes.
func (h *themeInstalledHandler) list(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	installed, err := h.store.ListInstalledThemes(ctx, userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	selected, err := h.selectedID(ctx, userID)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	catalog, err := h.catalogIndex(ctx)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	items := []installedThemeItem{}
	for _, t := range installed {
		items = append(items, installedItemFor(t, catalog, selected))
	}
	writeJSON(w, items)
}

// get answers GET /users/me/themes/{id} with the installed theme, its tokens
// and its CSS.
func (h *themeInstalledHandler) get(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	t, ok := h.ownInstalled(w, req, userID)
	if !ok {
		return
	}
	detail, err := h.detail(ctx, userID, t)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, detail)
}

// create answers POST /users/me/themes: it installs a valid catalog entry for
// the caller and, when select is true, makes it the caller's theme.
func (h *themeInstalledHandler) create(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	var body installBody
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return
	}
	t, err := h.svc.Install(ctx, userID, body.CatalogID, body.Select)
	if err != nil {
		writeInstalledErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeInstall,
		"source", t.SourceName, "theme", t.ThemeName, "to", t.Version, "digest", t.Digest))
	detail, err := h.detail(ctx, userID, t)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSONCreated(w, detail)
}

// update answers POST /users/me/themes/{id}/update: it moves the installed copy
// to the newer valid version from the same source.
func (h *themeInstalledHandler) update(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	id, ok := installedThemeID(w, req)
	if !ok {
		return
	}
	before, after, err := h.svc.Update(ctx, userID, id)
	if err != nil {
		writeInstalledErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeUpdate,
		"source", after.SourceName, "theme", after.ThemeName,
		"from", before.Version, "to", after.Version, "digest", after.Digest))
	detail, err := h.detail(ctx, userID, after)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, detail)
}

// remove answers DELETE /users/me/themes/{id} with 204. When the theme was
// selected, the preferences fall back to Pink in the same transaction.
func (h *themeInstalledHandler) remove(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	userID, ok := callerID(w, req)
	if !ok {
		return
	}
	id, ok := installedThemeID(w, req)
	if !ok {
		return
	}
	removed, err := h.svc.Remove(ctx, userID, id)
	if err != nil {
		writeInstalledErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeRemove,
		"source", removed.SourceName, "theme", removed.ThemeName,
		"from", removed.Version, "digest", removed.Digest))
	w.WriteHeader(http.StatusNoContent)
}

// detail builds the detail body of one installed theme for the caller.
func (h *themeInstalledHandler) detail(ctx context.Context, userID int64, t db.InstalledTheme) (installedThemeDetail, error) {
	selected, err := h.selectedID(ctx, userID)
	if err != nil {
		return installedThemeDetail{}, err
	}
	catalog, err := h.catalogIndex(ctx)
	if err != nil {
		return installedThemeDetail{}, err
	}
	return installedThemeDetail{
		installedThemeItem: installedItemFor(t, catalog, selected),
		Tokens:             rawJSONOrDefault(t.Tokens, "{}"),
		CSS:                t.CSS,
	}, nil
}

// ownInstalled loads the installed theme named by the {id} path parameter from
// the caller's own rows. It writes the 404 and reports false when the id is
// malformed, unknown, or another user's.
func (h *themeInstalledHandler) ownInstalled(w http.ResponseWriter, req *http.Request, userID int64) (db.InstalledTheme, bool) {
	id, ok := installedThemeID(w, req)
	if !ok {
		return db.InstalledTheme{}, false
	}
	t, err := h.store.GetInstalledTheme(req.Context(), userID, id)
	if err != nil {
		writeInstalledErr(w, req, err)
		return db.InstalledTheme{}, false
	}
	return t, true
}

// selectedID returns the installed theme the caller's preferences select, or nil
// when the caller has selected a preset or custom colors.
func (h *themeInstalledHandler) selectedID(ctx context.Context, userID int64) (*int64, error) {
	prefs, err := h.store.GetPreferences(ctx, userID)
	if err != nil {
		return nil, err
	}
	if prefs.ThemeType != "installed" {
		return nil, nil
	}
	return prefs.InstalledThemeID, nil
}

// catalogIndex maps the enabled catalog by source id and theme name. Themes of a
// disabled source are absent, so their installs read as source removed.
func (h *themeInstalledHandler) catalogIndex(ctx context.Context) (map[installedKey]db.ThemeCatalogEntry, error) {
	entries, err := h.store.ListThemeCatalog(ctx, db.ThemeCatalogFilter{})
	if err != nil {
		return nil, err
	}
	idx := make(map[installedKey]db.ThemeCatalogEntry, len(entries))
	for _, e := range entries {
		idx[installedKey{sourceID: e.SourceID, name: e.Name}] = e
	}
	return idx, nil
}

// installedItemFor builds the list shape of one installed theme. A theme is
// source removed when its source link is gone or no enabled entry of that
// source has its name. An update is offered only from a valid, newer entry.
func installedItemFor(t db.InstalledTheme, catalog map[installedKey]db.ThemeCatalogEntry, selected *int64) installedThemeItem {
	var meta themeMetaView
	// The metadata is a snapshot written at install; an undecodable one shows as
	// a theme without a display name rather than failing the list.
	_ = json.Unmarshal([]byte(t.Metadata), &meta)
	item := installedThemeItem{
		ID:          t.ID,
		Source:      t.SourceName,
		Name:        t.ThemeName,
		DisplayName: meta.DisplayName,
		Version:     t.Version,
		Modes:       themeModes(t.Tokens),
		Selected:    selected != nil && *selected == t.ID,
	}
	if t.SourceID == nil {
		item.SourceRemoved = true
		return item
	}
	e, ok := catalog[installedKey{sourceID: *t.SourceID, name: t.ThemeName}]
	if !ok {
		item.SourceRemoved = true
		return item
	}
	item.UpdateAvailable = e.Status == themeStatusValid && themes.SemverNewer(e.Version, t.Version)
	return item
}

// installedThemeID parses the {id} path parameter. A malformed id answers 404,
// the same as an unknown one.
func installedThemeID(w http.ResponseWriter, req *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(req, "id"), 10, 64)
	if err != nil || id <= 0 {
		httperr.WriteMessage(w, req, messages.New("server.themes.not_found", nil))
		return 0, false
	}
	return id, true
}

// writeInstalledErr maps the install, update and remove refusals to the
// contract's status codes and messages. Anything else goes through httperr.
func writeInstalledErr(w http.ResponseWriter, req *http.Request, err error) {
	switch {
	case errors.Is(err, db.ErrThemeNotFound):
		httperr.WriteMessage(w, req, messages.New("server.themes.not_found", nil))
	case errors.Is(err, db.ErrThemeAlreadyInstalled):
		httperr.WriteMessage(w, req, messages.New("server.themes.already_installed", nil))
	case errors.Is(err, db.ErrThemeInstallLimit):
		httperr.WriteMessage(w, req, messages.New("server.themes.install_limit", nil))
	case errors.Is(err, themes.ErrEntryNotValid):
		httperr.WriteMessage(w, req, messages.New("server.themes.entry_not_valid", nil))
	case errors.Is(err, themes.ErrSourceDisabled):
		httperr.WriteMessage(w, req, messages.New("server.themes.source_disabled", nil))
	case errors.Is(err, themes.ErrSourceRemoved):
		httperr.WriteMessage(w, req, messages.New("server.themes.source_removed", nil))
	case errors.Is(err, themes.ErrNoUpdate):
		httperr.WriteMessage(w, req, messages.New("server.themes.no_update", nil))
	default:
		httperr.Write(w, req, err)
	}
}
