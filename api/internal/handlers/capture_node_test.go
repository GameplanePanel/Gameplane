package handlers

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestNodeCaptureExceptionRequiresAuditedAdministrator(t *testing.T) {
	for _, admin := range []bool{false, true} {
		t.Run(map[bool]string{false: "capture-manager", true: "administrator"}[admin], func(t *testing.T) {
			gs := newCaptureServerObj("game", true)
			gs.SetUID("server-uid")
			k := fakeCaptureClient(gs)
			store := newTestStore(t)
			router := mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, audit.New(store))
			user := &auth.User{Username: "manager", Role: "custom", Perms: map[string]map[string]map[string]struct{}{"local": {"gameplane-games": {"captures:manage": {}}}}}
			want := http.StatusForbidden
			if admin {
				user = testAdminUser()
				want = http.StatusOK
			}
			rr := doWithUser(t, router, http.MethodPost, "/servers/game:capture-node-enable", nil, user)
			if rr.Code != want {
				t.Fatalf("status %d %s", rr.Code, rr.Body)
			}
			live, err := k.GetServer(t.Context(), scope.DefaultNamespace, "game")
			if err != nil {
				t.Fatal(err)
			}
			enabled, _, _ := unstructured.NestedBool(live.Object, "spec", "capture", "nodeDiagnostic")
			if enabled != admin {
				t.Fatalf("opt-in=%t", enabled)
			}
			if admin {
				var count int
				if err := store.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM audit_events WHERE reason = ?", "node_diagnostics_opt_in_host_privileges").Scan(&count); err != nil || count != 1 {
					t.Fatalf("missing explicit audit event count=%d err=%v", count, err)
				}
			}
		})
	}
}

func TestNodeExceptionRequiresAdministratorOnSelectedCluster(t *testing.T) {
	for _, test := range []struct {
		name, target, grantCluster, grantNamespace string
		allowed                                    bool
	}{
		{"other-cluster", "cluster-b", "cluster-a", "*", false},
		{"namespace-only", "cluster-b", "cluster-b", "gameplane-games", false},
		{"selected-cluster", "cluster-b", "cluster-b", "*", true},
		{"default-local", "", "local", "*", true},
		{"explicit-local", "local", "local", "*", true},
		{"normalized-local", " local ", "local", "*", true},
		{"other-cluster-local-target", "local", "cluster-a", "*", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			gs := newCaptureServerObj("game", true)
			gs.SetUID("server-uid")
			k := fakeCaptureClient(gs)
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set("local", k)
			reg.Set("cluster-b", k)
			router := chi.NewRouter()
			MountCapture(router, reg, audit.New(newTestStore(t)), CaptureConfig{FeatureEnabled: true}, "", "", "")
			user := &auth.User{Username: "custom-admin", Role: "custom", Perms: map[string]map[string]map[string]struct{}{test.grantCluster: {test.grantNamespace: {"*": {}}}}}
			target := strings.TrimSpace(test.target)
			if target == "" {
				target = "local"
			}
			if user.Perms[target] == nil {
				user.Perms[target] = map[string]map[string]struct{}{}
			}
			if user.Perms[target][scope.DefaultNamespace] == nil {
				user.Perms[target][scope.DefaultNamespace] = map[string]struct{}{}
			}
			user.Perms[target][scope.DefaultNamespace]["captures:manage"] = struct{}{}
			path := "/servers/game:capture-node-enable"
			if test.target != "" {
				path += "?cluster=" + url.QueryEscape(test.target)
			}
			rr := doWithUser(t, router, http.MethodPost, path, nil, user)
			want := http.StatusForbidden
			if test.allowed {
				want = http.StatusOK
			}
			if rr.Code != want {
				t.Fatalf("status %d want %d: %s", rr.Code, want, rr.Body)
			}
			live, err := k.GetServer(t.Context(), scope.DefaultNamespace, "game")
			if err != nil {
				t.Fatal(err)
			}
			enabled, _, _ := unstructured.NestedBool(live.Object, "spec", "capture", "nodeDiagnostic")
			if enabled != test.allowed {
				t.Fatal("wrong scope granted host exception")
			}
		})
	}
}

func TestNodeExceptionCannotBeGrantedByGenericEditOrClone(t *testing.T) {
	src := newServerObj("gameplane-games", "alpha")
	if err := unstructured.SetNestedField(src.Object, true, "spec", "capture", "nodeDiagnostic"); err != nil {
		t.Fatal(err)
	}
	k := fakeKubeClient(src)
	err := validateAndProtectGameServer(auth.WithUser(t.Context(), testAdminUser()), k, "local", "gameplane-games", "new", src, nil)
	if err == nil || !strings.Contains(err.Error(), "capture-node-enable") {
		t.Fatalf("generic create granted exception: %v", err)
	}
	rr := doWithUser(t, mountLifecycleRouter(k), http.MethodPost, "/servers/alpha:clone", map[string]any{"newName": "beta"}, testAdminUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("clone: %d %s", rr.Code, rr.Body)
	}
	clone, err := k.GetServer(t.Context(), "gameplane-games", "beta")
	if err != nil {
		t.Fatal(err)
	}
	if enabled, _, _ := unstructured.NestedBool(clone.Object, "spec", "capture", "nodeDiagnostic"); enabled {
		t.Fatal("clone inherited node exception")
	}
}

func TestNodeExceptionGenericEditPreservesOmittedOptIn(t *testing.T) {
	manager := &auth.User{Username: "manager", Role: "custom", Perms: map[string]map[string]map[string]struct{}{"local": {"gameplane-games": {"captures:manage": {}}}}}
	for _, user := range []*auth.User{testAdminUser(), manager} {
		for _, enabled := range []bool{false, true} {
			live := newServerObj("gameplane-games", "alpha")
			if err := unstructured.SetNestedField(live.Object, enabled, "spec", "capture", "nodeDiagnostic"); err != nil {
				t.Fatal(err)
			}
			for _, omitCapture := range []bool{false, true} {
				desired := live.DeepCopy()
				unstructured.RemoveNestedField(desired.Object, "spec", "capture", "nodeDiagnostic")
				if omitCapture {
					unstructured.RemoveNestedField(desired.Object, "spec", "capture")
				}
				if err := validateAndProtectGameServer(auth.WithUser(t.Context(), user), fakeKubeClient(live), "local", "gameplane-games", "alpha", desired, live); err != nil {
					t.Fatalf("omitted exception rejected for %s, enabled=%t: %v", user.Role, enabled, err)
				}
				got, _, _ := unstructured.NestedBool(desired.Object, "spec", "capture", "nodeDiagnostic")
				if got != enabled {
					t.Fatal("generic edit changed an omitted exception")
				}
			}
			desired := live.DeepCopy()
			if err := unstructured.SetNestedField(desired.Object, !enabled, "spec", "capture", "nodeDiagnostic"); err != nil {
				t.Fatal(err)
			}
			if err := validateAndProtectGameServer(auth.WithUser(t.Context(), user), fakeKubeClient(live), "local", "gameplane-games", "alpha", desired, live); err == nil || !strings.Contains(err.Error(), "capture-node-enable") {
				t.Fatalf("generic edit changed explicit exception for %s: %v", user.Role, err)
			}
		}
	}
}

func TestNodeCaptureExceptionAuditFailureDoesNotGrant(t *testing.T) {
	gs := newCaptureServerObj("game", true)
	gs.SetUID("server-uid")
	k := fakeCaptureClient(gs)
	store := newTestStore(t)
	auditor := audit.New(store)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	rr := doWithUser(t, mountCaptureTestRouter(k, CaptureConfig{FeatureEnabled: true}, auditor), http.MethodPost, "/servers/game:capture-node-enable", nil, testAdminUser())
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status %d", rr.Code)
	}
	live, err := k.GetServer(t.Context(), scope.DefaultNamespace, "game")
	if err != nil {
		t.Fatal(err)
	}
	enabled, _, _ := unstructured.NestedBool(live.Object, "spec", "capture", "nodeDiagnostic")
	if enabled {
		t.Fatal("granted exception without audit")
	}
}
