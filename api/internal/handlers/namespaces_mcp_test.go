package handlers

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// TestNamespaces_TokenUngrantedClusterLooksUnknown checks FR-021 (spec 037)
// on /namespaces: a token caller with no grant on a registered cluster gets
// the same status and body as for an unregistered cluster ID.
func TestNamespaces_TokenUngrantedClusterLooksUnknown(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace})
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, fakeKubeClient())
	reg.Set("remote", fakeKubeClient())
	r := chi.NewRouter()
	MountNamespaces(r, reg)

	u := &auth.User{ID: 7, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {scope.DefaultNamespace: namespacesPermSet("servers:read")},
	}}
	get := func(query string, token bool) (int, string) {
		t.Helper()
		ctx := auth.WithUser(t.Context(), u)
		if token {
			ctx = auth.WithMCPToken(ctx, 42)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "/namespaces?"+query, nil)
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		rr := httptest.NewRecorder()
		r.ServeHTTP(rr, req)
		return rr.Code, rr.Body.String()
	}

	ungrantedCode, ungrantedBody := get("cluster=remote", true)
	unknownCode, unknownBody := get("cluster=does-not-exist", true)
	if ungrantedCode == http.StatusOK {
		t.Fatalf("token on ungranted cluster: got 200 %s, want the unknown-cluster refusal", ungrantedBody)
	}
	if ungrantedCode != unknownCode || ungrantedBody != unknownBody {
		t.Fatalf("ungranted = %d %q, unknown = %d %q; want identical", ungrantedCode, ungrantedBody, unknownCode, unknownBody)
	}

	// A granted cluster still answers normally for the token.
	if code, body := get("cluster="+scope.DefaultCluster, true); code != http.StatusOK {
		t.Fatalf("token on granted cluster: got %d %s", code, body)
	}
	// Session callers keep the previous behaviour: an empty list.
	if code, body := get("cluster=remote", false); code != http.StatusOK {
		t.Fatalf("session on ungranted cluster: got %d %s", code, body)
	}
}
