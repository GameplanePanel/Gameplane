package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubetesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

func TestClusterDiscovery_FiltersRegistrationsAndInventoryCapability(t *testing.T) {
	for _, tc := range []struct {
		name, cluster, namespace, permission string
		want                                 []string
		inventory                            bool
	}{
		{"local reader", "local", "*", "cluster:read", []string{"local"}, true},
		{"remote reader", "remote", "*", "cluster:read", []string{"remote"}, true},
		{"remote server operator", "remote", "gameplane-games", "servers:read", []string{"remote"}, false},
		{"namespace admin", "remote", "gameplane-games", "*", []string{"remote"}, false},
		{"unrelated permission", "remote", "gameplane-games", "backups:read", nil, false},
		{"namespace inventory is insufficient", "remote", "gameplane-games", "cluster:read", nil, false},
		{"wildcard server reader", "*", "gameplane-games", "servers:read", []string{"local", "hidden", "remote"}, false},
		{"user manager discovers grant targets", "local", "*", "users:manage", []string{"local", "hidden", "remote"}, false},
		{"cluster manager discovers registration targets", "local", "*", "cluster:manage", []string{"local", "hidden", "remote"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := fakeKubeClientWithClusters(
				newCluster("remote", map[string]any{"displayName": "Remote display"}, map[string]any{"phase": "Healthy", "serverVersion": "v1.31.2", "message": "private health details"}),
				newCluster("hidden", map[string]any{"displayName": "Hidden display"}, nil),
			)
			reg := clusterTestRegistry(home)
			reg.Set("remote", inventoryClient("remote-node", "v1.31.2", "7Gi"))
			r := chi.NewRouter()
			r.Use(rbac.Middleware(reg))
			MountClusters(r, reg, home, "gameplane-system")
			MountCluster(r, reg, nil, "test", true, "edge")
			u := inventoryUser(tc.cluster, tc.namespace, tc.permission)
			rr := inventoryRequest(t, r, u, http.MethodGet, "/clusters/")
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			var got clustersListResp
			if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if len(got.Items) != len(tc.want) {
				t.Fatalf("items=%+v want names=%v", got.Items, tc.want)
			}
			for i, item := range got.Items {
				if item.Name != tc.want[i] || item.CanViewInventory != tc.inventory {
					t.Fatalf("item=%+v want name=%s inventory=%v", item, tc.want[i], tc.inventory)
				}
				if !tc.inventory && (item.ServerVersion != "" || item.Message == "private health details" || item.LastCheckTime != "") {
					t.Fatal("server-only discovery exposed inventory details")
				}
			}
			if tc.namespace == "gameplane-games" && tc.cluster == "remote" {
				denied := inventoryRequest(t, r, u, http.MethodGet, "/cluster?cluster=remote")
				if denied.Code != http.StatusForbidden {
					t.Fatalf("namespace-only inventory=%d want403", denied.Code)
				}
			}
		})
	}
}

func TestClusterDiscovery_RetainsDisconnectedRegistration(t *testing.T) {
	home := fakeKubeClientWithClusters(newCluster("offline", map[string]any{"displayName": "Offline cluster"}, map[string]any{"phase": "Healthy"}))
	reg := clusterTestRegistry(home)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountClusters(r, reg, home, "gameplane-system")
	MountCluster(r, reg, nil, "test", false, "")
	u := inventoryUser("offline", "*", "cluster:read")
	rr := inventoryRequest(t, r, u, http.MethodGet, "/clusters/")
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	var got clustersListResp
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].Name != "offline" || got.Items[0].Phase != "Unhealthy" || !got.Items[0].CanViewInventory {
		t.Fatalf("disconnected registration lost: %+v", got.Items)
	}
	for _, route := range []string{"/cluster", "/cluster/info", "/cluster/stats"} {
		rr := inventoryRequest(t, r, u, http.MethodGet, route+"?cluster=offline")
		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: status=%d body=%s", route, rr.Code, rr.Body)
		}
	}
}

func TestClusterDiscovery_RequiresAuthentication(t *testing.T) {
	home := fakeKubeClientWithClusters()
	reg := clusterTestRegistry(home)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountClusters(r, reg, home, "gameplane-system")
	rr := inventoryRequest(t, r, nil, http.MethodGet, "/clusters/")
	if rr.Code != http.StatusUnauthorized || strings.Contains(rr.Body.String(), "local") || kubeClientCalls(t, home) != 0 {
		t.Fatalf("unauthenticated discovery: status=%d body=%s", rr.Code, rr.Body)
	}
}

func TestClusterDiscovery_MissingCRDOnlyFallsBackToLocal(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
	}{
		{"missing CRD", apierrors.NewNotFound(kube.GVRCluster.GroupResource(), ""), http.StatusOK},
		{"forbidden", apierrors.NewForbidden(kube.GVRCluster.GroupResource(), "", errors.New("private denial")), http.StatusForbidden},
		{"unavailable", errors.New("private connection error"), http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := fakeKubeClientWithClusters()
			home.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("list", "clusters", func(kubetesting.Action) (bool, runtime.Object, error) {
				return true, nil, tc.err
			})
			reg := clusterTestRegistry(home)
			r := chi.NewRouter()
			r.Use(rbac.Middleware(reg))
			MountClusters(r, reg, home, "gameplane-system")
			rr := inventoryRequest(t, r, inventoryUser("local", "*", "cluster:read"), http.MethodGet, "/clusters/")
			if rr.Code != tc.status || strings.Contains(rr.Body.String(), "private") {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body)
			}
			if tc.status == http.StatusOK {
				var got clustersListResp
				if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if len(got.Items) != 1 || got.Items[0].Name != "local" || !got.Items[0].CanViewInventory {
					t.Fatalf("local-only discovery lost: %+v", got.Items)
				}
			}
		})
	}
}

func TestClusterDiscovery_ClientWithoutTypedInventoryIsUnavailable(t *testing.T) {
	home := fakeKubeClientWithClusters(newCluster("remote", nil, map[string]any{"phase": "Healthy"}))
	reg := clusterTestRegistry(home)
	reg.Set("remote", &kube.Client{})
	r := chi.NewRouter()
	MountClusters(r, reg, home, "gameplane-system")
	rr := inventoryRequest(t, r, inventoryUser("remote", "*", "cluster:read"), http.MethodGet, "/clusters/")
	var got clustersListResp
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if rr.Code != http.StatusOK || len(got.Items) != 1 || got.Items[0].Phase != "Unhealthy" {
		t.Fatalf("unavailable client not reported: status=%d items=%+v", rr.Code, got.Items)
	}
}
