package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
)

// minimalIssuer serves just enough OIDC discovery for NewOIDC to build a
// provider — the full token/JWKS flow is covered in the auth package.
func minimalIssuer(t *testing.T) *httptest.Server {
	t.Helper()
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srv.URL,
			"authorization_endpoint":                srv.URL + "/authorize",
			"token_endpoint":                        srv.URL + "/token",
			"jwks_uri":                              srv.URL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
			"response_types_supported":              []string{"code"},
			"subject_types_supported":               []string{"public"},
		})
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// oidcRoutesRegistry seeds a store with externalURL + one enabled OIDC
// provider pointing at issuer, with its clientSecret available.
func oidcRoutesRegistry(t *testing.T, issuerURL string) *auth.Registry {
	t.Helper()
	store := newTestStore(t)
	seedAuthConfig(t, store, fmt.Sprintf(
		`{"providers":[{"name":"local","kind":"local","enabled":true},
		 {"name":"corp","kind":"oidc","enabled":true,"issuer":%q,"clientID":"gameplane"}]}`, issuerURL))
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO config(key, value, updated_at) VALUES ('general', ?, datetime('now'))`,
		`{"instanceName":"t","externalURL":"https://gameplane.example","defaultNamespace":"games"}`); err != nil {
		t.Fatalf("seed general: %v", err)
	}
	secrets := func(_ context.Context, name string) (map[string][]byte, error) {
		if name != "gameplane-auth-corp" {
			return nil, fmt.Errorf("unexpected secret %q", name)
		}
		return map[string][]byte{"clientSecret": []byte("s3cret")}, nil
	}
	return auth.NewRegistry(store, secrets, nil, "", nil)
}

func oidcRouter(reg *auth.Registry) chi.Router {
	r := chi.NewRouter()
	r.Get("/auth/oidc/{provider}/start", OIDCStart(reg))
	r.Get("/auth/oidc/start", OIDCStartLegacy(reg))
	return r
}

func TestOIDCStart_RedirectsWithScopedCookies(t *testing.T) {
	issuer := minimalIssuer(t)
	r := oidcRouter(oidcRoutesRegistry(t, issuer.URL))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/oidc/corp/start", nil))
	if rr.Code != http.StatusFound {
		t.Fatalf("status = %d body=%s, want 302", rr.Code, rr.Body)
	}
	loc := rr.Header().Get("Location")
	if !strings.HasPrefix(loc, issuer.URL+"/authorize") {
		t.Fatalf("redirect = %q, want the issuer's authorize endpoint", loc)
	}
	// The redirect_uri must be the per-provider callback derived from
	// externalURL.
	if !strings.Contains(loc, "gameplane.example%2Fauth%2Foidc%2Fcorp%2Fcallback") {
		t.Fatalf("redirect_uri missing per-provider callback: %q", loc)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("start must set state/nonce cookies")
	}
	for _, c := range cookies {
		if c.Path != "/auth/oidc/corp" {
			t.Fatalf("cookie %s path = %q, want /auth/oidc/corp", c.Name, c.Path)
		}
	}
}

func TestOIDCStart_UnknownProviderIsNeutral404(t *testing.T) {
	issuer := minimalIssuer(t)
	r := oidcRouter(oidcRoutesRegistry(t, issuer.URL))
	for _, path := range []string{"/auth/oidc/ghost/start", "/auth/oidc/local/start"} {
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil))
		if rr.Code != http.StatusNotFound {
			t.Fatalf("%s: status = %d, want 404", path, rr.Code)
		}
		// Neutral body — no provider names, issuer URLs, or config detail.
		if b := rr.Body.String(); strings.Contains(b, "corp") || strings.Contains(b, "http") {
			t.Fatalf("%s: 404 body leaks detail: %q", path, b)
		}
	}
}

func TestOIDCStart_BrokenIssuerIs502WithoutDetail(t *testing.T) {
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(broken.Close)
	r := oidcRouter(oidcRoutesRegistry(t, broken.URL))

	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/oidc/corp/start", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "identity provider unavailable") {
		t.Fatalf("body = %q, want the generic message", body)
	}
	// The cause (issuer URL, secret name) stays in the server log only.
	if strings.Contains(body, broken.URL) || strings.Contains(body, "clientSecret") {
		t.Fatalf("502 body leaks detail: %q", body)
	}
}

func TestOIDCStartLegacy_WithoutHelmProviderIs404(t *testing.T) {
	issuer := minimalIssuer(t)
	r := oidcRouter(oidcRoutesRegistry(t, issuer.URL))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/oidc/start", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 with no helm provider", rr.Code)
	}
}
