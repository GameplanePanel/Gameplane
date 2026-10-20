package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type authorizationSnapshot struct{ uid string }

func (authorizationSnapshot) IDs() []string { return []string{"local", "remote"} }
func (a authorizationSnapshot) GetServer(context.Context, string, string, string) (*unstructured.Unstructured, error) {
	return &unstructured.Unstructured{Object: map[string]any{
		"metadata": map[string]any{"name": "alpha", "namespace": "gameplane-games", "uid": a.uid,
			"annotations": map[string]any{"gameplane.local/owner-id": "42"}},
	}}, nil
}

func TestStreamOwnershipCannotRebindToReplacementServer(t *testing.T) {
	k := &kube.Client{}
	_ = streamTestRegistry(k)
	// The current objects are valid but belong to a newer GameServer identity.
	if _, err := serverPod(t.Context(), k, "gameplane-games", "alpha"); err != nil {
		t.Fatal(err)
	}
	handler := rbac.Middleware(authorizationSnapshot{uid: "previous-server-uid"})(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if _, err := serverPod(req.Context(), k, "gameplane-games", "alpha"); err == nil {
			t.Error("ownership authorization rebound to a same-named replacement")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ws/servers/alpha/logs/pod?cluster=remote", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNoContent {
		t.Fatalf("authorization fixture did not reach handler: %d", response.Code)
	}
}

func TestStreamClientCannotChangeOwnershipAuthorizedCluster(t *testing.T) {
	reg := kube.NewRegistry("local")
	reg.Set("local", &kube.Client{})
	reg.Set("remote", &kube.Client{})
	handler := rbac.Middleware(authorizationSnapshot{uid: "original-uid"})(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		req.URL.RawQuery = "cluster=local"
		if _, ok := streamClient(w, req, reg); ok {
			t.Error("cluster changed after ownership authorization")
		}
	}))
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ws/servers/alpha/logs/pod?cluster=remote", nil)
	req = req.WithContext(auth.WithUser(req.Context(), &auth.User{ID: 42}))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, req)
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
}
