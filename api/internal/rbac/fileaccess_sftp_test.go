package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// faFailingLister reports an error for every namespace.
type faFailingLister struct{ faFakeCluster }

func (faFailingLister) ListServers(context.Context, string, string) ([]unstructured.Unstructured, error) {
	return nil, errors.New("list failed")
}

// faServe runs one request through MiddlewareWithFiles and returns whether next ran and the recorder.
func faServe(t *testing.T, h http.Handler, req *http.Request, u *auth.User) (bool, *httptest.ResponseRecorder) {
	t.Helper()
	rec := httptest.NewRecorder()
	req = req.WithContext(auth.WithUser(req.Context(), u))
	h.ServeHTTP(rec, req)
	return rec.Code == http.StatusOK, rec
}

func faFileHandler(files *FileChecker, fetch ServerFetcher) http.Handler {
	return MiddlewareWithFiles(fetch, files)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
}

func TestMiddlewareWithFiles_NilCheckerDefaultsToOwnerFallback(t *testing.T) {
	fetch := faFixture()
	h := faFileHandler(nil, fetch)
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/srv/files/list", nil)
	if ok, rec := faServe(t, h, req, faBare(7)); !ok {
		t.Fatalf("owner refused with default checker: %d", rec.Code)
	}
	req = httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/srv/files/list", nil)
	if ok, _ := faServe(t, h, req, faBare(9)); ok {
		t.Fatal("stranger allowed with default checker")
	}
}

func TestServeFileRoute_ScopeRefusals(t *testing.T) {
	fetch := faFixture()
	h := faFileHandler(NewFileAccess(fetch, fetch), fetch)
	owner := faUser(1, faLocal, scope.DefaultNamespace, "servers:read")
	for name, target := range map[string]string{
		"unknown namespace": "/servers/srv/files/list?namespace=not-a-game-namespace",
		"unknown cluster":   "/servers/srv/files/list?cluster=not-registered",
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			ok, rec := faServe(t, h, req, owner)
			if ok || rec.Code != http.StatusForbidden {
				t.Fatalf("called=%v code=%d, want a 403", ok, rec.Code)
			}
		})
	}
}

func TestServeFileRoute_MCPTokenWithoutClusterGrantGetsClusterForbidden(t *testing.T) {
	fetch := faFixture()
	h := faFileHandler(NewFileAccess(fetch, fetch), fetch)
	stranger := faBare(9)

	plain := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/servers/srv/files/list", nil)
	okPlain, plainRec := faServe(t, h, plain, stranger)

	mcpReq := httptest.NewRequestWithContext(auth.WithMCPToken(t.Context(), 5), http.MethodGet, "/servers/srv/files/list", nil)
	okMCP, mcpRec := faServe(t, h, mcpReq, stranger)

	if okPlain || okMCP {
		t.Fatalf("stranger allowed: plain=%v mcp=%v", okPlain, okMCP)
	}
	if plainRec.Code != http.StatusForbidden || mcpRec.Code != http.StatusForbidden {
		t.Fatalf("codes plain=%d mcp=%d, want 403", plainRec.Code, mcpRec.Code)
	}
	if plainRec.Body.String() == mcpRec.Body.String() {
		t.Fatalf("MCP refusal body equals the plain forbidden body: %s", mcpRec.Body.String())
	}
}

func TestServeFileRoute_SFTPCallerRecordsFileTarget(t *testing.T) {
	fetch := faFixture()
	h := faFileHandler(NewFileAccess(fetch, fetch), fetch)
	reader := faUser(1, faLocal, scope.DefaultNamespace, "servers:read")

	ctx, holder := auth.WithActorHolder(auth.WithSFTPCaller(t.Context(), auth.SFTPCaller{Cluster: faLocal, SessionID: 4}))
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/servers/srv/files/list?path=/world/level.dat", nil)
	if ok, rec := faServe(t, h, req, reader); !ok {
		t.Fatalf("sftp reader refused: %d", rec.Code)
	}
	want := faLocal + "/" + scope.DefaultNamespace + "/srv /world/level.dat"
	if got := holder.Target(); got != want {
		t.Fatalf("audit target = %q, want %q", got, want)
	}

	// A dashboard request leaves the target to the generic audit middleware.
	ctx, holder = auth.WithActorHolder(t.Context())
	req = httptest.NewRequestWithContext(ctx, http.MethodGet, "/servers/srv/files/list?path=/x", nil)
	if ok, _ := faServe(t, h, req, reader); !ok {
		t.Fatal("dashboard reader refused")
	}
	if got := holder.Target(); got != "" {
		t.Fatalf("dashboard request set audit target %q", got)
	}
}

func TestVisibleServers_ListerErrorDeletedAndNamespaceOrder(t *testing.T) {
	reader := func(ns string) *auth.User { return faUser(1, faLocal, ns, "servers:read") }

	failing := &faFailingLister{faFakeCluster: *faFixture()}
	files := NewFileAccess(failing, failing)
	if _, err := files.VisibleServers(t.Context(), reader(scope.DefaultNamespace), faLocal); err == nil {
		t.Fatal("VisibleServers swallowed the lister error")
	}

	orig := scope.AllowedNamespaces
	scope.AllowedNamespaces = []string{scope.DefaultNamespace, "zz-extra"}
	t.Cleanup(func() { scope.AllowedNamespaces = orig })

	gone := faServer(scope.DefaultNamespace, "gone", 1, "")
	now := metav1.NewTime(time.Now())
	gone.SetDeletionTimestamp(&now)
	fetch := faCluster([]string{faLocal},
		faServer("zz-extra", "alpha", 1, ""),
		faServer(scope.DefaultNamespace, "beta", 1, ""),
		gone,
	)
	user := &auth.User{ID: 1, Username: "user1", Perms: map[string]map[string]map[string]struct{}{
		faLocal: {
			scope.DefaultNamespace: {"servers:read": {}},
			"zz-extra":             {"servers:read": {}},
		},
	}}
	refs, err := NewFileAccess(fetch, fetch).VisibleServers(t.Context(), user, faLocal)
	if err != nil {
		t.Fatalf("VisibleServers: %v", err)
	}
	if len(refs) != 2 || refs[0].Name != "beta" || refs[1].Name != "alpha" {
		t.Fatalf("refs = %+v, want beta (default namespace) then alpha, deleted server hidden", refs)
	}
}
