package handlers

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

const ownerOnlyTestOwnerID int64 = 7

// ownedServerObj returns a GameServer fixture owned by ownerOnlyTestOwnerID,
// plus any extra annotations.
func ownedServerObj(name string, extra map[string]string) *unstructured.Unstructured {
	obj := newServerObj("gameplane-games", name)
	ann := map[string]string{ownerIDAnnotation: strconv.FormatInt(ownerOnlyTestOwnerID, 10)}
	for k, v := range extra {
		ann[k] = v
	}
	obj.SetAnnotations(ann)
	return obj
}

// serverOwnerUser is the owner of ownedServerObj fixtures; it holds no
// namespace permissions.
func serverOwnerUser() *auth.User {
	return &auth.User{ID: ownerOnlyTestOwnerID, Username: "owner_carol", Role: "viewer"}
}

// namespaceAdminUser holds "*" only in the default namespace of the home cluster.
func namespaceAdminUser() *auth.User {
	return &auth.User{ID: 11, Username: "ns_admin", Role: "custom", Perms: map[string]map[string]map[string]struct{}{
		scope.DefaultCluster: {scope.DefaultNamespace: {"*": {}}},
	}}
}

// otherClusterAdminUser holds "*" only on a cluster other than the target.
func otherClusterAdminUser() *auth.User {
	return &auth.User{ID: 12, Username: "remote_admin", Role: "custom", Perms: map[string]map[string]map[string]struct{}{
		"other": {"*": {"*": {}}},
	}}
}

func serverAnnotations(t *testing.T, k *kube.Client, name string) map[string]string {
	t.Helper()
	obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
		Namespace("gameplane-games").Get(t.Context(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get %s: %v", name, err)
	}
	return obj.GetAnnotations()
}

func TestOwnership_TransferRequiresOwnerOrAdmin(t *testing.T) {
	store := newTestStore(t)
	target := seedUser(t, store, "dave", "viewer", "")
	k := fakeKubeClient(ownedServerObj("alpha", nil))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)
	body := map[string]any{"userId": target}

	for _, tc := range []struct {
		name string
		user *auth.User
		want int
	}{
		{"unauthenticated", nil, http.StatusUnauthorized},
		{"servers:write holder who is not the owner", testOperatorUser(), http.StatusForbidden},
		{"admin of another cluster only", otherClusterAdminUser(), http.StatusForbidden},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", body, tc.user)
			if rr.Code != tc.want {
				t.Fatalf("got %d %s, want %d", rr.Code, rr.Body, tc.want)
			}
			if got := serverAnnotations(t, k, "alpha")[ownerIDAnnotation]; got != "7" {
				t.Fatalf("owner annotation = %q after a refused transfer, want 7", got)
			}
		})
	}

	t.Run("owner may transfer", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", body, serverOwnerUser())
		if rr.Code != http.StatusNoContent {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
		if got := serverAnnotations(t, k, "alpha")[ownerIDAnnotation]; got != strconv.FormatInt(target, 10) {
			t.Fatalf("owner annotation = %q, want %d", got, target)
		}
	})
	for _, tc := range []struct {
		name string
		user *auth.User
	}{
		{"cluster-wide admin", testAdminUser()},
		{"admin of the target namespace", namespaceAdminUser()},
	} {
		t.Run(tc.name+" may transfer a server it does not own", func(t *testing.T) {
			rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", body, tc.user)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
		})
	}
	t.Run("admin on a missing server gets 404", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/ghost:transfer", body, testAdminUser())
		if rr.Code != http.StatusNotFound {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})
}

func TestOwnership_SetCollaboratorsRequiresOwnerOrAdmin(t *testing.T) {
	store := newTestStore(t)
	erin := seedUser(t, store, "erin", "viewer", "")
	// testOperatorUser (ID 2) is already a collaborator on this server.
	k := fakeKubeClient(ownedServerObj("alpha", map[string]string{collaboratorsAnnotation: "2"}))
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)
	body := map[string]any{"userIds": []int64{erin}}

	for _, tc := range []struct {
		name string
		user *auth.User
		want int
	}{
		{"unauthenticated", nil, http.StatusUnauthorized},
		{"collaborator holding servers:write", testOperatorUser(), http.StatusForbidden},
		{"admin of another cluster only", otherClusterAdminUser(), http.StatusForbidden},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", body, tc.user)
			if rr.Code != tc.want {
				t.Fatalf("got %d %s, want %d", rr.Code, rr.Body, tc.want)
			}
			if got := serverAnnotations(t, k, "alpha")[collaboratorsAnnotation]; got != "2" {
				t.Fatalf("collaborators = %q after a refused edit, want 2", got)
			}
		})
	}

	for _, tc := range []struct {
		name string
		user *auth.User
	}{
		{"owner", serverOwnerUser()},
		{"cluster-wide admin", testAdminUser()},
		{"admin of the target namespace", namespaceAdminUser()},
	} {
		t.Run(tc.name+" may edit collaborators", func(t *testing.T) {
			rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", body, tc.user)
			if rr.Code != http.StatusNoContent {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
			if got := serverAnnotations(t, k, "alpha")[collaboratorsAnnotation]; got != strconv.FormatInt(erin, 10) {
				t.Fatalf("collaborators = %q, want %d", got, erin)
			}
		})
	}
	t.Run("admin on a missing server gets 404", func(t *testing.T) {
		rr := doWithUser(t, r, "PUT", "/servers/ghost:collaborators", body, testAdminUser())
		if rr.Code != http.StatusNotFound {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})
}

func TestLifecycle_WipeDataRequiresOwnerOrAdmin(t *testing.T) {
	k := fakeKubeClient(ownedServerObj("alpha", nil), newServerObj("gameplane-games", "orphan"))
	r := mountLifecycleRouter(k)
	confirm := func(name string) map[string]any { return map[string]any{"confirm": name} }

	for _, tc := range []struct {
		name   string
		server string
		user   *auth.User
		want   int
	}{
		{"unauthenticated", "alpha", nil, http.StatusUnauthorized},
		{"servers:write holder who is not the owner", "alpha", testOperatorUser(), http.StatusForbidden},
		{"admin of another cluster only", "alpha", otherClusterAdminUser(), http.StatusForbidden},
		{"servers:write holder on a server with no owner", "orphan", testOperatorUser(), http.StatusForbidden},
		{"owner of another server on a server with no owner", "orphan", serverOwnerUser(), http.StatusForbidden},
	} {
		t.Run(tc.name+" is refused", func(t *testing.T) {
			rr := doWithUser(t, r, "POST", "/servers/"+tc.server+":wipe-data", confirm(tc.server), tc.user)
			if rr.Code != tc.want {
				t.Fatalf("got %d %s, want %d", rr.Code, rr.Body, tc.want)
			}
			if got := serverAnnotations(t, k, tc.server)[wipeRequestedAnnotation]; got != "" {
				t.Fatalf("wipe annotation set after a refused request: %q", got)
			}
		})
	}

	for _, tc := range []struct {
		name   string
		server string
		user   *auth.User
	}{
		{"owner", "alpha", serverOwnerUser()},
		{"cluster-wide admin", "alpha", testAdminUser()},
		{"admin of the target namespace", "alpha", namespaceAdminUser()},
		{"admin on a server with no owner", "orphan", testAdminUser()},
	} {
		t.Run(tc.name+" may wipe", func(t *testing.T) {
			rr := doWithUser(t, r, "POST", "/servers/"+tc.server+":wipe-data", confirm(tc.server), tc.user)
			if rr.Code != http.StatusAccepted {
				t.Fatalf("got %d %s", rr.Code, rr.Body)
			}
		})
	}
	t.Run("admin on a missing server gets 404", func(t *testing.T) {
		rr := doWithUser(t, r, "POST", "/servers/ghost:wipe-data", confirm("ghost"), testAdminUser())
		if rr.Code != http.StatusNotFound {
			t.Fatalf("got %d %s", rr.Code, rr.Body)
		}
	})
}

// enforcePatchResourceVersion makes the fake dynamic client behave like the
// API server for GameServer merge patches that carry
// metadata.resourceVersion: a value that differs from the stored object's
// is rejected with 409 Conflict. The stock fake applies such patches
// unconditionally.
func enforcePatchResourceVersion(t *testing.T, k *kube.Client) {
	t.Helper()
	dyn, ok := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("dynamic client is %T, want *fake.FakeDynamicClient", k.Dynamic)
	}
	gvr := kube.GVRs["servers"]
	dyn.PrependReactor("patch", gvr.Resource, func(action clienttesting.Action) (bool, runtime.Object, error) {
		pa, ok := action.(clienttesting.PatchAction)
		if !ok {
			return false, nil, nil
		}
		var body struct {
			Metadata struct {
				ResourceVersion string `json:"resourceVersion"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(pa.GetPatch(), &body); err != nil {
			return true, nil, err
		}
		if body.Metadata.ResourceVersion == "" {
			return false, nil, nil
		}
		cur, err := dyn.Tracker().Get(gvr, pa.GetNamespace(), pa.GetName())
		if err != nil {
			return true, nil, err
		}
		m, err := meta.Accessor(cur)
		if err != nil {
			return true, nil, err
		}
		if m.GetResourceVersion() != body.Metadata.ResourceVersion {
			return true, nil, apierrors.NewConflict(gvr.GroupResource(), pa.GetName(),
				errors.New("the object has been modified; please apply your changes to the latest version and try again"))
		}
		return false, nil, nil
	})
}

// changeOwnerBeforePatches simulates a concurrent writer: before each of
// the first n GameServer patches reaches the store, it reassigns the named
// server to newOwnerID and bumps its resourceVersion, as an ownership
// transfer by another caller would. It returns a pointer to the number of
// patch attempts seen. Register it after enforcePatchResourceVersion so it
// runs first.
func changeOwnerBeforePatches(t *testing.T, k *kube.Client, name string, newOwnerID int64, n int) *int {
	t.Helper()
	dyn, ok := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	if !ok {
		t.Fatalf("dynamic client is %T, want *fake.FakeDynamicClient", k.Dynamic)
	}
	gvr := kube.GVRs["servers"]
	patches := 0
	dyn.PrependReactor("patch", gvr.Resource, func(action clienttesting.Action) (bool, runtime.Object, error) {
		pa, ok := action.(clienttesting.PatchAction)
		if !ok || pa.GetName() != name {
			return false, nil, nil
		}
		patches++
		if patches > n {
			return false, nil, nil
		}
		cur, err := dyn.Tracker().Get(gvr, pa.GetNamespace(), name)
		if err != nil {
			return true, nil, err
		}
		obj, ok := cur.(*unstructured.Unstructured)
		if !ok {
			return true, nil, errors.New("stored server is not unstructured")
		}
		ann := obj.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann[ownerIDAnnotation] = strconv.FormatInt(newOwnerID, 10)
		obj.SetAnnotations(ann)
		rv, err := strconv.Atoi(obj.GetResourceVersion())
		if err != nil {
			return true, nil, err
		}
		obj.SetResourceVersion(strconv.Itoa(rv + 1))
		if err := dyn.Tracker().Update(gvr, obj, pa.GetNamespace()); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})
	return &patches
}

// versionedOwnedServerObj is ownedServerObj with a resourceVersion, as a
// server read from the API server always has.
func versionedOwnedServerObj(name string, extra map[string]string) *unstructured.Unstructured {
	obj := ownedServerObj(name, extra)
	obj.SetResourceVersion("1")
	return obj
}

// otherOwnerID is the user a concurrent transfer hands the server to.
const otherOwnerID int64 = 99

func TestOwnerOnlyOps_OwnershipLostBeforePatchIsRefused(t *testing.T) {
	store := newTestStore(t)
	target := seedUser(t, store, "frank", "viewer", "")
	other := strconv.FormatInt(otherOwnerID, 10)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
		mount  func(r chi.Router, reg *kube.Registry)
		// unchanged asserts the refused mutation left no trace.
		unchanged func(t *testing.T, obj *unstructured.Unstructured)
	}{
		{
			name: "transfer", method: "POST", path: "/servers/alpha:transfer",
			body:  map[string]any{"userId": target},
			mount: func(r chi.Router, reg *kube.Registry) { MountOwnership(r, reg, store) },
			unchanged: func(t *testing.T, obj *unstructured.Unstructured) {
				if got := obj.GetAnnotations()[ownerIDAnnotation]; got != other {
					t.Fatalf("owner = %q, want the concurrent owner %s", got, other)
				}
			},
		},
		{
			name: "collaborator edit", method: "PUT", path: "/servers/alpha:collaborators",
			body:  map[string]any{"userIds": []int64{target}},
			mount: func(r chi.Router, reg *kube.Registry) { MountOwnership(r, reg, store) },
			unchanged: func(t *testing.T, obj *unstructured.Unstructured) {
				if got := obj.GetAnnotations()[collaboratorsAnnotation]; got != "" {
					t.Fatalf("collaborators = %q, want none", got)
				}
			},
		},
		{
			name: "wipe", method: "POST", path: "/servers/alpha:wipe-data",
			body:  map[string]any{"confirm": "alpha"},
			mount: func(r chi.Router, reg *kube.Registry) { MountLifecycle(r, reg) },
			unchanged: func(t *testing.T, obj *unstructured.Unstructured) {
				if got := obj.GetAnnotations()[wipeRequestedAnnotation]; got != "" {
					t.Fatalf("wipe annotation = %q, want none", got)
				}
				if suspended, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend"); suspended {
					t.Fatal("server suspended by a refused wipe")
				}
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			k := fakeKubeClient(versionedOwnedServerObj("alpha", nil))
			enforcePatchResourceVersion(t, k)
			patches := changeOwnerBeforePatches(t, k, "alpha", otherOwnerID, 1)
			reg := kube.NewRegistry(scope.DefaultCluster)
			reg.Set(scope.DefaultCluster, k)
			r := chi.NewRouter()
			tc.mount(r, reg)

			rr := doWithUser(t, r, tc.method, tc.path, tc.body, serverOwnerUser())
			if rr.Code != http.StatusForbidden {
				t.Fatalf("former owner got %d %s, want 403", rr.Code, rr.Body)
			}
			if *patches != 1 {
				t.Fatalf("patch attempts = %d, want 1 (no retry after the re-check refuses)", *patches)
			}
			obj, err := k.Dynamic.Resource(kube.GVRs["servers"]).
				Namespace("gameplane-games").Get(t.Context(), "alpha", metav1.GetOptions{})
			if err != nil {
				t.Fatalf("get alpha: %v", err)
			}
			tc.unchanged(t, obj)
		})
	}
}

func TestOwnerOnlyOps_AdminRetriesAfterConcurrentChange(t *testing.T) {
	store := newTestStore(t)
	target := seedUser(t, store, "gina", "viewer", "")
	k := fakeKubeClient(versionedOwnedServerObj("alpha", nil))
	enforcePatchResourceVersion(t, k)
	patches := changeOwnerBeforePatches(t, k, "alpha", otherOwnerID, 1)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)

	rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{"userId": target}, testAdminUser())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("admin got %d %s, want 204 after a re-checked retry", rr.Code, rr.Body)
	}
	if *patches != 2 {
		t.Fatalf("patch attempts = %d, want 2 (conflict, then retry)", *patches)
	}
	if got := serverAnnotations(t, k, "alpha")[ownerIDAnnotation]; got != strconv.FormatInt(target, 10) {
		t.Fatalf("owner = %q, want %d", got, target)
	}
}

func TestOwnerOnlyOps_PersistentConflictReturns409(t *testing.T) {
	store := newTestStore(t)
	target := seedUser(t, store, "hank", "viewer", "")
	k := fakeKubeClient(versionedOwnedServerObj("alpha", nil))
	enforcePatchResourceVersion(t, k)
	// The admin stays authorized on every re-check, but the server changes
	// before every patch.
	patches := changeOwnerBeforePatches(t, k, "alpha", otherOwnerID, 1000)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)

	rr := doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{"userId": target}, testAdminUser())
	if rr.Code != http.StatusConflict {
		t.Fatalf("got %d %s, want 409", rr.Code, rr.Body)
	}
	if *patches != ownerOnlyPatchAttempts {
		t.Fatalf("patch attempts = %d, want %d", *patches, ownerOnlyPatchAttempts)
	}
	if got := serverAnnotations(t, k, "alpha")[ownerIDAnnotation]; got != strconv.FormatInt(otherOwnerID, 10) {
		t.Fatalf("owner = %q, want the concurrent owner %d", got, otherOwnerID)
	}
}

func TestOwnerOnlyOps_UnchangedServerPatchesWithResourceVersion(t *testing.T) {
	store := newTestStore(t)
	target := seedUser(t, store, "ivy", "viewer", "")
	k := fakeKubeClient(versionedOwnedServerObj("alpha", nil), versionedOwnedServerObj("beta", nil))
	enforcePatchResourceVersion(t, k)
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	MountOwnership(r, reg, store)
	MountLifecycle(r, reg)

	rr := doWithUser(t, r, "PUT", "/servers/alpha:collaborators", map[string]any{"userIds": []int64{target}}, serverOwnerUser())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("collaborators: got %d %s", rr.Code, rr.Body)
	}
	if got := serverAnnotations(t, k, "alpha")[collaboratorsAnnotation]; got != strconv.FormatInt(target, 10) {
		t.Fatalf("collaborators = %q, want %d", got, target)
	}

	rr = doWithUser(t, r, "POST", "/servers/beta:wipe-data", map[string]any{"confirm": "beta"}, serverOwnerUser())
	if rr.Code != http.StatusAccepted {
		t.Fatalf("wipe: got %d %s", rr.Code, rr.Body)
	}
	if got := serverAnnotations(t, k, "beta")[wipeRequestedAnnotation]; got == "" {
		t.Fatal("wipe annotation not set")
	}

	rr = doWithUser(t, r, "POST", "/servers/alpha:transfer", map[string]any{"userId": target}, serverOwnerUser())
	if rr.Code != http.StatusNoContent {
		t.Fatalf("transfer: got %d %s", rr.Code, rr.Body)
	}
	if got := serverAnnotations(t, k, "alpha")[ownerIDAnnotation]; got != strconv.FormatInt(target, 10) {
		t.Fatalf("owner = %q, want %d", got, target)
	}
}
