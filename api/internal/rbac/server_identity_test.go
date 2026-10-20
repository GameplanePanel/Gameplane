package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

type identityFetcher struct{ server *unstructured.Unstructured }

func (f identityFetcher) GetServer(context.Context, string, string, string) (*unstructured.Unstructured, error) {
	return f.server, nil
}
func (identityFetcher) IDs() []string { return []string{scope.DefaultCluster, "remote"} }

func TestOwnershipGrantBindsExactServerIdentity(t *testing.T) {
	for _, annotation := range []string{"gameplane.local/owner-id", "gameplane.local/collaborators"} {
		for _, observedUID := range []string{"original-uid", "replacement-uid"} {
			t.Run(annotation+"/"+observedUID, func(t *testing.T) {
				server := &unstructured.Unstructured{Object: map[string]any{
					"metadata": map[string]any{"name": "same-name", "namespace": scope.DefaultNamespace, "uid": "original-uid", "annotations": map[string]any{annotation: "42"}},
				}}
				handler := Middleware(identityFetcher{server: server})(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					if err := ValidateServerIdentity(req.Context(), "remote", scope.DefaultNamespace, "same-name", observedUID); err != nil {
						http.Error(w, "server changed", http.StatusForbidden)
						return
					}
					w.WriteHeader(http.StatusNoContent)
				}))
				req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/same-name/files/write?cluster=remote", nil)
				req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, req)
				expected := http.StatusNoContent
				if observedUID != "original-uid" {
					expected = http.StatusForbidden
				}
				if response.Code != expected {
					t.Fatalf("status=%d want=%d", response.Code, expected)
				}
			})
		}
	}
}

func TestIdentityBindingChecksEveryTargetDimension(t *testing.T) {
	identity := ServerIdentity{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "uid"}
	ctx := context.WithValue(t.Context(), serverIdentityKey{}, identity)
	for _, observed := range []ServerIdentity{
		{Cluster: "local", Namespace: "games", Name: "same-name", UID: "uid"},
		{Cluster: "remote", Namespace: "other", Name: "same-name", UID: "uid"},
		{Cluster: "remote", Namespace: "games", Name: "other", UID: "uid"},
		{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "replacement"},
	} {
		if err := ValidateServerIdentity(ctx, observed.Cluster, observed.Namespace, observed.Name, observed.UID); !errors.Is(err, ErrServerIdentityChanged) {
			t.Fatalf("accepted mismatched target %+v: %v", observed, err)
		}
	}
	if err := ValidateServerIdentity(t.Context(), "remote", "games", "same-name", "replacement"); err != nil {
		t.Fatalf("unbound namespace grant changed: %v", err)
	}
	emptyUID := context.WithValue(t.Context(), serverIdentityKey{}, ServerIdentity{Cluster: "remote", Namespace: "games", Name: "same-name"})
	if err := ValidateServerIdentity(emptyUID, "remote", "games", "same-name", ""); err == nil {
		t.Fatal("empty resource identity accepted")
	}
}

func TestNamespacePermissionDoesNotBecomeOwnershipBound(t *testing.T) {
	user := &auth.User{ID: 42, Perms: map[string]map[string]map[string]struct{}{"remote": {"*": {"servers:write": {}}}}}
	handler := Middleware(identityFetcher{})(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, bound := BoundServerIdentity(req.Context()); bound {
			t.Fatal("namespace permission unexpectedly bound to server")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/same-name/files/write?cluster=remote", nil)
	req = req.WithContext(auth.WithUser(req.Context(), user))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatal(response.Code)
	}
}

func TestOwnerOnlyNamespacePermissionStillBindsOwnershipIdentity(t *testing.T) {
	server := &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "same-name", "namespace": scope.DefaultNamespace, "uid": "original-uid",
			"annotations": map[string]any{"gameplane.local/owner-id": "42"}},
	}}
	user := &auth.User{ID: 42, Perms: map[string]map[string]map[string]struct{}{"remote": {"*": {"servers:write": {}}}}}
	for _, path := range []string{"/servers/same-name", "/servers/same-name:wipe-data"} {
		t.Run(path, func(t *testing.T) {
			handler := Middleware(identityFetcher{server})(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				if err := ValidateServerIdentity(req.Context(), "remote", scope.DefaultNamespace, "same-name", "replacement-uid"); !errors.Is(err, ErrServerIdentityChanged) {
					t.Errorf("owner-only namespace grant rebound to replacement: %v", err)
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			method := http.MethodDelete
			if path != "/servers/same-name" {
				method = http.MethodPost
			}
			req := httptest.NewRequestWithContext(t.Context(), method, path+"?cluster=remote", nil)
			req = req.WithContext(auth.WithUser(req.Context(), user))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, req)
			if response.Code != http.StatusNoContent {
				t.Fatalf("got %d %s", response.Code, response.Body)
			}
		})
	}
}
