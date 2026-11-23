//go:build envtest

package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// T014: envtest for the spec.security permission behavior on create,
// update and clone (contracts/api-server-security.md cases 1 to 3).
//
// Like capture_rbac_envtest_test.go, this file builds its own router with
// rbac.Middleware in front of the handlers. It never uses the shared
// mountedR, which has no RBAC middleware. It reuses the envtest apiserver
// (kubeC) that suite_envtest_test.go starts.

// secOvRouter builds a dedicated router with rbac.Middleware wired in front
// of the resources and lifecycle handlers, backed by kubeC.
func secOvRouter(t *testing.T) *chi.Mux {
	t.Helper()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, kubeC)

	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountResources(r, reg, nil)
	MountLifecycle(r, reg)
	return r
}

// secOvWriter holds servers:write and deliberately not servers:security.
func secOvWriter() *auth.User {
	return &auth.User{
		ID:       301,
		Username: "sec-op",
		Role:     rbac.RoleOperator,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {
				"servers:read":  {},
				"servers:write": {},
			}},
		},
	}
}

// secOvHolder holds servers:write and servers:security.
func secOvHolder() *auth.User {
	return &auth.User{
		ID:       302,
		Username: "sec-holder",
		Role:     rbac.RoleOperator,
		Perms: map[string]map[string]map[string]struct{}{
			"*": {"*": {
				"servers:read":     {},
				"servers:write":    {},
				"servers:security": {},
			}},
		},
	}
}

// secOvDo sends one request as u through h. It asks for an RFC 9457 problem
// document so a refusal carries its messageId.
func secOvDo(t *testing.T, h http.Handler, u *auth.User, method, path string, body any) *httptest.ResponseRecorder {
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
	req.Header.Set("Accept", "application/problem+json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// secOvFixture creates a GameServer in scope.DefaultNamespace whose
// spec.security is security (nil leaves the field absent) and removes it at
// cleanup.
func secOvFixture(t *testing.T, name string, security map[string]any) {
	t.Helper()
	spec := map[string]any{"templateRef": map[string]any{"name": "minecraft"}}
	if security != nil {
		spec["security"] = security
	}
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": name, "namespace": scope.DefaultNamespace},
		"spec":       spec,
	}}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Create(t.Context(), gs, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create server fixture %s: %v", name, err)
	}
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})
}

// secOvStoredSecurity reads spec.security of the stored server name.
func secOvStoredSecurity(t *testing.T, name string) (map[string]any, bool) {
	t.Helper()
	got, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get server %s: %v", name, err)
	}
	sec, found, err := unstructured.NestedMap(got.Object, "spec", "security")
	if err != nil {
		t.Fatalf("read spec.security of %s: %v", name, err)
	}
	return sec, found
}

// secOvPutBody is a full GameServer body for PUT /servers/{name}. security
// nil omits the block; labels adds an unrelated metadata change.
func secOvPutBody(name string, security map[string]any, labels map[string]any) map[string]any {
	spec := map[string]any{"templateRef": map[string]any{"name": "minecraft"}}
	if security != nil {
		spec["security"] = security
	}
	meta := map[string]any{"name": name, "namespace": scope.DefaultNamespace}
	// PUT is an optimistic update, so the body carries the stored version.
	if live, err := kubeC.Dynamic.Resource(gvrServers()).Namespace(scope.DefaultNamespace).
		Get(context.Background(), name, metav1.GetOptions{}); err == nil {
		meta["resourceVersion"] = live.GetResourceVersion()
	}
	if labels != nil {
		meta["labels"] = labels
	}
	return map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   meta,
		"spec":       spec,
	}
}

// secOvProblemID decodes the messageId of an RFC 9457 refusal.
func secOvProblemID(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var doc struct {
		MessageID string `json:"messageId"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &doc); err != nil {
		t.Fatalf("decode problem body %q: %v", rr.Body.String(), err)
	}
	return doc.MessageID
}

func secOvChanged() map[string]any {
	return map[string]any{"runAsUser": map[string]any{"value": float64(3000)}}
}

func secOvStored() map[string]any {
	return map[string]any{"runAsUser": map[string]any{"value": int64(2000)}}
}

// TestSecurityOverride_Update_PermissionMatrix covers update behavior for a
// caller with servers:write only, and for an admin.
func TestSecurityOverride_Update_PermissionMatrix(t *testing.T) {
	r := secOvRouter(t)
	writer := secOvWriter()

	t.Run("changed block without servers:security is 403", func(t *testing.T) {
		name := uniqueResourceName("sec-ov-chg")
		secOvFixture(t, name, secOvStored())

		rr := secOvDo(t, r, writer, http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secOvChanged(), nil))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("PUT changed block = %d, want 403; body=%s", rr.Code, rr.Body.String())
		}
		if id := secOvProblemID(t, rr); id != "server.security.forbidden" {
			t.Errorf("messageId = %q, want server.security.forbidden", id)
		}
		if sec, _ := secOvStoredSecurity(t, name); !securityValuesEqual(sec, secOvStored()) {
			t.Errorf("refused write changed the stored block: %#v", sec)
		}
	})

	t.Run("unrelated edit sending the same block back is 200", func(t *testing.T) {
		name := uniqueResourceName("sec-ov-same")
		secOvFixture(t, name, secOvStored())

		rr := secOvDo(t, r, writer, http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secOvStored(), map[string]any{"sec-test": "unrelated"}))
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT unchanged block = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if sec, _ := secOvStoredSecurity(t, name); !securityValuesEqual(sec, secOvStored()) {
			t.Errorf("stored block after unrelated edit = %#v, want %#v", sec, secOvStored())
		}
	})

	t.Run("block omitted keeps the stored override", func(t *testing.T) {
		name := uniqueResourceName("sec-ov-omit")
		secOvFixture(t, name, secOvStored())

		rr := secOvDo(t, r, writer, http.MethodPut, "/servers/"+name,
			secOvPutBody(name, nil, map[string]any{"sec-test": "omitted"}))
		if rr.Code != http.StatusOK {
			t.Fatalf("PUT without block = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		sec, found := secOvStoredSecurity(t, name)
		if !found {
			t.Fatal("omitted block wiped the stored override")
		}
		if !securityValuesEqual(sec, secOvStored()) {
			t.Errorf("stored block = %#v, want %#v", sec, secOvStored())
		}
	})

	t.Run("admin changed block is 200", func(t *testing.T) {
		name := uniqueResourceName("sec-ov-admin")
		secOvFixture(t, name, secOvStored())

		rr := secOvDo(t, r, adminUser(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secOvChanged(), nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("admin PUT changed block = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
		if sec, _ := secOvStoredSecurity(t, name); !securityValuesEqual(sec, secOvChanged()) {
			t.Errorf("stored block = %#v, want %#v", sec, secOvChanged())
		}
	})

	t.Run("servers:security holder changed block is 200", func(t *testing.T) {
		name := uniqueResourceName("sec-ov-holder")
		secOvFixture(t, name, secOvStored())

		rr := secOvDo(t, r, secOvHolder(), http.MethodPut, "/servers/"+name,
			secOvPutBody(name, secOvChanged(), nil))
		if rr.Code != http.StatusOK {
			t.Fatalf("holder PUT changed block = %d, want 200; body=%s", rr.Code, rr.Body.String())
		}
	})
}

// TestSecurityOverride_Create_ForbiddenWithoutPermission covers create: a
// caller with servers:write only cannot create a server with an override.
func TestSecurityOverride_Create_ForbiddenWithoutPermission(t *testing.T) {
	r := secOvRouter(t)
	name := uniqueResourceName("sec-ov-create")
	t.Cleanup(func() {
		_ = kubeC.Dynamic.Resource(gvrServers()).
			Namespace(scope.DefaultNamespace).
			Delete(context.Background(), name, metav1.DeleteOptions{})
	})

	rr := secOvDo(t, r, secOvWriter(), http.MethodPost, "/servers",
		secOvPutBody(name, secOvStored(), nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("POST with override = %d, want 403; body=%s", rr.Code, rr.Body.String())
	}
	if id := secOvProblemID(t, rr); id != "server.security.forbidden" {
		t.Errorf("messageId = %q, want server.security.forbidden", id)
	}
	if _, err := kubeC.Dynamic.Resource(gvrServers()).
		Namespace(scope.DefaultNamespace).
		Get(t.Context(), name, metav1.GetOptions{}); err == nil {
		t.Error("refused create left a server behind")
	}
}

// TestSecurityOverride_Clone_PermissionMatrix covers clone: a caller without
// servers:security cannot clone a server that has an override; admin can.
func TestSecurityOverride_Clone_PermissionMatrix(t *testing.T) {
	r := secOvRouter(t)

	t.Run("clone of a server with override without permission is 403", func(t *testing.T) {
		src := uniqueResourceName("sec-ov-csrc")
		secOvFixture(t, src, secOvStored())
		clone := uniqueResourceName("sec-ov-cdst")

		rr := secOvDo(t, r, secOvWriter(), http.MethodPost, "/servers/"+src+":clone",
			map[string]any{"newName": clone})
		t.Cleanup(func() {
			_ = kubeC.Dynamic.Resource(gvrServers()).
				Namespace(scope.DefaultNamespace).
				Delete(context.Background(), clone, metav1.DeleteOptions{})
		})
		if rr.Code != http.StatusForbidden {
			t.Fatalf("clone with override = %d, want 403; body=%s", rr.Code, rr.Body.String())
		}
		if id := secOvProblemID(t, rr); id != "server.security.forbidden" {
			t.Errorf("messageId = %q, want server.security.forbidden", id)
		}
	})

	t.Run("admin clone of a server with override succeeds", func(t *testing.T) {
		src := uniqueResourceName("sec-ov-asrc")
		secOvFixture(t, src, secOvStored())
		clone := uniqueResourceName("sec-ov-adst")
		t.Cleanup(func() {
			_ = kubeC.Dynamic.Resource(gvrServers()).
				Namespace(scope.DefaultNamespace).
				Delete(context.Background(), clone, metav1.DeleteOptions{})
		})

		rr := secOvDo(t, r, adminUser(), http.MethodPost, "/servers/"+src+":clone",
			map[string]any{"newName": clone})
		if rr.Code/100 != 2 {
			t.Fatalf("admin clone = %d, want 2xx; body=%s", rr.Code, rr.Body.String())
		}
		if sec, found := secOvStoredSecurity(t, clone); !found || !securityValuesEqual(sec, secOvStored()) {
			t.Errorf("clone block = %#v (found %v), want %#v", sec, found, secOvStored())
		}
	})
}
