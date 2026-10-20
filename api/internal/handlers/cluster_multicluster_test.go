package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	"k8s.io/client-go/kubernetes"
	kubefake "k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	kubetesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
)

func inventoryUser(cluster, namespace, permission string) *auth.User {
	return &auth.User{ID: 42, Perms: map[string]map[string]map[string]struct{}{
		cluster: {namespace: {permission: {}}},
	}}
}

func inventoryRequest(t *testing.T, router http.Handler, user *auth.User, method, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(auth.WithUser(t.Context(), user), method, path, nil)
	rr := httptest.NewRecorder()
	router.ServeHTTP(rr, req)
	return rr
}

func inventoryClient(name, kubeVersion, storage string) *kube.Client {
	k := fakeKubeClientWithClusters()
	cs := kubefake.NewClientset(readyNode(name, true, "4", "8Gi"), boundPV(name+"-pv", storage, true))
	cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = &version.Info{GitVersion: kubeVersion}
	k.Typed = cs
	return k
}

func TestClusterInventory_SelectsClientAndMetadata(t *testing.T) {
	home := inventoryClient("home-node", "v1.30.1", "2Gi")
	remote := inventoryClient("remote-node", "v1.31.2", "7Gi")
	reg := clusterTestRegistry(home)
	reg.Set("remote", remote)
	store := newTestStore(t)
	if _, err := store.DB.ExecContext(t.Context(), `INSERT INTO config(key, value) VALUES ('general', '{"instanceName":"Home instance"}')`); err != nil {
		t.Fatal(err)
	}
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountCluster(r, reg, store, "central-api-build", true, "edge")
	for _, tc := range []struct {
		query, cluster, node, version, label string
		storage                              int64
		ops                                  bool
	}{
		{"", "local", "home-node", "v1.30.1", "Home instance", 2, true},
		{"?cluster=local", "local", "home-node", "v1.30.1", "Home instance", 2, true},
		{"?cluster=remote", "remote", "remote-node", "v1.31.2", "remote", 7, false},
	} {
		t.Run(tc.cluster+tc.query, func(t *testing.T) {
			before := kubeClientCalls(t, home)
			user := inventoryUser(tc.cluster, "*", "cluster:read")
			for _, route := range []string{"/cluster", "/cluster/info", "/cluster/stats"} {
				rr := inventoryRequest(t, r, user, http.MethodGet, route+tc.query)
				if rr.Code != http.StatusOK {
					t.Fatalf("%s: status=%d body=%s", route, rr.Code, rr.Body)
				}
				switch route {
				case "/cluster":
					var view clusterView
					if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
						t.Fatal(err)
					}
					if len(view.Nodes) != 1 || view.Nodes[0].Name != tc.node || view.Version != tc.version || view.Name != tc.label || view.Ready != 1 {
						t.Fatalf("wrong selected cluster view: %+v", view)
					}
				case "/cluster/info":
					var info clusterInfo
					if err := json.Unmarshal(rr.Body.Bytes(), &info); err != nil {
						t.Fatal(err)
					}
					if info.Version != tc.version || info.ClusterName != tc.label || info.ClusterOps != tc.ops || info.GameplaneVersion != "central-api-build" {
						t.Fatalf("wrong selected cluster info: %+v", info)
					}
					if tc.cluster == "remote" && info.UpdateChannel != "" {
						t.Fatal("remote inherited central update channel")
					}
				case "/cluster/stats":
					var stats clusterStats
					if err := json.Unmarshal(rr.Body.Bytes(), &stats); err != nil {
						t.Fatal(err)
					}
					if stats.Nodes != 1 || stats.TotalStorageBytes != 100<<30 || stats.UsedStorageBytes != tc.storage<<30 {
						t.Fatalf("wrong selected cluster storage: %+v", stats)
					}
				}
			}
			if tc.cluster == "remote" && kubeClientCalls(t, home) != before {
				t.Fatal("remote inventory touched home client")
			}
		})
	}
}

func TestClusterInventory_RejectsUnauthorizedAndUnavailable(t *testing.T) {
	for _, route := range []string{"/cluster", "/cluster/info", "/cluster/stats"} {
		for _, tc := range []struct {
			name, selected     string
			user               *auth.User
			want               int
			registrationLookup bool
		}{
			{"unauthenticated", "remote", nil, 401, false},
			{"local grant cannot read remote", "remote", inventoryUser("local", "*", "*"), 403, false},
			{"remote grant cannot read local", "local", inventoryUser("remote", "*", "cluster:read"), 403, false},
			{"namespace wildcard cannot read inventory", "remote", inventoryUser("remote", "gameplane-games", "*"), 403, false},
			{"unknown", "missing", inventoryUser("*", "*", "cluster:read"), 400, true},
			{"invalid cluster ID", "*", inventoryUser("*", "*", "cluster:read"), 400, false},
			{"registered without client", "disconnected", inventoryUser("disconnected", "*", "cluster:read"), 503, true},
			{"nil client", "nil-client", inventoryUser("nil-client", "*", "cluster:read"), 503, false},
		} {
			t.Run(route+"/"+tc.name, func(t *testing.T) {
				home := fakeKubeClientWithClusters(newCluster("disconnected", nil, nil))
				remote := inventoryClient("remote-node", "v1.31.2", "7Gi")
				reg := clusterTestRegistry(home)
				reg.Set("remote", remote)
				reg.Set("nil-client", nil)
				r := chi.NewRouter()
				r.Use(rbac.Middleware(reg))
				MountCluster(r, reg, nil, "test", true, "edge")
				rr := inventoryRequest(t, r, tc.user, http.MethodGet, route+"?cluster="+tc.selected)
				if rr.Code != tc.want {
					t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.want, rr.Body)
				}
				if len(home.Typed.(*kubefake.Clientset).Actions()) != 0 || kubeClientCalls(t, remote) != 0 {
					t.Fatal("denied request accessed inventory")
				}
				wantCalls := 0
				if tc.registrationLookup {
					wantCalls = 1
				}
				if kubeClientCalls(t, home) != wantCalls {
					t.Fatalf("home calls=%d want=%d", kubeClientCalls(t, home), wantCalls)
				}
			})
		}
	}
}

func TestClusterInventory_RemoteUpstreamErrorsFailClosed(t *testing.T) {
	for _, route := range []string{"/cluster", "/cluster/info", "/cluster/stats"} {
		for _, status := range []int{http.StatusForbidden, http.StatusServiceUnavailable} {
			t.Run(route+"/"+http.StatusText(status), func(t *testing.T) {
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_ = json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": http.StatusText(status), "message": "private-upstream-details", "code": status})
				}))
				defer upstream.Close()
				cs, err := kubernetes.NewForConfig(&rest.Config{Host: upstream.URL})
				if err != nil {
					t.Fatal(err)
				}
				home := inventoryClient("home-node", "v1.30.1", "2Gi")
				reg := clusterTestRegistry(home)
				reg.Set("remote", &kube.Client{Typed: cs})
				r := chi.NewRouter()
				r.Use(rbac.Middleware(reg))
				MountCluster(r, reg, nil, "test", true, "edge")
				rr := inventoryRequest(t, r, inventoryUser("remote", "*", "cluster:read"), http.MethodGet, route+"?cluster=remote")
				if rr.Code != status {
					t.Fatalf("status=%d want=%d body=%s", rr.Code, status, rr.Body)
				}
				if strings.Contains(rr.Body.String(), "private-upstream-details") {
					t.Fatal("upstream error details leaked")
				}
				if kubeClientCalls(t, home) != 0 {
					t.Fatal("remote failure fell back to home")
				}
			})
		}
	}
}

func TestClusterInventory_RemotePVFailureDoesNotReportZeroStorage(t *testing.T) {
	home := inventoryClient("home-node", "v1.30.1", "2Gi")
	remote := inventoryClient("remote-node", "v1.31.2", "7Gi")
	remote.Typed.(*kubefake.Clientset).PrependReactor("list", "persistentvolumes", func(kubetesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(corev1.Resource("persistentvolumes"), "", errors.New("denied"))
	})
	reg := clusterTestRegistry(home)
	reg.Set("remote", remote)
	r := chi.NewRouter()
	MountCluster(r, reg, nil, "test", false, "")
	rr := inventoryRequest(t, r, inventoryUser("remote", "*", "cluster:read"), http.MethodGet, "/cluster/stats?cluster=remote")
	if rr.Code != http.StatusForbidden || kubeClientCalls(t, home) != 0 {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}

func TestClusterInventory_RemoteMetricsAreOptionalAndSelected(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "available"}[available], func(t *testing.T) {
			home := inventoryClient("home-node", "v1.30.1", "2Gi")
			remote := mockAPIServer(t, []corev1.Node{*readyNode("remote-node", true, "4", "8Gi")}, func(w http.ResponseWriter, req *http.Request) {
				if !available {
					http.NotFound(w, req)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"remote-node"},"usage":{"cpu":"250m","memory":"1Mi"}}]}`))
			})
			reg := clusterTestRegistry(home)
			reg.Set("remote", remote)
			r := chi.NewRouter()
			MountCluster(r, reg, nil, "test", false, "")
			rr := inventoryRequest(t, r, inventoryUser("remote", "*", "cluster:read"), http.MethodGet, "/cluster?cluster=remote")
			if rr.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			var view clusterView
			if err := json.Unmarshal(rr.Body.Bytes(), &view); err != nil {
				t.Fatal(err)
			}
			if len(view.Nodes) != 1 || view.Nodes[0].Name != "remote-node" {
				t.Fatalf("wrong nodes: %+v", view.Nodes)
			}
			node := view.Nodes[0]
			if node.CPU == nil || node.Memory == nil || node.CPU.Capacity != 4 {
				t.Fatal("capacity missing")
			}
			if available {
				if node.CPU.Used == nil || *node.CPU.Used != 0.25 || node.Memory.Used == nil || *node.Memory.Used != 1<<20 {
					t.Fatal("selected remote usage missing")
				}
			} else if node.CPU.Used != nil || node.Memory.Used != nil {
				t.Fatal("missing metrics were reported as measured usage")
			}
			if kubeClientCalls(t, home) != 0 {
				t.Fatal("remote metrics used home client")
			}
		})
	}
}

func TestClusterActions_RejectRemoteBeforeCredentialOperations(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, route := range []string{"/cluster/nodes:join", "/cluster/kubeconfig"} {
			home := fakeKubeClient()
			reg := clusterTestRegistry(home)
			r := chi.NewRouter()
			r.Use(rbac.Middleware(reg))
			MountClusterActions(r, home, enabled, "")
			rr := inventoryRequest(t, r, inventoryUser("local", "*", "*"), http.MethodPost, route+"?cluster=remote")
			if rr.Code != http.StatusNotImplemented {
				t.Fatalf("%s: status=%d body=%s", route, rr.Code, rr.Body)
			}
			if kubeClientCalls(t, home) != 0 {
				t.Fatal("remote selection reached credential operations")
			}
		}
	}
}

func TestClusterInventory_PathVariantsCannotBypassSelectedAuthorization(t *testing.T) {
	home := inventoryClient("home-node", "v1.30.1", "2Gi")
	remote := inventoryClient("remote-node", "v1.31.2", "7Gi")
	reg := clusterTestRegistry(home)
	reg.Set("remote", remote)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountCluster(r, reg, nil, "test", true, "edge")
	for _, path := range []string{"/cluster/", "/cluster/info/", "/cluster/stats/", "//cluster", "/cluster//info", "/%63luster", "/cluster%2finfo"} {
		rr := inventoryRequest(t, r, inventoryUser("local", "*", "*"), http.MethodGet, path+"?cluster=remote")
		if rr.Code != http.StatusForbidden && rr.Code != http.StatusNotFound {
			t.Fatalf("%s: status=%d body=%s", path, rr.Code, rr.Body)
		}
		if kubeClientCalls(t, home) != 0 || kubeClientCalls(t, remote) != 0 {
			t.Fatalf("%s accessed inventory through an alternate path", path)
		}
	}
}
