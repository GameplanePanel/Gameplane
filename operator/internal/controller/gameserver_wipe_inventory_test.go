package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/util/cert"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestGameServerReconcileInventoriesWipeWorkersOnce(t *testing.T) {
	s := wipeScheme(t)
	if err := clientgoscheme.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	gs := wipeGameServer(false, "")
	tmpl := &gameplanev1alpha1.GameTemplate{ObjectMeta: metav1.ObjectMeta{Name: gs.Spec.TemplateRef.Name}, Spec: gameplanev1alpha1.GameTemplateSpec{Image: "example.invalid/game:unit"}}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := cert.NewSelfSignedCACert(cert.Config{CommonName: "wipe-inventory-test"}, key)
	if err != nil {
		t.Fatal(err)
	}
	caSecret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "agent-ca", Namespace: gs.Namespace}, Data: map[string][]byte{
		"ca.crt": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw}),
		"ca.key": pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}),
	}}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, tmpl, caSecret).WithStatusSubresource(gs).Build()
	reader := &wipeInventoryReader{Reader: c}
	r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s, AgentCASecretName: caSecret.Name, AgentCASecretNamespace: caSecret.Namespace}
	// Exercise both initial child/status creation and the next full pass. Idle
	// and capture reconciliation participate, rather than just lifecycle helpers.
	for range 2 {
		reader.jobGets, reader.podLists = 0, 0
		if _, err := r.Reconcile(t.Context(), ctrl.Request{NamespacedName: client.ObjectKeyFromObject(gs)}); err != nil {
			t.Fatal(err)
		}
		var workload appsv1.StatefulSet
		if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &workload); err != nil {
			t.Fatal(err)
		}
		if workload.Spec.Replicas == nil || *workload.Spec.Replicas != 1 {
			t.Fatal("ordinary reconcile did not produce a running workload")
		}
		if reader.jobGets != 1 || reader.podLists != 1 {
			t.Fatalf("full reconcile repeated its uncached worker inventory: Job GETs=%d Pod LISTs=%d", reader.jobGets, reader.podLists)
		}
	}
}

func TestWipeInventoryCannotAuthorizeRecreatedWorkload(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	reader := &wipeInventoryReader{Reader: c}
	r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
	var workers wipeWorkerState
	if changed, err := r.ensureWipeGuard(t.Context(), gs, &workers); err != nil || changed {
		t.Fatalf("preflight: changed=%t err=%v", changed, err)
	}
	if err := c.Delete(t.Context(), gs); err != nil {
		t.Fatal(err)
	}
	replacement := gs.DeepCopy()
	replacement.UID, replacement.ResourceVersion = "replacement", ""
	if err := c.Create(t.Context(), replacement); err != nil {
		t.Fatal(err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1, &workers); err == nil {
		t.Fatal("old inventory authorized a replacement GameServer's workload")
	}
	var workload appsv1.StatefulSet
	if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &workload); !apierrors.IsNotFound(err) {
		t.Fatalf("stale callback created workload: %v", err)
	}
}

type wipeInventoryReader struct {
	client.Reader
	jobGets, podLists int
}

func (r *wipeInventoryReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if _, ok := obj.(*batchv1.Job); ok {
		r.jobGets++
	}
	return r.Reader.Get(ctx, key, obj, opts...)
}

func (r *wipeInventoryReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if _, ok := list.(*corev1.PodList); ok {
		r.podLists++
	}
	return r.Reader.List(ctx, list, opts...)
}

func TestWipeInventoryIsSharedWithWorkloadMutation(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(false, "")
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
	reader := &wipeInventoryReader{Reader: c}
	r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
	var workers wipeWorkerState
	if changed, err := r.ensureWipeGuard(t.Context(), gs, &workers); err != nil || changed {
		t.Fatalf("preflight: changed=%t err=%v", changed, err)
	}
	if err := r.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1, &workers); err != nil {
		t.Fatal(err)
	}
	if reader.jobGets != 1 || reader.podLists != 1 {
		t.Fatalf("ordinary reconcile inventoried workers repeatedly: Job GETs=%d Pod LISTs=%d", reader.jobGets, reader.podLists)
	}
}

func TestWipeInventoryRechecksChangedServerBeforeWorkloadMutation(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(map[bool]string{false: "released guard with orphan", true: "concurrent wipe guard"}[guarded], func(t *testing.T) {
			s := wipeScheme(t)
			gs := wipeGameServer(false, "")
			c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs).Build()
			reader := &wipeInventoryReader{Reader: c}
			r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
			var workers wipeWorkerState
			if changed, err := r.ensureWipeGuard(t.Context(), gs, &workers); err != nil || changed {
				t.Fatalf("preflight: changed=%t err=%v", changed, err)
			}
			current := gs.DeepCopy()
			current.Annotations["test/concurrent-update"] = "new-version"
			if guarded {
				current.Annotations[WipeRequestedAnnotation] = "new-wipe"
				current.Annotations[wipeGuardAnnotation] = "new-wipe"
			}
			if err := c.Update(t.Context(), current); err != nil {
				t.Fatal(err)
			}
			pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "orphan", Namespace: gs.Namespace, Labels: map[string]string{wipeUIDLabel: string(gs.UID)}}, Status: corev1.PodStatus{Phase: corev1.PodRunning}}
			if err := c.Create(t.Context(), pod); err != nil {
				t.Fatal(err)
			}
			if err := r.reconcileStatefulSet(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, nil, &materializedConfig{}, 1, &workers); err != nil {
				t.Fatal(err)
			}
			var workload appsv1.StatefulSet
			if err := c.Get(t.Context(), client.ObjectKeyFromObject(gs), &workload); err != nil {
				t.Fatal(err)
			}
			if workload.Spec.Replicas == nil || *workload.Spec.Replicas != 0 {
				t.Fatal("cached empty inventory allowed a concurrent wipe to start the workload")
			}
			wantLists := 2
			if guarded {
				wantLists = 1
			}
			if reader.podLists != wantLists {
				t.Fatalf("Pod LISTs=%d want %d", reader.podLists, wantLists)
			}
		})
	}
}

func TestWipeReconcileReusesOwnedJobInventory(t *testing.T) {
	s := wipeScheme(t)
	gs := wipeGameServer(true, "active")
	gs.Annotations[wipeGuardAnnotation] = "active"
	job := &batchv1.Job{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-wipe", Namespace: gs.Namespace, UID: "worker", Labels: map[string]string{wipeTokenLabel: "active"}}, Status: batchv1.JobStatus{Active: 1}}
	if err := controllerutil.SetControllerReference(gs, job, s); err != nil {
		t.Fatal(err)
	}
	c := fake.NewClientBuilder().WithScheme(s).WithObjects(gs, job).Build()
	reader := &wipeInventoryReader{Reader: c}
	r := &GameServerReconciler{Client: c, APIReader: reader, Scheme: s}
	var workers wipeWorkerState
	if changed, err := r.ensureWipeGuard(t.Context(), gs, &workers); err != nil || changed {
		t.Fatalf("preflight: changed=%t err=%v", changed, err)
	}
	if err := r.reconcileWipe(t.Context(), gs, &gameplanev1alpha1.GameTemplate{}, &workers); err != nil {
		t.Fatal(err)
	}
	if reader.jobGets != 1 || reader.podLists != 1 {
		t.Fatalf("active wipe repeated its worker reads: Job GETs=%d Pod LISTs=%d", reader.jobGets, reader.podLists)
	}
}
