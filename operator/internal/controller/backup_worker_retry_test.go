package controller

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBackupWorkerSourceReadFailureRetriesWithoutAuthorizing(t *testing.T) {
	for _, phase := range []string{"before-authorization", "postcheck", "authorized", "authorized-quiesced"} {
		t.Run(phase, func(t *testing.T) {
			b := guardBackup("retry")
			b.Spec.Quiesce = phase == "authorized-quiesced"
			r, q := guardFixtureWorkers(t, false, b)
			budgetPass(t, r, b.Name)
			budgetReadyWorker(t, r, b.Name, "worker-uid")
			if phase == "authorized" || phase == "authorized-quiesced" {
				budgetPass(t, r, b.Name)
			}
			before := guardGetBackup(t, r, b.Name)
			readFailure := apierrors.NewServiceUnavailable("temporary source read failure")
			failedRead := false
			r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, pod := obj.(*corev1.Pod); pod && key.Name == "server-0" {
					if phase == "postcheck" {
						// Capturing the manifest also reads this Pod. Wait for
						// that capture to persist before failing the source
						// postcheck immediately before worker authorization.
						var current gameplanev1alpha1.Backup
						if err := c.Get(ctx, client.ObjectKeyFromObject(b), &current); err != nil {
							return err
						}
						if current.Status.CaptureManifest == nil {
							return c.Get(ctx, key, obj, opts...)
						}
					}
					failedRead = true
					return readFailure
				}
				return c.Get(ctx, key, obj, opts...)
			}})
			request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)}
			if _, err := r.Reconcile(t.Context(), request); !errors.Is(err, readFailure) {
				t.Fatalf("source read failure was not retried: %v", err)
			}
			if !failedRead {
				t.Fatal("reconcile never reached the intended source read")
			}
			after := guardGetBackup(t, r, b.Name)
			if after.Status.Phase == gameplanev1alpha1.BackupPhaseFailed || after.Annotations[backupCopyAbortAnnotation] != "" {
				t.Fatalf("transient source read permanently aborted copy: %+v", after)
			}
			for _, key := range []string{backupPreparationDeadlineAnnotation, backupWorkerSourceAnnotation, backupAuthorizedWorkerAnnotation} {
				if after.Annotations[key] != before.Annotations[key] {
					t.Fatalf("retry changed durable %s", key)
				}
			}
			var gate corev1.ConfigMap
			gateKey := client.ObjectKey{Namespace: b.Namespace, Name: backupStartConfigMapName(b)}
			if err := r.Get(t.Context(), gateKey, &gate); err != nil || gate.Data[backupStartTokenKey] != "" {
				t.Fatalf("unverified source retained an open gate: %v %+v", err, gate.Data)
			}
			var server gameplanev1alpha1.GameServer
			if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &server); err != nil {
				t.Fatal(err)
			}
			if server.Annotations[lifecycleOperationAnnotation] == "" || q.unquiesced != 0 {
				t.Fatal("retry released a possibly running copy's lifecycle/quiesce obligation")
			}
			deadline := after.Annotations[backupQuiesceDeadlineAnnotation]
			r.APIReader = r.Client
			budgetPass(t, r, b.Name)
			recovered := guardGetBackup(t, r, b.Name)
			if err := r.Get(t.Context(), gateKey, &gate); err != nil || gate.Data[backupStartTokenKey] == "" {
				t.Fatalf("healthy source did not regain authorization: %v", err)
			}
			if deadline != "" && recovered.Annotations[backupQuiesceDeadlineAnnotation] != deadline {
				t.Fatal("source retry extended the copy deadline")
			}
			if phase == "authorized-quiesced" && q.quiesced != 1 {
				t.Fatalf("source retry repeated save-off: %+v", q)
			}
		})
	}
}

func TestBackupWorkerUnverifiedSourceStillExpires(t *testing.T) {
	b := guardBackup("expired-read")
	b.Spec.Quiesce = false
	r, _ := guardFixtureWorkers(t, false, b)
	budgetPass(t, r, b.Name)
	b = guardGetBackup(t, r, b.Name)
	patchBackupAnnotations(b, map[string]string{backupPreparationDeadlineAnnotation: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)})
	if err := r.Update(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, pod := obj.(*corev1.Pod); pod && key.Name == "server-0" {
			return apierrors.NewTooManyRequests("source reads throttled", 1)
		}
		return c.Get(ctx, key, obj, opts...)
	}})
	budgetPass(t, r, b.Name)
	if got := guardGetBackup(t, r, b.Name); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Annotations[backupCopyAbortAnnotation] == "" {
		t.Fatalf("unavailable source escaped the preparation deadline: %+v", got)
	}
}

func TestBackupWorkerInvalidSourceRemainsTerminal(t *testing.T) {
	for _, state := range []string{"malformed-binding", "replacement-pod"} {
		t.Run(state, func(t *testing.T) {
			b := guardBackup("invalid-source")
			b.Spec.Quiesce = false
			r, _ := guardFixtureWorkers(t, false, b)
			budgetPass(t, r, b.Name)
			b = guardGetBackup(t, r, b.Name)
			if state == "malformed-binding" {
				patchBackupAnnotations(b, map[string]string{backupWorkerSourceAnnotation: "{"})
				if err := r.Update(t.Context(), b); err != nil {
					t.Fatal(err)
				}
				_, err := r.prepareBackupWorkerSource(t.Context(), b)
				var syntaxError *json.SyntaxError
				if !errors.Is(err, errBackupSourceChanged) || !errors.As(err, &syntaxError) {
					t.Fatalf("malformed source lost its terminal classification or decode cause: %v", err)
				}
			} else {
				r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
					if err := c.Get(ctx, key, obj, opts...); err != nil {
						return err
					}
					if pod, ok := obj.(*corev1.Pod); ok && key.Name == "server-0" {
						pod.UID = types.UID("replacement")
					}
					return nil
				}})
			}
			budgetPass(t, r, b.Name)
			if got := guardGetBackup(t, r, b.Name); got.Status.Phase != gameplanev1alpha1.BackupPhaseFailed || got.Annotations[backupCopyAbortAnnotation] == "" {
				t.Fatalf("invalid source was treated as retryable: %+v", got)
			}
		})
	}
}

func TestBackupWorkerRetrySurfacesGateRevocationFailure(t *testing.T) {
	b := guardBackup("revoke-error")
	b.Spec.Quiesce = false
	r, _ := guardFixture(t, b)
	budgetPass(t, r, b.Name)
	readFailure := apierrors.NewServiceUnavailable("source unavailable")
	revokeFailure := errors.New("gate write unavailable")
	base := r.Client.(client.WithWatch)
	r.Client = interceptor.NewClient(base, interceptor.Funcs{Update: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.UpdateOption) error {
		if _, gate := obj.(*corev1.ConfigMap); gate {
			return revokeFailure
		}
		return c.Update(ctx, obj, opts...)
	}})
	r.APIReader = interceptor.NewClient(base, interceptor.Funcs{Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
		if _, pod := obj.(*corev1.Pod); pod && key.Name == "server-0" {
			return readFailure
		}
		return c.Get(ctx, key, obj, opts...)
	}})
	_, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(b)})
	if !errors.Is(err, readFailure) || !errors.Is(err, revokeFailure) {
		t.Fatalf("retry lost a source or revocation failure: %v", err)
	}
}
