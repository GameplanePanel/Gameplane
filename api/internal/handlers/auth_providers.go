package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/i18n/catalog"
)

type loginProvider struct {
	Name  string `json:"name"`  // route slug for /auth/oidc/{name}/start
	Kind  string `json:"kind"`  // "local" | "oidc" | "google" | "github"
	Label string `json:"label"` // button text for the login page
}

type loginProvidersResp struct {
	Providers []loginProvider `json:"providers"`
}

// AuthProvidersHandler serves GET /auth/providers — a public, pre-auth
// endpoint listing the enabled login methods so the login page only
// offers providers that actually work. Resolved through the registry per
// request, so a config save shows up without a restart.
//
// Per the login-privacy rule this returns ONLY names, kinds, and labels:
// no version, cluster name, host, issuer URL, client ID, or counts.
// Labels are admin-configured display names (or neutral fallbacks) and
// never derive from the issuer URL, which would leak a hostname.
func AuthProvidersHandler(reg *auth.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		enabled := reg.Enabled(req.Context())
		resp := loginProvidersResp{Providers: make([]loginProvider, 0, len(enabled))}
		for _, p := range enabled {
			resp.Providers = append(resp.Providers, loginProvider{
				Name:  p.Name,
				Kind:  p.Kind,
				Label: p.Label(),
			})
		}
		writeJSON(w, resp)
	}
}

// fallbackLanguage is the tag served when no usable instance default is
// stored. English is always shipped and complete.
const fallbackLanguage = "en"

// authLocaleResp is the GET /auth/locale body. It carries only the default
// language tag: login privacy allows nothing else on an unauthenticated route.
type authLocaleResp struct {
	DefaultLanguage string `json:"defaultLanguage"`
}

// AuthLocaleHandler serves GET /auth/locale, a public, pre-auth endpoint that
// tells the login page which language to show before anyone signs in. It
// reads the admin's i18n config section. An absent, unparsable or unlisted
// tag, or a database failure, all fall back to English; internal error text
// is logged and never returned.
func AuthLocaleHandler(store *db.Store) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		writeJSON(w, authLocaleResp{DefaultLanguage: instanceDefaultLanguage(req, store)})
	}
}

// instanceDefaultLanguage resolves the stored i18n default to a listed tag.
func instanceDefaultLanguage(req *http.Request, store *db.Store) string {
	raw, ok, err := store.ConfigValue(req.Context(), "i18n")
	if err != nil {
		slog.Warn("auth locale: read i18n config", "err", err)
		return fallbackLanguage
	}
	if !ok {
		return fallbackLanguage
	}
	var section struct {
		DefaultLanguage string `json:"defaultLanguage"`
	}
	if err := json.Unmarshal([]byte(raw), &section); err != nil {
		slog.Warn("auth locale: parse i18n config", "err", err)
		return fallbackLanguage
	}
	if section.DefaultLanguage == "" || !catalog.IsListed(section.DefaultLanguage) {
		return fallbackLanguage
	}
	return section.DefaultLanguage
}
