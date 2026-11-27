package controller

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const (
	// exportDefaultTTL bounds an export whose spec.ttlSeconds is unset and
	// whose reconciler has no DefaultTTL.
	exportDefaultTTL = time.Hour
	// exportCertSlack keeps the worker certificate valid past the Pod's
	// active deadline so a transfer that ends at the TTL is not cut by the
	// certificate.
	exportCertSlack = time.Hour
	// exportRunAsID is the non-root UID and GID the worker runs as.
	exportRunAsID = int64(65532)
	// exportWorkerMode is the archive-worker mode for downloads.
	exportWorkerMode = "export"
	// exportWorkerContainer is the worker container name built by
	// buildArchiveWorkerPod.
	exportWorkerContainer = "worker"
	// exportManifestFormatVersion is the archive format version written to
	// gameplane-backup.json (spec 041 R3).
	exportManifestFormatVersion = 1
	// backupExportClusterAnnotation is set by the API on each BackupExport to
	// the cluster name the download was requested against. It is copied into
	// the manifest's cluster field. Must match kube.BackupExportClusterAnnotation
	// in api/internal/kube.
	backupExportClusterAnnotation = "gameplane.io/cluster-name"
)

// Mutating RBAC for Pods, Services and Secrets comes from the namespace-scoped
// Roles, not from ClusterRole markers, so no marker grants those verbs here.
// +kubebuilder:rbac:groups=gameplane.local,resources=backupexports,verbs=get;list;watch;update;patch;delete
// +kubebuilder:rbac:groups=gameplane.local,resources=backupexports/status,verbs=get;update;patch

// BackupExportReconciler runs one archive export per BackupExport (spec 041,
// R2). A worker Pod streams a restic dump as .tar.gz or .zip over mTLS behind
// a ClusterIP Service whose certificate is signed by the agent CA. The API or
// cluster gateway dials the Service and deletes the BackupExport when the
// transfer ends. Business logic stays here; the API only creates and deletes
// the resource.
type BackupExportReconciler struct {
	client.Client
	// APIReader bypasses the informer cache for Backup and repository Secret
	// reads. Nil falls back to Client.
	APIReader client.Reader
	Scheme    *runtime.Scheme
	// AgentImage runs the `archive-worker` subcommand.
	AgentImage           string
	AgentImagePullPolicy corev1.PullPolicy
	// ResticImage provides the restic binary copied into the worker. Empty
	// falls back to DefaultResticImage.
	ResticImage string
	// BackupResources shares the backup worker resource configuration.
	BackupResources *corev1.ResourceRequirements
	// AgentCASecretName and AgentCASecretNamespace locate the agent CA Secret
	// (ca.crt and ca.key) that signs the worker certificate.
	AgentCASecretName      string
	AgentCASecretNamespace string
	// DefaultTTL bounds exports whose spec.ttlSeconds is unset. Zero means one hour.
	DefaultTTL time.Duration
	// WriterVersion is the operator version recorded as "gameplane <version>"
	// in the archive manifest.
	WriterVersion string
	// Now is the clock. Nil means time.Now.
	Now func() time.Time
}

// Reconcile drives one BackupExport through validation, worker creation and
// phase tracking.
func (r *BackupExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	return requeueOnConflict(r.reconcile(ctx, req))
}

func (r *BackupExportReconciler) reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var export gameplanev1alpha1.BackupExport
	if err := r.Get(ctx, req.NamespacedName, &export); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Owned children are garbage collected with the BackupExport.
	if !export.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	now := r.now()
	ttl := exportTTL(&export, r.DefaultTTL)
	expires := export.CreationTimestamp.Add(ttl)

	if exportIsTerminal(export.Status.Phase) {
		return ctrl.Result{}, r.deleteExportWorker(ctx, &export)
	}

	if !now.Before(expires) {
		if err := r.deleteExportWorker(ctx, &export); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, r.updateExportStatus(ctx, &export, func(s *gameplanev1alpha1.BackupExportStatus) {
			s.Phase = gameplanev1alpha1.BackupExportPhaseExpired
			s.Message = "download expired"
			setTimeIfNil(&s.CompletionTime, now)
		})
	}

	// The source Backup is checked on every pass, not only while Pending: the
	// worker's manifest and Secret references depend on it, and a Backup that
	// is deleted or replaced mid-download must end the export with an error.
	backup, reason, err := r.validateExport(ctx, &export)
	if err != nil {
		return ctrl.Result{}, err
	}
	if reason != "" {
		return ctrl.Result{}, r.failExport(ctx, &export, reason, now)
	}

	backup, err = r.withServerTemplate(ctx, backup)
	if err != nil {
		return ctrl.Result{}, err
	}
	manifest, err := buildExportManifest(backup, r.WriterVersion, export.Annotations[backupExportClusterAnnotation])
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("build archive manifest for %s/%s: %w", export.Namespace, export.Name, err)
	}

	if err := r.ensureExportWorker(ctx, &export, backup, ttl, manifest); err != nil {
		return ctrl.Result{}, err
	}

	var pod *corev1.Pod
	var got corev1.Pod
	switch err := r.Get(ctx, types.NamespacedName{Namespace: export.Namespace, Name: export.Name}, &got); {
	case err == nil:
		pod = &got
	case !apierrors.IsNotFound(err):
		return ctrl.Result{}, fmt.Errorf("get export worker Pod %s/%s: %w", export.Namespace, export.Name, err)
	}
	obs := observeExportPod(pod)

	if err := r.updateExportStatus(ctx, &export, func(s *gameplanev1alpha1.BackupExportStatus) {
		applyExportObservation(s, obs, export.Name, backup.Status.SnapshotID, now)
	}); err != nil {
		return ctrl.Result{}, err
	}

	if exportIsTerminal(export.Status.Phase) {
		return ctrl.Result{}, r.deleteExportWorker(ctx, &export)
	}
	return ctrl.Result{RequeueAfter: expires.Sub(now)}, nil
}

// now returns the reconciler's clock.
func (r *BackupExportReconciler) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// apiReader returns the uncached reader, falling back to the cached Client.
func (r *BackupExportReconciler) apiReader() client.Reader {
	if r.APIReader != nil {
		return r.APIReader
	}
	return r.Client
}

// exportTTL resolves the export's lifetime: spec.ttlSeconds, then def, then
// one hour.
func exportTTL(export *gameplanev1alpha1.BackupExport, def time.Duration) time.Duration {
	if export.Spec.TTLSeconds != nil && *export.Spec.TTLSeconds > 0 {
		return time.Duration(*export.Spec.TTLSeconds) * time.Second
	}
	if def > 0 {
		return def
	}
	return exportDefaultTTL
}

// exportIsTerminal reports whether a phase is final: the worker is gone and
// nothing else changes.
func exportIsTerminal(p gameplanev1alpha1.BackupExportPhase) bool {
	return p == gameplanev1alpha1.BackupExportPhaseCompleted ||
		p == gameplanev1alpha1.BackupExportPhaseFailed ||
		p == gameplanev1alpha1.BackupExportPhaseExpired
}

// validateExport loads the source Backup and its repository Secret. It returns
// a non-empty reason when the export cannot run, and an error only for
// lookups that failed for reasons other than not found.
func (r *BackupExportReconciler) validateExport(
	ctx context.Context, export *gameplanev1alpha1.BackupExport,
) (*gameplanev1alpha1.Backup, string, error) {
	var backup gameplanev1alpha1.Backup
	err := r.apiReader().Get(ctx, types.NamespacedName{
		Namespace: export.Namespace, Name: export.Spec.BackupRef.Name,
	}, &backup)
	if apierrors.IsNotFound(err) {
		return nil, "backup not found", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get backup %s/%s: %w", export.Namespace, export.Spec.BackupRef.Name, err)
	}
	if reason := exportBackupProblem(export, &backup); reason != "" {
		return nil, reason, nil
	}

	repoName := backup.Spec.RepoRef.Name
	var repo corev1.Secret
	err = r.apiReader().Get(ctx, types.NamespacedName{Namespace: export.Namespace, Name: repoName}, &repo)
	if apierrors.IsNotFound(err) {
		return nil, fmt.Sprintf("repository secret %s not found", repoName), nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("get repository secret %s/%s: %w", export.Namespace, repoName, err)
	}
	if missing := missingRepoSecretKeys(&repo); len(missing) > 0 {
		return nil, fmt.Sprintf("repository secret %s is missing keys: %s", repoName, strings.Join(missing, ", ")), nil
	}
	return &backup, "", nil
}

// withServerTemplate fills the archive's template and game version from the
// Backup's GameServer when the capture manifest does not record them, as for
// backups that are not all-data captures, so an upload of the archive can be
// matched to its game. It returns backup unchanged when the manifest already
// names a template, when there is no manifest, or when the server is gone or is
// not the one the backup captured.
func (r *BackupExportReconciler) withServerTemplate(
	ctx context.Context, backup *gameplanev1alpha1.Backup,
) (*gameplanev1alpha1.Backup, error) {
	cm := backup.Status.CaptureManifest
	if cm == nil || cm.TemplateName != "" {
		return backup, nil
	}
	var gs gameplanev1alpha1.GameServer
	err := r.apiReader().Get(ctx, types.NamespacedName{Namespace: backup.Namespace, Name: backup.Spec.ServerRef.Name}, &gs)
	if apierrors.IsNotFound(err) {
		return backup, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get game server %s/%s: %w", backup.Namespace, backup.Spec.ServerRef.Name, err)
	}
	if cm.ServerUID != "" && string(gs.UID) != cm.ServerUID {
		return backup, nil
	}
	out := backup.DeepCopy()
	m := out.Status.CaptureManifest
	m.TemplateName = gs.Spec.TemplateRef.Name
	if m.TemplateRevision == "" && gs.Status.TemplateRevision != nil {
		m.TemplateRevision = gs.Status.TemplateRevision.Effective
	}
	if m.GameVersion == "" {
		m.GameVersion = gs.Spec.Version
	}
	return out, nil
}

// exportBackupProblem returns why the Backup cannot be exported, or "" when it
// can. It does not read the cluster.
func exportBackupProblem(export *gameplanev1alpha1.BackupExport, backup *gameplanev1alpha1.Backup) string {
	switch {
	case string(backup.UID) != export.Spec.BackupUID:
		return "backup was replaced"
	case backup.Status.Phase != gameplanev1alpha1.BackupPhaseSucceeded:
		return "backup has not finished"
	case backup.Spec.Strategy == "volume-snapshot":
		return "snapshot backups cannot be downloaded"
	case backup.Status.SnapshotID == "" || backup.Spec.RepoRef == nil:
		return "backup has no restic snapshot"
	}
	return ""
}

// ensureExportWorker creates the TLS Secret, Service and Pod that the export
// needs, skipping any that already exist.
func (r *BackupExportReconciler) ensureExportWorker(
	ctx context.Context, export *gameplanev1alpha1.BackupExport, backup *gameplanev1alpha1.Backup,
	ttl time.Duration, manifest []byte,
) error {
	if err := r.ensureExportTLS(ctx, export, ttl); err != nil {
		return err
	}
	svc := buildArchiveWorkerService(export.Name, export.Namespace, exportWorkerMode, string(export.UID))
	if err := r.ensureOwned(ctx, export, svc); err != nil {
		return err
	}
	return r.ensureOwned(ctx, export, r.buildExportPod(export, backup, ttl, manifest))
}

// ensureExportTLS creates the worker's server certificate Secret. The key is
// generated only when the Secret is missing, so an existing one is never
// rotated mid-transfer.
func (r *BackupExportReconciler) ensureExportTLS(
	ctx context.Context, export *gameplanev1alpha1.BackupExport, ttl time.Duration,
) error {
	var existing corev1.Secret
	err := r.Get(ctx, types.NamespacedName{Namespace: export.Namespace, Name: export.Name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get export TLS Secret %s/%s: %w", export.Namespace, export.Name, err)
	}

	caCert, caKey, caPEM, err := r.loadAgentCA(ctx)
	if err != nil {
		return err
	}
	certPEM, keyPEM, err := signServerCert(caCert, caKey, export.Name,
		workerDNSNames(export.Name, export.Namespace), ttl+exportCertSlack)
	if err != nil {
		return fmt.Errorf("sign export certificate for %s/%s: %w", export.Namespace, export.Name, err)
	}
	sec := buildArchiveWorkerTLSSecret(export.Name, export.Namespace, certPEM, keyPEM, caPEM)
	return r.ensureOwned(ctx, export, sec)
}

// loadAgentCA reads the agent CA Secret and parses it.
func (r *BackupExportReconciler) loadAgentCA(ctx context.Context) (*x509.Certificate, *rsa.PrivateKey, []byte, error) {
	if r.AgentCASecretName == "" || r.AgentCASecretNamespace == "" {
		return nil, nil, nil, errors.New("agent CA Secret reference not configured (set --agent-ca-secret-name and --agent-ca-secret-namespace)")
	}
	var caSecret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{
		Namespace: r.AgentCASecretNamespace, Name: r.AgentCASecretName,
	}, &caSecret); err != nil {
		return nil, nil, nil, fmt.Errorf("get agent CA Secret %s/%s: %w", r.AgentCASecretNamespace, r.AgentCASecretName, err)
	}
	caPEM, ok := caSecret.Data["ca.crt"]
	if !ok {
		return nil, nil, nil, fmt.Errorf("agent CA Secret %s/%s missing ca.crt", r.AgentCASecretNamespace, r.AgentCASecretName)
	}
	keyPEM, ok := caSecret.Data["ca.key"]
	if !ok {
		return nil, nil, nil, fmt.Errorf("agent CA Secret %s/%s missing ca.key", r.AgentCASecretNamespace, r.AgentCASecretName)
	}
	caCert, caKey, err := parseAgentCA(caPEM, keyPEM)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("parse agent CA: %w", err)
	}
	return caCert, caKey, caPEM, nil
}

// buildExportPod returns the export worker Pod for backup. Its environment
// carries the repository credentials, the snapshot, the archive format and the
// manifest the worker writes as the first archive entry.
func (r *BackupExportReconciler) buildExportPod(
	export *gameplanev1alpha1.BackupExport, backup *gameplanev1alpha1.Backup, ttl time.Duration, manifest []byte,
) *corev1.Pod {
	nonRoot := true
	pod := buildArchiveWorkerPod(archiveWorkerParams{
		Name:                 export.Name,
		Namespace:            export.Namespace,
		Mode:                 exportWorkerMode,
		UID:                  string(export.UID),
		AgentImage:           r.AgentImage,
		AgentImagePullPolicy: r.AgentImagePullPolicy,
		ResticImage:          r.ResticImage,
		Resources:            auxiliaryResources(r.BackupResources, "100m", "128Mi"),
		PodSecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: &nonRoot,
			RunAsUser:    ptrTo(exportRunAsID),
			RunAsGroup:   ptrTo(exportRunAsID),
			FSGroup:      ptrTo(exportRunAsID),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Env: []corev1.EnvVar{
			{Name: "RESTIC_REPOSITORY", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: backup.Spec.RepoRef.Name},
					Key:                  "repo",
				},
			}},
			{Name: "RESTIC_PASSWORD", ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: backup.Spec.RepoRef.Name},
					Key:                  "password",
				},
			}},
			{Name: "ARCHIVE_SNAPSHOT_ID", Value: backup.Status.SnapshotID},
			{Name: "ARCHIVE_FORMAT", Value: export.Spec.Format},
			{Name: "ARCHIVE_MANIFEST", Value: string(manifest)},
		},
		NeedsRestic: true,
		Serve:       true,
	})
	pod.Spec.ActiveDeadlineSeconds = ptrTo(int64(ttl / time.Second))
	return pod
}

// ensureOwned creates obj with a controller reference to owner unless an
// object with the same name already exists.
func (r *BackupExportReconciler) ensureOwned(
	ctx context.Context, owner *gameplanev1alpha1.BackupExport, obj client.Object,
) error {
	kind := fmt.Sprintf("%T", obj)
	key := client.ObjectKeyFromObject(obj)
	existing, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("deep copy of %s is not a client.Object", kind)
	}
	err := r.Get(ctx, key, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get %s %s: %w", kind, key, err)
	}
	if err := controllerutil.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return fmt.Errorf("set owner of %s %s: %w", kind, key, err)
	}
	if err := r.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create %s %s: %w", kind, key, err)
	}
	return nil
}

// deleteExportWorker removes the Pod and Service this export owns. It
// leaves objects it does not control alone and ignores ones already gone.
func (r *BackupExportReconciler) deleteExportWorker(ctx context.Context, export *gameplanev1alpha1.BackupExport) error {
	for _, obj := range []client.Object{&corev1.Pod{}, &corev1.Service{}} {
		obj.SetName(export.Name)
		obj.SetNamespace(export.Namespace)
		if err := r.apiReader().Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("get export worker %T %s/%s: %w", obj, export.Namespace, export.Name, err)
		}
		if !metav1.IsControlledBy(obj, export) {
			continue
		}
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("delete export worker %T %s/%s: %w", obj, export.Namespace, export.Name, err)
		}
	}
	return nil
}

// failExport removes the worker and records reason as the terminal Failed
// state.
func (r *BackupExportReconciler) failExport(
	ctx context.Context, export *gameplanev1alpha1.BackupExport, reason string, now time.Time,
) error {
	if err := r.deleteExportWorker(ctx, export); err != nil {
		return err
	}
	return r.updateExportStatus(ctx, export, func(s *gameplanev1alpha1.BackupExportStatus) {
		s.Phase = gameplanev1alpha1.BackupExportPhaseFailed
		s.Message = reason
		setTimeIfNil(&s.CompletionTime, now)
	})
}

// updateExportStatus applies mutate to the export's status and writes it only
// when the status changed.
func (r *BackupExportReconciler) updateExportStatus(
	ctx context.Context, export *gameplanev1alpha1.BackupExport, mutate func(*gameplanev1alpha1.BackupExportStatus),
) error {
	before := export.Status.DeepCopy()
	mutate(&export.Status)
	if apiequality.Semantic.DeepEqual(*before, export.Status) {
		return nil
	}
	if err := r.Status().Update(ctx, export); err != nil {
		return fmt.Errorf("update BackupExport %s/%s status: %w", export.Namespace, export.Name, err)
	}
	return nil
}

// SetupWithManager registers the reconciler. It watches its worker Pods.
func (r *BackupExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gameplanev1alpha1.BackupExport{}).
		Owns(&corev1.Pod{}).
		Complete(r)
}

// exportManifest is the archive's gameplane-backup.json (spec 041 data model).
type exportManifest struct {
	FormatVersion        int                    `json:"formatVersion"`
	Writer               string                 `json:"writer"`
	Namespace            string                 `json:"namespace"`
	Cluster              string                 `json:"cluster,omitempty"`
	Server               exportManifestServer   `json:"server"`
	Template             exportManifestTemplate `json:"template"`
	GameVersion          string                 `json:"gameVersion"`
	CapturedAt           string                 `json:"capturedAt"`
	ResticSnapshotID     string                 `json:"resticSnapshotID"`
	CaptureFormatVersion int32                  `json:"captureFormatVersion"`
	Volumes              []exportManifestVolume `json:"volumes"`
}

type exportManifestServer struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type exportManifestTemplate struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

type exportManifestVolume struct {
	LogicalName    string `json:"logicalName"`
	Folder         string `json:"folder"`
	Classification string `json:"classification"`
}

// buildExportManifest returns the JSON archive manifest for backup. writer is
// the operator version; the manifest records it as "gameplane <writer>".
// Capture manifest format 2 and later list each included volume under
// volumes/<logicalName>. Older or missing capture manifests get the single
// legacy "data" entry. cluster is the cluster name the export was requested
// against; an empty cluster leaves the manifest field out.
func buildExportManifest(backup *gameplanev1alpha1.Backup, writer, cluster string) ([]byte, error) {
	m := exportManifest{
		FormatVersion:    exportManifestFormatVersion,
		Writer:           "gameplane " + writer,
		Namespace:        backup.Namespace,
		Cluster:          cluster,
		Server:           exportManifestServer{Name: backup.Spec.ServerRef.Name},
		ResticSnapshotID: backup.Status.SnapshotID,
		Volumes:          exportVolumes(backup.Status.CaptureManifest),
	}
	if backup.Status.CompletionTime != nil {
		m.CapturedAt = backup.Status.CompletionTime.UTC().Format(time.RFC3339)
	}
	if cm := backup.Status.CaptureManifest; cm != nil {
		m.Server.UID = cm.ServerUID
		m.Template = exportManifestTemplate{Name: cm.TemplateName, Revision: cm.TemplateRevision}
		m.GameVersion = cm.GameVersion
		m.CaptureFormatVersion = cm.FormatVersion
	}
	return json.Marshal(m)
}

// exportVolumes maps a capture manifest to archive folders.
func exportVolumes(cm *gameplanev1alpha1.BackupCaptureManifest) []exportManifestVolume {
	if cm == nil || cm.FormatVersion < 2 {
		return []exportManifestVolume{{LogicalName: "data", Folder: "data", Classification: "data"}}
	}
	out := []exportManifestVolume{}
	for _, v := range cm.Volumes {
		if !v.Included {
			continue
		}
		out = append(out, exportManifestVolume{
			LogicalName:    v.LogicalName,
			Folder:         "volumes/" + v.LogicalName,
			Classification: v.Classification,
		})
	}
	return out
}

// exportWorkerReport is the JSON the worker writes to its termination message.
type exportWorkerReport struct {
	BytesSent *int64 `json:"bytesSent,omitempty"`
	Error     string `json:"error,omitempty"`
}

// parseExportWorkerReport decodes a termination message. A message that is not
// JSON is taken as the error text.
func parseExportWorkerReport(message string) exportWorkerReport {
	var rep exportWorkerReport
	if err := json.Unmarshal([]byte(message), &rep); err != nil {
		return exportWorkerReport{Error: strings.TrimSpace(message)}
	}
	return rep
}

// exportObservation is the phase the worker Pod implies.
type exportObservation struct {
	Phase     gameplanev1alpha1.BackupExportPhase
	Message   string
	BytesSent *int64
}

// observeExportPod maps a worker Pod to an export phase. A nil Pod, or one
// that is not yet ready, is Pending.
func observeExportPod(pod *corev1.Pod) exportObservation {
	if pod == nil {
		return exportObservation{Phase: gameplanev1alpha1.BackupExportPhasePending}
	}
	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		rep := exportTerminationReport(pod)
		return exportObservation{Phase: gameplanev1alpha1.BackupExportPhaseCompleted, BytesSent: rep.BytesSent}
	case corev1.PodFailed:
		msg := exportTerminationReport(pod).Error
		if msg == "" {
			msg = exportTerminationReason(pod)
		}
		if msg == "" {
			msg = "export worker failed"
		}
		return exportObservation{Phase: gameplanev1alpha1.BackupExportPhaseFailed, Message: msg}
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue {
			return exportObservation{Phase: gameplanev1alpha1.BackupExportPhaseReady}
		}
	}
	return exportObservation{Phase: gameplanev1alpha1.BackupExportPhasePending}
}

// exportTerminationReport decodes the worker container's termination message.
func exportTerminationReport(pod *corev1.Pod) exportWorkerReport {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == exportWorkerContainer && cs.State.Terminated != nil {
			return parseExportWorkerReport(cs.State.Terminated.Message)
		}
	}
	return exportWorkerReport{}
}

// exportTerminationReason returns the worker's termination reason, or the
// Pod's.
func exportTerminationReason(pod *corev1.Pod) string {
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.Name == exportWorkerContainer && cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason
		}
	}
	return pod.Status.Reason
}

// applyExportObservation writes the observed phase into s. Terminal phases
// keep their first completion time.
func applyExportObservation(
	s *gameplanev1alpha1.BackupExportStatus, obs exportObservation, serviceName, snapshotID string, now time.Time,
) {
	s.ServiceName = serviceName
	s.SnapshotID = snapshotID
	switch obs.Phase {
	case gameplanev1alpha1.BackupExportPhaseCompleted:
		s.Phase = gameplanev1alpha1.BackupExportPhaseCompleted
		s.Message = ""
		setTimeIfNil(&s.CompletionTime, now)
		if obs.BytesSent != nil {
			s.BytesSent = resource.NewQuantity(*obs.BytesSent, resource.DecimalSI)
		}
	case gameplanev1alpha1.BackupExportPhaseFailed:
		s.Phase = gameplanev1alpha1.BackupExportPhaseFailed
		s.Message = obs.Message
		setTimeIfNil(&s.CompletionTime, now)
	case gameplanev1alpha1.BackupExportPhaseReady:
		s.Phase = gameplanev1alpha1.BackupExportPhaseReady
		s.Message = ""
		setTimeIfNil(&s.StartTime, now)
	default:
		s.Phase = gameplanev1alpha1.BackupExportPhasePending
		s.Message = ""
	}
}

// setTimeIfNil stores now in *dst unless it is already set.
func setTimeIfNil(dst **metav1.Time, now time.Time) {
	if *dst == nil {
		t := metav1.NewTime(now)
		*dst = &t
	}
}
