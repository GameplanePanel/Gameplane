package handlers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/go-chi/chi/v5"
)

func TestStandaloneModulesUseExplicitTargetAndItsPermission(t *testing.T) {
	reg := kube.NewRegistry("local")
	reg.SetManagement(fakeKubeClientWithClusters())
	reg.Set("remote", fakeKubeClient(newModule("remote-only", nil)))
	reg.Set("other", fakeKubeClient(newModule("other-only", nil)))
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountModulesWithRegistry(r, reg, "gameplane-system")
	user := inventoryUser("remote", "*", "modules:read")
	rr := inventoryRequest(t, r, user, http.MethodGet, "/modules/?cluster=remote")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), "remote-only") || strings.Contains(rr.Body.String(), "other-only") {
		t.Fatalf("remote list: %d %s", rr.Code, rr.Body)
	}
	for _, target := range []string{"/modules/", "/modules/?cluster=other", "/modules/?cluster=remote&cluster=other"} {
		rr := inventoryRequest(t, r, user, http.MethodGet, target)
		if rr.Code < 400 {
			t.Fatalf("unscoped/unauthorized request %s: %d %s", target, rr.Code, rr.Body)
		}
	}
}

func TestCombinedModulesCannotUseRemoteGrantForLocalCatalog(t *testing.T) {
	reg := kube.NewRegistry("local")
	reg.Set("local", fakeKubeClient(newModule("local-only", nil)))
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountModulesWithRegistry(r, reg, "gameplane-system")
	rr := inventoryRequest(t, r, inventoryUser("remote", "*", "modules:read"), http.MethodGet, "/modules/?cluster=remote")
	if rr.Code != http.StatusNotImplemented {
		t.Fatalf("remote selector: %d %s", rr.Code, rr.Body)
	}
}
