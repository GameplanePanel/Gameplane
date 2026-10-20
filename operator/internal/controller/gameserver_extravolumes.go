package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// effectiveExtraVolumes resolves the extra-volume list that applies to a
// GameServer: gs.Spec.Storage.Extra replaces tmpl.Spec.Storage.Extra
// wholesale when non-empty — the same override semantics GameStorageSpec's
// scalar fields (Size, StorageClassName) already have on GameServer, just
// applied to a slice instead of a scalar — otherwise the template's list
// applies unmodified. Returns nil (not an empty slice) when neither
// declares any, so callers that range over it are no-ops and today's
// (no-extra) pod spec stays byte-identical.
func effectiveExtraVolumes(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) []gameplanev1alpha1.ExtraVolumeSpec {
	if gs.Spec.Storage != nil && len(gs.Spec.Storage.Extra) > 0 {
		return gs.Spec.Storage.Extra
	}
	return tmpl.Spec.Storage.Extra
}

// extraVolumeStorageClass resolves the StorageClassName extra PVCs are
// created with: the GameServer's override if set, else the template's —
// identical precedence to the primary data PVC (reconcilePVC) and the
// per-(version+loader) mod PVCs (reconcileModPVC), so all of a server's
// volumes land in the same storage class by default.
func extraVolumeStorageClass(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) *string {
	if gs.Spec.Storage != nil && gs.Spec.Storage.StorageClassName != nil {
		return gs.Spec.Storage.StorageClassName
	}
	return tmpl.Spec.Storage.StorageClassName
}

// extraVolumePodName is the pod Volume name for an extra volume (DNS-1123
// label, <=63 chars) — mirrors modVolumeName's truncate-with-hash-suffix
// safety net, though ExtraVolumeSpec.Name's own 40-char bound keeps
// "extra-"+name comfortably under the 63-char DNS label limit today.
func extraVolumePodName(name string) string {
	return truncateDNS("extra-"+name, 63)
}

// extraPVCName is the PVC name backing one of a template's extra volumes.
func extraPVCName(gs *gameplanev1alpha1.GameServer, name string) string {
	return truncateDNS(gs.Name+"-extra-"+name, 253)
}

// extraVolumes returns the pod Volumes backing every effective extra
// volume, each bound to its own PVC. Returns nil when there are none, so
// appending it to the StatefulSet's volume list is a no-op for templates
// that don't use the feature (regression guard: byte-identical pod spec).
func extraVolumes(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) []corev1.Volume {
	specs := effectiveExtraVolumes(gs, tmpl)
	if len(specs) == 0 {
		return nil
	}
	vols := make([]corev1.Volume, 0, len(specs))
	for _, ev := range specs {
		vols = append(vols, corev1.Volume{
			Name: extraVolumePodName(ev.Name),
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: extraPVCName(gs, ev.Name),
				},
			},
		})
	}
	return vols
}

// extraVolumeMounts returns the GAME container's mounts for every
// effective extra volume, each mounted directly at its own absolute
// MountPath — deliberately NOT nested under the primary Storage.MountPath,
// since extra volumes exist precisely for directories that share no safe
// common parent with it (see ExtraVolumeSpec's doc comment, e.g. 7 Days to
// Die's serverfiles/ install vs. its .local/share/ world saves). Returns
// nil when there are none (regression guard: byte-identical pod spec).
func extraVolumeMounts(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) []corev1.VolumeMount {
	specs := effectiveExtraVolumes(gs, tmpl)
	if len(specs) == 0 {
		return nil
	}
	mounts := make([]corev1.VolumeMount, 0, len(specs))
	for _, ev := range specs {
		mounts = append(mounts, corev1.VolumeMount{
			Name:      extraVolumePodName(ev.Name),
			MountPath: ev.MountPath,
		})
	}
	return mounts
}

// reconcileExtraPVCs provisions one PVC per effective extra volume (see
// effectiveExtraVolumes), matching reconcilePVC's mechanism exactly: same
// CreateOrUpdate-on-a-fixed-name shape, same ReadWriteOnce access mode, same
// owner reference (so extras share the primary volume's lifecycle and
// reclaim behavior), sized and classed from the ExtraVolumeSpec / storage-
// class precedence instead of the primary volume's Size. Like
// reconcileModPVC, a PVC whose entry is later removed from the effective
// list is deliberately left in place — GC'd only when the GameServer itself
// is deleted via the owner reference — so editing spec.storage.extra can
// never silently destroy a directory's persistent data.
// StorageClass precedence: per-volume override (GameServer.Spec.Storage.StorageClassName)
// > template default (GameTemplate.Spec.Storage.StorageClassName)
// > install-time default (r.DefaultStorageClassName) > cluster default.
func (r *GameServerReconciler) reconcileExtraPVCs(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	sc := extraVolumeStorageClass(gs, tmpl)
	for _, ev := range effectiveExtraVolumes(gs, tmpl) {
		pvc := &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{Name: extraPVCName(gs, ev.Name), Namespace: gs.Namespace},
		}
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
			if pvc.CreationTimestamp.IsZero() {
				pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
				pvc.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: ev.Size}
				if sc != nil {
					pvc.Spec.StorageClassName = sc
				} else if r.DefaultStorageClassName != "" {
					pvc.Spec.StorageClassName = &r.DefaultStorageClassName
				}
			}
			return controllerutil.SetControllerReference(gs, pvc, r.Scheme)
		})
		if err != nil {
			return fmt.Errorf("reconcile extra PVC %s/%s: %w", gs.Namespace, pvc.Name, err)
		}
	}
	return nil
}
