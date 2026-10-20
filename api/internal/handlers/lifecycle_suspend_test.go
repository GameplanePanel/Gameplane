package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/rbac"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func lifecycleSuspendServer() *unstructured.Unstructured {
	server := newServerObj(scope.DefaultNamespace, "alpha")
	server.SetUID("original-uid")
	server.SetResourceVersion("17")
	server.SetAnnotations(map[string]string{ownerIDAnnotation: "42", collaboratorsAnnotation: "43"})
	return server
}

func lifecycleBoundRouter(k *kube.Client) http.Handler {
	reg := kube.NewRegistry(scope.DefaultCluster)
	reg.Set(scope.DefaultCluster, k)
	r := chi.NewRouter()
	r.Use(rbac.Middleware(reg))
	MountLifecycle(r, reg)
	return r
}

func TestLifecycleSuspendRetriesOnlyUnchangedTarget(t *testing.T) {
	for _, verb := range []string{"start", "stop"} {
		for _, test := range []struct {
			name   string
			change func(*unstructured.Unstructured)
			want   int
		}{
			{"status", func(gs *unstructured.Unstructured) { gs.Object["status"] = map[string]any{"phase": "Running"} }, http.StatusAccepted},
			{"spec", func(gs *unstructured.Unstructured) {
				gs.Object["spec"].(map[string]any)["config"] = map[string]any{"difficulty": "hard"}
			}, http.StatusConflict},
			{"owner", func(gs *unstructured.Unstructured) {
				a := gs.GetAnnotations()
				a[ownerIDAnnotation] = "99"
				gs.SetAnnotations(a)
			}, http.StatusConflict},
			{"collaborator", func(gs *unstructured.Unstructured) {
				a := gs.GetAnnotations()
				delete(a, collaboratorsAnnotation)
				gs.SetAnnotations(a)
			}, http.StatusConflict},
			{"replacement", func(gs *unstructured.Unstructured) { gs.SetUID("replacement-uid") }, http.StatusNotFound},
			{"deleting", func(gs *unstructured.Unstructured) { now := metav1.Now(); gs.SetDeletionTimestamp(&now) }, http.StatusNotFound},
		} {
			t.Run(verb+"/"+test.name, func(t *testing.T) {
				server := lifecycleSuspendServer()
				server.Object["spec"].(map[string]any)["suspend"] = verb == "start"
				k := fakeKubeClient(server)
				dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
				attempts := 0
				var concurrent *unstructured.Unstructured
				dyn.PrependReactor("patch", "gameservers", func(action ktesting.Action) (bool, runtime.Object, error) {
					attempts++
					patch := action.(ktesting.PatchAction)
					var body struct {
						Metadata struct {
							UID             string `json:"uid"`
							ResourceVersion string `json:"resourceVersion"`
						} `json:"metadata"`
						Spec map[string]any `json:"spec"`
					}
					if err := json.Unmarshal(patch.GetPatch(), &body); err != nil {
						t.Fatal(err)
					}
					wantRV := "17"
					if attempts > 1 {
						wantRV = "18"
					}
					if body.Metadata.UID != "original-uid" || body.Metadata.ResourceVersion != wantRV || !reflect.DeepEqual(body.Spec, map[string]any{"suspend": verb == "stop"}) {
						t.Fatalf("patch lost identity/version or changed unrelated spec: %s", patch.GetPatch())
					}
					if attempts > 1 {
						return false, nil, nil
					}
					concurrent = server.DeepCopy()
					concurrent.SetResourceVersion("18")
					test.change(concurrent)
					if err := dyn.Tracker().Update(kube.GVRs["servers"], concurrent, scope.DefaultNamespace); err != nil {
						t.Fatal(err)
					}
					return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("resourceVersion changed"))
				})
				// Exercise real UID-bound collaborator authorization, including
				// revocation between the initial patch and its retry.
				response := doWithUser(t, lifecycleBoundRouter(k), http.MethodPost, "/servers/alpha:"+verb, nil, &auth.User{ID: 43})
				wantAttempts := 1
				if test.want == http.StatusAccepted {
					wantAttempts = 2
				}
				if response.Code != test.want || attempts != wantAttempts {
					t.Fatalf("status=%d want=%d attempts=%d want=%d body=%s", response.Code, test.want, attempts, wantAttempts, response.Body)
				}
				got, err := dyn.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				want := concurrent.DeepCopy()
				if test.want == http.StatusAccepted {
					want.Object["spec"].(map[string]any)["suspend"] = verb == "stop"
				}
				if !reflect.DeepEqual(got.Object, want.Object) {
					t.Fatalf("unexpected persisted mutation: got=%v want=%v", got.Object, want.Object)
				}
			})
		}
	}
}

func TestLifecycleSuspendRetryFailures(t *testing.T) {
	for _, test := range []struct {
		name           string
		err            error
		cancel         bool
		want, attempts int
	}{
		{"exhausted", apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("busy")), false, http.StatusConflict, retry.DefaultRetry.Steps},
		{"forbidden", apierrors.NewForbidden(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("denied")), false, http.StatusForbidden, 1},
		{"missing", apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), "alpha"), false, http.StatusNotFound, 1},
		{"canceled", apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("busy")), true, 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := lifecycleSuspendServer()
			k := fakeKubeClient(server)
			dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			attempts := 0
			dyn.PrependReactor("patch", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
				attempts++
				if test.cancel {
					cancel()
				}
				return true, nil, test.err
			})
			request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/servers/alpha:stop", nil)
			response := httptest.NewRecorder()
			mountLifecycleRouter(k).ServeHTTP(response, request)
			if attempts != test.attempts || (test.want != 0 && response.Code != test.want) || response.Code == http.StatusAccepted {
				t.Fatalf("status=%d want=%d attempts=%d want=%d", response.Code, test.want, attempts, test.attempts)
			}
			got, err := dyn.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got.Object, server.Object) {
				t.Fatal("failed request changed server")
			}
		})
	}
}

func TestLifecycleSuspendRejectsChangedBoundUID(t *testing.T) {
	server := lifecycleSuspendServer()
	k := fakeKubeClient(server)
	dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
	reads, patches := 0, 0
	dyn.PrependReactor("get", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
		reads++
		if reads == 2 { // First read belongs to RBAC, second to the handler.
			replacement := server.DeepCopy()
			replacement.SetUID("replacement-uid")
			if err := dyn.Tracker().Update(kube.GVRs["servers"], replacement, scope.DefaultNamespace); err != nil {
				t.Fatal(err)
			}
		}
		return false, nil, nil
	})
	dyn.PrependReactor("patch", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) { patches++; return false, nil, nil })
	response := doWithUser(t, lifecycleBoundRouter(k), http.MethodPost, "/servers/alpha:stop", nil, &auth.User{ID: 42})
	if response.Code != http.StatusNotFound || patches != 0 {
		t.Fatalf("replacement accepted: status=%d patches=%d", response.Code, patches)
	}
}

func TestLifecycleSuspendPreservesRetryReadErrors(t *testing.T) {
	for _, readErr := range []error{context.Canceled, context.DeadlineExceeded, apierrors.NewForbidden(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("denied")), apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("read conflict"))} {
		t.Run(readErr.Error(), func(t *testing.T) {
			k := fakeKubeClient(lifecycleSuspendServer())
			dyn := k.Dynamic.(*dynamicfake.FakeDynamicClient)
			reads, patches := 0, 0
			dyn.PrependReactor("get", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
				reads++
				return true, nil, readErr
			})
			dyn.PrependReactor("patch", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
				patches++
				return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("write conflict"))
			})
			err := patchServerSuspend(t.Context(), k, lifecycleSuspendServer(), true)
			if !errors.Is(err, readErr) || reads != 1 || patches != 1 {
				t.Fatalf("retry read error changed or retried: err=%v reads=%d patches=%d", err, reads, patches)
			}
		})
	}
}

func TestLifecycleSuspendUnidentifiedFixtureCannotRetry(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		t.Run(fmt.Sprintf("conflict-%t", conflict), func(t *testing.T) {
			server := lifecycleSuspendServer()
			server.SetUID("") // Real API objects always have a UID.
			k := fakeKubeClient(server)
			patches := 0
			k.Dynamic.(*dynamicfake.FakeDynamicClient).PrependReactor("patch", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
				patches++
				if conflict {
					return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("changed"))
				}
				return false, nil, nil
			})
			response := do(t, mountLifecycleRouter(k), http.MethodPost, "/servers/alpha:stop", nil)
			want := http.StatusAccepted
			if conflict {
				want = http.StatusNotFound
			}
			if response.Code != want || patches != 1 {
				t.Fatalf("unidentified fixture behavior: status=%d want=%d patches=%d", response.Code, want, patches)
			}
		})
	}
}
