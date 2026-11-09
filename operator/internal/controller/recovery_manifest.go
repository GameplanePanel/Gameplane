package controller

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"sort"

	v1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var recoveryLogicalName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func validateRecoveryManifest(m *v1.BackupCaptureManifest) error {
	if m == nil || m.State != "Recorded" || m.FormatVersion != 2 || m.VolumePolicy != "all-data" {
		return fmt.Errorf("all-data requires a recorded v2 manifest")
	}
	claims, roots, logical := map[string]bool{}, map[string]bool{}, map[string]bool{}
	primary := false
	for _, v := range m.Volumes {
		if v.ClaimName == "" || !recoveryLogicalName.MatchString(v.LogicalName) || claims[v.ClaimName] || logical[v.LogicalName] {
			return fmt.Errorf("ambiguous recovery claim or logical identity %q", v.ClaimName)
		}
		claims[v.ClaimName], logical[v.LogicalName] = true, true
		if len(v.MountPaths) == 0 {
			return fmt.Errorf("claim %s has no frozen mount path", v.ClaimName)
		}
		for _, mount := range v.MountPaths {
			if !path.IsAbs(mount) || path.Clean(mount) != mount {
				return fmt.Errorf("invalid original mount path")
			}
		}
		if v.Included {
			if v.ClaimUID == "" || v.Classification != "data" || v.SnapshotRoot != "/volumes/"+v.LogicalName || roots[v.SnapshotRoot] {
				return fmt.Errorf("invalid recovery root for %s", v.ClaimName)
			}
			roots[v.SnapshotRoot] = true
			primary = primary || v.LogicalName == "primary"
		} else if v.Classification != "rebuildable" || v.RecoveryPrerequisite == "" || v.SnapshotRoot != "" {
			return fmt.Errorf("unclassified or ambiguous excluded claim %s", v.ClaimName)
		}
	}
	if !primary {
		return fmt.Errorf("recovery manifest lacks primary data")
	}
	return nil
}

// Read the frozen StatefulSet rather than rebuilding a layout from today's catalog.
func recoveryLayout(ctx context.Context, reader client.Reader, gs *v1.GameServer) (*v1.BackupCaptureManifest, error) {
	tmpl, err := effectiveGameTemplate(ctx, reader, gs)
	if err != nil {
		return nil, err
	}
	_, revision, err := templateSnapshot(tmpl)
	if err != nil {
		return nil, err
	}
	ver, err := resolveVersion(gs, tmpl)
	if err != nil {
		return nil, err
	}
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return nil, err
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return nil, fmt.Errorf("recovery workload has foreign owner")
	}
	m := &v1.BackupCaptureManifest{State: "Recorded", FormatVersion: 2, VolumePolicy: "all-data", ServerUID: string(gs.UID), Strategy: "restic-snapshot", LayoutSource: "StatefulSet", TemplateName: tmpl.Name, TemplateRevision: revision, GameVersion: gs.Spec.Version}
	if ver != nil {
		m.GameVersion = ver.ID
	}
	declared := map[string]v1.BackupVolumeCoverage{gs.Name + "-data": {LogicalName: "primary", Classification: "data", MountPaths: []string{effectiveMountPath(tmpl)}}}
	for _, extra := range effectiveExtraVolumes(gs, tmpl) {
		role := extra.BackupRole
		if role == "" {
			role = "unclassified"
		}
		declared[extraPVCName(gs, extra.Name)] = v1.BackupVolumeCoverage{LogicalName: extraVolumePodName(extra.Name), Classification: role, MountPaths: []string{extra.MountPath}}
	}
	if volume := modVolume(gs, tmpl, ver); volume != nil {
		mount := modVolumeMount(tmpl, ver)
		declared[volume.PersistentVolumeClaim.ClaimName] = v1.BackupVolumeCoverage{LogicalName: volume.Name, Classification: "data", MountPaths: []string{mount.MountPath}}
	}
	observed := map[string][]string{}
	podNames := map[string]string{}
	for _, v := range ss.Spec.Template.Spec.Volumes {
		if v.PersistentVolumeClaim != nil {
			podNames[v.Name] = v.PersistentVolumeClaim.ClaimName
			observed[v.PersistentVolumeClaim.ClaimName] = nil
		}
	}
	for _, c := range ss.Spec.Template.Spec.Containers {
		if c.Name != "game" {
			continue
		}
		for _, mount := range c.VolumeMounts {
			if claim := podNames[mount.Name]; claim != "" {
				if mount.SubPath != "" || mount.SubPathExpr != "" {
					return nil, fmt.Errorf("all-data does not support claim subpaths")
				}
				observed[claim] = append(observed[claim], mount.MountPath)
			}
		}
	}
	var claims corev1.PersistentVolumeClaimList
	if err := reader.List(ctx, &claims, client.InNamespace(gs.Namespace)); err != nil {
		return nil, err
	}
	uids := map[string]string{}
	for _, claim := range claims.Items {
		if claim.DeletionTimestamp.IsZero() {
			uids[claim.Name] = string(claim.UID)
		}
		if metav1.IsControlledBy(&claim, gs) {
			if _, ok := observed[claim.Name]; !ok {
				observed[claim.Name] = nil
			}
		}
	}
	for name, mounts := range observed {
		v, ok := declared[name]
		if !ok {
			return nil, fmt.Errorf("unclassified observed claim %s", name)
		}
		sort.Strings(mounts)
		if !reflect.DeepEqual(mounts, v.MountPaths) {
			return nil, fmt.Errorf("claim %s frozen mount paths differ from declaration", name)
		}
	}
	for name, v := range declared {
		if !reflect.DeepEqual(observed[name], v.MountPaths) {
			return nil, fmt.Errorf("required claim %s is absent from frozen workload", name)
		}
		v.ClaimName, v.ClaimUID = name, uids[name]
		switch v.Classification {
		case "data":
			v.Included = true
			v.SnapshotRoot = "/volumes/" + v.LogicalName
			v.CoverageReason = "IncludedData"
		case "rebuildable":
			v.CoverageReason = "RebuildRequired"
			v.RecoveryPrerequisite = "If its contents are missing or incompatible, reconstruct this excluded volume from the declared module and installation sources before starting the recovered game."
		default:
			return nil, fmt.Errorf("claim %s must declare backupRole data or rebuildable", name)
		}
		m.Volumes = append(m.Volumes, v)
	}
	sort.Slice(m.Volumes, func(i, j int) bool { return m.Volumes[i].LogicalName < m.Volumes[j].LogicalName })
	return m, validateRecoveryManifest(m)
}

func recoveryDigest(m *v1.BackupCaptureManifest) string {
	data, _ := json.Marshal(m)
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

func sameRecoveryLayout(a, b *v1.BackupCaptureManifest) bool {
	return a.TemplateName == b.TemplateName && a.TemplateRevision == b.TemplateRevision && a.GameVersion == b.GameVersion && a.ServerUID == b.ServerUID && reflect.DeepEqual(a.Volumes, b.Volumes)
}
func (r *BackupReconciler) validateBackupRecoveryLayout(ctx context.Context, b *v1.Backup, gs *v1.GameServer) error {
	if b.Spec.VolumePolicy != "all-data" {
		return nil
	}
	if err := validateRecoveryManifest(b.Status.CaptureManifest); err != nil {
		return err
	}
	current, err := recoveryLayout(ctx, r.backupAPIReader(), gs)
	if err != nil {
		return err
	}
	if !sameRecoveryLayout(b.Status.CaptureManifest, current) {
		return fmt.Errorf("capture layout or claim identity changed after admission")
	}
	return nil
}
func podMountsRecoveryClaims(pod *corev1.Pod, volumes []v1.BackupVolumeCoverage) bool {
	for _, v := range pod.Spec.Volumes {
		if v.PersistentVolumeClaim == nil {
			continue
		}
		for _, want := range volumes {
			if want.Included && want.ClaimName == v.PersistentVolumeClaim.ClaimName {
				return true
			}
		}
	}
	return false
}

// Replace only persistent mounts, leaving repository credentials and scratch intact.
func recoveryWorkerVolumes(spec *corev1.PodSpec, volumes []v1.BackupVolumeCoverage, readOnly bool) {
	spec.Volumes = []corev1.Volume{{Name: "cache", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}}}
	spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{Name: "cache", MountPath: "/tmp"}}
	for i, v := range volumes {
		if !v.Included {
			continue
		}
		name := fmt.Sprintf("root-%d", i)
		spec.Volumes = append(spec.Volumes, corev1.Volume{Name: name, VolumeSource: corev1.VolumeSource{PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: v.ClaimName, ReadOnly: readOnly}}})
		spec.Containers[0].VolumeMounts = append(spec.Containers[0].VolumeMounts, corev1.VolumeMount{Name: name, MountPath: v.SnapshotRoot, ReadOnly: readOnly})
	}
}
