package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	kubefake "k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/registry"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Both clusters deliberately contain identical names but different identities,
// templates and saved values. A home-client fallback cannot pass these checks.
func modClusterFixture(cluster string) *kube.Client {
	tmpl := newTemplateObj("same-template", map[string]any{
		"provider": "modrinth", "community": cluster,
		"modpacks": map[string]any{"refEnv": cluster + "_PACK"},
	}, []any{
		map[string]any{"id": "default", "loader": "paper", "gameVersion": "default", "default": true},
		map[string]any{"id": "selected", "loader": cluster + "-loader", "gameVersion": cluster + "-version"},
	})
	_ = unstructured.SetNestedMap(tmpl.Object, map[string]any{"env": "MOD_IDS", "mode": "replace"}, "spec", "capabilities", "mods", "idList")
	gs := serverWithVersion(scope.DefaultNamespace, "alpha", "same-template", "selected")
	gs.SetUID(types.UID(cluster + "-uid"))
	gs.SetResourceVersion("17")
	gs.SetAnnotations(map[string]string{"gameplane.local/owner-id": "42", "gameplane.local/collaborators": "43"})
	setEnvVars(gs, []envKV{{Name: "KEEP", Value: cluster}})
	writeModIDs(gs, []ModID{{ID: cluster + "-saved", Name: cluster}})
	return fakeKubeClient(tmpl, gs)
}

func modClusterClients(home, remote *kube.Client) *kube.Registry {
	clients := kube.NewRegistry(scope.DefaultCluster)
	clients.Set(scope.DefaultCluster, home)
	clients.Set("remote", remote)
	return clients
}

func modRouteUser(cluster string, permissions ...string) *auth.User {
	perms := map[string]struct{}{}
	for _, permission := range permissions {
		perms[permission] = struct{}{}
	}
	return &auth.User{ID: 99, Perms: map[string]map[string]map[string]struct{}{
		cluster: {scope.DefaultNamespace: perms},
	}}
}

func mountClusterModRouter(clients *kube.Registry, set registrySet, user *auth.User, fetch rbac.ServerFetcher) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			next.ServeHTTP(w, req.WithContext(auth.WithUser(req.Context(), user)))
		})
	})
	if fetch == nil {
		fetch = clients
	}
	r.Use(rbac.Middleware(fetch))
	MountRegistryWithRegistry(r, clients, set)
	MountModIDsWithRegistry(r, clients)
	return r
}

type recordingRegistrySet struct {
	provider registry.Provider
	config   registry.Config
	calls    int
}

func (s *recordingRegistrySet) For(_ context.Context, cfg registry.Config) (registry.Provider, bool) {
	s.calls++
	s.config = cfg
	return s.provider, true
}

func (s *recordingRegistrySet) Available(context.Context, string) bool {
	s.calls++
	return true
}

func TestRegistryRemoteUsesSelectedTemplateAndWritesOnlySelectedServer(t *testing.T) {
	for _, route := range registryRoutes {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			home, remote := modClusterFixture("local"), modClusterFixture("remote")
			provider := &fakeProvider{}
			set := &recordingRegistrySet{provider: provider}
			router := mountClusterModRouter(modClusterClients(home, remote), set,
				modRouteUser("remote", "servers:read", "servers:write"), nil)
			response := do(t, router, route.method, route.path+"?cluster=remote", route.body)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", response.Code, response.Body)
			}
			if n := kubeClientCalls(t, home); n != 0 {
				t.Fatalf("home client saw %d calls", n)
			}
			if n := len(remote.Typed.(*kubefake.Clientset).Actions()); n != 0 {
				t.Fatalf("remote typed client saw %d calls; provider credentials must stay central", n)
			}
			switch route.path {
			case "/servers/alpha/mods/registry/providers":
				var got []providerInfo
				if err := json.Unmarshal(response.Body.Bytes(), &got); err != nil || len(got) != 1 || !got[0].Available || !got[0].Modpacks {
					t.Fatalf("providers = %s, err = %v", response.Body, err)
				}
			case "/servers/alpha/mods/registry/search":
				if set.config.Community != "remote" || provider.gotSearch.Loader != "remote-loader" || provider.gotSearch.GameVersion != "remote-version" {
					t.Fatalf("wrong provider configuration/filter: %+v %+v", set.config, provider.gotSearch)
				}
			case "/servers/alpha/mods/registry/projects/p1/versions":
				if set.config.Community != "remote" || provider.gotFilter.Loader != "remote-loader" || provider.gotFilter.GameVersion != "remote-version" || provider.gotProject != "p1" {
					t.Fatalf("wrong provider configuration/filter: %+v %+v", set.config, provider.gotFilter)
				}
			case "/servers/alpha/mods/registry/projects/p1/modpack":
				if set.config.Community != "remote" || provider.gotProject != "p1" {
					t.Fatalf("wrong dependency provider: %+v, project = %s", set.config, provider.gotProject)
				}
			case "/servers/alpha/modpack":
				gs, err := remote.GetServer(t.Context(), scope.DefaultNamespace, "alpha")
				if err != nil {
					t.Fatal(err)
				}
				env, _, _ := unstructured.NestedSlice(gs.Object, "spec", "env")
				want := []any{map[string]any{"name": "KEEP", "value": "remote"}, map[string]any{"name": "remote_PACK", "value": "pack"}}
				if !reflect.DeepEqual(env, want) || gs.GetUID() != "remote-uid" || gs.GetResourceVersion() != "17" {
					t.Fatalf("updated wrong server or fields: metadata=%v env=%v", gs.Object["metadata"], env)
				}
			}
		})
	}
}

func TestRegistryRemoteUsesCentralProviderCredentials(t *testing.T) {
	home, remote := modClusterFixture("local"), modClusterFixture("remote")
	tmpl, err := remote.Dynamic.Resource(kube.GVRs["templates"]).Get(t.Context(), "same-template", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_ = unstructured.SetNestedSlice(tmpl.Object, []any{map[string]any{"provider": "curseforge"}}, "spec", "capabilities", "mods", "registry", "providers")
	if _, err := remote.Dynamic.Resource(kube.GVRs["templates"]).Update(t.Context(), tmpl, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	keyReads := 0
	set := registry.NewSet("test", func(_ context.Context, provider string) string {
		if provider != "curseforge" {
			t.Fatalf("credential requested for undeclared provider %s", provider)
		}
		keyReads++
		return "test-central-key"
	})
	router := mountClusterModRouter(modClusterClients(home, remote), set, modRouteUser("remote", "servers:read"), nil)
	response := do(t, router, http.MethodGet, "/servers/alpha/mods/registry/providers?cluster=remote", nil)
	var providers []providerInfo
	if err := json.Unmarshal(response.Body.Bytes(), &providers); err != nil || response.Code != http.StatusOK || len(providers) != 1 || !providers[0].Available || keyReads == 0 {
		t.Fatalf("central provider unavailable: status=%d body=%s reads=%d err=%v", response.Code, response.Body, keyReads, err)
	}
	if kubeClientCalls(t, home) != 0 || len(remote.Typed.(*kubefake.Clientset).Actions()) != 0 {
		t.Fatal("remote selection unexpectedly read a cluster credential")
	}
}

type modAuthorizationSnapshot struct{ server *unstructured.Unstructured }

func (s modAuthorizationSnapshot) IDs() []string { return []string{"local", "remote"} }
func (s modAuthorizationSnapshot) GetServer(context.Context, string, string, string) (*unstructured.Unstructured, error) {
	return s.server.DeepCopy(), nil
}

func allClusterModRoutes() []modTestRoute {
	return append(append([]modTestRoute(nil), registryRoutes...),
		modTestRoute{http.MethodGet, "/servers/alpha/mods/ids", nil},
		modTestRoute{http.MethodPut, "/servers/alpha/mods/ids", []ModID{{ID: "new-mod"}}})
}

func TestClusterModRoutesEnforceScopedPermissionsAndOwnership(t *testing.T) {
	for _, route := range allClusterModRoutes() {
		for _, test := range []struct {
			name string
			user *auth.User
			want int
		}{
			{"owner", &auth.User{ID: 42}, http.StatusOK},
			{"collaborator", &auth.User{ID: 43}, http.StatusOK},
			{"unrelated", &auth.User{ID: 99}, http.StatusForbidden},
			{"local grant", modRouteUser("local", "servers:read", "servers:write"), http.StatusForbidden},
			{"remote read grant", modRouteUser("remote", "servers:read"), http.StatusOK},
		} {
			t.Run(route.method+" "+route.path+"/"+test.name, func(t *testing.T) {
				home, remote := modClusterFixture("local"), modClusterFixture("remote")
				set := &recordingRegistrySet{provider: &fakeProvider{}}
				router := mountClusterModRouter(modClusterClients(home, remote), set, test.user, nil)
				want := test.want
				if test.name == "remote read grant" && route.method != http.MethodGet {
					want = http.StatusForbidden
				}
				response := do(t, router, route.method, route.path+"?cluster=remote", route.body)
				if response.Code != want {
					t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body)
				}
				if kubeClientCalls(t, home) != 0 {
					t.Fatal("remote request reached the local client")
				}
				if want == http.StatusForbidden {
					if set.calls != 0 {
						t.Fatal("denied request reached a registry provider")
					}
					for _, action := range remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions() {
						if action.GetVerb() != "get" || action.GetResource() != kube.GVRs["servers"] {
							t.Fatalf("denied request accessed %s %s", action.GetVerb(), action.GetResource())
						}
					}
				}
			})
		}
	}
}

func TestClusterModRoutesRejectReplacementBeforeTemplateOrProviderRead(t *testing.T) {
	for _, route := range allClusterModRoutes() {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			home, remote := modClusterFixture("local"), modClusterFixture("remote")
			previous := serverWithVersion(scope.DefaultNamespace, "alpha", "same-template", "selected")
			previous.SetUID("previous-uid")
			previous.SetAnnotations(map[string]string{"gameplane.local/owner-id": "42"})
			set := &recordingRegistrySet{provider: &fakeProvider{}}
			router := mountClusterModRouter(modClusterClients(home, remote), set, &auth.User{ID: 42}, modAuthorizationSnapshot{previous})
			response := do(t, router, route.method, route.path+"?cluster=remote", route.body)
			if response.Code != http.StatusNotFound || set.calls != 0 || kubeClientCalls(t, home) != 0 {
				t.Fatalf("replacement was not refused before effects: status=%d provider calls=%d", response.Code, set.calls)
			}
			actions := remote.Dynamic.(*dynamicfake.FakeDynamicClient).Actions()
			if len(actions) != 1 || actions[0].GetVerb() != "get" || actions[0].GetResource() != kube.GVRs["servers"] {
				t.Fatalf("replacement accessed more than its identity: %v", actions)
			}
		})
	}
}

func TestClusterModRoutesNeverFallBackWhenTargetUnavailable(t *testing.T) {
	for _, route := range allClusterModRoutes() {
		for _, test := range []struct {
			name, query string
			want        int
			missing     bool
			outage      bool
		}{
			{"unknown cluster", "?cluster=unknown", http.StatusBadRequest, false, false},
			{"disallowed namespace", "?cluster=remote&namespace=forbidden", http.StatusBadRequest, false, false},
			{"missing remote server", "?cluster=remote", http.StatusNotFound, true, false},
			{"remote outage", "?cluster=remote", http.StatusInternalServerError, false, true},
		} {
			t.Run(route.method+" "+route.path+"/"+test.name, func(t *testing.T) {
				home, remote := modClusterFixture("local"), modClusterFixture("remote")
				if test.missing {
					remote = fakeKubeClient()
				}
				if test.outage {
					remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", "*", func(ktesting.Action) (bool, runtime.Object, error) {
						return true, nil, errors.New("remote apiserver unavailable")
					})
				}
				set := &recordingRegistrySet{provider: &fakeProvider{}}
				router := mountClusterModRouter(modClusterClients(home, remote), set, modRouteUser("remote", "servers:read", "servers:write"), nil)
				response := do(t, router, route.method, route.path+test.query, route.body)
				if response.Code != test.want || kubeClientCalls(t, home) != 0 || set.calls != 0 {
					t.Fatalf("status=%d want=%d home calls=%d provider calls=%d", response.Code, test.want, kubeClientCalls(t, home), set.calls)
				}
			})
		}
	}
}

func TestClusterModWritesPreserveIdentityAndSurfaceConflicts(t *testing.T) {
	for _, route := range allClusterModRoutes() {
		if route.method == http.MethodGet {
			continue
		}
		t.Run(route.path, func(t *testing.T) {
			home, remote := modClusterFixture("local"), modClusterFixture("remote")
			updates := 0
			remote.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("update", "gameservers", func(action ktesting.Action) (bool, runtime.Object, error) {
				updates++
				gs := action.(ktesting.UpdateAction).GetObject().(*unstructured.Unstructured)
				if gs.GetUID() != "remote-uid" || gs.GetResourceVersion() != "17" || gs.GetNamespace() != scope.DefaultNamespace {
					t.Fatalf("write lost identity preconditions: %v", gs.Object["metadata"])
				}
				return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("server changed"))
			})
			router := mountClusterModRouter(modClusterClients(home, remote), &recordingRegistrySet{provider: &fakeProvider{}}, modRouteUser("remote", "servers:write"), nil)
			response := do(t, router, route.method, route.path+"?cluster=remote", route.body)
			if response.Code != http.StatusConflict || updates != retry.DefaultRetry.Steps || kubeClientCalls(t, home) != 0 {
				t.Fatalf("conflict handling: status=%d updates=%d home calls=%d", response.Code, updates, kubeClientCalls(t, home))
			}
		})
	}
}
