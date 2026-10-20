package handlers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// clusterName reads the operator-set "general" config; the existing tests
// only exercise the nil-store path. Cover the store-backed branches.
func clusterInfoName(t *testing.T, generalJSON string) string {
	t.Helper()
	store := newTestStore(t)
	if generalJSON != "" {
		if _, err := store.DB.ExecContext(t.Context(), `INSERT INTO config(key, value) VALUES ('general', ?)`, generalJSON); err != nil {
			t.Fatalf("seed config: %v", err)
		}
	}
	k := &kube.Client{Typed: fake.NewSimpleClientset()}
	r := chi.NewRouter()
	MountCluster(r, clusterTestRegistry(k), store, "v1", false, "")
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cluster/info", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var info clusterInfo
	if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return info.ClusterName
}

func TestCluster_ClusterName(t *testing.T) {
	t.Run("from instanceName in general config", func(t *testing.T) {
		if got := clusterInfoName(t, `{"instanceName":"prod-east"}`); got != "prod-east" {
			t.Fatalf("clusterName = %q, want prod-east", got)
		}
	})
	t.Run("empty when no general config row", func(t *testing.T) {
		if got := clusterInfoName(t, ""); got != "" {
			t.Fatalf("clusterName = %q, want empty", got)
		}
	})
	t.Run("empty when general config is malformed", func(t *testing.T) {
		if got := clusterInfoName(t, "{not json"); got != "" {
			t.Fatalf("clusterName = %q, want empty", got)
		}
	})
}
