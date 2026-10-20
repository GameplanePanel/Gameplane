//go:build envtest

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T057/T058: capture RBAC and audit-failure regression coverage.
//
// This file deliberately builds its own chi.Mux with rbac.Middleware
// wired in front of MountCapture, rather than reusing
// suite_envtest_test.go's mountedR/apiSrv: that shared router is mounted
// WITHOUT auth/RBAC middleware (see its own doc comment), because most
// of this package's envtest suites only need to exercise handler<->kube
// wiring. RBAC coverage needs the middleware in the loop, so it has to
// live on a separate router — but it still reuses the SAME envtest
// apiserver (testEnv/cfg/kubeC) the rest of the package shares; no
// second apiserver is started here.
//
// "Login" in this file means auth.WithUser(ctx, ...) — injecting a
// pre-resolved *auth.User into the request context, exactly like
// capture_test.go's existing TestCaptureEnableDisable_RBAC_OperatorForbidden
// does. There is no /auth/login round trip anywhere in this file, so the
// per-IP/per-user login rate limiter (mounted only in api/cmd/main.go's
// production router, not here) is never touched: this file contributes
// ZERO logins toward any bucket's login budget. Each of the four *roles*
// below (operator, viewer, admin, owner-without-captures:manage) is
// still constructed exactly once and reused across all 8 routes, in the
// same spirit as the budget rule, even though no real login occurs.

// captureRBACRoute is one of the 8 capture routes under test.
type captureRBACRoute struct {
	label  string
	method string
	suffix string // appended to "/servers/{server}"
	body   any
}

// captureRBACRoutes returns the fixed 8-route matrix. Path/id values are
// deliberately bogus (a nonexistent capture) where a query id is needed:
// RBAC denial happens in the middleware, before the handler ever looks
// at them, and downstream 404/409 for a missing fixture is an acceptable
// outcome for the "admin is RBAC-reachable" checks.
func captureRBACRoutes() []captureRBACRoute {
	return []captureRBACRoute{
		{"capture-enable", http.MethodPost, ":capture-enable", nil},
		{"capture-disable", http.MethodPost, ":capture-disable", nil},
		{"capture-start", http.MethodPost, ":capture-start", map[string]any{
			"maxDurationSeconds": 60, "maxSizeBytes": int64(1024),
		}},
		{"capture-stop", http.MethodPost, ":capture-stop", map[string]any{"captureId": "cap-ghost"}},
		{"captures (list)", http.MethodGet, ":captures", nil},
		{"capture (get)", http.MethodGet, ":capture?id=cap-ghost", nil},
		{"capture-file (download)", http.MethodGet, ":capture-file?id=cap-ghost", nil},
		{"capture (delete)", http.MethodDelete, ":capture?id=cap-ghost", nil},
	}
}

// newCaptureRBACRouter builds a chi.Mux with rbac.Middleware wired in
// front of MountCapture, backed by the shared envtest kube client
// (kubeC) and the shared capture audit store (captureAuditStore) that
// suite_envtest_test.go's TestMain already opened and migrated.
func newCaptureRBACRouter(t *testing.T) (*chi.Mux, *kube.Registry) {
	t.Helper()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, kubeC)

	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountCapture(r, reg, audit.New(captureAuditStore), CaptureConfig{
		FeatureEnabled:          true,
		DefaultRetentionSeconds: 86400,
		MaxRetentionSeconds:     604800,
		DefaultMaxDurationSecs:  300,
		DefaultMaxSizeBytes:     943718400, // 900 MiB, matching production (charts/gameplane/values.yaml)
	}, "", "", "")
	return r, reg
}

// doAsUser sends method+path through h with u (nil for unauthenticated)
// injected into the request context via auth.WithUser.
func doAsUser(t *testing.T, h http.Handler, u *auth.User, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		buf = bytes.NewReader(b)
	}
	ctx := t.Context()
	if u != nil {
		ctx = auth.WithUser(ctx, u)
	}
	req := httptest.NewRequestWithContext(ctx, method, path, buf)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// createRBACTestServer writes a bare GameServer fixture (capture
// disabled) for the RBAC matrix; the 403/401 checks never reach the
// handler at all, and the "admin is reachable" checks only need
// downstream 400/404/409 to be acceptable, not a fully-provisioned
// capture-ready server.
func createRBACTestServer(t *testing.T, name string, ownerID int64) {
	t.Helper()
	metadata := map[string]any{"name": name, "namespace": scope.DefaultNamespace}
	if ownerID != 0 {
		metadata["annotations"] = map[string]any{
			"gameplane.local/owner-id": strconv.FormatInt(ownerID, 10),
		}
	}
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   metadata,
		"spec": map[string]any{
			"templateRef": map[string]any{"name": "minecraft"},
		},
	}}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Create(context.Background(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create server fixture %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

// operatorUser holds servers:read/write/console — the namespaced part of
// the built-in operator role's permission set
// (api/internal/db/migrations/003_roles.sql) — but deliberately NOT
// captures:manage. This is the load-bearing part of the fixture: the
// operator role's other perms (backups, schedules, templates, …) don't
// affect any capture route and are omitted so this stays a precise
// reproduction of what's being asserted, not a full role mirror.
func operatorUser() *auth.User {
	return &auth.User{
		ID:       101,
		Username: "op-rbac",
		Role:     rbac.RoleOperator,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {
				"servers:read":    {},
				"servers:write":   {},
				"servers:console": {},
			}},
		},
	}
}

// viewerUser holds only servers:read — the namespaced part of the
// built-in viewer role's permission set that's relevant here (read-only,
// no servers:write and no captures:manage).
func viewerUser() *auth.User {
	return &auth.User{
		ID:       102,
		Username: "view-rbac",
		Role:     rbac.RoleViewer,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {
				"servers:read": {},
			}},
		},
	}
}

// adminUser holds the built-in admin wildcard ("*" on any cluster/ns),
// which is how migration 003_roles.sql seeds the admin role and how
// admin reaches captures:manage without it being seeded explicitly
// (see api/internal/rbac/catalog.go's doc comment referenced by this
// task's brief).
func adminUser() *auth.User {
	return &auth.User{
		ID:       1,
		Username: "admin-rbac",
		Role:     rbac.RoleAdmin,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {"*": {}}},
		},
	}
}

// TestCaptureRBAC_OperatorMatrix_403 is the mandatory regression test for
// the rule-ordering constraint documented at the top of
// api/internal/rbac/rbac.go: all 8 capture rules must precede the
// servers:write catch-all in the rule table. If a capture rule is ever
// moved after that catch-all, every capture route silently resolves to
// servers:write — which the operator role already holds — and this is
// the test that catches it, because operatorUser() above holds
// servers:write but deliberately does NOT hold captures:manage.
func TestCaptureRBAC_OperatorMatrix_403(t *testing.T) {
	r, _ := newCaptureRBACRouter(t)
	server := uniqueResourceName("rbac-op")
	createRBACTestServer(t, server, 0)
	op := operatorUser()

	for _, rt := range captureRBACRoutes() {
		t.Run(rt.label, func(t *testing.T) {
			rr := doAsUser(t, r, op, rt.method, "/servers/"+server+rt.suffix, rt.body)
			if rr.Code != http.StatusForbidden {
				t.Errorf("operator %s %s = %d, want 403 (captures:manage rule has likely drifted below the "+
					"servers:write catch-all in rbac.go's rule table — the operator role holds servers:write, "+
					"which would then wrongly satisfy this route); body=%s",
					rt.method, rt.suffix, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestCaptureRBAC_ViewerMatrix_Denied proves a viewer (read-only,
// holding neither servers:write nor captures:manage) is denied on every
// capture route.
func TestCaptureRBAC_ViewerMatrix_Denied(t *testing.T) {
	r, _ := newCaptureRBACRouter(t)
	server := uniqueResourceName("rbac-view")
	createRBACTestServer(t, server, 0)
	v := viewerUser()

	for _, rt := range captureRBACRoutes() {
		t.Run(rt.label, func(t *testing.T) {
			rr := doAsUser(t, r, v, rt.method, "/servers/"+server+rt.suffix, rt.body)
			if rr.Code != http.StatusForbidden {
				t.Errorf("viewer %s %s = %d, want 403; body=%s", rt.method, rt.suffix, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestCaptureRBAC_AdminMatrix_Reachable proves an admin token is never
// RBAC-blocked on any of the 8 capture routes. The precise status varies
// per route and fixture state (200/202/400/404/409/501 are all plausible
// depending on the route and whether a capture with the given id
// exists), so this asserts "not 403 and not 401" rather than a specific
// code — a specific downstream status belongs to capture_envtest_test.go
// and capture_test.go's handler-level tests, not this RBAC-focused file.
func TestCaptureRBAC_AdminMatrix_Reachable(t *testing.T) {
	r, _ := newCaptureRBACRouter(t)
	server := uniqueResourceName("rbac-admin")
	createRBACTestServer(t, server, 0)
	admin := adminUser()

	for _, rt := range captureRBACRoutes() {
		t.Run(rt.label, func(t *testing.T) {
			rr := doAsUser(t, r, admin, rt.method, "/servers/"+server+rt.suffix, rt.body)
			if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
				t.Errorf("admin %s %s = %d, want anything but 401/403 (admin must reach the handler); body=%s",
					rt.method, rt.suffix, rr.Code, rr.Body.String())
			}
		})
	}
}

// TestCaptureRBAC_OwnerWithoutCapturesManage_403 proves a distinct and
// easily-regressed property: rbac.go's owner/collaborator fallback is
// restricted to servers:read/servers:write/servers:console (see its own
// doc comment and the "Namespaced(r.perm) && ... (r.perm == "servers:read"
// || ...)" guard) and does NOT extend to captures:manage. A user who
// genuinely owns the GameServer (gameplane.local/owner-id annotation
// matches their ID) must still be denied every capture route, unlike the
// same owner hitting a plain /servers/{name} route.
func TestCaptureRBAC_OwnerWithoutCapturesManage_403(t *testing.T) {
	r, _ := newCaptureRBACRouter(t)
	const ownerID int64 = 999
	server := uniqueResourceName("rbac-owner")
	createRBACTestServer(t, server, ownerID)

	// This user holds NO catalog permissions at all (an empty Perms map,
	// like a freshly-registered account with no role binding) — the only
	// thing that could grant access is the ownership annotation on the
	// server fixture above, via rbac.go's owner/collaborator fallback.
	owner := &auth.User{ID: ownerID, Username: "owner-rbac", Role: "custom-no-perms"}

	for _, rt := range captureRBACRoutes() {
		t.Run(rt.label, func(t *testing.T) {
			rr := doAsUser(t, r, owner, rt.method, "/servers/"+server+rt.suffix, rt.body)
			if rr.Code != http.StatusForbidden {
				t.Errorf("server-owning-but-non-captures:manage user %s %s = %d, want 403 "+
					"(the owner/collaborator fallback in rbac.go only covers servers:read/write/console, "+
					"never captures:manage); body=%s", rt.method, rt.suffix, rr.Code, rr.Body.String())
			}
		})
	}
}

// newBrokenAuditStore opens a normal in-memory sqlite store, runs
// migrations (so it starts out schema-valid, not merely absent), then
// closes it — every subsequent Exec/Query on the underlying *sql.DB
// deterministically returns sql.ErrConnDone-derived errors, giving
// audit.Auditor.WriteSync a clean, reproducible failure without needing
// to point it at an unwritable filesystem path. Uses its own uniquely
// named DSN (not the unnamed "file::memory:" newTestStore uses, and not
// captureAuditStore's "captureaudit" name) so closing it can't pin or
// interfere with any other test's shared in-memory database — see
// suite_envtest_test.go's TestMain comment on why DSN names must be
// distinct here.
func newBrokenAuditStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file:capturebroken?mode=memory&cache=shared&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate store: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("close store: %v", err)
	}
	return store
}

// TestCaptureAudit_BrokenStore_FailsOperation is T058: FR-006 requires
// the audit row to be written before the operation response, and
// capture.go's auditWriteOrFail (unlike audit.WriteSync's own, more
// permissive doc comment — see this test's final subtest for a note on
// that mismatch) treats a failed write as fatal to the whole request: it
// writes its own 500 and returns false, and every call site in
// capture.go returns immediately without writing any other response.
// This asserts that contract holds for a mutating route (delete) and for
// download — the one route FR-006 explicitly calls out as exposing
// packet data, so it must never stream unaudited.
//
// Every case below exercises the EARLIEST auditWriteOrFail call in its
// handler (the "server not found" / "missing id" gate), deliberately:
// that is the one call site in each handler that is guaranteed to run
// before any Kubernetes mutation is attempted, so "the operation fails
// rather than silently proceeding" is unambiguously true here. Later
// call sites in captureStart/captureDelete (audited AFTER
// CreateNetworkCapture/DeleteNetworkCapture already succeeded against
// the apiserver) are a separate, real ordering gap worth flagging to the
// maintainer, but asserting against them here would require the test to
// pick a side on behavior this file's author does not own fixing.
func TestCaptureAudit_BrokenStore_FailsOperation(t *testing.T) {
	brokenStore := newBrokenAuditStore(t)
	brokenAuditor := audit.New(brokenStore)

	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, kubeC)
	r := chi.NewRouter()
	// No rbac.Middleware here deliberately: this test is about the
	// audit-write failure gate inside the handler itself, not about
	// permission checks (those are covered by the *Matrix tests above).
	MountCapture(r, reg, brokenAuditor, CaptureConfig{
		FeatureEnabled:          true,
		DefaultRetentionSeconds: 86400,
		MaxRetentionSeconds:     604800,
		DefaultMaxDurationSecs:  300,
		DefaultMaxSizeBytes:     943718400, // 900 MiB, matching production (charts/gameplane/values.yaml)
	}, "", "", "")

	ghostServer := uniqueResourceName("rbac-ghost")

	cases := []struct {
		label  string
		method string
		path   string
		body   any
	}{
		{"start (mutating)", http.MethodPost, "/servers/" + ghostServer + ":capture-start", map[string]any{
			"maxDurationSeconds": 60, "maxSizeBytes": int64(1024),
		}},
		{"delete (mutating)", http.MethodDelete, "/servers/" + ghostServer + ":capture?id=cap-ghost", nil},
		{"download", http.MethodGet, "/servers/" + ghostServer + ":capture-file?id=cap-ghost", nil},
	}

	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			rr := do(t, r, tc.method, tc.path, tc.body)
			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("%s status = %d, want 500 (a broken audit store must fail the operation, not let it "+
					"silently proceed); body=%s", tc.label, rr.Code, rr.Body.String())
			}
			// httperr.WriteCode discards the caller-supplied message for
			// status >= 500 and writes http.StatusText instead (a
			// deliberate anti-leak design) — the body must therefore be
			// exactly "Internal Server Error", never "audit write
			// failed" or any other detail that would leak internals.
			if got := strings.TrimSpace(rr.Body.String()); got != http.StatusText(http.StatusInternalServerError) {
				t.Errorf("%s body = %q, want %q", tc.label, got, http.StatusText(http.StatusInternalServerError))
			}
		})
	}

	// Confirm the ghost server truly never existed and nothing was
	// created for it — the 500s above were not masking a partial
	// success.
	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(context.Background(), ghostServer, metav1.GetOptions{}); err == nil {
		t.Errorf("ghost server %s unexpectedly exists", ghostServer)
	}
}
