package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/cert"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func lifecycleGameServerFixture(t *testing.T, gs *gameplanev1alpha1.GameServer, extras ...client.Object) *GameServerReconciler {
	t.Helper()
	s := wipeScheme(t)
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: gs.Spec.TemplateRef.Name}, Spec: gameplanev1alpha1.GameTemplateSpec{Image: "example.invalid/game:unit"}}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := cert.NewSelfSignedCACert(cert.Config{CommonName: "lifecycle-reconcile-test"}, key)
	if err != nil {
		t.Fatal(err)
	}
	caSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "agent-ca", Namespace: gs.Namespace}, Data: map[string][]byte{
		"ca.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}),
		"ca.key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}}
	objects := append([]client.Object{gs, tmpl, caSecret}, extras...)
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).
		WithStatusSubresource(gs, &appsv1.StatefulSet{}, &batchv1.Job{}).Build()
	return &GameServerReconciler{Client: c, APIReader: c, Scheme: s, AgentCASecretName: caSecret.Name, AgentCASecretNamespace: caSecret.Namespace}
}

func requireNoWipeJob(t *testing.T, r *GameServerReconciler, gs *gameplanev1alpha1.GameServer) {
	t.Helper()
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job); !apierrors.IsNotFound(err) {
		t.Fatalf("wipe launched before recovery completed: %v", err)
	}
}

func TestGameServerWipeReconcileDrainsOrphanRestoreStartup(t *testing.T) {
	gs := wipeGameServer(false, "wipe")
	predecessor := lifecycleIdentity{Kind: "Restore", Name: "deleted", UID: "deleted-restore", TargetUID: gs.UID, InitialStartup: true}
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: predecessor})
	gs.Annotations[restoreGuardAnnotation] = restoreIdentityJSON(predecessor.Name, predecessor.UID)
	ss := wipeStoppedSet(gs)
	one := int32(1)
	ss.Spec.Replicas, ss.Status.Replicas = &one, 1
	ss.Spec.Template.Spec.Containers = []corev1.Container{{Name: "game", Image: "restored-image"}}
	originalTemplate := ss.Spec.Template.DeepCopy()
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "startup", Finalizers: []string{"test/hold"},
		OwnerReferences: []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: "StatefulSet", Name: ss.Name, UID: ss.UID, Controller: ownerBoolPtr(true)}}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	r := lifecycleGameServerFixture(t, gs, ss, pod)
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}
	// Admission persists the successor. The next public pass must reach the
	// StatefulSet controller instead of waiting for its own scale-down.
	for range 2 {
		if _, err := r.Reconcile(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	current := wipeCurrent(t, r.Client, gs)
	op, held, err := lifecycleOwner(current)
	if err != nil || !held || op.Kind != "Wipe" || len(op.Recovery) != 1 || op.Recovery[0].UID != predecessor.UID || !current.Spec.Suspend {
		t.Fatalf("orphan recovery lost its identity or exclusion: %+v %v", current, err)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil {
		t.Fatal(err)
	}
	if ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || !reflect.DeepEqual(ss.Spec.Template, *originalTemplate) {
		t.Fatalf("recovery did not scale down with the original template: %+v", ss.Spec)
	}
	requireNoWipeJob(t, r, gs)
	ss.Status.Replicas, ss.Status.ObservedGeneration = 0, ss.Generation
	if err := r.Status().Update(t.Context(), ss); err != nil {
		t.Fatal(err)
	}
	if err := r.Delete(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	requireNoWipeJob(t, r, gs)
	current = wipeCurrent(t, r.Client, gs)
	op, held, err = lifecycleOwner(current)
	if err != nil || !held || len(op.Recovery) != 1 {
		t.Fatalf("terminating startup pod lost the recovery fence: %+v %v", current, err)
	}
	if err := r.Get(t.Context(), client.ObjectKeyFromObject(pod), pod); err != nil {
		t.Fatal(err)
	}
	pod.Finalizers = nil
	if err := r.Update(t.Context(), pod); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if _, err := r.Reconcile(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job); err != nil {
		t.Fatalf("drained predecessor did not permit wipe: %v", err)
	}
	current = wipeCurrent(t, r.Client, gs)
	op, held, err = lifecycleOwner(current)
	if err != nil || !held || op.Kind != "Wipe" || len(op.Recovery) != 0 || !current.Spec.Suspend || job.Labels[wipeTokenLabel] != "wipe" {
		t.Fatalf("wipe launch lost exclusive ownership: %+v %v", current, err)
	}
}

func TestGameServerWipeReconcileWaitsForPredecessorCleanup(t *testing.T) {
	gs := wipeGameServer(true, "wipe")
	gs.Annotations[wipeGuardAnnotation] = "wipe"
	predecessor := lifecycleIdentity{Kind: "Backup", Name: "deleted", UID: "deleted-backup", TargetUID: gs.UID, NeedsUnquiesce: true, QuiescePodUID: "original-game"}
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: wipeLifecycleIdentity(gs, "wipe"), Recovery: []lifecycleIdentity{predecessor}})
	// A labelled worker still counts even if its Job and PVC mount metadata
	// are gone. A stopped target alone does not complete predecessor cleanup.
	worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan-copy", Namespace: gs.Namespace, Labels: map[string]string{backupUIDLabel: string(predecessor.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	gamePod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-0", Namespace: gs.Namespace, UID: "original-game"}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
	r := lifecycleGameServerFixture(t, gs, wipeStoppedSet(gs), worker, gamePod)
	cleanupErr := errors.New("save-on unavailable")
	quiescer := &scrapeQuiescer{unquiesceErr: cleanupErr}
	r.OperationQuiescer = quiescer
	request := ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}
	if _, err := r.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	requireNoWipeJob(t, r, gs)
	if err := r.Delete(t.Context(), worker); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Reconcile(t.Context(), request); !errors.Is(err, cleanupErr) {
		t.Fatalf("unfinished save-on did not retain its error: %v", err)
	}
	requireNoWipeJob(t, r, gs)
	current := wipeCurrent(t, r.Client, gs)
	op, held, err := lifecycleOwner(current)
	if err != nil || !held || len(op.Recovery) != 1 || !current.Spec.Suspend {
		t.Fatalf("unfinished save-on lost its recovery fence: %+v %v", current, err)
	}
	quiescer.unquiesceErr = nil
	if _, err := r.Reconcile(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	requireNoWipeJob(t, r, gs)
	if err := r.Delete(t.Context(), gamePod); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := r.Reconcile(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	var job batchv1.Job
	if err := r.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job); err != nil {
		t.Fatalf("completed save-on did not permit wipe: %v", err)
	}
	if quiescer.unquiesced != 1 {
		t.Fatalf("save-on completed %d times, want once", quiescer.unquiesced)
	}
}

func TestWipeRecoveryRetainsCleanupFences(t *testing.T) {
	for _, mode := range []string{"cancel", "replace", "success"} {
		t.Run(mode, func(t *testing.T) {
			gs := wipeGameServer(true, "wipe")
			gs.Annotations[wipeGuardAnnotation] = "wipe"
			predecessor := lifecycleIdentity{Kind: "Backup", Name: "deleted", UID: "deleted-backup", TargetUID: gs.UID}
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: wipeLifecycleIdentity(gs, "wipe"), Recovery: []lifecycleIdentity{predecessor}})
			ss := wipeStoppedSet(gs)
			worker := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan-copy", Namespace: gs.Namespace, Labels: map[string]string{backupUIDLabel: string(predecessor.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			objects := []client.Object{gs, ss, worker}
			switch mode {
			case "cancel":
				delete(gs.Annotations, WipeRequestedAnnotation)
			case "replace":
				gs.Annotations[WipeRequestedAnnotation] = "replacement"
			case "success":
				gs.Annotations[wipeSuccessAnnotation] = "wipe"
				objects = append(objects, &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-wipe", Namespace: gs.Namespace, Labels: map[string]string{wipeTokenLabel: "wipe"},
					OwnerReferences: []metav1.OwnerReference{{APIVersion: gameplanev1alpha1.GroupVersion.String(), Kind: "GameServer", Name: gs.Name, UID: gs.UID, Controller: ownerBoolPtr(true)}}}, Status: batchv1.JobStatus{Succeeded: 1}})
			}
			s := wipeScheme(t)
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(objects...).Build()
			r := &GameServerReconciler{Client: c, APIReader: c, Scheme: s}
			if err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}); err != nil {
				t.Fatal(err)
			}
			current := wipeCurrent(t, c, gs)
			op, held, err := lifecycleOwner(current)
			if err != nil || !held || op.Token != "wipe" || len(op.Recovery) != 1 || current.Annotations[wipeGuardAnnotation] != "wipe" || current.Annotations[WipeCompletedAnnotation] != "" || !current.Spec.Suspend {
				t.Fatalf("cleanup changed unfinished recovery: %+v %v", current, err)
			}
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(ss), ss); err != nil || ss.Annotations[wipeGuardAnnotation] != "wipe" {
				t.Fatalf("cleanup removed the workload fence: %+v %v", ss, err)
			}
			if mode == "success" {
				var job batchv1.Job
				if err := c.Get(t.Context(), client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-wipe"}, &job); err != nil || !job.DeletionTimestamp.IsZero() {
					t.Fatalf("success cleanup ran before predecessor drained: %+v %v", job, err)
				}
			} else {
				requireNoWipeJob(t, r, gs)
			}
		})
	}
}

func TestWipeLaunchRevalidatesDurableOwnerAfterStopProof(t *testing.T) {
	for _, mode := range []string{"recovery", "other owner", "missing owner", "malformed owner"} {
		t.Run(mode, func(t *testing.T) {
			gs := wipeGameServer(true, "wipe")
			gs.Annotations[wipeGuardAnnotation] = "wipe"
			gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(lifecycleOperation{lifecycleIdentity: wipeLifecycleIdentity(gs, "wipe")})
			s := wipeScheme(t)
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, wipeStoppedSet(gs)).Build()
			observedWorkload, changedOwner := false, false
			reader := interceptor.NewClient(c, interceptor.Funcs{Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if err := cl.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				if _, ok := obj.(*appsv1.StatefulSet); ok {
					observedWorkload = true
				}
				live, ok := obj.(*gameplanev1alpha1.GameServer)
				if !ok || !observedWorkload || changedOwner {
					return nil
				}
				changedOwner = true
				op := lifecycleOperation{lifecycleIdentity: wipeLifecycleIdentity(live, "wipe")}
				switch mode {
				case "recovery":
					op.Recovery = []lifecycleIdentity{{Kind: "Backup", Name: "predecessor", UID: "predecessor", TargetUID: gs.UID, NeedsUnquiesce: true}}
				case "other owner":
					op.lifecycleIdentity = lifecycleIdentity{Kind: "Restore", Name: "replacement", UID: "replacement", TargetUID: gs.UID}
				}
				live.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
				switch mode {
				case "missing owner":
					delete(live.Annotations, lifecycleOperationAnnotation)
				case "malformed owner":
					live.Annotations[lifecycleOperationAnnotation] = "invalid"
				}
				return cl.Update(ctx, live)
			}})
			r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
			err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{})
			if mode == "malformed owner" {
				if err == nil {
					t.Fatal("malformed owner did not fail closed")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if !changedOwner {
				t.Fatal("did not reach the live owner read after stop proof")
			}
			requireNoWipeJob(t, r, gs)
		})
	}
}

type lifecycleErrorLog struct{ errors *[]error }

func (*lifecycleErrorLog) Init(logr.RuntimeInfo)                 {}
func (*lifecycleErrorLog) Enabled(int) bool                      { return true }
func (*lifecycleErrorLog) Info(int, string, ...any)              {}
func (l *lifecycleErrorLog) WithValues(...any) logr.LogSink      { return l }
func (l *lifecycleErrorLog) WithName(string) logr.LogSink        { return l }
func (l *lifecycleErrorLog) Error(err error, _ string, _ ...any) { *l.errors = append(*l.errors, err) }

func TestGameServerLifecyclePatchConflictsRetryQuietly(t *testing.T) {
	for _, transition := range []string{"idle", "restart"} {
		for _, conflict := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/conflict=%t", transition, conflict), func(t *testing.T) {
				gs := wipeGameServer(false, "")
				if transition == "idle" {
					// Disabling idle while asleep requests an annotation clear.
					gs.Annotations[IdleAsleepSinceAnnotation] = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
				} else {
					gs.Annotations[RestartRequestedAnnotation] = "restart"
				}
				r := lifecycleGameServerFixture(t, gs)
				cause := errors.New("API unavailable")
				patchErr := cause
				if conflict {
					patchErr = apierrors.NewConflict(schema.GroupResource{Group: "gameplane.local", Resource: "gameservers"}, gs.Name, cause)
				}
				patchErr = fmt.Errorf("write response: %w", patchErr)
				patches := 0
				r.Client = interceptor.NewClient(r.Client.(client.WithWatch), interceptor.Funcs{Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
					if _, ok := obj.(*gameplanev1alpha1.GameServer); ok {
						patches++
						return patchErr
					}
					return c.Patch(ctx, obj, patch, opts...)
				}})
				var logged []error
				ctx := log.IntoContext(t.Context(), logr.New(&lifecycleErrorLog{errors: &logged}))
				result, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)})
				if patches != 1 {
					t.Fatalf("lifecycle patch was not reached exactly once: %d", patches)
				}
				if conflict {
					if err != nil || result != (ctrl.Result{RequeueAfter: time.Second}) || len(logged) != 0 {
						t.Fatalf("expected quiet one-second conflict retry: result=%+v error=%v logs=%v", result, err, logged)
					}
				} else if !errors.Is(err, cause) || result != (ctrl.Result{}) || len(logged) != 1 || !errors.Is(logged[0], cause) {
					t.Fatalf("real error lost its identity/log/backoff: result=%+v error=%v logs=%v", result, err, logged)
				}
				var ss appsv1.StatefulSet
				if err := r.Get(t.Context(), client.ObjectKeyFromObject(gs), &ss); !apierrors.IsNotFound(err) {
					t.Fatalf("failed lifecycle patch still mutated the workload: %v", err)
				}
			})
		}
	}
}
