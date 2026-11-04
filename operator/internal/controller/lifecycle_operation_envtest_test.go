//go:build envtest

package controller

import (
	"sync"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// A real apiserver arbitrates across controller types; no process-local mutex
// or informer timing can allow both claimants to create workers.
func TestLifecycleCASAcrossControllers(t *testing.T) {
	ns := newNamespace(t)
	seedGameServer(t, ns, "target")
	b := buildBackup(ns, "backup", "target", "repo")
	b.Spec.Quiesce = false
	rs := &gameplanev1alpha1.Restore{ObjectMeta: metav1.ObjectMeta{Name: "restore", Namespace: ns}, Spec: gameplanev1alpha1.RestoreSpec{ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "target"}, BackupRef: gameplanev1alpha1.LocalObjectRef{Name: "source"}}}
	if err := k8sClient.Create(t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if err := k8sClient.Create(t.Context(), rs); err != nil {
		t.Fatal(err)
	}
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(t.Context(), client.ObjectKey{Namespace: ns, Name: "target"}, &gs); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		claimed bool
		err     error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(2)
	for _, want := range []lifecycleIdentity{backupLifecycleIdentity(&gs, b, false), restoreLifecycleIdentity(&gs, rs)} {
		go func() {
			target := gs.DeepCopy()
			ready.Done()
			<-start
			claimed, err := claimLifecycleOperation(t.Context(), k8sClient, k8sClient, target, want, nil)
			results <- outcome{claimed: claimed, err: err}
		}()
	}
	ready.Wait()
	close(start)
	winners := 0
	for range 2 {
		result := <-results
		if result.err != nil && !apierrors.IsConflict(result.err) {
			t.Fatalf("claim: %v", result.err)
		}
		if result.claimed {
			winners++
		}
	}
	if winners != 1 {
		t.Fatalf("exclusive admission had %d winners", winners)
	}
	if err := k8sClient.Get(t.Context(), client.ObjectKeyFromObject(&gs), &gs); err != nil {
		t.Fatal(err)
	}
	op, held, err := lifecycleOwner(&gs)
	if err != nil || !held || op.TargetUID != gs.UID {
		t.Fatalf("owner was not durable: held=%t owner=%+v err=%v", held, op, err)
	}
}
