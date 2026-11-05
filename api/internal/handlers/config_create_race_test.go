package handlers

import (
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
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestCreatePasswordProtectionSurvivesCatalogRace(t *testing.T) {
	for _, cluster := range []string{scope.DefaultCluster, "remote"} {
		for _, schema := range []string{"available", "missing", "malformed"} {
			t.Run(cluster+"/"+schema, func(t *testing.T) {
				tmpl := redactTemplate()
				if schema == "malformed" {
					tmpl.Object["spec"].(map[string]any)["configSchema"] = "invalid"
				}
				k := fakeKubeClient(tmpl)
				if schema == "missing" {
					k = fakeKubeClient()
				}
				fake := k.Dynamic.(*dynamicfake.FakeDynamicClient)
				fake.PrependReactor("create", "gameservers", func(action clienttesting.Action) (bool, runtime.Object, error) {
					obj := action.(clienttesting.CreateAction).GetObject().(*unstructured.Unstructured)
					obj.SetUID(types.UID("created-" + obj.GetName()))
					// Change the catalog after classification but before persistence,
					// then force redaction to resolve rules using the new server UID.
					if schema == "missing" {
						return false, nil, fake.Tracker().Create(kube.GVRs["templates"], catalogWithoutPasswords(), "")
					}
					return false, nil, fake.Tracker().Update(kube.GVRs["templates"], catalogWithoutPasswords(), "")
				})
				reg := kube.NewRegistry(scope.DefaultCluster)
				reg.Set(scope.DefaultCluster, fakeKubeClient())
				reg.Set(cluster, k)
				router := chi.NewRouter()
				MountResources(router, reg, nil)
				user := testAdminUser()
				user.Perms[cluster] = map[string]map[string]struct{}{"*": {"*": {}}}
				query := "?cluster=" + cluster
				gs := serverWithConfig("alpha", plainConfig())
				gs.SetAnnotations(map[string]string{kube.ProtectedConfigKeysAnnotation: "[]"})
				rr := doWithUser(t, router, http.MethodPost, "/servers/"+query, gs.Object, user)
				if rr.Code != http.StatusCreated {
					t.Fatalf("create: %d %s", rr.Code, rr.Body)
				}
				assertNoSecretLeak(t, rr.Body.String())
				if schema == "available" && configFromBody(t, rr.Body.Bytes())["MOTD"] != "hello" {
					t.Fatal("verified ordinary field was needlessly redacted")
				}
				created, err := k.GetServer(t.Context(), redactNS, "alpha")
				if err != nil {
					t.Fatal(err)
				}
				keys, err := kube.ProtectedConfigKeys(created.GetAnnotations())
				want := []string{"ADMIN_PASSWORD", "RCON_PASSWORD"}
				if schema != "available" {
					want = []string{"ADMIN_PASSWORD", "MOTD", "RCON_PASSWORD"}
				}
				if err != nil || !reflect.DeepEqual(keys, want) {
					t.Fatalf("creation lost password classification: %v %v", keys, err)
				}
				if !reflect.DeepEqual(storedConfig(t, k, "alpha"), plainConfig()) {
					t.Fatal("creation changed stored config")
				}
				for _, path := range []string{"/servers/alpha", "/servers/"} {
					rr = doWithUser(t, router, http.MethodGet, path+query, nil, user)
					if rr.Code != http.StatusOK {
						t.Fatalf("read: %d %s", rr.Code, rr.Body)
					}
					assertNoSecretLeak(t, rr.Body.String())
				}
			})
		}
	}
}

func TestCreateDoesNotProtectEmptyOrOrdinaryValues(t *testing.T) {
	k := fakeKubeClient(redactTemplate())
	gs := serverWithConfig("alpha", map[string]any{"MOTD": "hello", "RCON_PASSWORD": "", "ADMIN_PASSWORD": ""})
	rr := do(t, mountResourcesRouter(k), http.MethodPost, "/servers/", gs.Object)
	if rr.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", rr.Code, rr.Body)
	}
	created, err := k.GetServer(t.Context(), redactNS, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, found := created.GetAnnotations()[kube.ProtectedConfigKeysAnnotation]; found {
		t.Fatal("empty or ordinary value acquired persistent password protection")
	}
}

func TestUnpinnedUpdateProtectsNewPasswordsUsingLiveRules(t *testing.T) {
	for _, cluster := range []string{scope.DefaultCluster, "remote"} {
		for _, failedLookup := range []bool{false, true} {
			name := cluster + "/available"
			if failedLookup {
				name = cluster + "/unavailable"
			}
			t.Run(name, func(t *testing.T) {
				gs := serverWithConfig("alpha", map[string]any{"MOTD": "hello", "RCON_PASSWORD": "", "ADMIN_PASSWORD": ""})
				gs.SetUID("uid-alpha")
				// Empty protection is retired only at an authoritative pin update.
				gs.SetAnnotations(map[string]string{kube.ProtectedConfigKeysAnnotation: `["OLD_PASSWORD"]`})
				k := fakeKubeClient(redactTemplate(), gs)
				fake := k.Dynamic.(*dynamicfake.FakeDynamicClient)
				firstRead := true
				fake.PrependReactor("get", "gametemplates", func(clienttesting.Action) (bool, runtime.Object, error) {
					if !firstRead {
						return false, nil, nil
					}
					firstRead = false
					// The preservation lookup sees password rules, but later lookups
					// in this same request already see the changed catalog.
					if err := fake.Tracker().Update(kube.GVRs["templates"], catalogWithoutPasswords(), ""); err != nil {
						return true, nil, err
					}
					if failedLookup {
						return true, nil, apierrors.NewServiceUnavailable("transient catalog error")
					}
					return true, redactTemplate(), nil
				})
				reg := kube.NewRegistry(scope.DefaultCluster)
				reg.Set(scope.DefaultCluster, fakeKubeClient())
				reg.Set(cluster, k)
				router := chi.NewRouter()
				MountResources(router, reg, nil)
				user := testAdminUser()
				user.Perms[cluster] = map[string]map[string]struct{}{"*": {"*": {}}}
				body := updateBody(plainConfig())
				delete(body["metadata"].(map[string]any), "namespace")
				// As with a real API server, updates preserve the live UID even
				// when the PUT body omits it.
				fake.PrependReactor("update", "gameservers", func(action clienttesting.Action) (bool, runtime.Object, error) {
					action.(clienttesting.UpdateAction).GetObject().(*unstructured.Unstructured).SetUID(gs.GetUID())
					return false, nil, nil
				})
				query := "?cluster=" + cluster
				rr := doWithUser(t, router, http.MethodPut, "/servers/alpha"+query, body, user)
				if rr.Code != http.StatusOK {
					t.Fatalf("update: %d %s", rr.Code, rr.Body)
				}
				assertNoSecretLeak(t, rr.Body.String())
				if !failedLookup && configFromBody(t, rr.Body.Bytes())["MOTD"] != "hello" {
					t.Fatal("verified ordinary field was needlessly redacted")
				}
				stored, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(redactNS).Get(t.Context(), "alpha", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				keys, err := kube.ProtectedConfigKeys(stored.GetAnnotations())
				want := []string{"ADMIN_PASSWORD", "OLD_PASSWORD", "RCON_PASSWORD"}
				if failedLookup {
					want = []string{"ADMIN_PASSWORD", "MOTD", "OLD_PASSWORD", "RCON_PASSWORD"}
				}
				if err != nil || !reflect.DeepEqual(keys, want) {
					t.Fatalf("update lost existing or newly populated password classification: %v %v", keys, err)
				}
				if !reflect.DeepEqual(storedConfig(t, k, "alpha"), plainConfig()) {
					t.Fatal("update changed stored config")
				}
				rr = doWithUser(t, router, http.MethodGet, "/servers/alpha"+query, nil, user)
				if rr.Code != http.StatusOK {
					t.Fatalf("read: %d %s", rr.Code, rr.Body)
				}
				assertNoSecretLeak(t, rr.Body.String())
			})
		}
	}
}

func TestUnpinnedUpdateKeepsMalformedProtectionFailClosed(t *testing.T) {
	gs := serverWithConfig("alpha", plainConfig())
	gs.SetAnnotations(map[string]string{kube.ProtectedConfigKeysAnnotation: "invalid"})
	k := fakeKubeClient(redactTemplate(), gs)
	rr := do(t, mountResourcesRouter(k), http.MethodPut, "/servers/alpha", updateBody(plainConfig()))
	if rr.Code != http.StatusOK {
		t.Fatalf("update: %d %s", rr.Code, rr.Body)
	}
	for key, value := range configFromBody(t, rr.Body.Bytes()) {
		if value != configRedactedMarker {
			t.Fatalf("malformed protection exposed %s", key)
		}
	}
	stored, err := k.GetServer(t.Context(), redactNS, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if stored.GetAnnotations()[kube.ProtectedConfigKeysAnnotation] != "invalid" {
		t.Fatal("PUT repaired malformed protection without authoritative admission")
	}
}
