package handlers

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func snapshotSourceServer(name string) *unstructured.Unstructured {
	server := newServerObj("gameplane-games", name)
	server.Object["spec"].(map[string]any)["storage"] = map[string]any{
		"dataSource": map[string]any{"kind": "VolumeSnapshot", "name": "saved-world"},
	}
	return server
}

func snapshotSourceObjects() (*unstructured.Unstructured, *unstructured.Unstructured) {
	backup := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1", "kind": "Backup",
		"metadata": map[string]any{"namespace": "gameplane-games", "name": "world-backup", "uid": "backup-original"},
		"status":   map[string]any{"phase": "Succeeded", "snapshotID": "saved-world"},
	}}
	snapshot := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "snapshot.storage.k8s.io/v1", "kind": "VolumeSnapshot",
		"metadata": map[string]any{"namespace": "gameplane-games", "name": "saved-world", "uid": "snapshot-original"},
		"status":   map[string]any{"readyToUse": true},
	}}
	controller := true
	snapshot.SetOwnerReferences([]metav1.OwnerReference{{
		APIVersion: "gameplane.local/v1alpha1", Kind: "Backup", Name: backup.GetName(), UID: backup.GetUID(), Controller: &controller,
	}})
	return snapshot, backup
}

func snapshotSourceUser(cluster, namespace string, permissions ...string) *auth.User {
	grants := make(map[string]struct{}, len(permissions))
	for _, permission := range permissions {
		grants[permission] = struct{}{}
	}
	return &auth.User{ID: 2, Username: "restorer", Role: "custom", Perms: map[string]map[string]map[string]struct{}{
		cluster: {namespace: grants},
	}}
}

func TestResources_SnapshotSourceRequiresBothScopedPermissions(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, tc := range []struct {
			name    string
			user    *auth.User
			allowed bool
		}{
			{"writer", snapshotSourceUser("local", "gameplane-games", "servers:write"), false},
			{"restorer", snapshotSourceUser("local", "gameplane-games", "backups:restore"), false},
			{"both", snapshotSourceUser("local", "gameplane-games", "servers:write", "backups:restore"), true},
			{"other namespace", snapshotSourceUser("local", "other", "servers:write", "backups:restore"), false},
			{"other cluster", snapshotSourceUser("remote", "gameplane-games", "servers:write", "backups:restore"), false},
			{"no session", nil, false},
		} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				snapshot, backup := snapshotSourceObjects()
				k := fakeKubeClient(snapshot, backup)
				path, want := "/servers/", http.StatusCreated
				if method == http.MethodPut {
					k = fakeKubeClient(snapshot, backup, newServerObj("gameplane-games", "alpha"))
					path, want = "/servers/alpha", http.StatusOK
				}
				if !tc.allowed {
					want = http.StatusForbidden
				}
				rr := doWithUser(t, mountResourcesRouter(k), method, path, snapshotSourceServer("alpha"), tc.user)
				if rr.Code != want {
					t.Fatalf("got %d %s, want %d", rr.Code, rr.Body, want)
				}
			})
		}
	}
}

func TestResources_SnapshotSourceRejectsUntrustedProvenance(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*unstructured.Unstructured, *unstructured.Unstructured)
	}{
		{"orphan", func(s, _ *unstructured.Unstructured) { s.SetOwnerReferences(nil) }},
		{"replaced backup", func(_, b *unstructured.Unstructured) { b.SetUID(types.UID("replacement")) }},
		{"empty owner UID", func(s, _ *unstructured.Unstructured) {
			refs := s.GetOwnerReferences()
			refs[0].UID = ""
			s.SetOwnerReferences(refs)
		}},
		{"foreign group", func(s, _ *unstructured.Unstructured) {
			refs := s.GetOwnerReferences()
			refs[0].APIVersion = "foreign.local/v1alpha1"
			s.SetOwnerReferences(refs)
		}},
		{"noncontroller owner", func(s, _ *unstructured.Unstructured) {
			refs := s.GetOwnerReferences()
			refs[0].Controller = nil
			s.SetOwnerReferences(refs)
		}},
		{"other namespace", func(_, b *unstructured.Unstructured) { b.SetNamespace("other") }},
		{"deleted backup", func(_, b *unstructured.Unstructured) { now := metav1.Now(); b.SetDeletionTimestamp(&now) }},
		{"deleting snapshot", func(s, _ *unstructured.Unstructured) { now := metav1.Now(); s.SetDeletionTimestamp(&now) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot, backup := snapshotSourceObjects()
			tc.change(snapshot, backup)
			rr := doWithUser(t, mountResourcesRouter(fakeKubeClient(snapshot, backup)), http.MethodPost, "/servers/", snapshotSourceServer("alpha"), testAdminUser())
			if rr.Code != http.StatusForbidden {
				t.Fatalf("got %d %s, want 403", rr.Code, rr.Body)
			}
		})
	}
}

func TestResources_SnapshotSourceUnchangedDoesNotRequireRestore(t *testing.T) {
	live := snapshotSourceServer("alpha")
	desired := live.DeepCopy()
	desired.Object["spec"].(map[string]any)["suspend"] = true
	// The source may have expired after provisioning; ordinary server updates
	// must not require it to remain available.
	rr := doWithUser(t, mountResourcesRouter(fakeKubeClient(live)), http.MethodPut, "/servers/alpha", desired, testOperatorUser())
	if rr.Code != http.StatusOK {
		t.Fatalf("got %d %s, want 200", rr.Code, rr.Body)
	}
}

func TestResources_SnapshotSourceRejectsUnfinishedOrAbortedCapture(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*unstructured.Unstructured, *unstructured.Unstructured)
	}{
		{"pending", func(_, b *unstructured.Unstructured) { b.Object["status"] = map[string]any{"phase": "Pending"} }},
		{"running", func(_, b *unstructured.Unstructured) {
			b.Object["status"] = map[string]any{"phase": "Running", "snapshotID": "saved-world"}
		}},
		{"failed with late ready", func(_, b *unstructured.Unstructured) {
			b.Object["status"] = map[string]any{"phase": "Failed", "snapshotID": "saved-world"}
		}},
		{"wrong snapshot", func(_, b *unstructured.Unstructured) {
			b.Object["status"] = map[string]any{"phase": "Succeeded", "snapshotID": "another-capture"}
		}},
		{"not ready", func(s, _ *unstructured.Unstructured) { s.Object["status"] = map[string]any{"readyToUse": false} }},
		{"unknown readiness", func(s, _ *unstructured.Unstructured) { delete(s.Object, "status") }},
		{"backup aborted before status update", func(_, b *unstructured.Unstructured) {
			b.SetAnnotations(map[string]string{"backup.gameplane.local/snapshot-aborted": "true"})
		}},
		{"snapshot aborted", func(s, _ *unstructured.Unstructured) {
			s.SetAnnotations(map[string]string{"backup.gameplane.local/snapshot-aborted": "true"})
		}},
	} {
		for _, method := range []string{http.MethodPost, http.MethodPut, "clone"} {
			t.Run(tc.name+"/"+method, func(t *testing.T) {
				snapshot, backup := snapshotSourceObjects()
				tc.change(snapshot, backup)
				k := fakeKubeClient(snapshot, backup)
				router, path, verb := mountResourcesRouter(k), "/servers/", method
				var body any = snapshotSourceServer("alpha")
				switch method {
				case http.MethodPut:
					k = fakeKubeClient(snapshot, backup, newServerObj("gameplane-games", "alpha"))
					router, path = mountResourcesRouter(k), "/servers/alpha"
				case "clone":
					k = fakeKubeClient(snapshot, backup, snapshotSourceServer("alpha"))
					router, path, verb = mountLifecycleRouter(k), "/servers/alpha:clone", http.MethodPost
					body = map[string]any{"newName": "beta"}
				}
				rr := doWithUser(t, router, verb, path, body, testAdminUser())
				if rr.Code != http.StatusForbidden {
					t.Fatalf("got %d %s, want 403", rr.Code, rr.Body)
				}
			})
		}
	}
}

func TestResources_SnapshotSourceDefaultsKind(t *testing.T) {
	snapshot, backup := snapshotSourceObjects()
	desired := snapshotSourceServer("alpha")
	unstructured.RemoveNestedField(desired.Object, "spec", "storage", "dataSource", "kind")
	rr := doWithUser(t, mountResourcesRouter(fakeKubeClient(snapshot, backup)), http.MethodPost, "/servers/", desired, testAdminUser())
	if rr.Code != http.StatusCreated {
		t.Fatalf("got %d %s, want 201", rr.Code, rr.Body)
	}
}

func TestResources_SnapshotSourceRemovalRequiresRestore(t *testing.T) {
	for _, allowed := range []bool{false, true} {
		live := snapshotSourceServer("alpha")
		desired := live.DeepCopy()
		unstructured.RemoveNestedField(desired.Object, "spec", "storage", "dataSource")
		user := testOperatorUser()
		want := http.StatusForbidden
		if allowed {
			user = snapshotSourceUser("local", "gameplane-games", "servers:write", "backups:restore")
			want = http.StatusOK
		}
		rr := doWithUser(t, mountResourcesRouter(fakeKubeClient(live)), http.MethodPut, "/servers/alpha", desired, user)
		if rr.Code != want {
			t.Fatalf("allowed=%t: got %d %s, want %d", allowed, rr.Code, rr.Body, want)
		}
	}
}

func TestResources_SnapshotSourceUsesSelectedCluster(t *testing.T) {
	snapshot, backup := snapshotSourceObjects()
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set("local", fakeKubeClient())
	reg.Set("remote", fakeKubeClient(snapshot, backup))
	router := chi.NewRouter()
	MountResources(router, reg, nil)
	user := snapshotSourceUser("remote", "gameplane-games", "servers:write", "backups:restore")
	rr := doWithUser(t, router, http.MethodPost, "/servers/?cluster=remote", snapshotSourceServer("alpha"), user)
	if rr.Code != http.StatusCreated {
		t.Fatalf("remote source: got %d %s, want 201", rr.Code, rr.Body)
	}
	local, _ := reg.Get("local")
	if _, err := local.GetServer(t.Context(), "gameplane-games", "alpha"); err == nil {
		t.Fatal("remote create wrote to local cluster")
	}
}

func TestResources_SnapshotSourceReadFailureFailsClosed(t *testing.T) {
	for _, resource := range []string{"volumesnapshots", "backups"} {
		t.Run(resource, func(t *testing.T) {
			snapshot, backup := snapshotSourceObjects()
			k := fakeKubeClient(snapshot, backup)
			k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", resource, func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("private-cluster-address: connection failed")
			})
			rr := doWithUser(t, mountResourcesRouter(k), http.MethodPost, "/servers/", snapshotSourceServer("alpha"), testAdminUser())
			if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private-cluster-address") {
				t.Fatalf("got %d %s, want generic 503", rr.Code, rr.Body)
			}
			if _, err := k.GetServer(t.Context(), "gameplane-games", "alpha"); err == nil {
				t.Fatal("created server despite unverifiable snapshot")
			}
		})
	}
}

func TestLifecycle_CloneSnapshotReadFailureIsGeneric(t *testing.T) {
	for _, resource := range []string{"volumesnapshots", "backups"} {
		t.Run(resource, func(t *testing.T) {
			snapshot, backup := snapshotSourceObjects()
			k := fakeKubeClient(snapshot, backup, snapshotSourceServer("alpha"))
			k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("get", resource, func(ktesting.Action) (bool, runtime.Object, error) {
				return true, nil, errors.New("private-cluster-address: connection failed")
			})
			rr := doWithUser(t, mountLifecycleRouter(k), http.MethodPost, "/servers/alpha:clone", map[string]any{"newName": "beta"}, testAdminUser())
			if rr.Code != http.StatusServiceUnavailable || strings.Contains(rr.Body.String(), "private-cluster-address") {
				t.Fatalf("got %d %s, want generic 503", rr.Code, rr.Body)
			}
			if _, err := k.GetServer(t.Context(), "gameplane-games", "beta"); err == nil {
				t.Fatal("cloned server despite unverifiable snapshot")
			}
		})
	}
}

func TestResources_SnapshotSourceNormalizesClusterSelector(t *testing.T) {
	for _, selector := range []string{"%20", "%20local%20"} {
		for _, method := range []string{http.MethodPost, http.MethodPut, "clone"} {
			t.Run(method+"/"+selector, func(t *testing.T) {
				snapshot, backup := snapshotSourceObjects()
				k := fakeKubeClient(snapshot, backup)
				router := mountResourcesRouter(k)
				path, want := "/servers/", http.StatusCreated
				var body any = snapshotSourceServer("alpha")
				verb := method
				switch method {
				case http.MethodPut:
					k = fakeKubeClient(snapshot, backup, newServerObj("gameplane-games", "alpha"))
					router = mountResourcesRouter(k)
					path, want = "/servers/alpha", http.StatusOK
				case "clone":
					k = fakeKubeClient(snapshot, backup, snapshotSourceServer("alpha"))
					router = mountLifecycleRouter(k)
					path, want, verb = "/servers/alpha:clone", http.StatusOK, http.MethodPost
					body = map[string]any{"newName": "beta"}
				}
				user := snapshotSourceUser("local", "gameplane-games", "servers:write", "backups:restore")
				rr := doWithUser(t, router, verb, path+"?cluster="+selector, body, user)
				if rr.Code != want {
					t.Fatalf("got %d %s, want %d", rr.Code, rr.Body, want)
				}
			})
		}
	}
}
