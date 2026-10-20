package rbac

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// ownerOnlyRequests lists the four owner-only server operations.
var ownerOnlyRequests = []struct{ method, path string }{
	{http.MethodPost, "/servers/alpha:transfer"},
	{http.MethodPut, "/servers/alpha:collaborators"},
	{http.MethodPost, "/servers/alpha:wipe-data"},
	{http.MethodDelete, "/servers/alpha"},
}

// userWith builds a user holding perms in one (cluster, namespace) binding.
func userWith(id int64, cluster, ns string, perms ...string) *auth.User {
	set := map[string]struct{}{}
	for _, p := range perms {
		set[p] = struct{}{}
	}
	return &auth.User{ID: id, Perms: map[string]map[string]map[string]struct{}{cluster: {ns: set}}}
}

// serveThrough runs one request through Middleware and returns the status
// and whether the inner handler ran. The inner handler answers 204.
func serveThrough(t *testing.T, fetch ServerFetcher, u *auth.User, method, path string) (int, bool) {
	t.Helper()
	called := false
	h := Middleware(fetch)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))
	rr := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(t.Context(), method, path, nil)
	if u != nil {
		req = req.WithContext(auth.WithUser(req.Context(), u))
	}
	h.ServeHTTP(rr, req)
	return rr.Code, called
}

func TestMiddleware_OwnerOnlyOperationsNeedOwnerOrAdmin(t *testing.T) {
	const ownerID, collabID, writerID int64 = 42, 7, 5
	owned := func() ServerFetcher {
		return &fakeFetcher{obj: newServerWithAnnotations(ownerID, []int64{collabID})}
	}
	cases := []struct {
		name    string
		user    *auth.User
		allowed bool
	}{
		{"cluster-wide servers:write holder who is not the owner", userWith(writerID, scope.DefaultCluster, "*", "servers:read", "servers:write"), false},
		{"namespace servers:write holder who is not the owner", userWith(writerID, scope.DefaultCluster, scope.DefaultNamespace, "servers:write"), false},
		{"collaborator holding servers:write", userWith(collabID, scope.DefaultCluster, "*", "servers:write"), false},
		{"admin of another cluster only", userWith(writerID, "other", "*", "*"), false},
		{"owner holding servers:write", userWith(ownerID, scope.DefaultCluster, "*", "servers:write"), true},
		{"owner without namespace permissions", &auth.User{ID: ownerID}, true},
		{"cluster-wide admin", userWith(writerID, scope.DefaultCluster, "*", "*"), true},
		{"admin of the target namespace", userWith(writerID, scope.DefaultCluster, scope.DefaultNamespace, "*"), true},
		{"admin on every cluster", userWith(writerID, "*", "*", "*"), true},
	}
	for _, tc := range cases {
		for _, op := range ownerOnlyRequests {
			t.Run(tc.name+" "+op.method+" "+op.path, func(t *testing.T) {
				code, called := serveThrough(t, owned(), tc.user, op.method, op.path)
				if tc.allowed && (code != http.StatusNoContent || !called) {
					t.Fatalf("want the request to reach the handler, got %d called=%v", code, called)
				}
				if !tc.allowed && (code != http.StatusForbidden || called) {
					t.Fatalf("want 403 without reaching the handler, got %d called=%v", code, called)
				}
			})
		}
	}
}

func TestMiddleware_ServerWithoutOwnerIsAdminOnlyForOwnerOnlyOperations(t *testing.T) {
	unowned := &fakeFetcher{obj: newServerWithAnnotations(0, nil)}
	for _, op := range ownerOnlyRequests {
		t.Run("servers:write holder "+op.method+" "+op.path, func(t *testing.T) {
			code, called := serveThrough(t, unowned, userWith(5, scope.DefaultCluster, "*", "servers:write"), op.method, op.path)
			if code != http.StatusForbidden || called {
				t.Fatalf("want 403, got %d called=%v", code, called)
			}
		})
		t.Run("servers:write holder with user id 0 "+op.method+" "+op.path, func(t *testing.T) {
			code, called := serveThrough(t, unowned, userWith(0, scope.DefaultCluster, "*", "servers:write"), op.method, op.path)
			if code != http.StatusForbidden || called {
				t.Fatalf("want 403, got %d called=%v", code, called)
			}
		})
		t.Run("admin "+op.method+" "+op.path, func(t *testing.T) {
			code, called := serveThrough(t, unowned, userWith(1, scope.DefaultCluster, "*", "*"), op.method, op.path)
			if code != http.StatusNoContent || !called {
				t.Fatalf("want the request to reach the handler, got %d called=%v", code, called)
			}
		})
	}
}

func TestMiddleware_OtherServerWritesUnchangedForServersWriteHolders(t *testing.T) {
	// A fetcher that always fails: none of these requests may depend on
	// reading the server.
	failing := &fakeFetcher{err: context.DeadlineExceeded}
	writer := userWith(5, scope.DefaultCluster, "*", "servers:read", "servers:write")
	for _, rq := range []struct{ method, path string }{
		{http.MethodPost, "/servers"},
		{http.MethodPut, "/servers/alpha"},
		{http.MethodPost, "/servers/alpha:start"},
		{http.MethodPost, "/servers/alpha:stop"},
		{http.MethodPost, "/servers/alpha:clone"},
		{http.MethodPost, "/servers/alpha/files/write"},
		{http.MethodDelete, "/servers/alpha/mods"},
	} {
		t.Run(rq.method+" "+rq.path, func(t *testing.T) {
			code, called := serveThrough(t, failing, writer, rq.method, rq.path)
			if code != http.StatusNoContent || !called {
				t.Fatalf("want the request to reach the handler, got %d called=%v", code, called)
			}
		})
	}
}

func TestMiddleware_OwnerOnlyOperationOnMissingServer(t *testing.T) {
	missing := &fakeFetcher{obj: nil}
	t.Run("admin reaches the handler, which answers for the missing server", func(t *testing.T) {
		code, called := serveThrough(t, missing, userWith(1, scope.DefaultCluster, "*", "*"), http.MethodDelete, "/servers/alpha")
		if code != http.StatusNoContent || !called {
			t.Fatalf("want the request to reach the handler, got %d called=%v", code, called)
		}
	})
	t.Run("servers:write holder is refused", func(t *testing.T) {
		code, called := serveThrough(t, missing, userWith(5, scope.DefaultCluster, "*", "servers:write"), http.MethodDelete, "/servers/alpha")
		if code != http.StatusForbidden || called {
			t.Fatalf("want 403, got %d called=%v", code, called)
		}
	})
}

func TestMiddleware_OwnerOnlyOperationWithoutFetcherFailsClosed(t *testing.T) {
	code, called := serveThrough(t, nil, userWith(5, scope.DefaultCluster, "*", "servers:write"), http.MethodDelete, "/servers/alpha")
	if code != http.StatusForbidden || called {
		t.Fatalf("servers:write holder with no fetcher: want 403, got %d called=%v", code, called)
	}
	code, called = serveThrough(t, nil, userWith(1, scope.DefaultCluster, "*", "*"), http.MethodDelete, "/servers/alpha")
	if code != http.StatusNoContent || !called {
		t.Fatalf("admin with no fetcher: want the request to reach the handler, got %d called=%v", code, called)
	}
}
