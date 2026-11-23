//go:build envtest

package controller

import (
	"context"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// withFilesReconciler is withGameServerReconciler with a short file-only idle
// timeout, so teardown can be observed without a long wait.
func withFilesReconciler(t *testing.T, ns string, idle time.Duration) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&GameServerReconciler{
			Client:                 mgr.GetClient(),
			APIReader:              mgr.GetAPIReader(),
			Scheme:                 mgr.GetScheme(),
			AgentImage:             "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentCASecretName:      "agent-ca",
			AgentCASecretNamespace: ns,
			FilesIdleTimeout:       idle,
		}).SetupWithManager(mgr)
	}
}

// filesPodFor returns the file-only pod <name>-files, or false when absent.
func filesPodFor(t *testing.T, ns, name string) (*corev1.Pod, bool) {
	t.Helper()
	var pod corev1.Pod
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name + "-files"}, &pod)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get files pod: %v", err)
	}
	return &pod, true
}

// statefulSetReplicas returns the StatefulSet's spec.replicas, or -1 when the
// StatefulSet or the field is absent.
func statefulSetReplicas(t *testing.T, ns, name string) int32 {
	t.Helper()
	var ss appsv1.StatefulSet
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &ss)
	if apierrors.IsNotFound(err) {
		return -1
	}
	if err != nil {
		t.Fatalf("get statefulset: %v", err)
	}
	if ss.Spec.Replicas == nil {
		return -1
	}
	return *ss.Spec.Replicas
}

// assertFilesPodShape checks the file-only pod's identity, mounts and security
// surface. It must hold for every file-only pod, so it reports with Errorf.
func assertFilesPodShape(t *testing.T, pod *corev1.Pod, gs *gameplanev1alpha1.GameServer) {
	t.Helper()
	if len(pod.Labels) != 2 ||
		pod.Labels["app.kubernetes.io/name"] != "gameplane-files" ||
		pod.Labels["app.kubernetes.io/instance"] != gs.Name {
		t.Errorf("files pod labels = %v, want only the files labels", pod.Labels)
	}
	owner := metav1.GetControllerOf(pod)
	if owner == nil || owner.Kind != "GameServer" || owner.Name != gs.Name || owner.UID != gs.UID ||
		owner.BlockOwnerDeletion == nil || !*owner.BlockOwnerDeletion {
		t.Errorf("files pod controller owner = %+v, want GameServer %s (%s)", owner, gs.Name, gs.UID)
	}
	if pod.Spec.HostNetwork {
		t.Errorf("files pod uses hostNetwork")
	}
	if pod.Spec.AutomountServiceAccountToken == nil || *pod.Spec.AutomountServiceAccountToken {
		t.Errorf("files pod automounts a service account token")
	}
	if len(pod.Spec.Containers) != 1 {
		t.Fatalf("files pod has %d containers, want only the agent", len(pod.Spec.Containers))
	}
	agent := pod.Spec.Containers[0]
	hasFilesOnly := false
	for _, arg := range agent.Args {
		if arg == "--files-only" {
			hasFilesOnly = true
		}
	}
	if !hasFilesOnly {
		t.Errorf("agent args %v lack --files-only", agent.Args)
	}
	for _, p := range agent.Ports {
		if p.HostPort != 0 {
			t.Errorf("agent port %s has hostPort %d", p.Name, p.HostPort)
		}
	}
	volumes := map[string]bool{}
	for _, v := range pod.Spec.Volumes {
		volumes[v.Name] = true
		if v.HostPath != nil {
			t.Errorf("files pod has hostPath volume %s", v.Name)
		}
	}
	if !volumes["data"] || !volumes["agent-tls"] || len(volumes) != 2 {
		t.Errorf("files pod volumes = %v, want exactly data and agent-tls", volumes)
	}
	for _, m := range agent.VolumeMounts {
		if m.Name == "rcon-password" || m.Name == agentTokenVolumeName {
			t.Errorf("files pod mounts %s", m.Name)
		}
	}
}

// TestGameServerFiles_PodOnlyWhileStoppedAndFresh covers the file-only pod's
// gate: a running server gets none, a stopped server with a fresh request gets
// one with the expected shape, and a stale request gets none.
func TestGameServerFiles_PodOnlyWhileStoppedAndFresh(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	fresh := time.Now().UTC().Format(time.RFC3339)
	stale := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)

	running := buildGameServer(ns, "running", tmpl.Name)
	running.Annotations = map[string]string{FilesRequestedAtAnnotation: fresh}
	if err := k8sClient.Create(context.Background(), running); err != nil {
		t.Fatalf("create running gameserver: %v", err)
	}
	consistently(t, 3*time.Second, func() (bool, string) {
		_, ok := filesPodFor(t, ns, "running")
		return !ok, "files pod exists while the server is running"
	})

	stopped := buildGameServer(ns, "stopped", tmpl.Name)
	stopped.Spec.Suspend = true
	stopped.Annotations = map[string]string{FilesRequestedAtAnnotation: fresh}
	if err := k8sClient.Create(context.Background(), stopped); err != nil {
		t.Fatalf("create stopped gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		pod, ok := filesPodFor(t, ns, "stopped")
		if !ok {
			return false, "files pod not created for a stopped server with a fresh request"
		}
		assertFilesPodShape(t, pod, getGameServer(t, ns, "stopped"))
		return true, ""
	})

	staleGS := buildGameServer(ns, "stale", tmpl.Name)
	staleGS.Spec.Suspend = true
	staleGS.Annotations = map[string]string{FilesRequestedAtAnnotation: stale}
	if err := k8sClient.Create(context.Background(), staleGS); err != nil {
		t.Fatalf("create stale gameserver: %v", err)
	}
	consistently(t, 3*time.Second, func() (bool, string) {
		_, ok := filesPodFor(t, ns, "stale")
		return !ok, "files pod exists for a stale request"
	})
}

// TestGameServerFiles_IdleTeardownRevertsService checks that the <gs>-agent
// Service follows the file-only pod: it selects the pod while it exists, the
// pod is removed after the idle timeout, and the Service reverts to the game.
func TestGameServerFiles_IdleTeardownRevertsService(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withFilesReconciler(t, ns, 3*time.Second))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "idle", tmpl.Name)
	gs.Spec.Suspend = true
	gs.Annotations = map[string]string{FilesRequestedAtAnnotation: time.Now().UTC().Format(time.RFC3339)}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}

	serviceSelector := func() (map[string]string, bool, string) {
		var svc corev1.Service
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: "idle-agent"}, &svc); err != nil {
			return nil, false, "agent service: " + err.Error()
		}
		if !svc.Spec.PublishNotReadyAddresses {
			t.Errorf("agent service lost PublishNotReadyAddresses")
		}
		return svc.Spec.Selector, true, ""
	}
	filesLabels := filesPodLabels(gs)
	gameLabels := map[string]string{
		"app.kubernetes.io/name":     "gameplane-game",
		"app.kubernetes.io/instance": "idle",
	}

	eventually(t, func() (bool, string) {
		if _, ok := filesPodFor(t, ns, "idle"); !ok {
			return false, "files pod not created"
		}
		sel, ok, msg := serviceSelector()
		if !ok {
			return false, msg
		}
		return sel["app.kubernetes.io/name"] == filesLabels["app.kubernetes.io/name"], "service does not select the files pod"
	})

	eventuallyWith(t, 20*time.Second, func() (bool, string) {
		_, ok := filesPodFor(t, ns, "idle")
		return !ok, "files pod still present after the idle timeout"
	})
	eventually(t, func() (bool, string) {
		sel, ok, msg := serviceSelector()
		if !ok {
			return false, msg
		}
		return sel["app.kubernetes.io/name"] == gameLabels["app.kubernetes.io/name"], "service did not revert to the game pod"
	})
}

// TestGameServerFiles_StartWaitsForFilesPodGone checks FR-013c: a scale-up
// must not create the game pod while the file-only pod still exists. A
// finalizer keeps the file-only pod terminating, so the StatefulSet must stay
// at zero replicas until it is actually removed.
func TestGameServerFiles_StartWaitsForFilesPodGone(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildGameServer(ns, "wait", tmpl.Name)
	gs.Spec.Suspend = true
	gs.Annotations = map[string]string{FilesRequestedAtAnnotation: time.Now().UTC().Format(time.RFC3339)}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		_, ok := filesPodFor(t, ns, "wait")
		return ok, "files pod not created"
	})

	// Hold the file-only pod open with a finalizer, then request a start.
	const hold = "test/hold"
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, ok := filesPodFor(t, ns, "wait")
		if !ok {
			t.Fatalf("files pod vanished before the hold was set")
		}
		pod.Finalizers = append(pod.Finalizers, hold)
		return k8sClient.Update(context.Background(), pod)
	}); err != nil {
		t.Fatalf("add finalizer: %v", err)
	}
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur := getGameServer(t, ns, "wait")
		cur.Spec.Suspend = false
		return k8sClient.Update(context.Background(), cur)
	}); err != nil {
		t.Fatalf("unsuspend: %v", err)
	}

	eventually(t, func() (bool, string) {
		pod, ok := filesPodFor(t, ns, "wait")
		if !ok || pod.DeletionTimestamp == nil {
			return false, "files pod not yet terminating"
		}
		return true, ""
	})
	consistently(t, 2*time.Second, func() (bool, string) {
		if r := statefulSetReplicas(t, ns, "wait"); r == 1 {
			return false, "game StatefulSet scaled up while the files pod still exists"
		}
		return true, ""
	})

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		pod, ok := filesPodFor(t, ns, "wait")
		if !ok {
			return nil
		}
		var kept []string
		for _, f := range pod.Finalizers {
			if f != hold {
				kept = append(kept, f)
			}
		}
		pod.Finalizers = kept
		return k8sClient.Update(context.Background(), pod)
	}); err != nil {
		t.Fatalf("remove finalizer: %v", err)
	}
	eventually(t, func() (bool, string) {
		if _, ok := filesPodFor(t, ns, "wait"); ok {
			return false, "files pod still present"
		}
		if r := statefulSetReplicas(t, ns, "wait"); r != 1 {
			return false, "game StatefulSet not yet scaled to 1"
		}
		return true, ""
	})
}

// TestGameServerFiles_AsleepServerNotWoken checks that a file-only pod never
// wakes a sleeping server: no wake request, suspend unchanged, no replicas.
func TestGameServerFiles_AsleepServerNotWoken(t *testing.T) {
	ns := newNamespace(t)
	startMgr(t, ns, withGameServerReconciler(t, ns))

	tmpl := buildGameTemplate(uniqueName("minecraft"))
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	now := time.Now().UTC().Format(time.RFC3339)
	gs := buildGameServer(ns, "asleep", tmpl.Name)
	gs.Spec.Suspend = true
	gs.Annotations = map[string]string{
		IdleAsleepSinceAnnotation:  now,
		FilesRequestedAtAnnotation: now,
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	eventually(t, func() (bool, string) {
		_, ok := filesPodFor(t, ns, "asleep")
		return ok, "files pod not created for an asleep server"
	})
	consistently(t, 2*time.Second, func() (bool, string) {
		cur := getGameServer(t, ns, "asleep")
		if _, woke := cur.Annotations[IdleWakeRequestedAnnotation]; woke {
			return false, "file access stamped a wake request"
		}
		if !cur.Spec.Suspend {
			return false, "file access cleared spec.suspend"
		}
		if r := statefulSetReplicas(t, ns, "asleep"); r > 0 {
			return false, "file access scaled the StatefulSet up"
		}
		return true, ""
	})
}
