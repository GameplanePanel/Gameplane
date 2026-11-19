package controller

import (
	"context"
	"fmt"
	"reflect"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Spec 035 (specs/035-curseforge-modpacks/contracts/operator-modpack.md).
//
// The API writes GameServer.spec.modpack and never touches spec.env. The
// operator owns two things here: rendering the pack's env into the game
// container (rule 1) and the change state machine that moves
// status.modpack through Pending -> Applying -> Applied. Backups (BackingUp,
// BackupFailed), the mods wipe, adopted-pack rendering (rule 2) and key
// sharing arrive in later tasks.

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

// modpackManaged reports whether the operator owns this server's pack env.
// A server that has never had spec.modpack and has no status.modpack is a
// legacy server whose pack env still lives in spec.env (written by the
// pre-035 API); it must render exactly as before so the first reconcile after
// an operator upgrade never rolls its pod. status.modpack, once created, is
// never dropped, so a removed pack (applied == nil) stays managed and its
// stale spec.env entries cannot come back.
func modpackManaged(gs *gameplanev1alpha1.GameServer) bool {
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
	if gs.Status.Modpack == nil || gs.Status.Modpack.Applied == nil {
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
// (status.modpack.applied) and, if so, returns the next status: Applying with
// the new snapshot. changed is false when nothing needs to move. Pure.
//
// Without backups there is no wait between Pending and Applying, so Pending is
// never persisted in this batch; the backup step (T032) will park a change in
// Pending/BackingUp before Applying. observedGeneration records the generation
// of the change handled, so it moves only when a change starts (unrelated spec
// edits bump metadata.generation without touching it).
func modpackStart(gs *gameplanev1alpha1.GameServer) (next *gameplanev1alpha1.GameServerModpackStatus, changed bool) {
	var desired *gameplanev1alpha1.ModpackRef
	if gs.Spec.Modpack != nil {
		ref := gs.Spec.Modpack.ModpackRef
		desired = &ref
	}
	cur := gs.Status.Modpack
	if cur == nil && desired == nil {
		return nil, false
	}
	var applied *gameplanev1alpha1.ModpackRef
	next = &gameplanev1alpha1.GameServerModpackStatus{}
	if cur != nil {
		applied = cur.Applied
		next = cur.DeepCopy()
	}
	if reflect.DeepEqual(desired, applied) {
		return next, false
	}
	next.Applied = desired
	next.Phase = gameplanev1alpha1.ModpackPhaseApplying
	next.ObservedGeneration = gs.Generation
	next.BackupName = ""
	next.Message = ""
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

// reconcileModpackChange starts a modpack change. It runs before the
// StatefulSet is built, and updates gs in memory, so the same pass renders the
// new applied pack into the pod template.
func (r *GameServerReconciler) reconcileModpackChange(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	next, changed := modpackStart(gs)
	if !changed {
		return nil
	}
	return r.patchModpackStatus(ctx, gs, next)
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
