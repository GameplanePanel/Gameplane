package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/themes"
)

// themeSourceHandler serves the admin theme source routes (spec 043,
// contracts/themes-api.md, "Theme sources"). The RBAC table admits them only with
// themes:manage. Credentials are never returned: the list and write responses
// carry no credential field, and the stored reference is not part of the item.
type themeSourceHandler struct {
	store *db.Store
	svc   *themes.Service
}

// themeSourceBody is the POST and PUT request. Unknown fields are refused, so a
// credential sent before it is supported (OD-5) is an error, not silently
// dropped. On PUT the name comes from the path and the body name is ignored.
type themeSourceBody struct {
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	Spec            json.RawMessage `json:"spec"`
	Verify          json.RawMessage `json:"verify"`
	RefreshInterval *int            `json:"refreshInterval"`
	Enabled         *bool           `json:"enabled"`
}

// themeSourceItem is one source as the admin API shows it. Builtin marks the
// reserved official source, which can be disabled but not deleted.
type themeSourceItem struct {
	Name            string          `json:"name"`
	Type            string          `json:"type"`
	Spec            json.RawMessage `json:"spec"`
	Verify          json.RawMessage `json:"verify"`
	RefreshInterval int             `json:"refreshInterval"`
	Enabled         bool            `json:"enabled"`
	Builtin         bool            `json:"builtin"`
	LastSyncAt      *string         `json:"lastSyncAt"`
	LastError       *string         `json:"lastError"`
	ThemeCount      int             `json:"themeCount"`
}

// settings converts the request body to the service input.
func (b themeSourceBody) settings() themes.SourceSettings {
	return themes.SourceSettings{
		Type:             b.Type,
		Spec:             b.Spec,
		Verify:           b.Verify,
		RefreshIntervalS: b.RefreshInterval,
		Enabled:          b.Enabled,
	}
}

// sourceItemOf renders a stored source. The spec and policy are the canonical
// JSON the service wrote; no credential reference is included.
func sourceItemOf(src db.ThemeSource) themeSourceItem {
	item := themeSourceItem{
		Name:            src.Name,
		Type:            src.Type,
		Spec:            json.RawMessage(src.Spec),
		RefreshInterval: src.RefreshIntervalS,
		Enabled:         src.Enabled,
		Builtin:         src.Name == themes.OfficialSourceName,
		LastSyncAt:      src.LastSyncAt,
		LastError:       src.LastError,
		ThemeCount:      src.ThemeCount,
	}
	if src.Verify != nil {
		item.Verify = json.RawMessage(*src.Verify)
	}
	return item
}

// item renders src with its current catalog count, which a single-row read
// does not carry.
func (h *themeSourceHandler) item(ctx context.Context, src db.ThemeSource) (themeSourceItem, error) {
	all, err := h.store.ListThemeSources(ctx)
	if err != nil {
		return themeSourceItem{}, fmt.Errorf("list theme sources: %w", err)
	}
	for _, s := range all {
		if s.ID == src.ID {
			src = s
			break
		}
	}
	return sourceItemOf(src), nil
}

// list answers GET /themes/sources.
func (h *themeSourceHandler) list(w http.ResponseWriter, req *http.Request) {
	srcs, err := h.store.ListThemeSources(req.Context())
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	items := make([]themeSourceItem, 0, len(srcs))
	for _, src := range srcs {
		items = append(items, sourceItemOf(src))
	}
	writeJSON(w, items)
}

// create answers POST /themes/sources with 201 and the new source.
func (h *themeSourceHandler) create(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	body, ok := decodeThemeSourceBody(w, req)
	if !ok {
		return
	}
	src, err := h.svc.CreateSource(ctx, body.Name, body.settings())
	if err != nil {
		writeThemeSourceErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeSourceCreate,
		"source", src.Name, "type", src.Type))
	item, err := h.item(ctx, src)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSONCreated(w, item)
}

// update answers PUT /themes/sources/{name} with the replaced source.
func (h *themeSourceHandler) update(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	name := chi.URLParam(req, "name")
	body, ok := decodeThemeSourceBody(w, req)
	if !ok {
		return
	}
	src, err := h.svc.UpdateSource(ctx, name, body.settings())
	if err != nil {
		writeThemeSourceErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeSourceUpdate,
		"source", src.Name, "type", src.Type))
	item, err := h.item(ctx, src)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	writeJSON(w, item)
}

// remove answers DELETE /themes/sources/{name} with 204. Installed copies stay.
func (h *themeSourceHandler) remove(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	src, err := h.svc.DeleteSource(ctx, chi.URLParam(req, "name"))
	if err != nil {
		writeThemeSourceErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeSourceDelete,
		"source", src.Name, "type", src.Type))
	w.WriteHeader(http.StatusNoContent)
}

// refresh answers POST /themes/sources/{name}/refresh with 202. The index runs in
// the background. A disabled source answers 409 (source_disabled).
func (h *themeSourceHandler) refresh(w http.ResponseWriter, req *http.Request) {
	ctx := req.Context()
	src, err := h.svc.StartRefresh(ctx, chi.URLParam(req, "name"))
	if err != nil {
		writeThemeSourceErr(w, req, err)
		return
	}
	auth.SetAuditTarget(ctx, audit.ThemeTarget(audit.EventThemeSourceRefresh,
		"source", src.Name, "type", src.Type))
	w.WriteHeader(http.StatusAccepted)
}

// decodeThemeSourceBody reads the request body strictly. It writes the 400
// response and returns false when the body is not valid JSON or has unknown fields.
func decodeThemeSourceBody(w http.ResponseWriter, req *http.Request) (themeSourceBody, bool) {
	var body themeSourceBody
	dec := json.NewDecoder(req.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return body, false
	}
	return body, true
}

// writeThemeSourceErr maps a source service error to its response: 422 for
// refused settings, 404, 409 for duplicate, built-in and disabled cases, and a
// generic 500 for anything else.
func writeThemeSourceErr(w http.ResponseWriter, req *http.Request, err error) {
	var se *themes.SourceError
	switch {
	case errors.As(err, &se):
		httperr.WriteCode(w, req, http.StatusUnprocessableEntity, se)
	case errors.Is(err, db.ErrThemeNotFound):
		httperr.WriteMessage(w, req, messages.New("server.themes.source_not_found", nil))
	case errors.Is(err, db.ErrThemeSourceExists):
		httperr.WriteMessage(w, req, messages.New("server.themes.source_exists", nil))
	case errors.Is(err, themes.ErrSourceBuiltin):
		httperr.WriteMessage(w, req, messages.New("server.themes.source_builtin", nil))
	case errors.Is(err, themes.ErrSourceDisabled):
		httperr.WriteMessage(w, req, messages.New("server.themes.source_disabled", nil))
	default:
		httperr.Write(w, req, err)
	}
}
