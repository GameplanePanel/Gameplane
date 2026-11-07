package controller

import (
	"context"
	"errors"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestLegacyQuiesceRecoveryRequiresProcessExitProof(t *testing.T) {
	t.Parallel()
	for _, path := range []string{"lifecycle", "compatibility", "backup-cleanup"} {
		for _, state := range []string{"running-replacement", "unready", "terminating", "read-error", "absent", "failed", "succeeded"} {
			t.Run(path+"/"+state, func(t *testing.T) {
				b := guardBackup("next")
				r, q := guardFixture(t, b)
				var gs gameplanev1alpha1.GameServer
				if err := r.Get(t.Context(), client.ObjectKey{Namespace: "ns", Name: "server"}, &gs); err != nil {
					t.Fatal(err)
				}
				if path == "lifecycle" {
					gs.Annotations = map[string]string{backupGuardAnnotation: backupIdentityJSON("deleted", "deleted-uid")}
					if err := r.Update(t.Context(), &gs); err != nil {
						t.Fatal(err)
					}
				} else {
					if claimed, err := r.claimBackupTarget(t.Context(), b); err != nil || !claimed {
						t.Fatalf("claim: %t %v", claimed, err)
					}
					if path == "compatibility" {
						b.Annotations[backupRecoveryAnnotation] = backupIdentityJSON("deleted", "deleted-uid")
					} else {
						b.Annotations[annoQuiesceAttempted] = "true"
					}
					if err := r.Update(t.Context(), b); err != nil {
						t.Fatal(err)
					}
				}
				readErr := errors.New("pod observation unavailable")
				r.APIReader = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{
					Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
						pod, isPod := obj.(*corev1.Pod)
						if isPod && key.Name == "server-0" {
							switch state {
							case "absent":
								return apierrors.NewNotFound(corev1.Resource("pods"), key.Name)
							case "read-error":
								return readErr
							}
						}
						if err := c.Get(ctx, key, obj, opts...); err != nil {
							return err
						}
						if !isPod || key.Name != "server-0" {
							return nil
						}
						pod.UID = "replacement-with-unknown-predecessor"
						switch state {
						case "unready":
							pod.Status.Phase = corev1.PodPending
						case "terminating":
							now := metav1.Now()
							pod.DeletionTimestamp = &now
							pod.Status.Phase = corev1.PodFailed
						case "failed":
							pod.Status.Phase = corev1.PodFailed
						case "succeeded":
							pod.Status.Phase = corev1.PodSucceeded
						}
						return nil
					},
				})
				var ready bool
				var err error
				switch path {
				case "lifecycle":
					ready, err = r.claimBackupTarget(t.Context(), b)
				case "compatibility":
					ready, err = r.recoverBackupTarget(t.Context(), b)
				case "backup-cleanup":
					err = r.maybeUnquiesce(t.Context(), b)
					ready = err == nil
				}
				wantReady := state == "absent" || state == "failed" || state == "succeeded"
				if ready != wantReady || (err == nil) != wantReady {
					t.Fatalf("recovery ready=%t err=%v; want ready=%t", ready, err, wantReady)
				}
				if state == "read-error" && !errors.Is(err, readErr) {
					t.Fatalf("pod read error was not preserved: %v", err)
				}
				if q.quiesced != 0 || q.unquiesced != 0 {
					t.Fatalf("legacy recovery called an unbound agent: %+v", q)
				}
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
					t.Fatal(err)
				}
				op, held, parseErr := lifecycleOwner(&gs)
				if parseErr != nil || !held || op.UID != b.UID {
					t.Fatalf("lost durable owner: %+v %v", op, parseErr)
				}
				if path == "lifecycle" && (len(op.Recovery) != 0) == wantReady {
					t.Fatalf("recovery lineage changed without exit proof: %+v", op.Recovery)
				}
				stored := guardGetBackup(t, r, b.Name)
				if path == "compatibility" && (stored.Annotations[backupRecoveryAnnotation] == "") != wantReady {
					t.Fatal("legacy recovery marker did not follow exit proof")
				}
				if path == "backup-cleanup" && (stored.Annotations[annoUnquiescedAt] != "") != wantReady {
					t.Fatal("cleanup completion did not follow exit proof")
				}
			})
		}
	}
}
