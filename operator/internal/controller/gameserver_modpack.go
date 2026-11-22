package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path"
	"reflect"
	"regexp"
	"sort"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Spec 035 (specs/035-curseforge-modpacks/contracts/operator-modpack.md).
//
// The API writes GameServer.spec.modpack and never touches spec.env. The
// operator owns two things here: rendering the pack's env into the game
// container (rule 1) and the change state machine that moves
// status.modpack through Pending -> BackingUp -> Applying -> Applied. Key
// sharing (rule 5) is not in this change (OD-9: the key reaches pods through
// apiKeyEnv/secretKeyRef, handled elsewhere).

// modpackProviders returns the template's registry providers that declare a
// modpacks block, in template order.
func modpackProviders(tmpl *gameplanev1alpha1.GameTemplate) []gameplanev1alpha1.ModProvider {
	if tmpl == nil || tmpl.Spec.Capabilities == nil || tmpl.Spec.Capabilities.Mods == nil ||
		tmpl.Spec.Capabilities.Mods.Registry == nil {
		return nil
	}
	var out []gameplanev1alpha1.ModProvider
	for _, p := range tmpl.Spec.Capabilities.Mods.Registry.Providers {
		if p.Modpacks != nil {
			out = append(out, p)
		}
	}
	return out
}

// modpackDeclaredEnvNames is every env name any provider's modpacks block owns:
// refEnv, versionEnv, apiKeyEnv and each fixed env[].name.
func modpackDeclaredEnvNames(tmpl *gameplanev1alpha1.GameTemplate) map[string]struct{} {
	names := map[string]struct{}{}
	for _, p := range modpackProviders(tmpl) {
		for _, n := range []string{p.Modpacks.RefEnv, p.Modpacks.VersionEnv, p.Modpacks.APIKeyEnv} {
			if n != "" {
				names[n] = struct{}{}
			}
		}
		for _, e := range p.Modpacks.Env {
			names[e.Name] = struct{}{}
		}
	}
	return names
}

const (
	// modpackRequestAnnotation is stamped on the Backup of a change with the
	// removal or install request id (empty when absent), so a retry that only changes
	// the remove annotation is told apart from the attempt that failed.
	modpackRequestAnnotation = "gameplane.local/modpack-request-id"
	// modpackBackupLabel marks Backups created by a modpack change.
	modpackBackupLabel = "gameplane.local/modpack-change"
	// backupDestinationLabel is the discovery label of a restic destination
	// Secret (api/internal/handlers/destinations.go); keys: repo, password.
	backupDestinationLabel = "gameplane.local/backup-destination"
	// modpackWipeInitName is the one-shot init container that empties the mods mount.
	modpackWipeInitName = "modpack-wipe"
	// modpackWipeMarkerPrefix + generation names the marker file in the mods mount.
	modpackWipeMarkerPrefix = ".gameplane-mods-wiped-"
	modpackMessageMax       = 512
)

var modpackProjectRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// modpackRemoval is the JSON in the gameplane.local/modpack-remove annotation.
type modpackRemoval struct {
	RequestID   string `json:"requestId"`
	BackupFirst bool   `json:"backupFirst"`
}

// modpackRemovalRequest parses the remove annotation. ok is false when it is
// absent. Malformed JSON still counts as a removal request, without a backup,
// with the raw text as its id.
func modpackRemovalRequest(gs *gameplanev1alpha1.GameServer) (req modpackRemoval, ok bool) {
	raw := gs.Annotations[gameplanev1alpha1.ModpackRemoveAnnotation]
	if raw == "" {
		return modpackRemoval{}, false
	}
	if err := json.Unmarshal([]byte(raw), &req); err != nil {
		return modpackRemoval{RequestID: raw}, true
	}
	return req, true
}

// modpackWantsBackup reports whether the current change asks for a backup first.
func modpackWantsBackup(gs *gameplanev1alpha1.GameServer) bool {
	if gs.Spec.Modpack != nil {
		return gs.Spec.Modpack.BackupFirst
	}
	req, ok := modpackRemovalRequest(gs)
	return ok && req.BackupFirst
}

// modpackRequestID is the removal request id when a removal is requested,
// otherwise spec.modpack.requestId, the id the API sets on each install ("" when
// absent).
func modpackRequestID(gs *gameplanev1alpha1.GameServer) string {
	if req, ok := modpackRemovalRequest(gs); ok {
		return req.RequestID
	}
	if gs.Spec.Modpack == nil {
		return ""
	}
	return gs.Spec.Modpack.RequestID
}

// modpackLegacyRender reports that the pod must keep rendering the pre-035 env
// (spec.env verbatim, no pack env): the server is adopted and the pack is not
// applied yet, or an adopted server's change is still waiting on its backup.
// Rendering switches the moment the change reaches Applying.
func modpackLegacyRender(gs *gameplanev1alpha1.GameServer) bool {
	if gs.Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation] != "true" {
		return false
	}
	st := gs.Status.Modpack
	if st == nil {
		return true
	}
	switch st.Phase {
	case gameplanev1alpha1.ModpackPhasePending,
		gameplanev1alpha1.ModpackPhaseBackingUp,
		gameplanev1alpha1.ModpackPhaseBackupFailed:
		return true
	}
	return false
}

// modpackAdoptedHold reports an adopted server whose pin is still held back:
// adopted annotation, spec.modpack recorded by the API sweep, nothing applied
// yet, and no removal requested.
func modpackAdoptedHold(gs *gameplanev1alpha1.GameServer) bool {
	if gs.Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation] != "true" ||
		gs.Status.Modpack != nil || gs.Spec.Modpack == nil {
		return false
	}
	_, removing := modpackRemovalRequest(gs)
	return !removing
}

// modpackAdoptedRemovalRef recovers the identity of an adopted, never-pinned
// pack from spec.env when its removal is requested (status.modpack is nil, so
// there is no applied record). Nil when the server is not adopted, no removal
// is requested, or no provider's refEnv has a value.
func modpackAdoptedRemovalRef(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) *gameplanev1alpha1.ModpackRef {
	if gs.Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation] != "true" {
		return nil
	}
	if _, removing := modpackRemovalRequest(gs); !removing {
		return nil
	}
	for _, p := range modpackProviders(tmpl) {
		if p.Modpacks.RefEnv == "" {
			continue
		}
		for _, e := range gs.Spec.Env {
			if e.Name == p.Modpacks.RefEnv && e.Value != "" {
				project := e.Value
				if !modpackProjectRe.MatchString(project) {
					project = "unknown" // refEnv may hold a URL; the CRD pattern forbids it
				}
				return &gameplanev1alpha1.ModpackRef{Provider: p.Provider, Project: project}
			}
		}
	}
	return nil
}

// modpackSpecMatchesApplied reports that spec.modpack equals status.modpack.applied
// (both absent counts as equal).
func modpackSpecMatchesApplied(gs *gameplanev1alpha1.GameServer) bool {
	var applied *gameplanev1alpha1.ModpackRef
	if gs.Status.Modpack != nil {
		applied = gs.Status.Modpack.Applied
	}
	if gs.Spec.Modpack == nil || applied == nil {
		return gs.Spec.Modpack == nil && applied == nil
	}
	return gs.Spec.Modpack.ModpackRef == *applied
}

// modpackManaged reports whether the operator owns this server's pack env.
// A server that has never had spec.modpack and has no status.modpack is a
// legacy server whose pack env still lives in spec.env (written by the
// pre-035 API); it must render exactly as before so the first reconcile after
// an operator upgrade never rolls its pod. status.modpack, once created, is
// never dropped, so a removed pack (applied == nil) stays managed and its
// stale spec.env entries cannot come back. An adopted server keeps the legacy
// rendering until its pin is applied (modpackLegacyRender).
func modpackManaged(gs *gameplanev1alpha1.GameServer) bool {
	if modpackLegacyRender(gs) {
		return false
	}
	return gs.Spec.Modpack != nil || gs.Status.Modpack != nil
}

// modpackSpecEnv returns spec.env as the game container sees it: unchanged for
// a legacy server, otherwise without any entry named like a modpack-declared
// env, so a stale MODRINTH_MODPACK / CF_SLUG / TYPE=MODRINTH left in spec.env
// can never run a second pack (FR-004).
//
// Only spec.env is filtered, not the whole merged env: template and version
// env legitimately define names that are also fixed pack env (every
// minecraft-java version sets TYPE=PAPER/FORGE/...), and removing a pack must
// leave that loader TYPE in place.
func modpackSpecEnv(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) []corev1.EnvVar {
	if !modpackManaged(gs) {
		return gs.Spec.Env
	}
	declared := modpackDeclaredEnvNames(tmpl)
	out := make([]corev1.EnvVar, 0, len(gs.Spec.Env))
	for _, e := range gs.Spec.Env {
		if _, drop := declared[e.Name]; !drop {
			out = append(out, e)
		}
	}
	return out
}

// modpackPackEnv is the env of the pack the pod renders now
// (status.modpack.applied): the provider's fixed env, refEnv=project and,
// when a version is pinned, versionEnv=version. Nil for a legacy server, no
// applied pack, or an applied provider the template no longer declares with
// modpacks. It is appended after spec.env so it wins on duplicate names.
func modpackPackEnv(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) []corev1.EnvVar {
	if gs.Status.Modpack == nil || gs.Status.Modpack.Applied == nil || modpackLegacyRender(gs) {
		return nil
	}
	applied := gs.Status.Modpack.Applied
	for _, p := range modpackProviders(tmpl) {
		if p.Provider != applied.Provider {
			continue
		}
		mp := p.Modpacks
		out := make([]corev1.EnvVar, 0, len(mp.Env)+2)
		for i := range mp.Env {
			out = append(out, *mp.Env[i].DeepCopy())
		}
		if mp.RefEnv != "" {
			out = append(out, corev1.EnvVar{Name: mp.RefEnv, Value: applied.Project})
		}
		if mp.VersionEnv != "" && applied.Version != "" {
			out = append(out, corev1.EnvVar{Name: mp.VersionEnv, Value: applied.Version})
		}
		return out
	}
	return nil
}

// modpackStart decides whether spec.modpack differs from what the pod renders
// (status.modpack.applied) and, if so, returns the next status. changed is
// false when nothing needs to move. Pure.
//
// backupName "" starts the change as Applying (applied = desired, the pod
// renders it); a non-empty backupName parks it in BackingUp with applied
// unchanged, so the old pack keeps rendering until the backup succeeds.
// When spec has gone back to what is applied while a change was waiting
// (Pending/BackingUp/BackupFailed) the change is abandoned: phase Applied.
// observedGeneration records the generation of the change handled, so it
// moves only when a change starts.
func modpackStart(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, backupName string,
) (next *gameplanev1alpha1.GameServerModpackStatus, changed bool) {
	var desired *gameplanev1alpha1.ModpackRef
	if gs.Spec.Modpack != nil {
		ref := gs.Spec.Modpack.ModpackRef
		desired = &ref
	}
	cur := gs.Status.Modpack
	var applied *gameplanev1alpha1.ModpackRef
	next = &gameplanev1alpha1.GameServerModpackStatus{}
	switch {
	case cur != nil:
		applied = cur.Applied
		next = cur.DeepCopy()
	case desired == nil:
		// An adopted pack removed before it was ever pinned has no applied
		// record; recover its identity from spec.env (rule 2 + removal).
		applied = modpackAdoptedRemovalRef(gs, tmpl)
		if applied == nil {
			return nil, false
		}
	}
	if reflect.DeepEqual(desired, applied) {
		switch next.Phase {
		case gameplanev1alpha1.ModpackPhasePending,
			gameplanev1alpha1.ModpackPhaseBackingUp,
			gameplanev1alpha1.ModpackPhaseBackupFailed:
			next.Phase = gameplanev1alpha1.ModpackPhaseApplied
			next.BackupName = ""
			next.Message = ""
			return next, true
		}
		return next, false
	}
	next.ObservedGeneration = gs.Generation
	next.Message = ""
	if backupName != "" {
		next.Applied = applied
		next.Phase = gameplanev1alpha1.ModpackPhaseBackingUp
		next.BackupName = backupName
		next.Target = desired
		next.RequestID = modpackRequestID(gs)
		return next, true
	}
	next.Target = nil
	next.RequestID = ""
	next.Applied = desired
	next.Phase = gameplanev1alpha1.ModpackPhaseApplying
	next.BackupName = ""
	return next, true
}

// modpackReadyCondition derives ModpackReady from status.modpack.phase: True
// when Applied, otherwise False with the phase as Reason. Nil (no condition)
// while the server has never had a managed pack.
func modpackReadyCondition(gs *gameplanev1alpha1.GameServer) *metav1.Condition {
	st := gs.Status.Modpack
	if st == nil || st.Phase == "" {
		return nil
	}
	c := &metav1.Condition{
		Type:               gameplanev1alpha1.GameServerConditionModpackReady,
		ObservedGeneration: gs.Generation,
		Reason:             string(st.Phase),
	}
	if st.Phase == gameplanev1alpha1.ModpackPhaseApplied {
		c.Status = metav1.ConditionTrue
		c.Message = "the modpack is applied"
		return c
	}
	c.Status = metav1.ConditionFalse
	c.Message = st.Message
	if c.Message == "" {
		c.Message = "the modpack change is " + string(st.Phase)
	}
	return c
}

// statefulSetRolledOut reports that the StatefulSet controller has seen the
// latest pod template and every pod is on the update revision. Ready is
// deliberately not required: a pack whose install blocks on a manual download
// crash-loops, and the change must still read Applied so the operator can pick
// another version.
func statefulSetRolledOut(ss *appsv1.StatefulSet) bool {
	return ss.Generation > 0 &&
		ss.Status.ObservedGeneration >= ss.Generation &&
		ss.Status.UpdateRevision != "" &&
		ss.Status.UpdatedReplicas == ss.Status.Replicas
}

// patchModpackStatus stores next in gs.Status.Modpack, refreshes the
// ModpackReady condition and issues a JSON merge patch of only status. The
// patch target is a copy so the caller's gs keeps the in-memory values the
// rest of this reconcile (env rendering, reconcileStatus' own base copy) reads,
// the same pattern as patchCaptureStatus.
func (r *GameServerReconciler) patchModpackStatus(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, next *gameplanev1alpha1.GameServerModpackStatus,
) error {
	base := gs.DeepCopy()
	gs.Status.Modpack = next
	if c := modpackReadyCondition(gs); c != nil {
		gs.Status.Conditions = upsertCondition(gs.Status.Conditions, *c)
	}
	if reflect.DeepEqual(base.Status, gs.Status) {
		return nil
	}
	if err := r.Status().Patch(ctx, gs.DeepCopy(), client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch modpack status for %s: %w", gs.Name, err)
	}
	return nil
}

// reconcileModpackChange moves a modpack change forward. It runs before the
// StatefulSet is built, and updates gs in memory, so the same pass renders the
// new applied pack into the pod template.
//
// Order: (1) a change parked in BackingUp/BackupFailed is driven first;
// (2) an adopted server's pin stays held until the pod is stopped or no
// players are online (rule 2, OD-8 B); (3) a new change starts, creating a
// Backup first when the change asks for one and a destination exists.
func (r *GameServerReconciler) reconcileModpackChange(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	if st := gs.Status.Modpack; st != nil &&
		(st.Phase == gameplanev1alpha1.ModpackPhaseBackingUp || st.Phase == gameplanev1alpha1.ModpackPhaseBackupFailed) {
		done, err := r.driveModpackBackup(ctx, gs, tmpl)
		if err != nil || done {
			return err
		}
	}
	if modpackAdoptedHold(gs) {
		released, err := r.modpackHoldReleased(ctx, gs)
		if err != nil || !released {
			return err
		}
	}
	first, changed := modpackStart(gs, tmpl, "")
	if !changed {
		return nil
	}
	if first.Phase == gameplanev1alpha1.ModpackPhaseApplying && modpackWantsBackup(gs) {
		name, err := r.createModpackBackup(ctx, gs)
		if err != nil {
			return err
		}
		if name != "" {
			next, _ := modpackStart(gs, tmpl, name)
			return r.patchModpackStatus(ctx, gs, next)
		}
	}
	return r.patchModpackStatus(ctx, gs, first)
}

// driveModpackBackup advances a change parked in BackingUp or BackupFailed.
// done is true when this pass must not start or apply anything else. It
// returns done=false when the change was superseded (spec.modpack or the
// request id moved on from the recorded target, or the Backup is gone) so the
// caller restarts the flow from the top.
func (r *GameServerReconciler) driveModpackBackup(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) (done bool, err error) {
	st := gs.Status.Modpack
	if st.BackupName == "" || st.RequestID != modpackRequestID(gs) {
		return false, nil
	}
	// Superseded only when the desired pack moved on from what this change
	// started from; unrelated spec edits (env, resources, suspend) bump the
	// generation without changing the change.
	var desired *gameplanev1alpha1.ModpackRef
	if gs.Spec.Modpack != nil {
		ref := gs.Spec.Modpack.ModpackRef
		desired = &ref
	}
	if !reflect.DeepEqual(desired, st.Target) {
		return false, nil
	}
	var b gameplanev1alpha1.Backup
	switch err := r.wipeReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: st.BackupName}, &b); {
	case apierrors.IsNotFound(err):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("get modpack backup %s/%s: %w", gs.Namespace, st.BackupName, err)
	}
	if b.Annotations[modpackRequestAnnotation] != modpackRequestID(gs) {
		return false, nil
	}
	if st.Phase == gameplanev1alpha1.ModpackPhaseBackupFailed {
		return true, nil // keep applied + the remove annotation; a new change or request id retries
	}
	switch b.Status.Phase {
	case gameplanev1alpha1.BackupPhaseSucceeded:
		next, changed := modpackStart(gs, tmpl, "")
		if !changed {
			return false, nil
		}
		next.BackupName = st.BackupName
		return true, r.patchModpackStatus(ctx, gs, next)
	case gameplanev1alpha1.BackupPhaseFailed:
		next := st.DeepCopy()
		next.Phase = gameplanev1alpha1.ModpackPhaseBackupFailed
		next.Message = b.Status.Message
		if next.Message == "" {
			next.Message = "the backup before the modpack change failed"
		}
		if m := []rune(next.Message); len(m) > modpackMessageMax {
			next.Message = string(m[:modpackMessageMax])
		}
		return true, r.patchModpackStatus(ctx, gs, next)
	}
	return true, nil // Pending/Running/unset: keep waiting; the Backup watch wakes us
}

// modpackBackupRepo returns the name of the namespace's backup destination
// Secret (first by name), or "" when there is none.
func (r *GameServerReconciler) modpackBackupRepo(ctx context.Context, namespace string) (string, error) {
	var list corev1.SecretList
	if err := r.wipeReader().List(ctx, &list, client.InNamespace(namespace),
		client.MatchingLabels{backupDestinationLabel: "true"}); err != nil {
		return "", fmt.Errorf("list backup destinations in %s: %w", namespace, err)
	}
	if len(list.Items) == 0 {
		return "", nil
	}
	names := make([]string, 0, len(list.Items))
	for i := range list.Items {
		names = append(names, list.Items[i].Name)
	}
	sort.Strings(names)
	return names[0], nil
}

// createModpackBackup creates (or finds) the Backup for the current change and
// returns its name; "" with a nil error when the namespace has no destination
// (backupFirst is then skipped). The name is <gs>-modpack-<generation>; a retry
// of a removal at the same generation, told apart by request id, gets a
// request-id suffix instead.
func (r *GameServerReconciler) createModpackBackup(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) (string, error) {
	repo, err := r.modpackBackupRepo(ctx, gs.Namespace)
	if err != nil || repo == "" {
		return "", err
	}
	requestID := modpackRequestID(gs)
	name := fmt.Sprintf("%s-modpack-%d", gs.Name, gs.Generation)
	var existing gameplanev1alpha1.Backup
	switch err := r.wipeReader().Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: name}, &existing); {
	case err == nil:
		if existing.Annotations[modpackRequestAnnotation] == requestID {
			return name, nil
		}
		sum := sha256.Sum256([]byte(requestID))
		name += "-" + hex.EncodeToString(sum[:])[:8]
	case !apierrors.IsNotFound(err):
		return "", fmt.Errorf("get modpack backup %s/%s: %w", gs.Namespace, name, err)
	}
	b := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: gs.Namespace,
			Labels:    map[string]string{modpackBackupLabel: "true"},
		},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: gs.Name},
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: repo, Key: "repo"},
			// No omitempty on the field: an unset false would override the CRD default.
			Quiesce: true,
		},
	}
	if requestID != "" {
		b.Annotations = map[string]string{modpackRequestAnnotation: requestID}
	}
	if err := controllerutil.SetControllerReference(gs, b, r.Scheme); err != nil {
		return "", fmt.Errorf("own modpack backup %s: %w", name, err)
	}
	if err := r.Create(ctx, b); err != nil {
		if apierrors.IsAlreadyExists(err) {
			return name, nil // an earlier pass created it before its status patch landed
		}
		return "", fmt.Errorf("create modpack backup %s: %w", name, err)
	}
	return name, nil
}

// modpackHoldReleased reports whether an adopted server's pin may be applied now
// (OD-8 B): right away when the server is stopped or asleep (no StatefulSet, or
// live spec.replicas == 0), otherwise only when the pack has a version and the
// agent reports a known player count of 0. An unknown count never releases.
func (r *GameServerReconciler) modpackHoldReleased(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	return r.modpackServerQuiet(ctx, gs, true)
}

// modpackServerQuiet reports whether a pod template change may roll the server
// now (OD-8 B, OD-10 A): right away when stopped or asleep (no StatefulSet, or
// live replicas 0), otherwise only on a known player count of 0. An unknown
// count never counts as quiet. needVersion additionally requires the adopted
// pack to have a version (OD-8 only).
func (r *GameServerReconciler) modpackServerQuiet(ctx context.Context, gs *gameplanev1alpha1.GameServer, needVersion bool) (bool, error) {
	var ss appsv1.StatefulSet
	switch err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); {
	case apierrors.IsNotFound(err):
		return true, nil
	case err != nil:
		return false, fmt.Errorf("get StatefulSet for modpack restart gate of %s: %w", gs.Name, err)
	}
	if ss.Spec.Replicas != nil && *ss.Spec.Replicas == 0 {
		return true, nil
	}
	if needVersion && gs.Spec.Modpack.Version == "" {
		return false, nil
	}
	a := gs.Status.Agent
	return a != nil && a.PlayersOnline != nil && *a.PlayersOnline == 0, nil
}

// reconcileModpackRollout finishes a change: Applying becomes Applied once the
// StatefulSet has rolled the new pod template. It runs after the StatefulSet is
// written and reads it live, because the informer cache may still hold the
// pre-write object and would report an old, finished rollout.
func (r *GameServerReconciler) reconcileModpackRollout(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	st := gs.Status.Modpack
	if st == nil || st.Phase != gameplanev1alpha1.ModpackPhaseApplying {
		return nil
	}
	var ss appsv1.StatefulSet
	if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &ss); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("get StatefulSet for modpack rollout of %s: %w", gs.Name, err)
	}
	if !statefulSetRolledOut(&ss) {
		return nil
	}
	next := st.DeepCopy()
	next.Phase = gameplanev1alpha1.ModpackPhaseApplied
	return r.patchModpackStatus(ctx, gs, next)
}

// reconcileModpackAnnotations runs after the StatefulSet is written. It drops
// the adopted annotation once the pin (or removal) is rendered (Applying or
// Applied), and the remove annotation once the removal is Applied. A removal
// whose backup failed keeps it (phase BackupFailed) so the API can retry.
func (r *GameServerReconciler) reconcileModpackAnnotations(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	st := gs.Status.Modpack
	if st == nil {
		return nil
	}
	var drop []string
	if _, ok := gs.Annotations[gameplanev1alpha1.ModpackAdoptedAnnotation]; ok &&
		(st.Phase == gameplanev1alpha1.ModpackPhaseApplying || st.Phase == gameplanev1alpha1.ModpackPhaseApplied) {
		drop = append(drop, gameplanev1alpha1.ModpackAdoptedAnnotation)
	}
	if _, ok := modpackRemovalRequest(gs); ok &&
		st.Phase == gameplanev1alpha1.ModpackPhaseApplied && modpackSpecMatchesApplied(gs) {
		drop = append(drop, gameplanev1alpha1.ModpackRemoveAnnotation)
	}
	if len(drop) == 0 {
		return nil
	}
	base := gs.DeepCopy()
	patched := gs.DeepCopy()
	for _, k := range drop {
		delete(patched.Annotations, k)
	}
	if err := r.Patch(ctx, patched, client.MergeFrom(base)); err != nil {
		return fmt.Errorf("clear modpack annotations on %s: %w", gs.Name, err)
	}
	for _, k := range drop {
		delete(gs.Annotations, k)
	}
	return nil
}

// modpackWipeInit is the one-shot init step of a removal (R5): it empties the
// active loader's mods mount (and only that mount, never the data volume) unless
// the marker file for this removal's generation already exists. Rendered while
// status.modpack has no applied pack and the removal is Applying/Applied; nil
// otherwise, so installing or switching packs never wipes. Nil too when the
// active version has no per-loader mod volume (nothing safe to empty).
func modpackWipeInit(
	image string, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	ver *gameplanev1alpha1.GameVersion, defaults ...*corev1.ResourceRequirements,
) *corev1.Container {
	st := gs.Status.Modpack
	if st == nil || st.Applied != nil ||
		(st.Phase != gameplanev1alpha1.ModpackPhaseApplying && st.Phase != gameplanev1alpha1.ModpackPhaseApplied) {
		return nil
	}
	mount := modVolumeMount(tmpl, ver)
	if mount == nil {
		return nil
	}
	marker := path.Join(mount.MountPath, fmt.Sprintf("%s%d", modpackWipeMarkerPrefix, st.ObservedGeneration))
	return &corev1.Container{
		Name:      modpackWipeInitName,
		Image:     configInitImageOrDefault(image),
		Resources: auxiliaryResources(optionalResources(defaults), "25m", "64Mi"),
		Command:   []string{"/bin/sh", "-c"},
		// $1 = mods mount, $2 = marker. Paths are positional args, never interpolated.
		Args: []string{
			`[ -e "$2" ] && exit 0; find "$1" -mindepth 1 -maxdepth 1 -exec rm -rf {} + && touch "$2"`,
			modpackWipeInitName, mount.MountPath, marker,
		},
		VolumeMounts: []corev1.VolumeMount{{Name: mount.Name, MountPath: mount.MountPath}},
	}
}
