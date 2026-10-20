package rbac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const (
	ownerIDAnnotation       = "gameplane.local/owner-id"
	collaboratorsAnnotation = "gameplane.local/collaborators"
)

func TestMiddleware_Unauthenticated(t *testing.T) {
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("code=%d", rr.Code)
	}
}

// clusterWide builds a User holding perms as a cluster-wide ("*") binding on the default cluster.
func clusterWide(role string, perms ...string) *auth.User {
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return &auth.User{ID: 1, Role: role, Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {
			"*": set,
		},
	}}
}

func TestMiddleware_AllowsViewerRead(t *testing.T) {
	called := false
	h := Middleware(nil)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers", nil)
	req = req.WithContext(auth.WithUser(req.Context(), clusterWide(RoleViewer, "servers:read")))
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !called {
		t.Fatalf("code=%d called=%v", rr.Code, called)
	}
}

func TestMiddleware_DeniesViewerWrite(t *testing.T) {
	h := Middleware(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("inner handler should not run")
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers", nil)
	// Viewer holds reads but not servers:write.
	req = req.WithContext(auth.WithUser(req.Context(), clusterWide(RoleViewer, "servers:read")))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("code=%d", rr.Code)
	}
}

func TestServerNameFromPath(t *testing.T) {
	cases := map[string]struct {
		path string
		name string
		ok   bool
	}{
		"simple":        {"/servers/alpha", "alpha", true},
		"with verb":     {"/servers/alpha:transfer", "alpha", true},
		"with subpath":  {"/servers/alpha/files", "alpha", true},
		"ws console":    {"/ws/servers/alpha/console", "alpha", true},
		"ws with verb":  {"/ws/servers/alpha:collaborators", "alpha", true},
		"list":          {"/servers", "", false},
		"empty name":    {"/servers/", "", false},
		"wrong segment": {"/backups/alpha", "", false},
	}
	for label, tc := range cases {
		name, ok := serverNameFromPath(tc.path)
		if ok != tc.ok || name != tc.name {
			t.Errorf("%s: got (%q, %v) want (%q, %v)", label, name, ok, tc.name, tc.ok)
		}
	}
}

func TestParseServerPath(t *testing.T) {
	cases := map[string]struct {
		path    string
		name    string
		verb    string
		isExact bool
		ok      bool
	}{
		"simple":                            {"/servers/alpha", "alpha", "", true, true},
		"with transfer verb":                {"/servers/alpha:transfer", "alpha", "transfer", false, true},
		"with collaborators verb":           {"/servers/alpha:collaborators", "alpha", "collaborators", false, true},
		"with wipe-data verb":               {"/servers/alpha:wipe-data", "alpha", "wipe-data", false, true},
		"with clone verb":                   {"/servers/alpha:clone", "alpha", "clone", false, true},
		"with subpath":                      {"/servers/alpha/files", "alpha", "", false, true},
		"with subpath and name":             {"/servers/alpha/players", "alpha", "", false, true},
		"ws simple":                         {"/ws/servers/alpha", "alpha", "", true, true},
		"ws with verb":                      {"/ws/servers/alpha:transfer", "alpha", "transfer", false, true},
		"ws with subpath":                   {"/ws/servers/alpha/console", "alpha", "", false, true},
		"invalid: verb with trailing slash": {"/servers/alpha:transfer/extra", "", "", false, false},
		"invalid: verb with subpath":        {"/servers/alpha:clone/files", "", "", false, false},
		"list":                              {"/servers", "", "", false, false},
		"empty name":                        {"/servers/", "", "", false, false},
		"wrong segment":                     {"/backups/alpha", "", "", false, false},
	}
	for label, tc := range cases {
		name, verb, isExact, ok := parseServerPath(tc.path)
		if ok != tc.ok || name != tc.name || verb != tc.verb || isExact != tc.isExact {
			t.Errorf("%s: got (%q, %q, %v, %v) want (%q, %q, %v, %v)", label, name, verb, isExact, ok, tc.name, tc.verb, tc.isExact, tc.ok)
		}
	}
}

type fakeFetcher struct {
	obj *unstructured.Unstructured
	err error
}

func (f *fakeFetcher) GetServer(_ context.Context, _ string, _, _ string) (*unstructured.Unstructured, error) {
	return f.obj, f.err
}

func (f *fakeFetcher) IDs() []string { return []string{scope.DefaultCluster} }

// newServerWithAnnotations creates a GameServer object with the given annotations.
func newServerWithAnnotations(ownerID int64, collaborators []int64) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{}
	obj.SetAPIVersion("gameplane.local/v1alpha1")
	obj.SetKind("GameServer")
	obj.SetName("alpha")
	obj.SetNamespace("gameplane-games")

	ann := make(map[string]string)
	if ownerID > 0 {
		ann[ownerIDAnnotation] = strconv.FormatInt(ownerID, 10)
	}
	if len(collaborators) > 0 {
		collabStrs := make([]string, len(collaborators))
		for i, id := range collaborators {
			collabStrs[i] = strconv.FormatInt(id, 10)
		}
		ann[collaboratorsAnnotation] = strings.Join(collabStrs, ",")
	}
	if len(ann) > 0 {
		obj.SetAnnotations(ann)
	}
	return obj
}

func TestMiddleware_OwnershipFallback_Owner(t *testing.T) {
	t.Run("owner allowed on :clone verb", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		// User 1 is the owner, requesting :clone
		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:clone", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("owner should be allowed on :clone, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("owner allowed on DELETE", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "DELETE", "/servers/alpha", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("owner should be allowed on DELETE, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("owner allowed on :transfer", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:transfer", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("owner should be allowed on :transfer, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("owner allowed on :wipe-data", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:wipe-data", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("owner should be allowed on :wipe-data, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("owner without servers:write denied on generic PUT", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "PUT", "/servers/alpha", nil)
		user := &auth.User{ID: 1, Username: "alice", Role: "viewer"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("owner without servers:write should be denied on generic PUT, got %d", rr.Code)
		}
	})

	t.Run("owner without servers:write allowed on sub-resource write", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha/files/write", nil)
		user := &auth.User{ID: 1, Username: "alice", Role: "viewer"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("owner should be allowed on sub-resource write, got %d called=%v", rr.Code, called)
		}
	})
}

func TestMiddleware_OwnershipFallback_Collaborator(t *testing.T) {
	t.Run("collaborator denied on DELETE", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "DELETE", "/servers/alpha", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("collaborator should be denied on DELETE, got %d", rr.Code)
		}
	})

	t.Run("collaborator denied on :transfer", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:transfer", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("collaborator should be denied on :transfer, got %d", rr.Code)
		}
	})

	t.Run("collaborator denied on :collaborators", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "PUT", "/servers/alpha:collaborators", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("collaborator should be denied on :collaborators, got %d", rr.Code)
		}
	})

	t.Run("collaborator denied on :wipe-data", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:wipe-data", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("collaborator should be denied on :wipe-data, got %d", rr.Code)
		}
	})

	t.Run("collaborator denied on generic PUT", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "PUT", "/servers/alpha", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("collaborator should be denied on generic PUT, got %d", rr.Code)
		}
	})

	t.Run("collaborator allowed on sub-resource write", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha/files/write", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("collaborator should be allowed on sub-resource write, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("collaborator allowed on :clone", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:clone", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("collaborator should be allowed on :clone, got %d called=%v", rr.Code, called)
		}
	})

	t.Run("collaborator allowed on :start", func(t *testing.T) {
		called := false
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{2}),
		})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			called = true
			w.WriteHeader(204)
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:start", nil)
		user := &auth.User{ID: 2, Username: "bob"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != 204 || !called {
			t.Errorf("collaborator should be allowed on :start, got %d called=%v", rr.Code, called)
		}
	})
}

func TestMiddleware_OwnershipFallback_InvalidPath(t *testing.T) {
	t.Run("invalid path with trailing segments after verb fails closed", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: newServerWithAnnotations(1, []int64{}),
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:transfer/extra", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("invalid path should be forbidden, got %d", rr.Code)
		}
	})
}

func TestMiddleware_OwnershipFallback_FetchError(t *testing.T) {
	t.Run("fetch error fails closed", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			err: context.DeadlineExceeded,
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("fetch error should be forbidden, got %d", rr.Code)
		}
	})

	t.Run("server not found fails closed", func(t *testing.T) {
		h := Middleware(&fakeFetcher{
			obj: nil,
			err: nil,
		})(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
			t.Fatal("handler should not be called")
		}))

		rr := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
		user := &auth.User{ID: 1, Username: "alice"}
		req = req.WithContext(auth.WithUser(req.Context(), user))
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusForbidden {
			t.Errorf("server not found should be forbidden, got %d", rr.Code)
		}
	})
}

func TestFirstSegment(t *testing.T) {
	cases := map[string]string{
		"/":            "",
		"/servers":     "servers",
		"/servers/foo": "servers",
		"users":        "users",
	}
	for in, want := range cases {
		if got := firstSegment(in); got != want {
			t.Errorf("firstSegment(%q)=%q want %q", in, got, want)
		}
	}
}

// fakeServerFetcher mocks the ServerFetcher for tests.
type fakeServerFetcher map[string]*unstructured.Unstructured

func (f fakeServerFetcher) GetServer(_ context.Context, _ string, ns, name string) (*unstructured.Unstructured, error) {
	if obj, ok := f[ns+"/"+name]; ok {
		return obj, nil
	}
	return nil, nil // not found (same as API not-found behavior)
}

func (f fakeServerFetcher) IDs() []string { return []string{scope.DefaultCluster} }

func TestMiddleware_OwnerAllowedOnOwnedServer(t *testing.T) {
	// Owner should be allowed to read/write their own server even without namespace role.
	called := false
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", ""),
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
	// User has no namespace permissions but is the owner.
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42, Role: "viewer"}))
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !called {
		t.Fatalf("owner GET: code=%d called=%v", rr.Code, called)
	}
}

func TestMiddleware_CollaboratorAllowedOnServer(t *testing.T) {
	// Collaborator should be allowed on GET/POST/console but not on :transfer/:collaborators.
	called := false
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", "100,200"),
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))

	// User 100 is a collaborator.
	user := &auth.User{ID: 100, Role: "viewer"}

	// Test read allowed
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !called {
		t.Fatalf("collaborator GET: code=%d called=%v", rr.Code, called)
	}
}

func TestMiddleware_CollaboratorDeniedOnTransfer(t *testing.T) {
	// Collaborator should NOT be allowed on :transfer (owner-only).
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", "100,200"),
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:transfer", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 100}))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("collaborator :transfer want 403 got %d", rr.Code)
	}
}

func TestMiddleware_OwnerAllowedOnTransfer(t *testing.T) {
	// Owner should be allowed on :transfer.
	called := false
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", "100"),
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "POST", "/servers/alpha:transfer", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !called {
		t.Fatalf("owner :transfer: code=%d called=%v", rr.Code, called)
	}
}

func TestMiddleware_StrangerDenied(t *testing.T) {
	// A user who is neither owner nor collaborator should be denied.
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", "100"),
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 999}))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("stranger want 403 got %d", rr.Code)
	}
}

func TestMiddleware_UnknownClusterRejected(t *testing.T) {
	// A namespaced route with an unregistered ?cluster= selector must be
	// rejected with 400 before the permission check or fallback ever runs.
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", ""),
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha?cluster=ghost", nil)
	// Owner of the server, but the cluster selector is invalid — the
	// fallback must never even be attempted.
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown cluster want 400 got %d", rr.Code)
	}
}

func TestMiddleware_OwnershipFallback_DefaultCluster(t *testing.T) {
	// With no ?cluster= param, the owner/collaborator fallback still
	// grants access — it resolves to scope.DefaultCluster.
	called := false
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", ""),
	})(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(204)
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42, Role: "viewer"}))
	h.ServeHTTP(rr, req)
	if rr.Code != 204 || !called {
		t.Fatalf("owner fallback with default cluster: code=%d called=%v", rr.Code, called)
	}
}

func TestMiddleware_NilFetcher(t *testing.T) {
	// With nil fetcher, fallback is disabled; permission denial is final.
	h := Middleware(nil)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers/alpha", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("nil fetcher want 403 got %d", rr.Code)
	}
}

func TestMiddleware_ListEndpointNoFallback(t *testing.T) {
	// List endpoints (no name) should not trigger the fallback.
	h := Middleware(fakeServerFetcher{
		"gameplane-games/alpha": newOwnershipObj("alpha", 42, "alice", ""),
	})(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("handler should not run")
	}))

	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), "GET", "/servers", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("list endpoint want 403 got %d", rr.Code)
	}
}

// newOwnershipObj creates a test GameServer object with owner and collaborators.
// Annotations go through SetAnnotations: unstructured requires JSON-shaped
// values (map[string]interface{}), and embedding a map[string]string directly
// makes GetAnnotations silently return nil.
func newOwnershipObj(name string, ownerID int64, ownerName, collaborators string) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata": map[string]any{
			"name":      name,
			"namespace": "gameplane-games",
		},
	}}
	ann := map[string]string{
		"gameplane.local/owner-id": strconv.FormatInt(ownerID, 10),
		"gameplane.local/owner":    ownerName,
	}
	if collaborators != "" {
		ann["gameplane.local/collaborators"] = collaborators
	}
	obj.SetAnnotations(ann)
	return obj
}
