package controller

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const lifecycleOperationAnnotation = "operations.gameplane.local/owner"

var errLifecycleGenerationChanged = errors.New("lifecycle operation spec changed after admission")

// The GameServer is the arbitration object. A wipe has no separate CR, so its
// request token and GameServer UID identify the operation. Generation is pinned
// at admission; subsequent power requests must not change that identity.
type lifecycleIdentity struct {
	Kind           string    `json:"kind"`
	Name           string    `json:"name"`
	UID            types.UID `json:"uid"`
	TargetUID      types.UID `json:"targetUID"`
	Generation     int64     `json:"generation"`
	Token          string    `json:"token,omitempty"`
	NeedsUnquiesce bool      `json:"needsUnquiesce,omitempty"`
	QuiescePodUID  types.UID `json:"quiescePodUID,omitempty"`
	// InitialStartup permits only the first workload of a CSI restore's new
	// target (including WaitForFirstConsumer PVCs), while other operations wait.
	InitialStartup bool `json:"initialStartup,omitempty"`
	// Baseline establishes a first template pin without replacing an existing
	// workload. Durable intent prevents a leader restart turning it into a drain.
	Baseline bool `json:"baseline,omitempty"`
}

type lifecycleOperation struct {
	lifecycleIdentity
	// Recovery survives deletion of a successor while it is draining an older
	// owner's workers. No new work is authorized until every entry is cleaned.
	Recovery []lifecycleIdentity `json:"recovery,omitempty"`
}

func lifecycleOperationJSON(op lifecycleOperation) string {
	value, _ := json.Marshal(op)
	return string(value)
}

func parseLifecycleOperation(value string) (lifecycleOperation, error) {
	var op lifecycleOperation
	if err := json.Unmarshal([]byte(value), &op); err != nil {
		return op, fmt.Errorf("decode lifecycle owner: %w", err)
	}
	for _, id := range append([]lifecycleIdentity{op.lifecycleIdentity}, op.Recovery...) {
		if id.Name == "" || id.UID == "" || id.TargetUID == "" || (id.Kind != "Backup" && id.Kind != "Restore" && id.Kind != "Wipe" && id.Kind != "Rollout") || (id.Kind == "Wipe" && id.Token == "") || (id.Kind == "Rollout" && !validTemplateRevision(id.Token)) {
			return op, fmt.Errorf("invalid lifecycle owner identity")
		}
		if id.TargetUID != op.TargetUID {
			return op, fmt.Errorf("lifecycle recovery target changed identity")
		}
	}
	return op, nil
}

func backupLifecycleIdentity(gs *gameplanev1alpha1.GameServer, b *gameplanev1alpha1.Backup, quiesce bool) lifecycleIdentity {
	return lifecycleIdentity{Kind: "Backup", Name: b.Name, UID: b.UID, TargetUID: gs.UID, Generation: b.Generation, NeedsUnquiesce: quiesce}
}

func restoreLifecycleIdentity(gs *gameplanev1alpha1.GameServer, rs *gameplanev1alpha1.Restore) lifecycleIdentity {
	return lifecycleIdentity{Kind: "Restore", Name: rs.Name, UID: rs.UID, TargetUID: gs.UID, Generation: rs.Generation}
}

func wipeLifecycleIdentity(gs *gameplanev1alpha1.GameServer, token string) lifecycleIdentity {
	return lifecycleIdentity{Kind: "Wipe", Name: gs.Name, UID: gs.UID, TargetUID: gs.UID, Generation: gs.Generation, Token: token}
}

func sameLifecycleOwner(a, b lifecycleIdentity) bool {
	return a.Kind == b.Kind && a.Name == b.Name && a.UID == b.UID && a.TargetUID == b.TargetUID && a.Token == b.Token
}

// Existing guards remain readable during upgrades and provide the compatibility
// markers used by stop/drain cleanup. They are never independent permission to
// start new work. Contradictory legacy guards fail closed until their owners
// complete cleanup; migration must not select one concurrent writer as safe.
func lifecycleOwner(gs *gameplanev1alpha1.GameServer) (lifecycleOperation, bool, error) {
	if value := gs.Annotations[lifecycleOperationAnnotation]; value != "" {
		op, err := parseLifecycleOperation(value)
		if err == nil && op.TargetUID != gs.UID {
			err = fmt.Errorf("lifecycle owner belongs to a replaced GameServer")
		}
		return op, true, err
	}
	op := lifecycleOperation{}
	count := 0
	if value := gs.Annotations[backupGuardAnnotation]; value != "" {
		id, err := parseBackupIdentity(value)
		if err != nil {
			return op, true, err
		}
		op.lifecycleIdentity = lifecycleIdentity{Kind: "Backup", Name: id.Name, UID: id.UID, TargetUID: gs.UID, NeedsUnquiesce: true}
		lineage, err := parseBackupRecoveryLineage(gs.Annotations[backupRecoveryLineageAnnotation])
		if err != nil {
			return op, true, err
		}
		for _, predecessor := range lineage {
			op.Recovery = append(op.Recovery, lifecycleIdentity{Kind: "Backup", Name: predecessor.Name, UID: predecessor.UID, TargetUID: gs.UID, NeedsUnquiesce: true})
		}
		count++
	}
	if value := gs.Annotations[restoreGuardAnnotation]; value != "" {
		id, err := parseRestoreIdentity(value)
		if err != nil {
			return op, true, err
		}
		op.lifecycleIdentity = lifecycleIdentity{Kind: "Restore", Name: id.Name, UID: id.UID, TargetUID: gs.UID}
		count++
	}
	if token := gs.Annotations[wipeGuardAnnotation]; token != "" {
		op.lifecycleIdentity = wipeLifecycleIdentity(gs, token)
		count++
	}
	if count > 1 {
		return op, true, fmt.Errorf("overlapping legacy lifecycle owners require cleanup before new work")
	}
	return op, count != 0, nil
}

func lifecycleOwnerExists(ctx context.Context, reader client.Reader, namespace string, id lifecycleIdentity) (bool, error) {
	var obj client.Object
	switch id.Kind {
	case "Backup":
		obj = &gameplanev1alpha1.Backup{}
	case "Restore":
		obj = &gameplanev1alpha1.Restore{}
	case "Wipe", "Rollout":
		// Only the GameServer reconciler may cancel/replace a wipe token after
		// its workers drain. A removed request is not proof of worker absence.
		return true, nil
	default:
		return false, fmt.Errorf("unknown lifecycle operation %q", id.Kind)
	}
	err := reader.Get(ctx, client.ObjectKey{Namespace: namespace, Name: id.Name}, obj)
	if apierrors.IsNotFound(err) {
		return false, nil
	}
	return err == nil && obj.GetUID() == id.UID, err
}

// claimLifecycleOperation refreshes gs with the uncached reader and writes the
// shared owner plus compatibility guard in ONE resourceVersion-checked Update.
// Ownership is retained during recovery. A losing claimant performs no cleanup
// or worker creation. No timeout ever permits stealing from an existing owner.
func claimLifecycleOperation(ctx context.Context, c client.Client, reader client.Reader, gs *gameplanev1alpha1.GameServer, want lifecycleIdentity, quiescer AgentQuiescer) (bool, error) {
	var live gameplanev1alpha1.GameServer
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
		return false, err
	}
	if live.UID != want.TargetUID || !live.DeletionTimestamp.IsZero() {
		return false, fmt.Errorf("lifecycle target changed identity or is being deleted")
	}
	if want.Kind == "Rollout" && live.Generation != want.Generation {
		return false, errLifecycleGenerationChanged
	}
	*gs = live
	op, occupied, err := lifecycleOwner(gs)
	if err != nil {
		return false, err
	}
	if occupied && !sameLifecycleOwner(op.lifecycleIdentity, want) {
		exists, err := lifecycleOwnerExists(ctx, reader, gs.Namespace, op.lifecycleIdentity)
		if err != nil || exists {
			return false, err
		}
		op.Recovery = append(op.Recovery, op.lifecycleIdentity)
		// A force-deleted restore may have left partial data. Preserve its
		// suspension even when the next operation is a backup.
		if op.Kind == "Restore" {
			gs.Spec.Suspend = true
		}
		op.lifecycleIdentity = want
	} else if occupied {
		if gs.Annotations[lifecycleOperationAnnotation] != "" && op.Kind != "Wipe" && op.Kind != "Rollout" && op.Generation != want.Generation {
			return false, errLifecycleGenerationChanged
		}
		if gs.Annotations[lifecycleOperationAnnotation] == "" {
			op.lifecycleIdentity = want
		}
	} else {
		if want.Kind != "Wipe" {
			workers, err := wipeWorkers(ctx, reader, gs)
			if err != nil || pendingWipe(gs) || workers.busy {
				return false, err
			}
		}
		// A stop command already sent may still save data and exit. Waiting
		// here also lets pending restarts finish instead of freezing mid-stop.
		if want.Kind == "Backup" || want.Kind == "Rollout" {
			pending, err := backupLifecycleTransitionPending(ctx, reader, gs)
			if err != nil || pending {
				return false, err
			}
		}
		busy, err := legacyLifecycleCopiesLive(ctx, c, reader, gs, want)
		if err != nil || busy {
			return false, err
		}
		op = lifecycleOperation{lifecycleIdentity: want}
	}
	before := gs.DeepCopy()
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
	delete(gs.Annotations, backupGuardAnnotation)
	delete(gs.Annotations, restoreGuardAnnotation)
	delete(gs.Annotations, wipeGuardAnnotation)
	switch want.Kind {
	case "Backup":
		gs.Annotations[backupGuardAnnotation] = backupIdentityJSON(want.Name, want.UID)
	case "Restore":
		gs.Annotations[restoreGuardAnnotation] = restoreIdentityJSON(want.Name, want.UID)
		if !op.InitialStartup {
			gs.Spec.Suspend = true
		}
	case "Wipe":
		gs.Annotations[wipeGuardAnnotation] = want.Token
		if pendingWipe(gs) {
			gs.Spec.Suspend = true
		}
	}
	// An acquired owner can be re-read many times while its worker runs.
	// Avoid needless resourceVersion churn, but always CAS new admission.
	if before.Annotations[lifecycleOperationAnnotation] != gs.Annotations[lifecycleOperationAnnotation] || before.Spec.Suspend != gs.Spec.Suspend {
		if err := c.Update(ctx, gs); err != nil {
			return false, err
		}
	}
	return recoverLifecycleOperation(ctx, c, reader, gs, op, quiescer)
}

// New code must also respect workers started before shared ownership existed,
// including quiesce=false copies which had no old backup guard at all.
func legacyLifecycleCopiesLive(ctx context.Context, c client.Client, reader client.Reader, gs *gameplanev1alpha1.GameServer, want lifecycleIdentity) (bool, error) {
	var backups gameplanev1alpha1.BackupList
	if err := reader.List(ctx, &backups, client.InNamespace(gs.Namespace)); err != nil {
		return false, err
	}
	r := &BackupReconciler{Client: c, APIReader: reader, Scheme: c.Scheme()}
	for i := range backups.Items {
		b := &backups.Items[i]
		if b.Spec.ServerRef.Name != gs.Name || (want.Kind == "Backup" && b.UID == want.UID) {
			continue
		}
		if legacyBackupOwesUnquiesce(b) {
			return true, nil
		}
		busy, err := r.backupWorkersLive(ctx, gs.Namespace, backupIdentity{Name: b.Name, UID: b.UID}, false)
		if err != nil || busy {
			return busy, err
		}
	}
	return false, nil
}

func lifecycleRecoveryNeedsStop(op lifecycleOperation) bool {
	for _, predecessor := range op.Recovery {
		if predecessor.Kind == "Restore" {
			return true
		}
	}
	return false
}

func recoverLifecycleOperation(ctx context.Context, c client.Client, reader client.Reader, gs *gameplanev1alpha1.GameServer, op lifecycleOperation, quiescer AgentQuiescer) (bool, error) {
	for _, predecessor := range op.Recovery {
		var busy bool
		var err error
		switch predecessor.Kind {
		case "Backup":
			r := &BackupReconciler{Client: c, APIReader: reader, Scheme: c.Scheme()}
			busy, err = r.backupWorkersLive(ctx, gs.Namespace, backupIdentity{Name: predecessor.Name, UID: predecessor.UID}, true)
		case "Restore":
			r := &RestoreReconciler{Client: c, APIReader: reader, Scheme: c.Scheme()}
			busy, err = r.restoreWorkersLive(ctx, gs.Namespace, restoreIdentity{Name: predecessor.Name, UID: predecessor.UID}, true)
		default:
			return false, fmt.Errorf("unsupported orphan lifecycle owner %q", predecessor.Kind)
		}
		if err != nil || busy {
			return false, err
		}
	}
	if lifecycleRecoveryNeedsStop(op) {
		r := &RestoreReconciler{Client: c, APIReader: reader, Scheme: c.Scheme()}
		stopped, _, err := r.restoreTargetStopped(ctx, gs)
		if err != nil || !stopped {
			return false, err
		}
	}
	for _, predecessor := range op.Recovery {
		if !predecessor.NeedsUnquiesce {
			continue
		}
		gone, err := backupQuiesceProcessGone(ctx, reader, gs.Namespace, gs.Name, predecessor.QuiescePodUID)
		if err != nil {
			return false, err
		}
		if gone {
			continue
		}
		if !agentQuiesceAvailable(quiescer) {
			return false, fmt.Errorf("cannot recover orphaned quiesce without an agent client")
		}
		if err := callBackupQuiesce(ctx, quiescer, gs.Namespace, gs.Name, predecessor.QuiescePodUID, true); err != nil {
			return false, fmt.Errorf("recover orphaned lifecycle quiesce: %w", err)
		}
	}
	if len(op.Recovery) > 0 {
		op.Recovery = nil
		gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
		delete(gs.Annotations, backupRecoveryLineageAnnotation)
		if err := c.Update(ctx, gs); err != nil {
			return false, err
		}
	}
	return true, nil
}

func backupLifecycleTransitionPending(ctx context.Context, reader client.Reader, gs *gameplanev1alpha1.GameServer) (bool, error) {
	if tokenPending(gs, RestartRequestedAnnotation, RestartCompletedAnnotation) {
		return true, nil
	}
	if gs.Annotations[stopRequestedAtAnnotation] == "" {
		return false, nil
	}
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		return true, client.IgnoreNotFound(err)
	}
	if !metav1.IsControlledBy(&ss, gs) || ss.Spec.Replicas == nil || *ss.Spec.Replicas != 0 || ss.Status.Replicas != 0 || ss.Status.ObservedGeneration < ss.Generation {
		return true, nil
	}
	var pods corev1.PodList
	if err := reader.List(ctx, &pods, client.InNamespace(gs.Namespace)); err != nil {
		return true, err
	}
	for i := range pods.Items {
		if liveRestorePod(&pods.Items[i]) && (pods.Items[i].Name == gs.Name+"-0" || podMountsRestoreTarget(&pods.Items[i], gs.Name)) {
			return true, nil
		}
	}
	return false, nil
}

// Carry cleanup intent on the surviving arbitration object as well as Backup.
// A force-deleted Backup cannot hide a partial/ambiguous save-off RPC.
func (r *BackupReconciler) setLifecycleUnquiesceIntent(ctx context.Context, b *gameplanev1alpha1.Backup, needed bool) error {
	gs, err := r.ownedBackupTarget(ctx, b)
	if err != nil || gs == nil || gs.Annotations[lifecycleOperationAnnotation] == "" {
		return err
	}
	op, err := parseLifecycleOperation(gs.Annotations[lifecycleOperationAnnotation])
	if err != nil {
		return err
	}
	var podUID types.UID
	if needed {
		preparation, err := parseBackupPreparation(b)
		if err != nil {
			return err
		}
		if preparation != nil {
			podUID = preparation.PodUID
		}
	}
	if op.NeedsUnquiesce == needed && (!needed || op.QuiescePodUID == podUID) {
		return nil
	}
	op.NeedsUnquiesce = needed
	if needed {
		op.QuiescePodUID = podUID
	}
	gs.Annotations[lifecycleOperationAnnotation] = lifecycleOperationJSON(op)
	return r.Update(ctx, gs)
}

// fenceBackupWorkload performs a checked write to the workload before a copy
// can start. A stale workload Update either precedes this fence (and is fully
// observed before admission) or conflicts with it. StatefulSet mutation also
// reads the live owner inside its callback and preserves the whole pod template.
func fenceBackupWorkload(ctx context.Context, c client.Client, reader client.Reader, gs *gameplanev1alpha1.GameServer) (bool, error) {
	var ss appsv1.StatefulSet
	if err := reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		if apierrors.IsNotFound(err) {
			return false, nil // wait for the workload reconciler to create a fenced set
		}
		return false, err
	}
	if !metav1.IsControlledBy(&ss, gs) {
		return false, fmt.Errorf("backup StatefulSet is not controlled by the bound GameServer")
	}
	value := gs.Annotations[lifecycleOperationAnnotation]
	if value == "" {
		return false, fmt.Errorf("backup has no shared lifecycle owner")
	}
	if ss.Annotations[lifecycleOperationAnnotation] != value {
		if ss.Annotations == nil {
			ss.Annotations = map[string]string{}
		}
		ss.Annotations[lifecycleOperationAnnotation] = value
		if err := c.Update(ctx, &ss); err != nil {
			return false, err
		}
	}
	if ss.Status.ObservedGeneration < ss.Generation || (ss.Status.CurrentRevision != "" && ss.Status.CurrentRevision != ss.Status.UpdateRevision) {
		return false, nil
	}
	return true, nil
}

// clearLifecycleOperation prepares the release; the caller removes its legacy
// guard and commits the GameServer Update only AFTER worker/agent cleanup and a
// durable completion decision. Release never changes the caller's power intent.
func clearLifecycleOperation(ctx context.Context, c client.Client, reader client.Reader, gs *gameplanev1alpha1.GameServer, want lifecycleIdentity) error {
	value := gs.Annotations[lifecycleOperationAnnotation]
	if value == "" {
		return nil
	}
	op, err := parseLifecycleOperation(value)
	if err != nil {
		return err
	}
	if !sameLifecycleOwner(op.lifecycleIdentity, want) || len(op.Recovery) != 0 {
		return fmt.Errorf("cannot release lifecycle ownership before recovery completes")
	}
	var ss appsv1.StatefulSet
	err = reader.Get(ctx, client.ObjectKeyFromObject(gs), &ss)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	if err == nil && metav1.IsControlledBy(&ss, gs) && ss.Annotations[lifecycleOperationAnnotation] == value {
		delete(ss.Annotations, lifecycleOperationAnnotation)
		if err := c.Update(ctx, &ss); err != nil {
			return err
		}
	}
	delete(gs.Annotations, lifecycleOperationAnnotation)
	return nil
}
