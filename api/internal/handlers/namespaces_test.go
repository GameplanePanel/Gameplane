package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func mountNamespacesRouter(k *kube.Client) *chi.Mux {
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountNamespaces(r, reg)
	return r
}

// withAllowedNamespaces temporarily overrides scope.AllowedNamespaces for the
// duration of the test, following the pattern in scope_test.go.
func withAllowedNamespaces(t *testing.T, ns []string) {
	t.Helper()
	saved := scope.AllowedNamespaces
	t.Cleanup(func() { scope.AllowedNamespaces = saved })
	scope.AllowedNamespaces = ns
}

func namespacesPermSet(perms ...string) map[string]struct{} {
	set := make(map[string]struct{}, len(perms))
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return set
}

func doNamespaces(t *testing.T, h http.Handler, u *auth.User, query string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/namespaces"
	if query != "" {
		path += "?" + query
	}
	ctx := t.Context()
	if u != nil {
		ctx = auth.WithUser(ctx, u)
	}
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeNamespaces(t *testing.T, rr *httptest.ResponseRecorder) []string {
	t.Helper()
	var body namespacesResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response: %v, body=%s", err, rr.Body.String())
	}
	sort.Strings(body.Namespaces)
	return body.Namespaces
}

func TestNamespaces_FiltersToPermittedNamespaces(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace, "extra-a", "extra-b"})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	u := &auth.User{ID: 1, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {
			scope.DefaultNamespace: namespacesPermSet("servers:read"),
			"extra-a":              namespacesPermSet("servers:read"),
			// extra-b: no binding at all.
		},
	}}

	rr := doNamespaces(t, r, u, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	got := decodeNamespaces(t, rr)
	want := []string{"extra-a", scope.DefaultNamespace}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestNamespaces_ForbiddenNamespacesLeftOut(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace, "secret-ns"})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	// No binding at all for secret-ns: it must never appear, even though
	// it's on the server-side allow-list.
	u := &auth.User{ID: 2, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {
			scope.DefaultNamespace: namespacesPermSet("servers:read"),
		},
	}}

	rr := doNamespaces(t, r, u, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	got := decodeNamespaces(t, rr)
	if len(got) != 1 || got[0] != scope.DefaultNamespace {
		t.Fatalf("got %v, want only %q", got, scope.DefaultNamespace)
	}
}

func TestNamespaces_ClusterWideBindingGrantsAll(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace, "extra-a", "extra-b"})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	u := &auth.User{ID: 3, Role: "admin", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {"*": namespacesPermSet("*")},
	}}

	rr := doNamespaces(t, r, u, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	got := decodeNamespaces(t, rr)
	want := []string{"extra-a", "extra-b", scope.DefaultNamespace}
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestNamespaces_BindingOnAnotherClusterDoesNotLeak(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace, "extra-a"})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	// A cluster-wide binding on a DIFFERENT cluster must not grant
	// namespaced servers:read on scope.DefaultCluster (cross-cluster
	// privilege escalation guard mirrored from rbac.Middleware).
	u := &auth.User{ID: 4, Perms: map[string]map[string]map[string]struct{}{
		"other-cluster": {"*": namespacesPermSet("servers:read")},
	}}

	rr := doNamespaces(t, r, u, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	got := decodeNamespaces(t, rr)
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
}

// TestNamespaces_NoUserReturnsEmpty exercises the handler directly with no
// user in context, a path rbac.Middleware makes unreachable in production
// (it 401s before the handler runs). The handler itself has no
// authentication of its own and relies on that middleware for it — this
// test only pins the handler's own no-user behavior in isolation.
func TestNamespaces_NoUserReturnsEmpty(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	rr := doNamespaces(t, r, nil, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	got := decodeNamespaces(t, rr)
	if len(got) != 0 {
		t.Fatalf("got %v, want none", got)
	}
	// The handler builds the slice with make([]string, 0, ...) so the
	// authoritative "no namespaces" answer serializes as [], not null —
	// callers can range over it without a nil check.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(raw["namespaces"]) != "[]" {
		t.Fatalf("namespaces = %s, want []", raw["namespaces"])
	}
}

func TestNamespaces_ResponseShape(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	u := &auth.User{ID: 5, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {scope.DefaultNamespace: namespacesPermSet("servers:read")},
	}}
	rr := doNamespaces(t, r, u, "")
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s", rr.Code, rr.Body)
	}
	var raw map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := raw["namespaces"]; !ok {
		t.Fatalf("response missing \"namespaces\" key: %s", rr.Body.String())
	}
}

func TestNamespaces_UnknownClusterRejected(t *testing.T) {
	withAllowedNamespaces(t, []string{scope.DefaultNamespace})
	k := fakeKubeClient()
	r := mountNamespacesRouter(k)

	u := &auth.User{ID: 6, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {"*": namespacesPermSet("*")},
	}}
	rr := doNamespaces(t, r, u, "cluster=does-not-exist")
	if rr.Code == http.StatusOK {
		t.Fatalf("got 200, want an error for an unregistered cluster")
	}
}
