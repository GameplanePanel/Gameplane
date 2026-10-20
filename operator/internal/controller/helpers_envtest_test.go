//go:build envtest

package controller

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-logr/logr"
	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"

	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	configv1 "sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// ---------------------------------------------------------------------
// Per-test manager + namespace
// ---------------------------------------------------------------------

// setupReconciler wires a single controller into the given Manager.
// Tests pass these to startMgr to choose which reconcilers participate
// — keeps tests focused and prevents cross-controller interference.
type setupReconciler func(mgr manager.Manager) error

// backupReconcilerOpts lets tests inject fakes for the agent client
// and the pod-log reader without standing up real HTTPS or pods.
type backupReconcilerOpts struct {
	agent AgentQuiescer
	logs  BackupLogReader
}

func withBackupReconciler(opts ...backupReconcilerOpts) setupReconciler {
	var o backupReconcilerOpts
	if len(opts) > 0 {
		o = opts[0]
	}
	return func(mgr manager.Manager) error {
		return (&BackupReconciler{
			Client:      mgr.GetClient(),
			Scheme:      mgr.GetScheme(),
			AgentClient: o.agent,
			LogReader:   o.logs,
		}).SetupWithManager(mgr)
	}
}

func withRestoreReconciler() setupReconciler {
	return func(mgr manager.Manager) error {
		return (&RestoreReconciler{
			Client:    mgr.GetClient(),
			APIReader: mgr.GetAPIReader(),
			Scheme:    mgr.GetScheme(),
		}).SetupWithManager(mgr)
	}
}

func withScheduleReconciler() setupReconciler {
	return func(mgr manager.Manager) error {
		return (&BackupScheduleReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr)
	}
}

// withGameServerReconciler wires up GameServerReconciler and seeds an
// agent-CA Secret in ns so reconcileAgentTLS has something to sign with.
// All GameServer envtest cases call this — the per-server agent TLS
// Secret is part of the reconcile, so the CA must always be present.
func withGameServerReconciler(t *testing.T, ns string) setupReconciler {
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
		}).SetupWithManager(mgr)
	}
}

// withGameServerReconcilerAgentPullPolicy is withGameServerReconciler with
// AgentImagePullPolicy also set, proving the operator's
// --agent-image-pull-policy flag (wired through main.go into
// GameServerReconciler.AgentImagePullPolicy) reaches the agent sidecar
// container's ImagePullPolicy end to end.
func withGameServerReconcilerAgentPullPolicy(t *testing.T, ns, policy string) setupReconciler {
	t.Helper()
	seedAgentCA(t, ns, "agent-ca")
	return func(mgr manager.Manager) error {
		return (&GameServerReconciler{
			Client:                 mgr.GetClient(),
			APIReader:              mgr.GetAPIReader(),
			Scheme:                 mgr.GetScheme(),
			AgentImage:             "ghcr.io/gameplanepanel/gameplane/agent:test",
			AgentImagePullPolicy:   policy,
			AgentCASecretName:      "agent-ca",
			AgentCASecretNamespace: ns,
		}).SetupWithManager(mgr)
	}
}

// withGameServerReconcilerAddressManager is withGameServerReconciler with
// AddressManager also set, proving the address conflict detection logic
// is reachable when an address manager is configured.
func withGameServerReconcilerAddressManager(t *testing.T, ns, addressManager string) setupReconciler {
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
			AddressManager:         addressManager,
		}).SetupWithManager(mgr)
	}
}

func withGameTemplateReconciler() setupReconciler {
	return func(mgr manager.Manager) error {
		return (&GameTemplateReconciler{Client: mgr.GetClient(), Scheme: mgr.GetScheme()}).SetupWithManager(mgr)
	}
}

// startMgr boots a controller-runtime Manager scoped to ns with the
// given reconcilers, runs it in a goroutine, and registers cleanup.
// Returns the manager's cached client (the same one the reconcilers
// see) once the cache is synced. Tests that need to assert on the
// reconciler's view of the world (e.g. "the controller has observed
// the new status") should read through this client; tests that need
// strong-read semantics should keep using the package-level k8sClient.
func startMgr(t *testing.T, ns string, setups ...setupReconciler) client.Client {
	t.Helper()

	skip := true
	mgr, err := manager.New(cfg, manager.Options{
		Scheme: scheme,
		Logger: logr.Discard(),
		Cache: cache.Options{
			// Include the test namespace AND cluster scope (""). Without
			// the "" entry, controller-runtime's namespace-restricted
			// cache will refuse to serve reads of cluster-scoped CRDs
			// like GameTemplate.
			DefaultNamespaces: map[string]cache.Config{ns: {}, "": {}},
		},
		Metrics:                metricsserver.Options{BindAddress: "0"},
		HealthProbeBindAddress: "0",
		// Each test starts a fresh Manager, so the same controller name
		// re-registers in the same process. Skip the uniqueness check.
		Controller: configv1.Controller{SkipNameValidation: &skip},
	})
	if err != nil {
		t.Fatalf("manager.New: %v", err)
	}

	for _, s := range setups {
		if err := s(mgr); err != nil {
			t.Fatalf("setup reconciler: %v", err)
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	started := make(chan struct{})
	go func() {
		close(started)
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("mgr.Start: %v", err)
		}
	}()

	<-started
	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatalf("cache did not sync")
	}
	return mgr.GetClient()
}

// newNamespace creates a uniquely-named namespace and registers cleanup.
func newNamespace(t *testing.T) string {
	t.Helper()
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		t.Fatalf("rand: %v", err)
	}
	ns := "gameplane-test-" + hex.EncodeToString(buf)

	if err := k8sClient.Create(context.Background(), &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: ns},
	}); err != nil {
		t.Fatalf("create namespace %s: %v", ns, err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})
	})
	return ns
}

// ---------------------------------------------------------------------
// Polling assertion
// ---------------------------------------------------------------------

const (
	defaultEventuallyTimeout  = 10 * time.Second
	defaultEventuallyInterval = 200 * time.Millisecond
)

// eventually polls cond until it returns true or the timeout fires. The
// returned message is included in t.Fatal on timeout — useful for
// "had X, wanted Y" diffs.
func eventually(t *testing.T, cond func() (bool, string)) {
	t.Helper()
	eventuallyWith(t, defaultEventuallyTimeout, cond)
}

func eventuallyWith(t *testing.T, timeout time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var lastMsg string
	for time.Now().Before(deadline) {
		ok, msg := cond()
		if ok {
			return
		}
		lastMsg = msg
		time.Sleep(defaultEventuallyInterval)
	}
	if ok, _ := cond(); ok {
		return
	}
	t.Fatalf("eventually: timed out after %s: %s", timeout, lastMsg)
}

// consistently asserts cond stays true for the entire window. Used to
// verify a controller does NOT advance state (e.g. Restore stays
// Pending while Backup is still Running).
func consistently(t *testing.T, window time.Duration, cond func() (bool, string)) {
	t.Helper()
	deadline := time.Now().Add(window)
	for time.Now().Before(deadline) {
		ok, msg := cond()
		if !ok {
			t.Fatalf("consistently: condition broke within %s: %s", window, msg)
		}
		time.Sleep(defaultEventuallyInterval)
	}
}

// ---------------------------------------------------------------------
// Object constructors
// ---------------------------------------------------------------------

func buildResticRepoSecret(ns, name string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		StringData: map[string]string{
			"repo":     "rest:http://restic.local/repo",
			"password": "test-secret",
			"url":      "rest:http://restic.local/repo",
		},
	}
}

func buildBackup(ns, name, gsName, repoSecret string) *gameplanev1alpha1.Backup {
	return &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: gsName},
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: repoSecret, Key: "url"},
		},
	}
}

func buildRestore(ns, name, backupName, gsName string) *gameplanev1alpha1.Restore {
	return &gameplanev1alpha1.Restore{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.RestoreSpec{
			BackupRef: gameplanev1alpha1.LocalObjectRef{Name: backupName},
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: gsName},
		},
	}
}

// buildGameServer constructs a minimal GameServer that references an
// (assumed-to-exist) GameTemplate. The reconcilers under test in PR #1
// (Backup, Restore) do not require the GameTemplate to exist; they
// only Get the GameServer.
func buildGameServer(ns, name, tmplName string) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: tmplName},
		},
	}
}

// buildGameTemplate produces a small but valid cluster-scoped template
// suitable for GameServer reconciler tests. Only the fields the
// reconciler actually reads are populated.
func buildGameTemplate(name string) *gameplanev1alpha1.GameTemplate {
	return &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			DisplayName: "Test " + name,
			Game:        name,
			Version:     "test",
			Image:       "ghcr.io/test/" + name + ":latest",
			Ports: []gameplanev1alpha1.GamePort{{
				Name:          "game",
				ContainerPort: 25565,
				Protocol:      corev1.ProtocolTCP,
				Advertise:     true,
			}},
			Storage: gameplanev1alpha1.GameStorageSpec{
				MountPath: "/data",
			},
		},
	}
}

// buildBackupSchedule builds a BackupSchedule with the given cron and
// retention. retention may be nil to disable trimming.
func buildBackupSchedule(
	ns, name, gsName, repoSecret, cron string, ret *gameplanev1alpha1.BackupRetention,
) *gameplanev1alpha1.BackupSchedule {
	return &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: gsName},
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: repoSecret, Key: "url"},
			Schedule:  cron,
			Retention: ret,
		},
	}
}

// seedAgentCA generates a real RSA CA in-memory and creates a Secret
// holding ca.crt + ca.key in ns. The GameServer reconciler reads this
// Secret to sign per-server agent server certs. Cheap to call (~ms);
// the CA stays scoped to the test's namespace so cleanup is automatic
// when newNamespace's deferred Delete fires.
func seedAgentCA(t *testing.T, ns, name string) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("seedAgentCA: gen key: %v", err)
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gameplane-test-agent-ca"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("seedAgentCA: sign: %v", err)
	}
	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(caKey)})

	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"ca.crt": caCertPEM,
			"ca.key": caKeyPEM,
		},
	}
	if err := k8sClient.Create(context.Background(), sec); err != nil {
		t.Fatalf("seedAgentCA: create Secret: %v", err)
	}
}

// uniqueName produces a short, lowercase suffix suitable for K8s
// resource names — useful for cluster-scoped objects (templates) where
// concurrent test runs would collide on a fixed name.
func uniqueName(prefix string) string {
	buf := make([]byte, 3)
	_, _ = rand.Read(buf)
	return prefix + "-" + hex.EncodeToString(buf)
}

// deleteCleanup registers an unconditional Delete on cleanup. Useful
// for cluster-scoped objects (GameTemplate) since namespace deletion
// won't reap them.
func deleteCleanup(t *testing.T, obj client.Object) {
	t.Helper()
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), obj)
	})
}

// ---------------------------------------------------------------------
// Status mutation helpers
//
// envtest does not run the kube workload controllers, so a Job created
// by a reconciler stays in zero-status forever unless we mutate it
// here to simulate the kubelet/Job controller.
// ---------------------------------------------------------------------

// patchJobStatus fetches the named Job, runs mut on its Status, and
// posts back via the status subresource. mut should set Active /
// Succeeded / Failed / StartTime / CompletionTime as needed.
//
// envtest's apiserver enforces Kubernetes 1.36's strengthened Job status
// transition validation (gated by the JobManagedBy feature, enabled by
// default): a Job whose status flips into a terminal state in a single
// update must carry the condition pairs the real Job controller always
// emits together, and any finished Job must have a StartTime. Faked
// fixtures here only set the counters/timestamps a reconciler cares
// about, so this backfills the rest to keep the status subresource
// Update from being rejected:
//
//   - Succeeded > 0 (job_controller.go's newSuccessCondition /
//     enactJobFinished always append SuccessCriteriaMet=True before
//     transforming it into Complete=True) requires both
//     Complete=True and SuccessCriteriaMet=True, plus StartTime and
//     CompletionTime.
//   - Failed > 0 (mirroring newFailureCondition /
//     newFailedConditionForFailureTarget, which always transitions
//     FailureTarget=True into Failed=True) requires both Failed=True
//     and FailureTarget=True, plus StartTime. The real controller
//     never sets CompletionTime for a failed Job, so this doesn't
//     either.
//
// Every current caller sets Succeeded/Failed only when the fixture is
// genuinely terminal (none fake a still-retrying Failed>0 alongside
// Active>0), so gating on the counters alone is safe here.
func patchJobStatus(t *testing.T, ns, name string, mut func(s *batchv1.JobStatus)) {
	t.Helper()
	var job batchv1.Job
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &job); err != nil {
		t.Fatalf("get job %s/%s: %v", ns, name, err)
	}
	mut(&job.Status)

	now := metav1.Now()

	switch {
	case job.Status.Succeeded > 0:
		if job.Status.CompletionTime == nil {
			job.Status.CompletionTime = &now
		}
		if job.Status.StartTime == nil {
			job.Status.StartTime = job.Status.CompletionTime
		}
		job.Status.Conditions = upsertJobCondition(job.Status.Conditions, batchv1.JobSuccessCriteriaMet)
		job.Status.Conditions = upsertJobCondition(job.Status.Conditions, batchv1.JobComplete)
	case job.Status.Failed > 0:
		if job.Status.StartTime == nil {
			job.Status.StartTime = &now
		}
		job.Status.Conditions = upsertJobCondition(job.Status.Conditions, batchv1.JobFailureTarget)
		job.Status.Conditions = upsertJobCondition(job.Status.Conditions, batchv1.JobFailed)
	}

	if err := k8sClient.Status().Update(context.Background(), &job); err != nil {
		t.Fatalf("status update job %s/%s: %v", ns, name, err)
	}
}

// upsertJobCondition sets/replaces the JobCondition of type t to
// Status: True in conds, returning the updated slice. Unlike
// upsertCondition (helpers.go), which operates on the CRD condition
// type metav1.Condition, this operates on batchv1.JobCondition — a
// distinct type with different fields (LastProbeTime/LastTransitionTime
// instead of ObservedGeneration, and corev1.ConditionStatus instead of
// metav1.ConditionStatus) used by the Job status subresource.
func upsertJobCondition(conds []batchv1.JobCondition, t batchv1.JobConditionType) []batchv1.JobCondition {
	now := metav1.Now()
	for i := range conds {
		if conds[i].Type == t {
			conds[i].Status = corev1.ConditionTrue
			conds[i].LastTransitionTime = now
			return conds
		}
	}
	return append(conds, batchv1.JobCondition{
		Type:               t,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: now,
	})
}

// setDeploymentReady fakes a Deployment's status in envtest, which runs no
// Deployment controller. Setting only ReadyReplicas while Status.Replicas
// stays at its zero default makes the apiserver reject the status update
// outright — status.readyReplicas cannot exceed status.replicas — so this
// sets all four counters consistently, matching what the real Deployment
// controller reports for a fully rolled-out, fully available Deployment.
// Uses RetryOnConflict like setSSStatus: a live reconciler may be writing
// the same object concurrently under startMgr.
func setDeploymentReady(t *testing.T, ns, name string, ready int32) {
	t.Helper()
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var dep appsv1.Deployment
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &dep); err != nil {
			return err
		}
		dep.Status.Replicas = ready
		dep.Status.ReadyReplicas = ready
		dep.Status.AvailableReplicas = ready
		dep.Status.UpdatedReplicas = ready
		return k8sClient.Status().Update(context.Background(), &dep)
	}); err != nil {
		t.Fatalf("set deployment %s ready=%d: %v", name, ready, err)
	}
}

// markBackupSucceeded sets a Backup's status to Succeeded with the
// given snapshot ID and size — the state restore tests depend on.
func markBackupSucceeded(t *testing.T, ns, name, snapshotID, size string) {
	t.Helper()
	var b gameplanev1alpha1.Backup
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	now := metav1.Now()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	b.Status.SnapshotID = snapshotID
	if size != "" {
		q := resource.MustParse(size)
		b.Status.Size = &q
	}
	if b.Status.StartTime == nil {
		b.Status.StartTime = &now
	}
	b.Status.CompletionTime = &now
	if err := k8sClient.Status().Update(context.Background(), &b); err != nil {
		t.Fatalf("status update backup: %v", err)
	}
}

// markGameServerPhase sets a GameServer's status.phase. The Restore
// reconciler waits for Suspended/Stopped before advancing to Running.
func markGameServerPhase(t *testing.T, ns, name string, phase gameplanev1alpha1.GameServerPhase) {
	t.Helper()
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	gs.Status.Phase = phase
	if err := k8sClient.Status().Update(context.Background(), &gs); err != nil {
		t.Fatalf("status update gameserver: %v", err)
	}
}

// ---------------------------------------------------------------------
// VolumeSnapshot helpers (volume-snapshot backup/restore strategy)
//
// envtest runs no CSI driver, so a created VolumeSnapshot never becomes
// readyToUse on its own — tests drive its status here, the same way
// patchJobStatus simulates the Job controller.
// ---------------------------------------------------------------------

// buildVolumeSnapshotBackup builds a Backup using the volume-snapshot
// strategy (no restic repo needed).
func buildVolumeSnapshotBackup(ns, name, gsName string) *gameplanev1alpha1.Backup {
	return &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: gsName},
			Strategy:  "volume-snapshot",
		},
	}
}

// getVolumeSnapshot returns (vs, true) when present, (nil, false) on NotFound.
func getVolumeSnapshot(t *testing.T, ns, name string) (*snapshotv1.VolumeSnapshot, bool) {
	t.Helper()
	var vs snapshotv1.VolumeSnapshot
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &vs)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get volumesnapshot %s/%s: %v", ns, name, err)
	}
	return &vs, true
}

// markVolumeSnapshotReady simulates the CSI snapshotter completing a
// VolumeSnapshot: readyToUse=true with a bound content name + restoreSize.
func markVolumeSnapshotReady(t *testing.T, ns, name, contentName, restoreSize string) {
	t.Helper()
	var vs snapshotv1.VolumeSnapshot
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &vs); err != nil {
		t.Fatalf("get volumesnapshot: %v", err)
	}
	ready := true
	now := metav1.Now()
	q := resource.MustParse(restoreSize)
	vs.Status = &snapshotv1.VolumeSnapshotStatus{
		ReadyToUse:                     &ready,
		BoundVolumeSnapshotContentName: &contentName,
		CreationTime:                   &now,
		RestoreSize:                    &q,
	}
	if err := k8sClient.Status().Update(context.Background(), &vs); err != nil {
		t.Fatalf("status update volumesnapshot: %v", err)
	}
}

// markVolumeSnapshotError simulates the CSI snapshotter rejecting a snapshot.
func markVolumeSnapshotError(t *testing.T, ns, name, msg string) {
	t.Helper()
	var vs snapshotv1.VolumeSnapshot
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &vs); err != nil {
		t.Fatalf("get volumesnapshot: %v", err)
	}
	now := metav1.Now()
	m := msg
	vs.Status = &snapshotv1.VolumeSnapshotStatus{
		Error: &snapshotv1.VolumeSnapshotError{Time: &now, Message: &m},
	}
	if err := k8sClient.Status().Update(context.Background(), &vs); err != nil {
		t.Fatalf("status update volumesnapshot: %v", err)
	}
}

// markBackupSucceededVolumeSnapshot sets a Backup to Succeeded as the
// volume-snapshot path would — snapshotID + bound content name. Restore
// reads the content name to confirm the snapshot actually bound.
func markBackupSucceededVolumeSnapshot(t *testing.T, ns, name, snapshotID, contentName string) {
	t.Helper()
	var b gameplanev1alpha1.Backup
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	now := metav1.Now()
	b.Status.Phase = gameplanev1alpha1.BackupPhaseSucceeded
	b.Status.SnapshotID = snapshotID
	b.Status.VolumeSnapshotContentName = contentName
	if b.Status.StartTime == nil {
		b.Status.StartTime = &now
	}
	b.Status.CompletionTime = &now
	if err := k8sClient.Status().Update(context.Background(), &b); err != nil {
		t.Fatalf("status update backup: %v", err)
	}
}

// ---------------------------------------------------------------------
// Read convenience wrappers
// ---------------------------------------------------------------------

func getBackup(t *testing.T, ns, name string) *gameplanev1alpha1.Backup {
	t.Helper()
	var b gameplanev1alpha1.Backup
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &b); err != nil {
		t.Fatalf("get backup: %v", err)
	}
	return &b
}

func getRestore(t *testing.T, ns, name string) *gameplanev1alpha1.Restore {
	t.Helper()
	var r gameplanev1alpha1.Restore
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &r); err != nil {
		t.Fatalf("get restore: %v", err)
	}
	return &r
}

// getTemplateByName fetches a cluster-scoped GameTemplate.
func getTemplateByName(t *testing.T, name string) *gameplanev1alpha1.GameTemplate {
	t.Helper()
	var tmpl gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &tmpl); err != nil {
		t.Fatalf("get template: %v", err)
	}
	return &tmpl
}

func getGameServer(t *testing.T, ns, name string) *gameplanev1alpha1.GameServer {
	t.Helper()
	var gs gameplanev1alpha1.GameServer
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &gs); err != nil {
		t.Fatalf("get gameserver: %v", err)
	}
	return &gs
}

// getJob returns (job, true) when present, (nil, false) when NotFound,
// t.Fatal on any other error.
func getJob(t *testing.T, ns, name string) (*batchv1.Job, bool) {
	t.Helper()
	var job batchv1.Job
	err := k8sClient.Get(context.Background(), types.NamespacedName{Namespace: ns, Name: name}, &job)
	if apierrors.IsNotFound(err) {
		return nil, false
	}
	if err != nil {
		t.Fatalf("get job: %v", err)
	}
	return &job, true
}

// ---------------------------------------------------------------------
// Debug helpers
// ---------------------------------------------------------------------

func describeBackupStatus(b *gameplanev1alpha1.Backup) string {
	return fmt.Sprintf("phase=%q snapshotID=%q size=%v message=%q",
		b.Status.Phase, b.Status.SnapshotID, b.Status.Size, b.Status.Message)
}

func describeRestoreStatus(r *gameplanev1alpha1.Restore) string {
	return fmt.Sprintf("phase=%q snapshotID=%q message=%q",
		r.Status.Phase, r.Status.SnapshotID, r.Status.Message)
}

// ---------------------------------------------------------------------
// Fakes for the BackupReconciler's external dependencies
// ---------------------------------------------------------------------

// fakeAgent implements AgentQuiescer for tests. Counts each call and
// optionally returns a stubbed error per method. Safe for concurrent
// use — the reconciler's workqueue may dispatch in parallel with the
// test's eventually-poll loop.
type fakeAgent struct {
	mu                       sync.Mutex
	quiesceErr, unquiesceErr error
	nQuiesce, nUnquiesce     int
}

func (f *fakeAgent) Quiesce(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nQuiesce++
	return f.quiesceErr
}

func (f *fakeAgent) Unquiesce(_ context.Context, _, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nUnquiesce++
	return f.unquiesceErr
}

func (f *fakeAgent) quiesceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nQuiesce
}

func (f *fakeAgent) unquiesceCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.nUnquiesce
}

// fakeLogReader returns a fixed body when BackupLogs is called. Each
// call yields a fresh ReadCloser so the reconciler can safely close
// it on every requeue.
type fakeLogReader struct {
	body string
}

func (f *fakeLogReader) BackupLogs(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(f.body)), nil
}

// seedGameServer creates a bare GameServer so reconcilers that resolve
// Backup/Restore serverRefs find their target. No GameServer reconciler
// runs in these suites, so the object stays inert (no children).
func seedGameServer(t *testing.T, ns, name string) {
	t.Helper()
	gs := buildGameServer(ns, name, "seed-template")
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("seed gameserver %s/%s: %v", ns, name, err)
	}
}
