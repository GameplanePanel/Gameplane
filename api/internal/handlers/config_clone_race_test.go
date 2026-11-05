package handlers

import (
	"net/http"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestClonePasswordProtectionSurvivesCatalogRaceAndMutation(t *testing.T) {
	for _, cluster := range []string{scope.DefaultCluster, "remote"} {
		t.Run(cluster, func(t *testing.T) {
			k := fakeKubeClient(redactTemplate())
			src := serverWithConfig("alpha", plainConfig())
			pinRedactionTemplate(t, k, src, redactTemplate())
			if _, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Create(t.Context(), src, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
			fake := k.Dynamic.(*dynamicfake.FakeDynamicClient)
			fake.PrependReactor("create", "gameservers", func(action clienttesting.Action) (bool, runtime.Object, error) {
				obj := action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured)
				obj.SetUID(types.UID("clone-" + obj.GetName())) // Force the real API server's post-Create cache miss.
				return false, nil, fake.Tracker().Update(kube.GVRs["templates"], catalogWithoutPasswords(), "")
			})
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set(scope.DefaultCluster, fakeKubeClient())
			reg.Set(cluster, k)
			router := chi.NewRouter()
			MountLifecycle(router, reg)
			MountResources(router, reg, nil)
			store := newTestStore(t)
			MountOwnership(router, reg, store)
			user := testAdminUser()
			user.Perms[cluster] = map[string]map[string]struct{}{"*": {"*": {}}}
			query := "?cluster=" + cluster
			rr := doWithUser(t, router, http.MethodPost, "/servers/alpha:clone"+query, map[string]any{"newName": "beta"}, user)
			if rr.Code != http.StatusOK {
				t.Fatalf("clone: %d %s", rr.Code, rr.Body)
			}
			assertNoSecretLeak(t, rr.Body.String())
			clone, err := k.GetServer(t.Context(), redactNS, "beta")
			if err != nil {
				t.Fatal(err)
			}
			keys, err := kube.ProtectedConfigKeys(clone.GetAnnotations())
			if err != nil || !reflect.DeepEqual(keys, []string{"ADMIN_PASSWORD", "RCON_PASSWORD"}) {
				t.Fatalf("clone lost inherited password classification: %v %v", keys, err)
			}
			for _, path := range []string{"/servers/beta", "/servers/"} {
				rr = doWithUser(t, router, http.MethodGet, path+query, nil, user)
				if rr.Code != http.StatusOK {
					t.Fatalf("read: %d %s", rr.Code, rr.Body)
				}
				assertNoSecretLeak(t, rr.Body.String())
			}
			body := updateBody(map[string]any{"MOTD": "hello", "RCON_PASSWORD": configRedactedMarker, "ADMIN_PASSWORD": configRedactedMarker})
			body["metadata"].(map[string]any)["uid"] = string(clone.GetUID())
			body["metadata"].(map[string]any)["annotations"] = map[string]any{kube.ProtectedConfigKeysAnnotation: "[]"}
			rr = doWithUser(t, router, http.MethodPut, "/servers/beta"+query, body, user)
			if rr.Code != http.StatusOK {
				t.Fatalf("PUT: %d %s", rr.Code, rr.Body)
			}
			assertNoSecretLeak(t, rr.Body.String())
			if !reflect.DeepEqual(storedConfig(t, k, "beta"), plainConfig()) {
				t.Fatal("PUT lost stored secrets")
			}
			bob := seedUser(t, store, "bob", "viewer", "")
			rr = doWithUser(t, router, http.MethodPost, "/servers/beta:transfer"+query, map[string]any{"userId": bob}, user)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("transfer: %d %s", rr.Code, rr.Body)
			}
			rr = doWithUser(t, router, http.MethodGet, "/servers/beta"+query, nil, user)
			assertNoSecretLeak(t, rr.Body.String())
			// A second-generation clone must inherit the source classification,
			// even though that source has no pin yet.
			if err := fake.Tracker().Update(kube.GVRs["templates"], redactTemplate(), ""); err != nil {
				t.Fatal(err)
			}
			rr = doWithUser(t, router, http.MethodPost, "/servers/beta:clone"+query, map[string]any{"newName": "gamma"}, user)
			if rr.Code != http.StatusOK {
				t.Fatalf("second clone: %d %s", rr.Code, rr.Body)
			}
			assertNoSecretLeak(t, rr.Body.String())
			grandchild, err := k.GetServer(t.Context(), redactNS, "gamma")
			if err != nil {
				t.Fatal(err)
			}
			if grandchild.GetAnnotations()[kube.ProtectedConfigKeysAnnotation] != clone.GetAnnotations()[kube.ProtectedConfigKeysAnnotation] {
				t.Fatal("clone of clone lost password classification")
			}
		})
	}
}

func TestProtectedConfigMetadataFailsClosedAndCannotBeForged(t *testing.T) {
	for _, raw := range []string{"", "null", `"PASSWORD"`, `[1]`, `[""]`} {
		gs := serverWithConfig("alpha", plainConfig())
		gs.SetAnnotations(map[string]string{kube.ProtectedConfigKeysAnnotation: raw})
		newConfigRuleCache(fakeKubeClient(catalogWithoutPasswords())).redact(t.Context(), gs)
		for key, value := range specConfig(t, gs.Object) {
			if value != configRedactedMarker {
				t.Fatalf("malformed metadata exposed %s", key)
			}
		}
	}
	gs := serverWithConfig("alpha", plainConfig())
	gs.SetAnnotations(map[string]string{kube.ProtectedConfigKeysAnnotation: `["MOTD"]`})
	k := fakeKubeClient(redactTemplate())
	rr := do(t, mountResourcesRouter(k), http.MethodPost, "/servers/", gs.Object)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	stored, err := k.GetServer(t.Context(), redactNS, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	keys, err := kube.ProtectedConfigKeys(stored.GetAnnotations())
	if err != nil || !reflect.DeepEqual(keys, []string{"ADMIN_PASSWORD", "RCON_PASSWORD"}) {
		t.Fatalf("generic create did not replace client metadata with verified password keys: %v %v", keys, err)
	}
}
