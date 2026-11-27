package controller

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Upload restores (spec 041 R5) receive an archive onto a temporary staging
// volume, check it, wait for the user to confirm, then copy it over the target
// server's volumes. The server is fenced only after confirmation, exactly as
// a backup-based restore fences it. Failures before confirmation never touch it.
const (
	// uploadConfirmTimeout bounds how long a checked archive waits for confirmation.
	uploadConfirmTimeout = 30 * time.Minute
	// uploadPollInterval is the requeue period while a worker Pod runs.
	uploadPollInterval = 5 * time.Second
	// uploadCertValidity covers the receiver's TLS certificate.
	uploadCertValidity = 24 * time.Hour
	// uploadRunAsID is the non-root UID and GID the workers run as.
	uploadRunAsID = int64(65532)
	// uploadStagingDir is where the staging volume is mounted in the receiver and copy Pods.
	uploadStagingDir = "/staging"
	// uploadTargetMountRoot holds one mount per target volume in the copy Pod.
	uploadTargetMountRoot = "/targets/"
	// uploadStagingVolume is the volume name of the staging claim.
	uploadStagingVolume = "staging"
	// uploadWorkerContainer is the worker container name built by buildArchiveWorkerPod.
	uploadWorkerContainer = "worker"
	// uploadReceiverMode and uploadCopyMode are the archive-worker modes.
	uploadReceiverMode = "receive"
	uploadCopyMode     = "copy"
	// Object name suffixes. Every object is named after the Restore.
	uploadReceiverSuffix = "-receive"
	uploadCopySuffix     = "-copy"
	uploadStagingSuffix  = "-staging"
	// uploadStorageClassDefaultAnnotation marks the cluster default StorageClass.
	uploadStorageClassDefaultAnnotation = "storageclass.kubernetes.io/is-default-class"
	// uploadPrimaryLogicalName is the logical name the backup layout gives the game's data claim.
	uploadPrimaryLogicalName = "primary"
	// uploadPlainLogicalName is the volume a plain archive's content is placed on.
	uploadPlainLogicalName = "data"
)

var (
	// errNoStagingStorageClass is the Failed message when no staging class is configured or defaulted.
	errNoStagingStorageClass = errors.New("NoStagingStorageClass: set the Helm value backups.uploadStagingStorageClass or add a default storage class")
	// errUploadClaimMissing marks a target volume claim that does not exist.
	errUploadClaimMissing = errors.New("target volume claim not found")
)

// uploadVolume is one target volume the archive must fill.
type uploadVolume struct {
	LogicalName   string
	ClaimName     string
	VolumeName    string
	CapacityBytes int64
}

// uploadTarget is the target server with its template and volumes.
type uploadTarget struct {
	gs      *gameplanev1alpha1.GameServer
	tmpl    *gameplanev1alpha1.GameTemplate
	volumes []uploadVolume
}

// archiveTarget returns the facts FR-013 checks an archive against.
func (t uploadTarget) archiveTarget() uploadArchiveTarget {
	names := make([]string, 0, len(t.volumes))
	for _, v := range t.volumes {
		names = append(names, v.LogicalName)
	}
	return uploadArchiveTarget{
		TemplateName: t.gs.Spec.TemplateRef.Name,
		GameVersion:  t.gs.Spec.Version,
		VolumeNames:  names,
	}
}

// uploadArchiveTarget is what an archive is checked against.
type uploadArchiveTarget struct {
	TemplateName string
	GameVersion  string
	VolumeNames  []string
}

// uploadManifest is the archive's gameplane-backup.json as the receiver reports it.
type uploadManifest struct {
	FormatVersion int32                `json:"formatVersion"`
	Cluster       string               `json:"cluster"`
	Namespace     string               `json:"namespace"`
	Server        uploadManifestServer `json:"server"`
	Template      uploadManifestTmpl   `json:"template"`
	GameVersion   string               `json:"gameVersion"`
	CapturedAt    string               `json:"capturedAt"`
}

type uploadManifestServer struct {
	Name string `json:"name"`
	UID  string `json:"uid"`
}

type uploadManifestTmpl struct {
	Name     string `json:"name"`
	Revision string `json:"revision"`
}

// uploadSummary is the JSON summary the receiver writes to its termination message.
type uploadSummary struct {
	Kind                string          `json:"kind"`
	Manifest            *uploadManifest `json:"manifest,omitempty"`
	UnpackedBytes       int64           `json:"unpackedBytes"`
	Volumes             []string        `json:"volumes"`
	ZipPermissionsReset bool            `json:"zipPermissionsReset"`
	Error               string          `json:"error"`
}

// parseUploadSummary decodes a receiver termination message.
func parseUploadSummary(message string) (uploadSummary, error) {
	var s uploadSummary
	if strings.TrimSpace(message) == "" {
		return s, errors.New("receiver wrote no result")
	}
	if err := json.Unmarshal([]byte(message), &s); err != nil {
		return s, fmt.Errorf("decode receiver result: %w", err)
	}
	if s.Error == "" && s.Kind != "gameplane" && s.Kind != "plain" {
		return s, fmt.Errorf("receiver result has unknown kind %q", s.Kind)
	}
	return s, nil
}

// checkUploadArchive applies the FR-013 checks. It returns warnings and a
// non-empty reason when the archive must be refused.
func checkUploadArchive(s uploadSummary, confirmedTemplate string, target uploadArchiveTarget) ([]string, string) {
	var warnings []string
	switch s.Kind {
	case "plain":
		if confirmedTemplate != target.TemplateName {
			return nil, fmt.Sprintf("plain archive is for %s, this server runs %s", confirmedTemplate, target.TemplateName)
		}
	case "gameplane":
		if s.Manifest == nil {
			return nil, "archive manifest is missing"
		}
		if s.Manifest.Template.Name != target.TemplateName {
			return nil, fmt.Sprintf("archive is for %s, this server runs %s", s.Manifest.Template.Name, target.TemplateName)
		}
		if !legacyArchiveLayout(s.Kind, s.Volumes, target.VolumeNames) {
			present := make(map[string]bool, len(s.Volumes))
			for _, v := range s.Volumes {
				present[v] = true
			}
			for _, v := range target.VolumeNames {
				if !present[v] {
					return nil, fmt.Sprintf("archive has no volume %q, which this server uses", v)
				}
			}
		}
		if archiveGameVersionOlder(s.Manifest.GameVersion, target.GameVersion) {
			warnings = append(warnings, fmt.Sprintf("archive was made with game version %s, older than this server's %s",
				s.Manifest.GameVersion, target.GameVersion))
		}
	default:
		return nil, fmt.Sprintf("unknown archive kind %q", s.Kind)
	}
	if s.ZipPermissionsReset {
		warnings = append(warnings, "zip file permissions were reset; file modes in the archive were not preserved")
	}
	return warnings, ""
}

// legacyArchiveLayout reports whether a Gameplane archive holds only the single
// "data" folder of a capture format 1 backup. Like a plain archive, that folder
// fills the game's data claim. targetNames are the server's logical volume
// names; a server with its own volume named "data" keeps the per-volume match.
func legacyArchiveLayout(kind string, archiveVolumes, targetNames []string) bool {
	if kind != "gameplane" || len(archiveVolumes) != 1 || archiveVolumes[0] != uploadPlainLogicalName {
		return false
	}
	for _, n := range targetNames {
		if n == uploadPlainLogicalName {
			return false
		}
	}
	return true
}

// archiveGameVersionOlder reports whether the archive's game version sorts
// before the server's. Versions that are not semver-comparable never warn.
func archiveGameVersionOlder(archive, target string) bool {
	if archive == "" || target == "" {
		return false
	}
	a, b := "v"+strings.TrimPrefix(archive, "v"), "v"+strings.TrimPrefix(target, "v")
	if !semver.IsValid(a) || !semver.IsValid(b) {
		return false
	}
	return semver.Compare(a, b) < 0
}

// chooseStagingStorageClass returns the configured class, or else the
// alphabetically first cluster default class.
func chooseStagingStorageClass(configured string, classes []storagev1.StorageClass) (string, error) {
	if configured != "" {
		return configured, nil
	}
	var defaults []string
	for i := range classes {
		if classes[i].Annotations[uploadStorageClassDefaultAnnotation] == "true" {
			defaults = append(defaults, classes[i].Name)
		}
	}
	if len(defaults) == 0 {
		return "", errNoStagingStorageClass
	}
	sort.Strings(defaults)
	return defaults[0], nil
}

// uploadStagingSize sizes the staging claim for the archive plus the unpacked
// content, rounded up to whole Gi with a 1 Gi floor.
func uploadStagingSize(declaredBytes, targetBytes int64) resource.Quantity {
	const gi = int64(1) << 30
	total := declaredBytes + targetBytes
	gib := (total + gi - 1) / gi
	if gib < 1 {
		gib = 1
	}
	return *resource.NewQuantity(gib*gi, resource.BinarySI)
}

// uploadConfirmationExpired reports whether the confirmation window closed.
func uploadConfirmationExpired(receivedAt, now time.Time) bool {
	return !now.Before(receivedAt.Add(uploadConfirmTimeout))
}

// claimCapacityBytes returns the claim's provisioned size, or its request before provisioning.
func claimCapacityBytes(pvc *corev1.PersistentVolumeClaim) int64 {
	if q, ok := pvc.Status.Capacity[corev1.ResourceStorage]; ok {
		return q.Value()
	}
	if q, ok := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
		return q.Value()
	}
	return 0
}

// sumCapacity totals the target volumes' capacities.
func sumCapacity(volumes []uploadVolume) int64 {
	var total int64
	for _, v := range volumes {
		total += v.CapacityBytes
	}
	return total
}

// uploadCopyVolumes returns the volumes the copy Pod fills. A plain archive
// fills only the game's data claim, under the name "data".
func uploadCopyVolumes(kind string, volumes []uploadVolume) []uploadVolume {
	if kind != "plain" {
		return volumes
	}
	for _, v := range volumes {
		if v.LogicalName == uploadPrimaryLogicalName {
			return []uploadVolume{{LogicalName: uploadPlainLogicalName, ClaimName: v.ClaimName, VolumeName: v.VolumeName, CapacityBytes: v.CapacityBytes}}
		}
	}
	return nil
}

// parseCopyFailure extracts the worker error from a copy termination message.
func parseCopyFailure(message string) string {
	var report struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(message), &report); err == nil && report.Error != "" {
		return report.Error
	}
	return strings.TrimSpace(message)
}

// uploadObjectName names a child object of an upload Restore.
func uploadObjectName(rs *gameplanev1alpha1.Restore, suffix string) string {
	return truncateDNS(rs.Name+suffix, 63)
}

// nodeAffinityFor pins a Pod to the nodes a target volume can attach to.
func nodeAffinityFor(sel *corev1.NodeSelector) *corev1.Affinity {
	if sel == nil {
		return nil
	}
	return &corev1.Affinity{NodeAffinity: &corev1.NodeAffinity{RequiredDuringSchedulingIgnoredDuringExecution: sel}}
}

// workerTerminationMessage returns the worker container's termination message, or "".
func workerTerminationMessage(pod *corev1.Pod) string {
	for _, st := range pod.Status.ContainerStatuses {
		if st.Name == uploadWorkerContainer && st.State.Terminated != nil {
			return st.State.Terminated.Message
		}
	}
	return ""
}

// archiveManifestSummary converts the receiver's manifest into the status summary.
func archiveManifestSummary(m *uploadManifest, volumes []string) *gameplanev1alpha1.ArchiveManifestSummary {
	if m == nil {
		return nil
	}
	out := &gameplanev1alpha1.ArchiveManifestSummary{
		FormatVersion:    m.FormatVersion,
		Cluster:          m.Cluster,
		Namespace:        m.Namespace,
		ServerName:       m.Server.Name,
		ServerUID:        m.Server.UID,
		TemplateName:     m.Template.Name,
		TemplateRevision: m.Template.Revision,
		GameVersion:      m.GameVersion,
		Volumes:          volumes,
	}
	if t, err := time.Parse(time.RFC3339, m.CapturedAt); err == nil {
		captured := metav1.NewTime(t)
		out.CapturedAt = &captured
	}
	return out
}

// summaryFromStatus rebuilds the facts checkUploadArchive needs from status.
func summaryFromStatus(up *gameplanev1alpha1.RestoreUploadStatus) uploadSummary {
	s := uploadSummary{Kind: up.Kind}
	if up.Manifest != nil {
		s.Volumes = up.Manifest.Volumes
		s.Manifest = &uploadManifest{
			GameVersion: up.Manifest.GameVersion,
			Template:    uploadManifestTmpl{Name: up.Manifest.TemplateName},
		}
	}
	return s
}

// reconcileUpload drives a Restore created from an uploaded archive (spec 041 R5).
func (r *RestoreReconciler) reconcileUpload(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	switch rs.Status.Phase {
	case "", gameplanev1alpha1.RestorePhasePending:
		return r.uploadPending(ctx, rs)
	case gameplanev1alpha1.RestorePhaseReceiving:
		return r.uploadReceiving(ctx, rs)
	case gameplanev1alpha1.RestorePhaseAwaitingConfirmation:
		return r.uploadAwaitingConfirmation(ctx, rs)
	case gameplanev1alpha1.RestorePhaseSuspending, gameplanev1alpha1.RestorePhaseRunning:
		return r.uploadCopy(ctx, rs)
	default:
		return r.failUpload(ctx, rs, fmt.Sprintf("upload restore has unexpected phase %q", rs.Status.Phase))
	}
}

// uploadPending creates the staging claim, receiver Secret, Service and Pod.
func (r *RestoreReconciler) uploadPending(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	if r.AgentImage == "" || r.AgentCASecretName == "" || r.AgentCASecretNamespace == "" {
		return r.failUpload(ctx, rs, "upload restores need the archive worker image and agent CA Secret, which are not configured")
	}
	target, reason, err := r.loadUploadTarget(ctx, rs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if reason != "" {
		return r.failUpload(ctx, rs, reason)
	}
	class, err := r.stagingStorageClass(ctx)
	if errors.Is(err, errNoStagingStorageClass) {
		return r.failUpload(ctx, rs, err.Error())
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	sel, err := r.targetNodeSelector(ctx, target.volumes)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.ensureUploadReceiver(ctx, rs, target, class, sel); err != nil {
		return ctrl.Result{}, err
	}
	rs.Status.Upload = &gameplanev1alpha1.RestoreUploadStatus{
		StagingClaimName: uploadObjectName(rs, uploadStagingSuffix),
		ReceiverService:  uploadObjectName(rs, uploadReceiverSuffix),
	}
	rs.Status.Phase = gameplanev1alpha1.RestorePhaseReceiving
	rs.Status.ObservedGeneration = rs.Generation
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: uploadPollInterval}, nil
}

// uploadReceiving waits for the receiver to finish, then applies the checks.
func (r *RestoreReconciler) uploadReceiving(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	var pod corev1.Pod
	err := r.apiReader().Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: uploadObjectName(rs, uploadReceiverSuffix)}, &pod)
	if apierrors.IsNotFound(err) {
		return r.failUpload(ctx, rs, "archive receiver Pod disappeared before the archive was checked")
	}
	if err != nil {
		return ctrl.Result{}, err
	}
	if pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed {
		return ctrl.Result{RequeueAfter: uploadPollInterval}, nil
	}
	summary, err := parseUploadSummary(workerTerminationMessage(&pod))
	if err != nil {
		return r.failUpload(ctx, rs, fmt.Sprintf("archive receiver ended without a usable result: %v", err))
	}
	if summary.Error != "" {
		return r.failUpload(ctx, rs, summary.Error)
	}
	target, reason, err := r.loadUploadTarget(ctx, rs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if reason != "" {
		return r.failUpload(ctx, rs, reason)
	}
	warnings, reason := checkUploadArchive(summary, rs.Spec.Upload.ConfirmedTemplate, target.archiveTarget())
	if reason != "" {
		return r.failUpload(ctx, rs, reason)
	}
	now := metav1.Now()
	rs.Status.Upload = &gameplanev1alpha1.RestoreUploadStatus{
		Kind:             summary.Kind,
		Manifest:         archiveManifestSummary(summary.Manifest, summary.Volumes),
		UnpackedBytes:    summary.UnpackedBytes,
		StagingClaimName: uploadObjectName(rs, uploadStagingSuffix),
		ReceiverService:  uploadObjectName(rs, uploadReceiverSuffix),
		ReceivedAt:       &now,
		Warnings:         warnings,
	}
	rs.Status.Phase = gameplanev1alpha1.RestorePhaseAwaitingConfirmation
	rs.Status.ObservedGeneration = rs.Generation
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: time.Nanosecond}, nil
}

// uploadAwaitingConfirmation waits for the user. On confirmation it fences the
// target and moves on to the copy.
func (r *RestoreReconciler) uploadAwaitingConfirmation(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	if err := r.deleteReceiverObjects(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	up := rs.Status.Upload
	if up == nil || up.ReceivedAt == nil {
		return r.failUpload(ctx, rs, "upload restore has no received archive record")
	}
	if !rs.Spec.Upload.Confirmed {
		receivedAt := up.ReceivedAt.Time
		if uploadConfirmationExpired(receivedAt, time.Now()) {
			return r.failUpload(ctx, rs, "confirmation timed out")
		}
		return ctrl.Result{RequeueAfter: time.Until(receivedAt.Add(uploadConfirmTimeout))}, nil
	}
	target, reason, err := r.loadUploadTarget(ctx, rs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if reason != "" {
		return r.failUpload(ctx, rs, reason)
	}
	if _, reason := checkUploadArchive(summaryFromStatus(up), rs.Spec.Upload.ConfirmedTemplate, target.archiveTarget()); reason != "" {
		return r.failUpload(ctx, rs, reason)
	}
	res, proceed, err := r.bindAndClaimUpload(ctx, rs, target.gs)
	if err != nil || !proceed {
		return res, err
	}
	rs.Status.Phase = gameplanev1alpha1.RestorePhaseSuspending
	rs.Status.ObservedGeneration = rs.Generation
	if err := r.Status().Update(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: time.Nanosecond}, nil
}

// bindAndClaimUpload binds and claims the target exactly as a backup-based
// restore does. proceed is false when the caller must return res.
func (r *RestoreReconciler) bindAndClaimUpload(
	ctx context.Context, rs *gameplanev1alpha1.Restore, gs *gameplanev1alpha1.GameServer,
) (res ctrl.Result, proceed bool, err error) {
	changed, err := r.bindRestoreTarget(ctx, rs, gs)
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if changed {
		return ctrl.Result{RequeueAfter: reconcileRetryInterval}, false, nil
	}
	target, err := parseRestoreIdentity(rs.Annotations[restoreTargetAnnotation])
	if err != nil {
		return ctrl.Result{}, false, err
	}
	if target.Name != gs.Name || target.UID != gs.UID {
		res, err := r.failUpload(ctx, rs, "target server changed identity during restore")
		return res, false, err
	}
	claimed, err := r.claimRestoreTarget(ctx, rs, gs)
	if err != nil {
		if errors.Is(err, errLifecycleGenerationChanged) {
			res, err := r.failUpload(ctx, rs, err.Error())
			return res, false, err
		}
		return ctrl.Result{}, false, err
	}
	if !claimed {
		return ctrl.Result{RequeueAfter: 5 * time.Second}, false, nil
	}
	if rs.Status.Message != "" {
		rs.Status.Message = ""
		if err := r.Status().Update(ctx, rs); err != nil {
			return ctrl.Result{}, false, err
		}
	}
	return ctrl.Result{}, true, nil
}

// uploadCopy waits for the fence, runs the copy Pod and finishes the restore.
func (r *RestoreReconciler) uploadCopy(ctx context.Context, rs *gameplanev1alpha1.Restore) (ctrl.Result, error) {
	target, reason, err := r.loadUploadTarget(ctx, rs)
	if err != nil {
		return ctrl.Result{}, err
	}
	if reason != "" {
		return r.failUpload(ctx, rs, reason)
	}
	gs := target.gs
	res, proceed, err := r.bindAndClaimUpload(ctx, rs, gs)
	if err != nil || !proceed {
		return res, err
	}

	if rs.Status.Phase == gameplanev1alpha1.RestorePhaseSuspending {
		if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseSuspended && gs.Status.Phase != gameplanev1alpha1.GameServerPhaseStopped {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		stopped, progressed, err := r.restoreTargetStopped(ctx, gs)
		if err != nil {
			return ctrl.Result{}, err
		}
		if progressed {
			return ctrl.Result{RequeueAfter: time.Nanosecond}, nil
		}
		if !stopped {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		rs.Status.Phase = gameplanev1alpha1.RestorePhaseRunning
		rs.Status.ObservedGeneration = rs.Generation
		if err := r.Status().Update(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: reconcileRetryInterval}, nil
	}

	name := uploadObjectName(rs, uploadCopySuffix)
	var pod corev1.Pod
	err = r.apiReader().Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: name}, &pod)
	if apierrors.IsNotFound(err) {
		// The copy Pod mounts the data claim, so the fence is checked only
		// before it exists. Afterwards the Pod itself is the workload.
		stopped, progressed, err := r.restoreTargetStopped(ctx, gs)
		if err != nil {
			return ctrl.Result{}, err
		}
		if progressed {
			return ctrl.Result{RequeueAfter: time.Nanosecond}, nil
		}
		if !stopped {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		fsGroup, err := appliedBackupFSGroup(ctx, r.apiReader(), gs, target.tmpl)
		if err != nil {
			return ctrl.Result{}, err
		}
		sel, err := r.targetNodeSelector(ctx, target.volumes)
		if err != nil {
			return ctrl.Result{}, err
		}
		kind := ""
		if up := rs.Status.Upload; up != nil {
			kind = up.Kind
			// A capture format 1 archive is copied like a plain one: its single
			// data folder fills the game's data claim.
			if up.Manifest != nil && legacyArchiveLayout(kind, up.Manifest.Volumes, target.archiveTarget().VolumeNames) {
				kind = "plain"
			}
		}
		if err := r.ensureUploadObject(ctx, rs, r.buildUploadCopyPod(rs, name, target, kind, sel, fsGroup)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: uploadPollInterval}, nil
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	switch pod.Status.Phase {
	case corev1.PodSucceeded:
		busy, err := r.restoreWorkersLive(ctx, rs.Namespace, restoreIdentity{Name: rs.Name, UID: rs.UID}, false)
		if err != nil {
			return ctrl.Result{}, err
		}
		if busy {
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		now := metav1.Now()
		rs.Status.Phase = gameplanev1alpha1.RestorePhaseSucceeded
		if rs.Status.StartTime == nil {
			rs.Status.StartTime = pod.Status.StartTime
		}
		if rs.Status.CompletionTime == nil {
			rs.Status.CompletionTime = &now
		}
		rs.Status.Conditions = upsertCondition(rs.Status.Conditions, metav1.Condition{
			Type:               "Completed",
			Status:             metav1.ConditionTrue,
			Reason:             "Succeeded",
			ObservedGeneration: rs.Generation,
		})
		if err := r.Status().Update(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.deleteUploadResources(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
		return r.cleanupRestore(ctx, rs)
	case corev1.PodFailed:
		// The server stays suspended: cleanup never resumes a failed restore.
		msg := fmt.Sprintf("copy failed partway: data on %s may be partly replaced; the server stays stopped", gs.Name)
		if detail := parseCopyFailure(workerTerminationMessage(&pod)); detail != "" {
			msg += ": " + detail
		}
		return r.failUpload(ctx, rs, msg)
	default:
		return ctrl.Result{RequeueAfter: uploadPollInterval}, nil
	}
}

// loadUploadTarget reads the target server, its template and volumes. A
// non-empty reason means the restore cannot continue and must fail.
func (r *RestoreReconciler) loadUploadTarget(ctx context.Context, rs *gameplanev1alpha1.Restore) (uploadTarget, string, error) {
	var gs gameplanev1alpha1.GameServer
	err := r.apiReader().Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: rs.Spec.ServerRef.Name}, &gs)
	if apierrors.IsNotFound(err) {
		return uploadTarget{}, fmt.Sprintf("target server %q not found", rs.Spec.ServerRef.Name), nil
	}
	if err != nil {
		return uploadTarget{}, "", err
	}
	if !gs.DeletionTimestamp.IsZero() {
		return uploadTarget{}, "target server is being deleted", nil
	}
	tmpl, err := effectiveGameTemplate(ctx, r.apiReader(), &gs)
	if apierrors.IsNotFound(err) {
		return uploadTarget{}, fmt.Sprintf("game template %q not found", gs.Spec.TemplateRef.Name), nil
	}
	if err != nil {
		return uploadTarget{}, "", err
	}
	volumes, err := r.uploadTargetVolumes(ctx, &gs, tmpl)
	if errors.Is(err, errUploadClaimMissing) {
		return uploadTarget{}, err.Error(), nil
	}
	if err != nil {
		return uploadTarget{}, "", err
	}
	return uploadTarget{gs: &gs, tmpl: tmpl, volumes: volumes}, "", nil
}

// uploadTargetVolumes lists the target's data volumes under the logical names
// the backup layout uses, with their sizes.
func (r *RestoreReconciler) uploadTargetVolumes(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) ([]uploadVolume, error) {
	ver, err := resolveVersion(gs, tmpl)
	if err != nil {
		return nil, fmt.Errorf("resolve target version: %w", err)
	}
	claims := map[string]string{gs.Name + "-data": uploadPrimaryLogicalName}
	for _, extra := range effectiveExtraVolumes(gs, tmpl) {
		claims[extraPVCName(gs, extra.Name)] = extraVolumePodName(extra.Name)
	}
	if volume := modVolume(gs, tmpl, ver); volume != nil {
		claims[volume.PersistentVolumeClaim.ClaimName] = volume.Name
	}
	volumes := make([]uploadVolume, 0, len(claims))
	for claim, logical := range claims {
		var pvc corev1.PersistentVolumeClaim
		if err := r.apiReader().Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: claim}, &pvc); err != nil {
			if apierrors.IsNotFound(err) {
				return nil, fmt.Errorf("%w: %s", errUploadClaimMissing, claim)
			}
			return nil, fmt.Errorf("get volume claim %s: %w", claim, err)
		}
		volumes = append(volumes, uploadVolume{
			LogicalName:   logical,
			ClaimName:     claim,
			VolumeName:    pvc.Spec.VolumeName,
			CapacityBytes: claimCapacityBytes(&pvc),
		})
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].LogicalName < volumes[j].LogicalName })
	return volumes, nil
}

// stagingStorageClass resolves the configured or default StorageClass.
func (r *RestoreReconciler) stagingStorageClass(ctx context.Context) (string, error) {
	if r.UploadStagingStorageClass != "" {
		return r.UploadStagingStorageClass, nil
	}
	var classes storagev1.StorageClassList
	if err := r.apiReader().List(ctx, &classes); err != nil {
		return "", fmt.Errorf("list storage classes: %w", err)
	}
	return chooseStagingStorageClass("", classes.Items)
}

// targetNodeSelector returns the node affinity of the first target volume
// whose bound PersistentVolume has one.
func (r *RestoreReconciler) targetNodeSelector(ctx context.Context, volumes []uploadVolume) (*corev1.NodeSelector, error) {
	for _, v := range volumes {
		if v.VolumeName == "" {
			continue
		}
		var pv corev1.PersistentVolume
		if err := r.apiReader().Get(ctx, types.NamespacedName{Name: v.VolumeName}, &pv); err != nil {
			return nil, fmt.Errorf("get persistent volume %s: %w", v.VolumeName, err)
		}
		if pv.Spec.NodeAffinity != nil && pv.Spec.NodeAffinity.Required != nil {
			return pv.Spec.NodeAffinity.Required, nil
		}
	}
	return nil, nil
}

// ensureUploadReceiver creates the staging claim, receiver TLS Secret, Service and Pod.
func (r *RestoreReconciler) ensureUploadReceiver(
	ctx context.Context, rs *gameplanev1alpha1.Restore, target uploadTarget, class string, sel *corev1.NodeSelector,
) error {
	name := uploadObjectName(rs, uploadReceiverSuffix)
	staging := uploadObjectName(rs, uploadStagingSuffix)
	size := uploadStagingSize(rs.Spec.Upload.DeclaredBytes, sumCapacity(target.volumes))
	if err := r.ensureUploadObject(ctx, rs, buildUploadStagingClaim(rs, staging, class, size)); err != nil {
		return err
	}
	if err := r.ensureUploadTLS(ctx, rs, name); err != nil {
		return err
	}
	if err := r.ensureUploadObject(ctx, rs, buildArchiveWorkerService(name, rs.Namespace, uploadReceiverMode, string(rs.UID))); err != nil {
		return err
	}
	return r.ensureUploadObject(ctx, rs, r.buildUploadReceiverPod(rs, name, staging, target, sel))
}

// ensureUploadTLS creates the receiver's certificate Secret unless it exists,
// so a certificate is never rotated mid-upload.
func (r *RestoreReconciler) ensureUploadTLS(ctx context.Context, rs *gameplanev1alpha1.Restore, name string) error {
	var existing corev1.Secret
	err := r.apiReader().Get(ctx, types.NamespacedName{Namespace: rs.Namespace, Name: name}, &existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get receiver TLS Secret %s/%s: %w", rs.Namespace, name, err)
	}
	caCert, caKey, caPEM, err := r.loadUploadCA(ctx)
	if err != nil {
		return err
	}
	certPEM, keyPEM, err := signServerCert(caCert, caKey, name, workerDNSNames(name, rs.Namespace), uploadCertValidity)
	if err != nil {
		return fmt.Errorf("sign receiver certificate for %s/%s: %w", rs.Namespace, name, err)
	}
	return r.ensureUploadObject(ctx, rs, buildArchiveWorkerTLSSecret(name, rs.Namespace, certPEM, keyPEM, caPEM))
}

// loadUploadCA reads and parses the agent CA that signs receiver certificates.
func (r *RestoreReconciler) loadUploadCA(ctx context.Context) (*x509.Certificate, *rsa.PrivateKey, []byte, error) {
	var caSecret corev1.Secret
	if err := r.apiReader().Get(ctx, types.NamespacedName{
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

// buildUploadStagingClaim returns the RWO staging claim owned by the Restore.
func buildUploadStagingClaim(rs *gameplanev1alpha1.Restore, name, class string, size resource.Quantity) *corev1.PersistentVolumeClaim {
	return &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: rs.Namespace,
			Labels:    map[string]string{restoreUIDLabel: string(rs.UID)},
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: ptrTo(class),
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{corev1.ResourceStorage: size},
			},
		},
	}
}

// buildUploadReceiverPod returns the receive-mode worker Pod. Its environment
// follows the archive-worker receive contract.
func (r *RestoreReconciler) buildUploadReceiverPod(
	rs *gameplanev1alpha1.Restore, name, staging string, target uploadTarget, sel *corev1.NodeSelector,
) *corev1.Pod {
	nonRoot := true
	env := []corev1.EnvVar{
		{Name: "ARCHIVE_FORMAT", Value: rs.Spec.Upload.Format},
		{Name: "ARCHIVE_DECLARED_BYTES", Value: strconv.FormatInt(rs.Spec.Upload.DeclaredBytes, 10)},
		{Name: "ARCHIVE_MAX_UNPACKED_BYTES", Value: strconv.FormatInt(sumCapacity(target.volumes), 10)},
		{Name: "ARCHIVE_STAGING_DIR", Value: uploadStagingDir},
	}
	if plain := rs.Spec.Upload.ConfirmedTemplate; plain != "" {
		env = append(env, corev1.EnvVar{Name: "ARCHIVE_PLAIN_TEMPLATE", Value: plain})
	}
	pod := buildArchiveWorkerPod(archiveWorkerParams{
		Name:                 name,
		Namespace:            rs.Namespace,
		Mode:                 uploadReceiverMode,
		UID:                  string(rs.UID),
		AgentImage:           r.AgentImage,
		AgentImagePullPolicy: r.AgentImagePullPolicy,
		Resources:            auxiliaryResources(r.BackupResources, "100m", "128Mi"),
		PodSecurityContext: &corev1.PodSecurityContext{
			RunAsNonRoot: &nonRoot,
			RunAsUser:    ptrTo(uploadRunAsID),
			RunAsGroup:   ptrTo(uploadRunAsID),
			FSGroup:      ptrTo(uploadRunAsID),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		Env: env,
		Volumes: []corev1.Volume{{
			Name: uploadStagingVolume,
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: staging,
			}},
		}},
		VolumeMounts: []corev1.VolumeMount{{Name: uploadStagingVolume, MountPath: uploadStagingDir}},
		Affinity:     nodeAffinityFor(sel),
		Serve:        true,
	})
	pod.Labels[restoreUIDLabel] = string(rs.UID)
	return pod
}

// buildUploadCopyPod returns the copy-mode worker Pod. It mounts the staging
// claim read-only and each copied target volume under /targets/<logicalName>.
func (r *RestoreReconciler) buildUploadCopyPod(
	rs *gameplanev1alpha1.Restore, name string, target uploadTarget, kind string, sel *corev1.NodeSelector, fsGroup *int64,
) *corev1.Pod {
	volumes := []corev1.Volume{{
		Name: uploadStagingVolume,
		VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
			ClaimName: uploadObjectName(rs, uploadStagingSuffix),
		}},
	}}
	mounts := []corev1.VolumeMount{{Name: uploadStagingVolume, MountPath: uploadStagingDir, ReadOnly: true}}
	copyVols := uploadCopyVolumes(kind, target.volumes)
	names := make([]string, 0, len(copyVols))
	for i, v := range copyVols {
		volName := fmt.Sprintf("target-%d", i)
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: v.ClaimName,
			}},
		})
		mounts = append(mounts, corev1.VolumeMount{Name: volName, MountPath: uploadTargetMountRoot + v.LogicalName})
		names = append(names, v.LogicalName)
	}
	pod := buildArchiveWorkerPod(archiveWorkerParams{
		Name:                 name,
		Namespace:            rs.Namespace,
		Mode:                 uploadCopyMode,
		UID:                  string(rs.UID),
		AgentImage:           r.AgentImage,
		AgentImagePullPolicy: r.AgentImagePullPolicy,
		Resources:            auxiliaryResources(r.BackupResources, "100m", "128Mi"),
		PodSecurityContext:   buildBackupPodSecurityContext(target.tmpl, fsGroup),
		Env:                  []corev1.EnvVar{{Name: "ARCHIVE_COPY_VOLUMES", Value: strings.Join(names, ",")}},
		Volumes:              volumes,
		VolumeMounts:         mounts,
		Affinity:             nodeAffinityFor(sel),
		Serve:                false,
	})
	pod.Labels[restoreUIDLabel] = string(rs.UID)
	return pod
}

// ensureUploadObject creates obj owned by the Restore unless it already exists.
func (r *RestoreReconciler) ensureUploadObject(ctx context.Context, owner *gameplanev1alpha1.Restore, obj client.Object) error {
	key := client.ObjectKeyFromObject(obj)
	existing, ok := obj.DeepCopyObject().(client.Object)
	if !ok {
		return fmt.Errorf("copy of %T is not a client.Object", obj)
	}
	err := r.apiReader().Get(ctx, key, existing)
	if err == nil {
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("get %T %s: %w", obj, key, err)
	}
	if err := controllerutil.SetControllerReference(owner, obj, r.Scheme); err != nil {
		return fmt.Errorf("set owner of %T %s: %w", obj, key, err)
	}
	if err := r.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("create %T %s: %w", obj, key, err)
	}
	return nil
}

// deleteIfPresent deletes obj, treating NotFound as success.
func (r *RestoreReconciler) deleteIfPresent(ctx context.Context, obj client.Object) error {
	if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("delete %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
	}
	return nil
}

// deleteReceiverObjects removes the receiver Pod, Service and TLS Secret.
func (r *RestoreReconciler) deleteReceiverObjects(ctx context.Context, rs *gameplanev1alpha1.Restore) error {
	name := uploadObjectName(rs, uploadReceiverSuffix)
	objs := []client.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rs.Namespace}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rs.Namespace}},
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: rs.Namespace}},
	}
	for _, obj := range objs {
		if err := r.deleteIfPresent(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

// deleteUploadResources removes every worker Pod, Service, Secret and the
// staging claim. It runs on each terminal phase and on deletion. Owner
// references are the backstop.
func (r *RestoreReconciler) deleteUploadResources(ctx context.Context, rs *gameplanev1alpha1.Restore) error {
	if err := r.deleteReceiverObjects(ctx, rs); err != nil {
		return err
	}
	objs := []client.Object{
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: uploadObjectName(rs, uploadCopySuffix), Namespace: rs.Namespace}},
		&corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: uploadObjectName(rs, uploadStagingSuffix), Namespace: rs.Namespace}},
	}
	for _, obj := range objs {
		if err := r.deleteIfPresent(ctx, obj); err != nil {
			return err
		}
	}
	return nil
}

// failUpload marks the Restore Failed, runs its cleanup and removes the upload objects.
func (r *RestoreReconciler) failUpload(ctx context.Context, rs *gameplanev1alpha1.Restore, msg string) (ctrl.Result, error) {
	res, err := r.fail(ctx, rs, msg)
	if err != nil {
		return res, err
	}
	if err := r.deleteUploadResources(ctx, rs); err != nil {
		return ctrl.Result{}, err
	}
	return res, nil
}
