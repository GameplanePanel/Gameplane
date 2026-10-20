package handlers

import (
	"net/http"
	"testing"

	"github.com/go-chi/chi/v5"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func annotationRouter(k *kube.Client) http.Handler {
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountResources(r, reg, nil)
	MountLifecycle(r, reg)
	return r
}

func TestGenericServerWritesCannotInjectLifecycleAnnotations(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut} {
		for _, user := range []*auth.User{testOperatorUser(), testAdminUser()} {
			t.Run(method+"/"+user.Role, func(t *testing.T) {
				live := ownedServerObj("alpha", nil)
				k := fakeKubeClient()
				path := "/servers/"
				if method == http.MethodPut {
					k = fakeKubeClient(live)
					path += "alpha"
				}
				body := live.DeepCopy()
				body.SetAnnotations(map[string]string{
					wipeRequestedAnnotation:                        "injected",
					"gameplane.local/wipe-data-completed":          "forged",
					"gameplane.local/restore-guard":                "forged",
					"gameserver.gameplane.local/stop-requested-at": "forged",
					"restore.gameplane.local/created-by":           "forged",
					ownerIDAnnotation:                              "999",
					"example.com/note":                             "editable",
					"gameplane.local/description":                  "new description",
				})
				body.Object["spec"].(map[string]any)["suspend"] = true
				response := doWithUser(t, annotationRouter(k), method, path, body.Object, user)
				if response.Code != http.StatusOK && response.Code != http.StatusCreated {
					t.Fatalf("write failed: %d %s", response.Code, response.Body)
				}
				got := serverAnnotations(t, k, "alpha")
				for _, key := range []string{wipeRequestedAnnotation, "gameplane.local/wipe-data-completed", "gameplane.local/restore-guard", "gameserver.gameplane.local/stop-requested-at", "restore.gameplane.local/created-by"} {
					if _, found := got[key]; found {
						t.Errorf("generic write injected %s", key)
					}
				}
				if got["example.com/note"] != "editable" || got[ownerIDAnnotation] == "999" {
					t.Fatalf("incorrect ownership or user annotations: %v", got)
				}
				if got["gameplane.local/description"] != "new description" {
					t.Fatal("description edit was lost")
				}
			})
		}
	}
}

func TestGenericServerUpdateClearsUserEditableAnnotations(t *testing.T) {
	live := ownedServerObj("alpha", map[string]string{
		"gameplane.local/description":          "old description",
		"gameplane.local/grace-period-seconds": "30",
	})
	k := fakeKubeClient(live)
	body := live.DeepCopy()
	body.SetAnnotations(nil)
	body.Object["spec"].(map[string]any)["stopGracePeriodSeconds"] = int64(30)
	response := doWithUser(t, annotationRouter(k), http.MethodPut, "/servers/alpha", body.Object, testOperatorUser())
	if response.Code != http.StatusOK {
		t.Fatalf("settings update failed: %d %s", response.Code, response.Body)
	}
	got := serverAnnotations(t, k, "alpha")
	for _, key := range []string{"gameplane.local/description", "gameplane.local/grace-period-seconds"} {
		if _, exists := got[key]; exists {
			t.Errorf("user annotation %s was restored after removal", key)
		}
	}
	if got[ownerIDAnnotation] != live.GetAnnotations()[ownerIDAnnotation] {
		t.Fatal("owner was not preserved")
	}
}

func TestGenericServerUpdatePreservesControllerState(t *testing.T) {
	for _, omit := range []bool{false, true} {
		t.Run(map[bool]string{false: "overwrite", true: "omit"}[omit], func(t *testing.T) {
			protected := map[string]string{
				wipeRequestedAnnotation:                        "pending",
				"gameplane.local/wipe-data-completed":          "previous",
				"gameplane.local/backup-guard":                 "active-backup",
				"gameserver.gameplane.local/stop-requested-at": "original-time",
				"restore.gameplane.local/created-by":           "original-restore",
			}
			live := ownedServerObj("alpha", protected)
			k := fakeKubeClient(live)
			body := live.DeepCopy()
			annotations := map[string]string{"example.com/note": "new-note"}
			if !omit {
				for key := range protected {
					annotations[key] = "tampered"
				}
			}
			body.SetAnnotations(annotations)
			response := doWithUser(t, annotationRouter(k), http.MethodPut, "/servers/alpha", body.Object, testOperatorUser())
			if response.Code != http.StatusOK {
				t.Fatalf("settings update failed: %d %s", response.Code, response.Body)
			}
			got := serverAnnotations(t, k, "alpha")
			for key, value := range live.GetAnnotations() {
				if got[key] != value {
					t.Errorf("changed protected %s: got %q, want %q", key, got[key], value)
				}
			}
			if got["example.com/note"] != "new-note" {
				t.Fatal("ordinary annotation edit was lost")
			}
		})
	}
}

func TestAuthorizedWipeStillUsesDedicatedRoute(t *testing.T) {
	// Owner-only grants are bound to the server UID (rbac.ValidateServerIdentity
	// fails closed on an empty UID), so the fixture needs one like a real object.
	live := ownedServerObj("alpha", nil)
	live.SetUID("alpha-uid")
	k := fakeKubeClient(live)
	r := annotationRouter(k)
	denied := doWithUser(t, r, http.MethodPost, "/servers/alpha:wipe-data", wipeDataReq{Confirm: "alpha"}, testOperatorUser())
	if denied.Code != http.StatusForbidden {
		t.Fatalf("non-owner wipe status %d", denied.Code)
	}
	allowed := doWithUser(t, r, http.MethodPost, "/servers/alpha:wipe-data", wipeDataReq{Confirm: "alpha"}, serverOwnerUser())
	if allowed.Code != http.StatusAccepted {
		t.Fatalf("owner wipe failed: %d %s", allowed.Code, allowed.Body)
	}
	gs, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha", metav1.GetOptions{})
	if err != nil || gs.GetAnnotations()[wipeRequestedAnnotation] == "" {
		t.Fatalf("authorized wipe missing: %v", err)
	}
}
