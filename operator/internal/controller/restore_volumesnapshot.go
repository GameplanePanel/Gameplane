package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ctrl "sigs.k8s.io/controller-runtime"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// annoRestoreCreatedBy marks a GameServer that a volume-snapshot Restore
// provisioned, so a re-running restore re-finds its own server instead of
// mistaking it for a pre-existing name collision. The new server is
// intentionally NOT owner-referenced by the Restore (it must outlive it),
// so this annotation is how the controller tracks it.
const annoRestoreCreatedBy = "restore.gameplane.local/created-by"

// errRefNotOwned marks the one reference-handling failure that is terminal
// for a Restore: an object the restore would copy (or overwrite) is not
// owned by the expected GameServer. Every other error is transient (stale
// cache, write conflict, API hiccup) and is returned so the Restore is
// requeued instead of failed.
var errRefNotOwned = errors.New("reference not owned")

// Constants for volume-snapshot restore reference handling.
const (
	// VolumeSnapshotRestoreDeadline is the maximum time a restore waits for
	// a restored server to reach Running phase.
	VolumeSnapshotRestoreDeadline = 10 * time.Minute

	// maxObjectNameLength is the maximum length for Kubernetes object names.
	maxObjectNameLength = 253

	// secretRefKind and configMapRefKind are the GVK kinds for reference validation.
	secretRefKind    = "Secret"
	configMapRefKind = "ConfigMap"
)

// reconcileVolumeSnapshotRestore restores a volume-snapshot Backup by
// standing up a brand-new GameServer whose data PVC is seeded from the CSI
// snapshot (GameServer.spec.storage.dataSource → reconcilePVC). The original
// server is never touched. The new server's spec is copied from the original
// (which must still exist to source the spec).
func (r *RestoreReconciler) reconcileVolumeSnapshotRestore(
	ctx context.Context, rs *gameplanev1alpha1.Restore, src *gameplanev1alpha1.Backup,
) (ctrl.Result, error) {
	// The snapshot must have actually bound or the new PVC can't be seeded.
	if src.Status.VolumeSnapshotContentName == "" {
		return r.fail(ctx, rs, fmt.Sprintf(
			"source backup %q has no bound VolumeSnapshot", rs.Spec.BackupRef.Name))
	}

	newKey := types.NamespacedName{Name: rs.Spec.ServerRef.Name, Namespace: rs.Namespace}
	var newGS gameplanev1alpha1.GameServer
	err := r.Get(ctx, newKey, &newGS)
	switch {
	case err == nil:
		// A server with the target name exists. If we created it, wait for
		// it to come up; otherwise it's a collision — volume-snapshot
		// restores must create a fresh server, never overwrite one.
		if newGS.Annotations[annoRestoreCreatedBy] != rs.Name {
			return r.fail(ctx, rs, fmt.Sprintf(
				"target server %q already exists; volume-snapshot restores create a new server",
				rs.Spec.ServerRef.Name))
		}
		return r.awaitRestoredServer(ctx, rs, src, &newGS)
	case !apierrors.IsNotFound(err):
		return ctrl.Result{}, err
	}

	// Target doesn't exist yet — build it from the original server's spec.
	var orig gameplanev1alpha1.GameServer
	if err := r.Get(ctx, types.NamespacedName{Name: src.Spec.ServerRef.Name, Namespace: rs.Namespace}, &orig); err != nil {
		if apierrors.IsNotFound(err) {
			return r.fail(ctx, rs, fmt.Sprintf(
				"original server %q not found; cannot derive spec for a volume-snapshot restore",
				src.Spec.ServerRef.Name))
		}
		return ctrl.Result{}, err
	}

	created := &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{
			Name:        rs.Spec.ServerRef.Name,
			Namespace:   rs.Namespace,
			Annotations: map[string]string{annoRestoreCreatedBy: rs.Name},
		},
	}
	orig.Spec.DeepCopyInto(&created.Spec)
	created.Spec.Suspend = false
	if created.Spec.Storage == nil {
		created.Spec.Storage = &gameplanev1alpha1.GameStorageSpec{}
	}
	created.Spec.Storage.DataSource = &gameplanev1alpha1.GameDataSource{
		Kind: "VolumeSnapshot",
		Name: rs.Status.SnapshotID,
	}
	// Record StartTime before planning so the restore deadline also bounds
	// the pre-Create path: a referenced object that stays missing requeues
	// (it may just not be cached yet) and must not do so forever.
	if rs.Status.StartTime == nil {
		now := metav1.Now()
		rs.Status.StartTime = &now
		if err := r.Status().Update(ctx, rs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Refuse before creating anything if the original server does not own
	// every referenced Secret/ConfigMap. The copy names depend only on the
	// restored server's name, so the references are rewritten in the spec
	// before Create; the copies themselves are made after Create because
	// they need the restored server's UID.
	copies, err := r.planOwnedRefCopies(ctx, &orig, created)
	if err != nil {
		return r.failOrRequeue(ctx, rs, err)
	}
	rewriteRefs(&created.Spec, copies)

	// Persist the copy plan on the Restore now, before creating anything.
	// ensureOwnedRefCopies below can fail transiently (e.g. a write
	// conflict), and awaitRestoredServer's later passes must be able to
	// re-ensure exactly this plan (ensureOwnedRefCopies) instead of
	// falling back to a read-only check that can never recreate a missing
	// copy — persisting only after ensureOwnedRefCopies succeeds would
	// leave that fallback as the only path on a restore whose very first
	// copy attempt failed transiently.
	copiesJSON, err := json.Marshal(copies)
	if err != nil {
		return r.failOrRequeue(ctx, rs, fmt.Errorf("marshal copy plan: %w", err))
	}
	// client.MergeFrom snapshots its argument by reference, not by value,
	// so the "before" snapshot must be taken before rs is mutated below —
	// otherwise the computed merge patch is empty and the write is a no-op.
	rsBeforePatch := rs.DeepCopy()
	if rs.Annotations == nil {
		rs.Annotations = make(map[string]string)
	}
	rs.Annotations["restore.gameplane.local/copy-plan"] = string(copiesJSON)
	if err := r.Patch(ctx, rs, client.MergeFrom(rsBeforePatch)); err != nil {
		return r.failOrRequeue(ctx, rs, fmt.Errorf("update restore copy plan: %w", err))
	}

	if err := r.Create(ctx, created); err != nil {
		if apierrors.IsAlreadyExists(err) {
			// Lost a create race; the next pass finds it via the annotation.
			return ctrl.Result{Requeue: true}, nil
		}
		// Route non-AlreadyExists errors through the same deadline-aware path
		// used for reference-copy failures. This ensures transient errors do not
		// cause indefinite requeuing past the deadline.
		return r.failOrRequeue(ctx, rs, err)
	}

	// Make the copies now that the restored server has a UID. A transient
	// error here requeues; the next pass finds the server via the
	// annotation and re-ensures the persisted plan in awaitRestoredServer.
	if err := r.ensureOwnedRefCopies(ctx, &orig, created, copies); err != nil {
		return r.failOrRequeue(ctx, rs, err)
	}

	return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
}

// ensureRestoredRefsFromSpec validates that every Secret/ConfigMap referenced
// by the restored server's spec still exists and is owned, without creating new copies.
// It reads through the uncached APIReader, so a NotFound means the object is
// really gone rather than not yet in the informer cache.
func (r *RestoreReconciler) ensureRestoredRefsFromSpec(
	ctx context.Context, restored *gameplanev1alpha1.GameServer,
) error {
	for _, ref := range extractSpecRefs(&restored.Spec) {
		switch ref.kind {
		case secretRefKind:
			sec := &corev1.Secret{}
			if err := r.apiReader().Get(ctx, types.NamespacedName{Name: ref.name, Namespace: restored.Namespace}, sec); err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("referenced Secret %q not found: %w", ref.name, errRefNotOwned)
				}
				return fmt.Errorf("check referenced Secret %q: %w", ref.name, err)
			}
			if !isServerOwnedSecret(sec, restored) {
				return fmt.Errorf("referenced Secret %q not owned by restored server: %w", ref.name, errRefNotOwned)
			}
		case configMapRefKind:
			cm := &corev1.ConfigMap{}
			if err := r.apiReader().Get(ctx, types.NamespacedName{Name: ref.name, Namespace: restored.Namespace}, cm); err != nil {
				if apierrors.IsNotFound(err) {
					return fmt.Errorf("referenced ConfigMap %q not found: %w", ref.name, errRefNotOwned)
				}
				return fmt.Errorf("check referenced ConfigMap %q: %w", ref.name, err)
			}
			if !isServerOwnedConfigMap(cm, restored) {
				return fmt.Errorf("referenced ConfigMap %q not owned by restored server: %w", ref.name, errRefNotOwned)
			}
		}
	}
	return nil
}

// awaitRestoredServer drives the Restore to a terminal phase based on the
// newly-provisioned server: Running (with its references copied) →
// Succeeded, Failed → Failed, otherwise keep polling while it starts up
// (with a deadline).
func (r *RestoreReconciler) awaitRestoredServer(
	ctx context.Context, rs *gameplanev1alpha1.Restore, src *gameplanev1alpha1.Backup, gs *gameplanev1alpha1.GameServer,
) (ctrl.Result, error) {
	// Re-ensure owned references are copied on every pass. The original
	// server is named by the source Backup's ServerRef, not by
	// rs.Spec.BackupRef.Name (the Backup CR's own name). An ownership
	// refusal is terminal and returned immediately; any other error is
	// transient and must NOT return early here, or a restore stuck on a
	// retryable copy error would never reach the deadline check below and
	// could run past the documented limit.
	var refsErr error
	orig := &gameplanev1alpha1.GameServer{}
	err := r.Get(ctx, types.NamespacedName{Name: src.Spec.ServerRef.Name, Namespace: rs.Namespace}, orig)
	if err == nil {
		// Original server exists: re-ensure only the persisted copy plan,
		// not the original's current spec (which may have changed).
		// A missing or replaced copy is recreated only if its source still exists
		// and is owned; a changed or no-longer-matching source is treated as transient.
		var plannedCopies []refCopy
		if planJSON := rs.Annotations["restore.gameplane.local/copy-plan"]; planJSON != "" {
			if err := json.Unmarshal([]byte(planJSON), &plannedCopies); err == nil {
				refsErr = r.ensureOwnedRefCopies(ctx, orig, gs, plannedCopies)
			}
		} else {
			// Fallback if plan was not persisted: use restored server's own spec
			refsErr = r.ensureRestoredRefsFromSpec(ctx, gs)
		}
		if refsErr != nil && errors.Is(refsErr, errRefNotOwned) {
			return r.fail(ctx, rs, refsErr.Error())
		}
	} else if apierrors.IsNotFound(err) {
		// Original server was deleted (legitimate): verify that every Secret/ConfigMap
		// referenced by the RESTORED server's spec exists and is owned by the restored server.
		// Read-only check: do not create copies, just validate references exist and are owned.
		// Stop at the first bad reference so a later transient error cannot
		// overwrite an errRefNotOwned result.
	refsLoop:
		for _, ref := range extractSpecRefs(&gs.Spec) {
			switch ref.kind {
			case secretRefKind:
				sec := &corev1.Secret{}
				if err := r.apiReader().Get(ctx, types.NamespacedName{Name: ref.name, Namespace: gs.Namespace}, sec); err != nil {
					if apierrors.IsNotFound(err) {
						refsErr = fmt.Errorf("referenced Secret %q not found (transient): %w", ref.name, err)
					} else {
						refsErr = fmt.Errorf("check referenced Secret %q: %w", ref.name, err)
					}
					break refsLoop
				}
				if !isServerOwnedSecret(sec, gs) {
					refsErr = fmt.Errorf("referenced Secret %q not owned by restored server: %w", ref.name, errRefNotOwned)
					break refsLoop
				}
			case configMapRefKind:
				cm := &corev1.ConfigMap{}
				if err := r.apiReader().Get(ctx, types.NamespacedName{Name: ref.name, Namespace: gs.Namespace}, cm); err != nil {
					if apierrors.IsNotFound(err) {
						refsErr = fmt.Errorf("referenced ConfigMap %q not found (transient): %w", ref.name, err)
					} else {
						refsErr = fmt.Errorf("check referenced ConfigMap %q: %w", ref.name, err)
					}
					break refsLoop
				}
				if !isServerOwnedConfigMap(cm, gs) {
					refsErr = fmt.Errorf("referenced ConfigMap %q not owned by restored server: %w", ref.name, errRefNotOwned)
					break refsLoop
				}
			}
		}
	} else {
		// Any other Get error is transient: set refsErr but do not return early.
		refsErr = fmt.Errorf("get original GameServer %s: %w", src.Spec.ServerRef.Name, err)
	}
	if refsErr != nil && errors.Is(refsErr, errRefNotOwned) {
		return r.fail(ctx, rs, refsErr.Error())
	}

	if gs.Status.Phase == gameplanev1alpha1.GameServerPhaseFailed {
		return r.fail(ctx, rs, fmt.Sprintf("restored server %q failed to start", gs.Name))
	}
	if gs.Status.Phase == gameplanev1alpha1.GameServerPhaseRunning {
		// Always check the restored server's own references, regardless of original state.
		// (Original's spec may have changed, so we validate only what the restored server actually uses.)
		// Read through the uncached APIReader: ensureOwnedRefCopies may have
		// just recreated a copy the informer cache has not seen yet, and a
		// cached NotFound here would fail the Restore permanently.
		var finalRefsErr error
		for _, ref := range extractSpecRefs(&gs.Spec) {
			switch ref.kind {
			case secretRefKind:
				sec := &corev1.Secret{}
				if err := r.apiReader().Get(ctx, types.NamespacedName{Name: ref.name, Namespace: gs.Namespace}, sec); err != nil {
					if apierrors.IsNotFound(err) {
						finalRefsErr = fmt.Errorf("referenced Secret %q not found: %w", ref.name, errRefNotOwned)
					} else {
						finalRefsErr = fmt.Errorf("check referenced Secret %q: %w", ref.name, err)
					}
				} else if !isServerOwnedSecret(sec, gs) {
					finalRefsErr = fmt.Errorf("referenced Secret %q not owned by restored server: %w", ref.name, errRefNotOwned)
				}
			case configMapRefKind:
				cm := &corev1.ConfigMap{}
				if err := r.apiReader().Get(ctx, types.NamespacedName{Name: ref.name, Namespace: gs.Namespace}, cm); err != nil {
					if apierrors.IsNotFound(err) {
						finalRefsErr = fmt.Errorf("referenced ConfigMap %q not found: %w", ref.name, errRefNotOwned)
					} else {
						finalRefsErr = fmt.Errorf("check referenced ConfigMap %q: %w", ref.name, err)
					}
				} else if !isServerOwnedConfigMap(cm, gs) {
					finalRefsErr = fmt.Errorf("referenced ConfigMap %q not owned by restored server: %w", ref.name, errRefNotOwned)
				}
			}
			if finalRefsErr != nil {
				break
			}
		}
		if finalRefsErr != nil && errors.Is(finalRefsErr, errRefNotOwned) {
			return r.fail(ctx, rs, finalRefsErr.Error())
		}
		if finalRefsErr == nil && refsErr == nil {
			now := metav1.Now()
			rs.Status.Phase = gameplanev1alpha1.RestorePhaseSucceeded
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
			return ctrl.Result{}, nil
		}
	}

	// Fail once the deadline has passed, but only while the server is still starting.
	// A Running server with transient copy errors should not be failed by deadline.
	if gs.Status.Phase != gameplanev1alpha1.GameServerPhaseRunning {
		if rs.Status.StartTime == nil {
			now := metav1.Now()
			rs.Status.StartTime = &now
			if err := r.Status().Update(ctx, rs); err != nil {
				return ctrl.Result{}, err
			}
		}
		if time.Since(rs.Status.StartTime.Time) > VolumeSnapshotRestoreDeadline {
			return r.fail(ctx, rs, fmt.Sprintf(
				"restore did not complete within %v; restored server %q phase is %s",
				VolumeSnapshotRestoreDeadline, gs.Name, gs.Status.Phase))
		}
	}
	if refsErr != nil {
		return ctrl.Result{}, refsErr
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

// refCopy describes a single reference to copy from the original server to
// the restored server. Its fields are exported (with json tags) because a
// []refCopy is round-tripped through JSON: it's marshaled into the Restore's
// "restore.gameplane.local/copy-plan" annotation and unmarshaled back out of
// it on later reconcile passes (see reconcileVolumeSnapshotRestore and
// awaitRestoredServer). encoding/json only sees exported fields, so
// unexported fields here would silently round-trip as zero values.
type refCopy struct {
	Kind     string `json:"kind"` // "Secret" or "ConfigMap"
	OrigName string `json:"origName"`
	CopyName string `json:"copyName"`
}

// planOwnedRefCopies traverses the original server's spec and returns a list of
// references that must be copied, along with their new names. It validates that
// the original server owns each referenced object.
func (r *RestoreReconciler) planOwnedRefCopies(
	ctx context.Context, orig, restored *gameplanev1alpha1.GameServer,
) ([]refCopy, error) {
	var copies []refCopy
	seen := make(map[string]bool)

	// Traverse spec.env for value references (Secret/ConfigMap in ValueFrom)
	for _, env := range orig.Spec.Env {
		if env.ValueFrom == nil {
			continue
		}

		// Check for SecretKeyRef
		if env.ValueFrom.SecretKeyRef != nil {
			refName := env.ValueFrom.SecretKeyRef.Name
			key := secretRefKind + "/" + refName
			if seen[key] {
				continue
			}
			seen[key] = true

			// Validate ownership
			owned, err := r.ownershipOfRef(ctx, orig, secretRefKind, refName, orig.Namespace)
			if err != nil {
				return nil, fmt.Errorf("check ownership of Secret %q: %w", refName, err)
			}
			if !owned {
				return nil, fmt.Errorf("original server does not own Secret %q: %w", refName, errRefNotOwned)
			}

			// Plan the copy with a new name
			copyName := restoredRefName(restored.Name, refName)
			copies = append(copies, refCopy{
				Kind:     secretRefKind,
				OrigName: refName,
				CopyName: copyName,
			})
		}

		// Check for ConfigMapKeyRef
		if env.ValueFrom.ConfigMapKeyRef != nil {
			refName := env.ValueFrom.ConfigMapKeyRef.Name
			key := configMapRefKind + "/" + refName
			if seen[key] {
				continue
			}
			seen[key] = true

			// Validate ownership
			owned, err := r.ownershipOfRef(ctx, orig, configMapRefKind, refName, orig.Namespace)
			if err != nil {
				return nil, fmt.Errorf("check ownership of ConfigMap %q: %w", refName, err)
			}
			if !owned {
				return nil, fmt.Errorf("original server does not own ConfigMap %q: %w", refName, errRefNotOwned)
			}

			// Plan the copy with a new name
			copyName := restoredRefName(restored.Name, refName)
			copies = append(copies, refCopy{
				Kind:     configMapRefKind,
				OrigName: refName,
				CopyName: copyName,
			})
		}
	}

	// Traverse spec.networking.tunnel.credentialsSecretRef
	if orig.Spec.Networking.Tunnel != nil &&
		orig.Spec.Networking.Tunnel.CredentialsSecretRef != nil {
		ref := orig.Spec.Networking.Tunnel.CredentialsSecretRef
		key := "Secret/" + ref.Name
		if !seen[key] {
			owned, err := r.ownershipOfRef(ctx, orig, secretRefKind, ref.Name, orig.Namespace)
			if err != nil {
				return nil, fmt.Errorf("check ownership of Secret %q: %w", ref.Name, err)
			}
			if !owned {
				return nil, fmt.Errorf("original server does not own Secret %q: %w", ref.Name, errRefNotOwned)
			}

			copyName := restoredRefName(restored.Name, ref.Name)
			copies = append(copies, refCopy{
				Kind:     secretRefKind,
				OrigName: ref.Name,
				CopyName: copyName,
			})
			seen[key] = true
		}
	}

	// Traverse spec.backupPolicy.repoRef
	if orig.Spec.BackupPolicy != nil && orig.Spec.BackupPolicy.RepoRef.Name != "" {
		ref := &orig.Spec.BackupPolicy.RepoRef
		key := "Secret/" + ref.Name
		if !seen[key] {
			owned, err := r.ownershipOfRef(ctx, orig, secretRefKind, ref.Name, orig.Namespace)
			if err != nil {
				return nil, fmt.Errorf("check ownership of Secret %q: %w", ref.Name, err)
			}
			if !owned {
				return nil, fmt.Errorf("original server does not own Secret %q: %w", ref.Name, errRefNotOwned)
			}

			copyName := restoredRefName(restored.Name, ref.Name)
			copies = append(copies, refCopy{
				Kind:     secretRefKind,
				OrigName: ref.Name,
				CopyName: copyName,
			})
			seen[key] = true
		}
	}

	return copies, nil
}

// ensureOwnedRefCopies creates or updates the planned copies. Each copy is
// created as a new Secret or ConfigMap, controller-owned by the restored server.
// If an object that the restored server does not control (controller
// OwnerReference) already exists under the copy name, the restore fails.
func (r *RestoreReconciler) ensureOwnedRefCopies(
	ctx context.Context, orig, restored *gameplanev1alpha1.GameServer, copies []refCopy,
) error {
	for _, cp := range copies {
		switch cp.Kind {
		case secretRefKind:
			dst := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: cp.CopyName, Namespace: restored.Namespace},
			}
			_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dst, func() error {
				if !dst.CreationTimestamp.IsZero() {
					// Object exists. Verify the live API-server object is controlled by the restored server.
					// Re-fetch through the uncached APIReader: dst above was
					// populated by CreateOrUpdate's own cache-backed Get, so
					// re-reading through r.Client here would just observe the
					// same stale cache entry and could never catch a copy
					// that was deleted and replaced before the informer
					// cache caught up.
					live := &corev1.Secret{}
					if err := r.apiReader().Get(ctx, types.NamespacedName{Name: cp.CopyName, Namespace: restored.Namespace}, live); err != nil {
						return fmt.Errorf("re-verify live Secret %q: %w", cp.CopyName, err)
					}
					// Compare UIDs: the live object must be the same one the cache saw, and it must be controlled.
					if live.UID != dst.UID || !metav1.IsControlledBy(live, restored) {
						return fmt.Errorf("secret %q is not controlled by the restored server or was replaced: %w", cp.CopyName, errRefNotOwned)
					}
					// Owned copy exists and verified live, skip update.
					return nil
				}
				// New copy only: read the source now. An existing copy that
				// passed the live check above is accepted without consulting
				// the source, so a later edit, deletion or replacement of the
				// original's Secret cannot stall or fail a restore whose copy
				// is already made.
				src := &corev1.Secret{}
				if err := r.Get(ctx, types.NamespacedName{Name: cp.OrigName, Namespace: orig.Namespace}, src); err != nil {
					return fmt.Errorf("read source Secret %q: %w", cp.OrigName, err)
				}
				// Re-verify ownership at copy time: planning may have run a
				// pass ago, and the named Secret could have been replaced with
				// an unowned object at the same name since.
				if !isServerOwnedSecret(src, orig) {
					return fmt.Errorf("source Secret %q is not owned by original server %q: %w", cp.OrigName, orig.Name, errRefNotOwned)
				}
				dst.Type = src.Type
				dst.Data = src.Data
				return controllerutil.SetControllerReference(restored, dst, r.Scheme)
			})
			if err != nil {
				return fmt.Errorf("ensure copy Secret %q: %w", cp.CopyName, err)
			}

		case configMapRefKind:
			dst := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{Name: cp.CopyName, Namespace: restored.Namespace},
			}
			_, err := controllerutil.CreateOrUpdate(ctx, r.Client, dst, func() error {
				if !dst.CreationTimestamp.IsZero() {
					// Object exists. Verify the live API-server object is controlled by the restored server.
					// Re-fetch through the uncached APIReader: dst above was
					// populated by CreateOrUpdate's own cache-backed Get, so
					// re-reading through r.Client here would just observe the
					// same stale cache entry and could never catch a copy
					// that was deleted and replaced before the informer
					// cache caught up.
					live := &corev1.ConfigMap{}
					if err := r.apiReader().Get(ctx, types.NamespacedName{Name: cp.CopyName, Namespace: restored.Namespace}, live); err != nil {
						return fmt.Errorf("re-verify live ConfigMap %q: %w", cp.CopyName, err)
					}
					// Compare UIDs: the live object must be the same one the cache saw, and it must be controlled.
					if live.UID != dst.UID || !metav1.IsControlledBy(live, restored) {
						return fmt.Errorf("configmap %q is not controlled by the restored server or was replaced: %w", cp.CopyName, errRefNotOwned)
					}
					// Owned copy exists and verified live, skip update.
					return nil
				}
				// New copy only: read the source now (see the Secret case).
				src := &corev1.ConfigMap{}
				if err := r.Get(ctx, types.NamespacedName{Name: cp.OrigName, Namespace: orig.Namespace}, src); err != nil {
					return fmt.Errorf("read source ConfigMap %q: %w", cp.OrigName, err)
				}
				// Re-verify ownership at copy time: planning may have run a
				// pass ago, and the named ConfigMap could have been replaced
				// with an unowned object at the same name since.
				if !isServerOwnedConfigMap(src, orig) {
					return fmt.Errorf("source ConfigMap %q is not owned by original server %q: %w", cp.OrigName, orig.Name, errRefNotOwned)
				}
				dst.Data = src.Data
				dst.BinaryData = src.BinaryData
				return controllerutil.SetControllerReference(restored, dst, r.Scheme)
			})
			if err != nil {
				return fmt.Errorf("ensure copy ConfigMap %q: %w", cp.CopyName, err)
			}
		}
	}

	return nil
}

// rewriteRefs points the spec's env Secret/ConfigMap references and the
// tunnel credentials Secret at the planned copies.
func rewriteRefs(spec *gameplanev1alpha1.GameServerSpec, copies []refCopy) {
	for i := range spec.Env {
		vf := spec.Env[i].ValueFrom
		if vf == nil {
			continue
		}
		for _, cp := range copies {
			if cp.Kind == secretRefKind && vf.SecretKeyRef != nil && vf.SecretKeyRef.Name == cp.OrigName {
				vf.SecretKeyRef.Name = cp.CopyName
			}
			if cp.Kind == configMapRefKind && vf.ConfigMapKeyRef != nil && vf.ConfigMapKeyRef.Name == cp.OrigName {
				vf.ConfigMapKeyRef.Name = cp.CopyName
			}
		}
	}
	if spec.Networking.Tunnel != nil && spec.Networking.Tunnel.CredentialsSecretRef != nil {
		ref := spec.Networking.Tunnel.CredentialsSecretRef
		for _, cp := range copies {
			if cp.Kind == secretRefKind && ref.Name == cp.OrigName {
				ref.Name = cp.CopyName
			}
		}
	}
	if spec.BackupPolicy != nil && spec.BackupPolicy.RepoRef.Name != "" {
		ref := &spec.BackupPolicy.RepoRef
		for _, cp := range copies {
			if cp.Kind == secretRefKind && ref.Name == cp.OrigName {
				ref.Name = cp.CopyName
			}
		}
	}
}

// failOrRequeue marks the Restore Failed for an ownership refusal, which no
// retry can fix, and for any other error once the restore deadline (counted
// from rs.Status.StartTime) has passed. Any other error is returned so the
// controller requeues the Restore with backoff.
func (r *RestoreReconciler) failOrRequeue(
	ctx context.Context, rs *gameplanev1alpha1.Restore, err error,
) (ctrl.Result, error) {
	if errors.Is(err, errRefNotOwned) {
		return r.fail(ctx, rs, err.Error())
	}
	if rs.Status.StartTime != nil && time.Since(rs.Status.StartTime.Time) > VolumeSnapshotRestoreDeadline {
		return r.fail(ctx, rs, fmt.Sprintf(
			"restore did not complete within %v: %v", VolumeSnapshotRestoreDeadline, err))
	}
	return ctrl.Result{}, err
}

// ownershipOfRef checks whether the GameServer owns the named Secret or
// ConfigMap. A NotFound is returned as an error rather than treated as "not
// owned": the object may simply not have reached the informer cache yet, and
// collapsing that into "not owned" would make callers mark the Restore
// permanently Failed on a transient cache miss instead of requeuing.
func (r *RestoreReconciler) ownershipOfRef(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, kind, name, namespace string,
) (bool, error) {
	switch kind {
	case secretRefKind:
		sec := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sec); err != nil {
			return false, err
		}
		return isServerOwnedSecret(sec, gs), nil
	case configMapRefKind:
		cm := &corev1.ConfigMap{}
		if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cm); err != nil {
			return false, err
		}
		return isServerOwnedConfigMap(cm, gs), nil
	default:
		return false, fmt.Errorf("unsupported reference kind %q", kind)
	}
}

// restoredRefName generates a new name for a copied reference, unique to the restored server.
func restoredRefName(restoredServerName, origRefName string) string {
	// Use the format: <truncated-server>-<digest-of-server-and-original>
	// to ensure uniqueness and keep the name under the 253-character limit.
	// Hash both the server name and original ref name to avoid collisions
	// when two servers share an identical prefix and reference the same source.
	combinedHash := fmt.Sprintf("%012x", hashString(restoredServerName+"/"+origRefName))[:12]
	candidate := fmt.Sprintf("%s-ref-%s", restoredServerName, combinedHash)
	if len(candidate) > maxObjectNameLength {
		// Truncate the server name if needed, but keep the hash independent
		// of the truncated length to avoid collisions across different truncations.
		available := maxObjectNameLength - len("-ref-") - 12
		if available < 1 {
			available = 1
		}
		candidate = fmt.Sprintf("%s-ref-%s",
			restoredServerName[:available],
			combinedHash)
	}
	return strings.ToLower(candidate)
}

// hashString returns a FNV-1a hash of a string for use in name generation.
// It's not cryptographic, just for deterministic uniqueness.
func hashString(s string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return h.Sum64()
}

// refItem describes a single Secret or ConfigMap reference in a spec.
type refItem struct {
	kind string // "Secret" or "ConfigMap"
	name string
}

// extractSpecRefs returns all Secret/ConfigMap references in the spec,
// deduplicating by kind and name. Used by ref-walking logic to enumerate refs.
func extractSpecRefs(spec *gameplanev1alpha1.GameServerSpec) []refItem {
	var refs []refItem
	seen := make(map[string]bool)

	// Traverse spec.env for value references (Secret/ConfigMap in ValueFrom)
	for _, env := range spec.Env {
		if env.ValueFrom == nil {
			continue
		}

		// Check for SecretKeyRef
		if env.ValueFrom.SecretKeyRef != nil {
			key := secretRefKind + "/" + env.ValueFrom.SecretKeyRef.Name
			if !seen[key] {
				seen[key] = true
				refs = append(refs, refItem{
					kind: secretRefKind,
					name: env.ValueFrom.SecretKeyRef.Name,
				})
			}
		}

		// Check for ConfigMapKeyRef
		if env.ValueFrom.ConfigMapKeyRef != nil {
			key := configMapRefKind + "/" + env.ValueFrom.ConfigMapKeyRef.Name
			if !seen[key] {
				seen[key] = true
				refs = append(refs, refItem{
					kind: configMapRefKind,
					name: env.ValueFrom.ConfigMapKeyRef.Name,
				})
			}
		}
	}

	// Traverse spec.networking.tunnel.credentialsSecretRef
	if spec.Networking.Tunnel != nil &&
		spec.Networking.Tunnel.CredentialsSecretRef != nil {
		ref := spec.Networking.Tunnel.CredentialsSecretRef
		key := "Secret/" + ref.Name
		if !seen[key] {
			seen[key] = true
			refs = append(refs, refItem{
				kind: secretRefKind,
				name: ref.Name,
			})
		}
	}

	// Traverse spec.backupPolicy.repoRef
	if spec.BackupPolicy != nil && spec.BackupPolicy.RepoRef.Name != "" {
		ref := &spec.BackupPolicy.RepoRef
		key := "Secret/" + ref.Name
		if !seen[key] {
			seen[key] = true
			refs = append(refs, refItem{
				kind: secretRefKind,
				name: ref.Name,
			})
		}
	}

	return refs
}
