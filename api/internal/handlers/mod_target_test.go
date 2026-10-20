package handlers

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	ktesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

func TestClusterModWritesRetryOnlyUnchangedTarget(t *testing.T) {
	for _, route := range allClusterModRoutes() {
		if route.method == http.MethodGet {
			continue
		}
		for _, test := range []struct {
			name   string
			change func(*unstructured.Unstructured)
			want   int
		}{
			{"status only", func(gs *unstructured.Unstructured) {
				gs.Object["status"] = map[string]any{"phase": "Running"}
				annotations := gs.GetAnnotations()
				annotations["test.example/controller"] = "latest"
				gs.SetAnnotations(annotations)
			}, http.StatusOK},
			{"spec edit", func(gs *unstructured.Unstructured) {
				setEnvVars(gs, []envKV{{Name: "KEEP", Value: "concurrent edit"}})
			}, http.StatusConflict},
			{"owner changed", func(gs *unstructured.Unstructured) {
				annotations := gs.GetAnnotations()
				annotations[ownerIDAnnotation] = "100"
				gs.SetAnnotations(annotations)
			}, http.StatusConflict},
			{"collaborator revoked", func(gs *unstructured.Unstructured) {
				annotations := gs.GetAnnotations()
				delete(annotations, collaboratorsAnnotation)
				gs.SetAnnotations(annotations)
			}, http.StatusConflict},
			{"replacement", func(gs *unstructured.Unstructured) {
				gs.SetUID("replacement-uid")
			}, http.StatusNotFound},
			{"deleting", func(gs *unstructured.Unstructured) {
				now := metav1.Now()
				gs.SetDeletionTimestamp(&now)
			}, http.StatusNotFound},
		} {
			t.Run(route.path+"/"+test.name, func(t *testing.T) {
				home, remote := modClusterFixture("local"), modClusterFixture("remote")
				client := remote.Dynamic.(*dynamicfake.FakeDynamicClient)
				updates := 0
				var concurrent, intended *unstructured.Unstructured
				client.PrependReactor("update", "gameservers", func(action ktesting.Action) (bool, runtime.Object, error) {
					updates++
					candidate := action.(ktesting.UpdateAction).GetObject().(*unstructured.Unstructured)
					if updates == 1 {
						intended = candidate.DeepCopy()
						stored, err := client.Tracker().Get(kube.GVRs["servers"], scope.DefaultNamespace, "alpha")
						if err != nil {
							t.Fatal(err)
						}
						concurrent = stored.(*unstructured.Unstructured).DeepCopy()
						concurrent.SetResourceVersion("18")
						test.change(concurrent)
						if err := client.Tracker().Update(kube.GVRs["servers"], concurrent, scope.DefaultNamespace); err != nil {
							t.Fatal(err)
						}
						return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("resourceVersion changed"))
					}
					if candidate.GetUID() != "remote-uid" || candidate.GetResourceVersion() != "18" || candidate.GetNamespace() != scope.DefaultNamespace {
						t.Fatalf("retry lost selected identity: %v", candidate.Object["metadata"])
					}
					return false, nil, nil
				})
				router := mountClusterModRouter(modClusterClients(home, remote), &recordingRegistrySet{provider: &fakeProvider{}}, modRouteUser("remote", "servers:write"), nil)
				response := do(t, router, route.method, route.path+"?cluster=remote", route.body)
				wantUpdates := 1
				if test.want == http.StatusOK {
					wantUpdates = 2
				}
				if response.Code != test.want || updates != wantUpdates || kubeClientCalls(t, home) != 0 {
					t.Fatalf("status=%d want=%d updates=%d want=%d home calls=%d body=%s", response.Code, test.want, updates, wantUpdates, kubeClientCalls(t, home), response.Body)
				}
				stored, err := client.Tracker().Get(kube.GVRs["servers"], scope.DefaultNamespace, "alpha")
				if err != nil {
					t.Fatal(err)
				}
				got := stored.(*unstructured.Unstructured)
				if test.want == http.StatusOK {
					if !reflect.DeepEqual(got.Object["spec"], intended.Object["spec"]) ||
						!reflect.DeepEqual(got.Object["status"], concurrent.Object["status"]) ||
						!reflect.DeepEqual(got.Object["metadata"], concurrent.Object["metadata"]) {
						t.Fatalf("retry did not preserve intended spec and fresh controller fields: %v", got.Object)
					}
				} else if !reflect.DeepEqual(got.Object, concurrent.Object) {
					t.Fatalf("refused retry modified concurrent server: got=%v want=%v", got.Object, concurrent.Object)
				}
			})
		}
	}
}

func TestUpdateModTargetDoesNotRetryOtherFailures(t *testing.T) {
	for _, test := range []struct {
		name       string
		updateErr  error
		readErr    error
		cancel     bool
		missingUID bool
	}{
		{"transport failure", errors.New("connection reset"), nil, false, false},
		{"forbidden", apierrors.NewForbidden(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("denied")), nil, false, false},
		{"read failure", nil, errors.New("target unavailable"), false, false},
		{"read conflict", nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("read conflict")), false, false},
		{"canceled", nil, nil, true, false},
		{"missing original UID", nil, nil, false, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			remote := modClusterFixture("remote")
			client := remote.Dynamic.(*dynamicfake.FakeDynamicClient)
			original, err := remote.Dynamic.Resource(kube.GVRs["servers"]).Namespace(scope.DefaultNamespace).Get(t.Context(), "alpha", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if test.missingUID {
				original.SetUID("")
			}
			snapshot := original.DeepCopy()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			updates, reads := 0, 0
			client.PrependReactor("get", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
				reads++
				return test.readErr != nil, nil, test.readErr
			})
			client.PrependReactor("update", "gameservers", func(ktesting.Action) (bool, runtime.Object, error) {
				updates++
				if test.updateErr != nil {
					return true, nil, test.updateErr
				}
				if test.cancel {
					cancel()
				}
				return true, nil, apierrors.NewConflict(kube.GVRs["servers"].GroupResource(), "alpha", errors.New("resourceVersion changed"))
			})
			updated, err := updateModTarget(ctx, &modTarget{k: remote, namespace: scope.DefaultNamespace, server: original}, func(gs *unstructured.Unstructured) {
				writeModIDs(gs, []ModID{{ID: "new-mod"}})
			})
			wantReads := 1
			if test.updateErr != nil || test.cancel {
				wantReads = 0
			}
			if updated != nil || err == nil || updates != 1 || reads != wantReads {
				t.Fatalf("unexpected retry: updated=%v err=%v updates=%d reads=%d want=%d", updated, err, updates, reads, wantReads)
			}
			switch {
			case test.cancel:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("lost cancellation: %v", err)
				}
			case test.missingUID:
				if !apierrors.IsNotFound(err) {
					t.Fatalf("missing identity was not refused: %v", err)
				}
			case test.updateErr != nil:
				if !errors.Is(err, test.updateErr) {
					t.Fatalf("lost update error: %v", err)
				}
			default:
				if !errors.Is(err, test.readErr) {
					t.Fatalf("lost read error: %v", err)
				}
			}
			if !reflect.DeepEqual(original.Object, snapshot.Object) {
				t.Fatal("attempt mutated the original authorization/configuration snapshot")
			}
		})
	}
}

func TestUpdateModTargetPreservesAlreadyInterruptedContext(t *testing.T) {
	for _, expired := range []bool{false, true} {
		name := "canceled"
		if expired {
			name = "deadline exceeded"
		}
		t.Run(name, func(t *testing.T) {
			remote := modClusterFixture("remote")
			client := remote.Dynamic.(*dynamicfake.FakeDynamicClient)
			obj, err := client.Tracker().Get(kube.GVRs["servers"], scope.DefaultNamespace, "alpha")
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			if expired {
				ctx, cancel = context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
				defer cancel()
			}
			updated, err := updateModTarget(ctx, &modTarget{k: remote, namespace: scope.DefaultNamespace, server: obj.(*unstructured.Unstructured)}, func(*unstructured.Unstructured) {
				t.Fatal("interrupted request reached mutation")
			})
			if updated != nil || !errors.Is(err, ctx.Err()) || len(client.Actions()) != 0 {
				t.Fatalf("interrupted request lost its error or called Kubernetes: updated=%v err=%v actions=%v", updated, err, client.Actions())
			}
		})
	}
}
