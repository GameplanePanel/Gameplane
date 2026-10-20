// Dynamic OIDC login routes: /auth/oidc/{provider}/start|callback resolve
// the provider through the auth registry per request, so a provider added
// in Admin Settings works without an API restart. The legacy
// /auth/oidc/start|callback pair aliases the synthetic "helm" provider —
// its redirect URL is registered verbatim at existing IdPs and must keep
// working.

package handlers

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

// resolveOIDC maps registry errors to pre-auth-safe responses: one
// neutral 404 for unknown/disabled providers, and a detail-free 502 for
// providers that exist but can't be built (missing Secret, unreachable
// issuer) — the cause goes to the server log for the admin.
func resolveOIDC(w http.ResponseWriter, req *http.Request, reg *auth.Registry, name string) *auth.OIDC {
	o, err := reg.OIDCFor(req.Context(), name)
	if err != nil {
		if errors.Is(err, auth.ErrUnknownProvider) {
			http.NotFound(w, req)
			return nil
		}
		slog.Warn("oidc provider unavailable", "provider", name, "err", err)
		http.Error(w, "identity provider unavailable", http.StatusBadGateway)
		return nil
	}
	return o
}

// OIDCStart serves GET /auth/oidc/{provider}/start.
func OIDCStart(reg *auth.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "provider")
		o := resolveOIDC(w, req, reg, name)
		if o == nil {
			return
		}
		// State/nonce cookies scope to this provider's path so concurrent
		// flows against different providers can't clobber each other.
		o.HandleStartAt("/auth/oidc/"+name)(w, req)
	}
}

// OIDCCallback serves GET /auth/oidc/{provider}/callback.
func OIDCCallback(reg *auth.Registry, sessions *auth.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		name := chi.URLParam(req, "provider")
		o := resolveOIDC(w, req, reg, name)
		if o == nil {
			return
		}
		o.HandleCallbackAt(sessions, "/auth/oidc/"+name)(w, req)
	}
}

// OIDCStartLegacy serves the pre-multi-provider GET /auth/oidc/start,
// aliasing the Helm-flag provider with its original Path=/ cookies.
func OIDCStartLegacy(reg *auth.Registry) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		o := resolveOIDC(w, req, reg, auth.HelmProviderName)
		if o == nil {
			return
		}
		o.HandleStart()(w, req)
	}
}

// OIDCCallbackLegacy serves the pre-multi-provider GET /auth/oidc/callback.
func OIDCCallbackLegacy(reg *auth.Registry, sessions *auth.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		o := resolveOIDC(w, req, reg, auth.HelmProviderName)
		if o == nil {
			return
		}
		o.HandleCallback(sessions)(w, req)
	}
}
