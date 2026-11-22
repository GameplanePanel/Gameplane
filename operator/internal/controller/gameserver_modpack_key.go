package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// modpackKeyRecheck bounds how long a rotation, key deletion or share toggle on
// the managed Secret takes to be read by CurseForge modpack servers. The operator
// cannot watch that Secret (resourceNames-scoped get only, spec 035 contract
// item 7), so it reads the Secret on each reconcile and requeues at this
// interval. A toggle that rolls the pod also waits for the server's quiet
// moment (OD-10 A), which the status heartbeat watch re-checks.
const modpackKeyRecheck = 60 * time.Second

// modpackKeyRequeue is modpackKeyRecheck for a server with an applied
// CurseForge pack, else 0.
func modpackKeyRequeue(gs *gameplanev1alpha1.GameServer) time.Duration {
	if gs.Status.Modpack != nil && gs.Status.Modpack.Applied != nil &&
		gs.Status.Modpack.Applied.Provider == "curseforge" {
		return modpackKeyRecheck
	}
	return 0
}

// Spec 035 key sharing (rule 5, specs/035-curseforge-modpacks/contracts/
// operator-modpack.md item 5/7, data-model.md "Secrets").
//
// The API owns the managed Secret gameplane-modreg-curseforge in the
// control-plane namespace. When it is shared, each modpack server with an
// applied CurseForge pack gets a copy Secret <gs>-curseforge-key in its own
// namespace and the game container reads the key through a secretKeyRef.
// The key value never reaches the pod template, the config hash or the agent.
const (
	curseforgeKeyCopySuffix   = "-curseforge-key"
	curseforgeKeyCopyLabel    = "gameplane.local/curseforge-key-copy"
	curseforgeManagedName     = "gameplane-modreg-curseforge"
	curseforgeShareAnnotation = "gameplane.local/share-with-game-servers"
	modRegistryLabel          = "gameplane.local/mod-registry"
	curseforgeKeyDataKey      = "apiKey"
)

func curseforgeKeySecretName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + curseforgeKeyCopySuffix
}

// modpackKeyEnvName returns the apiKeyEnv of the applied provider when it is
// curseforge, else "". Legacy-rendered servers get no key env.
func modpackKeyEnvName(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) string {
	if modpackLegacyRender(gs) || gs.Status.Modpack == nil || gs.Status.Modpack.Applied == nil {
		return ""
	}
	applied := gs.Status.Modpack.Applied
	if applied.Provider != "curseforge" {
		return ""
	}
	for _, p := range modpackProviders(tmpl) {
		if p.Provider == "curseforge" && p.Modpacks.APIKeyEnv != "" {
			return p.Modpacks.APIKeyEnv
		}
	}
	return ""
}

// managedCurseForgeKey reads the managed key Secret from the control-plane
// namespace. It returns shared=false (no error) when the Secret is absent or
// sharing is off: label mod-registry=true, a non-empty apiKey and the share
// annotation set to "true" must all hold.
func (r *GameServerReconciler) managedCurseForgeKey(ctx context.Context) ([]byte, bool, error) {
	if r.ControlPlaneNamespace == "" {
		return nil, false, nil
	}
	// Uncached read: the chart grants get on this one Secret name only, and the
	// manager cache does not watch the credentials namespace.
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	var sec corev1.Secret
	if err := reader.Get(ctx, client.ObjectKey{Namespace: r.ControlPlaneNamespace, Name: curseforgeManagedName}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			log.FromContext(ctx).V(1).Info("managed mod-registry key not shared",
				"namespace", r.ControlPlaneNamespace, "name", curseforgeManagedName, "reason", string(apierrors.ReasonForError(err)))
			return nil, false, nil
		}
		if apierrors.IsForbidden(err) {
			// Info level: a forbidden read means the operator Role is missing or
			// misconfigured, which an operator should see without debug logging.
			log.FromContext(ctx).Info("managed mod-registry key read forbidden; treating as not shared",
				"namespace", r.ControlPlaneNamespace, "name", curseforgeManagedName, "reason", string(apierrors.ReasonForError(err)))
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get managed key Secret %s/%s: %w", r.ControlPlaneNamespace, curseforgeManagedName, err)
	}
	key := sec.Data[curseforgeKeyDataKey]
	if sec.Labels[modRegistryLabel] != "true" || len(key) == 0 || sec.Annotations[curseforgeShareAnnotation] != "true" {
		return nil, false, nil
	}
	return key, true, nil
}

// reconcileModpackKey creates, updates or deletes the per-server copy Secret
// and keeps status.modpack.keyShared in step with it. It runs after
// reconcileModpackChange, so an applied CurseForge pack is already in memory.
// The status patch mutates gs, so the same pass renders the key env.
func (r *GameServerReconciler) reconcileModpackKey(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	key, shared, err := r.managedCurseForgeKey(ctx)
	if err != nil {
		return err
	}
	envName := modpackKeyEnvName(gs, tmpl)
	desired := envName != "" && shared
	applied := gs.Status.Modpack != nil && gs.Status.Modpack.KeyShared
	want := desired
	// OD-10 A: a share toggle adds or removes the env in the pod template, which
	// rolls the pod, so it takes effect only when the server is quiet (same gate
	// as OD-8 B) or a pack change is already rolling it. Rotation (desired ==
	// applied) needs no restart and is not gated. Until the flip, the applied
	// state and the env stay as they are.
	if envName != "" && desired != applied {
		quiet, err := r.modpackKeyFlipReady(ctx, gs)
		if err != nil {
			return err
		}
		if !quiet {
			log.FromContext(ctx).V(1).Info("curseforge key share change waits for 0 players",
				"server", gs.Name, "sharing", desired)
			want = applied
		}
	}
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: curseforgeKeySecretName(gs), Namespace: gs.Namespace},
	}
	switch {
	case desired:
		// Also runs for a share-on change still pending: the copy is harmless
		// before the env names it, and the env finds it the moment it flips.
		err := r.upsertCurseForgeKeyCopy(ctx, gs, sec, key)
		if err != nil {
			return err
		}
	case want:
		// Share-off change pending: keep the copy until the flip, so the running
		// pod and any in-place container restart still resolve the env.
	default:
		if err := r.deleteIfControlledBy(ctx, gs, sec); err != nil {
			return fmt.Errorf("delete curseforge key Secret %s/%s: %w", gs.Namespace, sec.Name, err)
		}
	}
	if gs.Status.Modpack != nil && gs.Status.Modpack.KeyShared != want {
		next := gs.Status.Modpack.DeepCopy()
		next.KeyShared = want
		return r.patchModpackStatus(ctx, gs, next)
	}
	return nil
}

// modpackKeyFlipReady reports whether a share toggle may roll the server now:
// a pack change is already rolling it (phase Applying, the only phase in which
// the new pack renders), or the server is quiet (OD-8 B gate, without the
// version requirement). Pending, BackingUp and BackupFailed roll no pod, so
// they do not release a held toggle. A toggle landing during Applying after the
// pod already rolled causes one extra restart; accepted under OD-10.
func (r *GameServerReconciler) modpackKeyFlipReady(ctx context.Context, gs *gameplanev1alpha1.GameServer) (bool, error) {
	if gs.Status.Modpack != nil && gs.Status.Modpack.Phase == gameplanev1alpha1.ModpackPhaseApplying {
		return true, nil
	}
	return r.modpackServerQuiet(ctx, gs, false)
}

// upsertCurseForgeKeyCopy creates or updates the per-server copy Secret with key.
func (r *GameServerReconciler) upsertCurseForgeKeyCopy(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, sec *corev1.Secret, key []byte,
) error {
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, sec, func() error {
		sec.Type = corev1.SecretTypeOpaque
		if sec.Labels == nil {
			sec.Labels = map[string]string{}
		}
		sec.Labels[curseforgeKeyCopyLabel] = "true"
		sec.Data = map[string][]byte{curseforgeKeyDataKey: key}
		return controllerutil.SetControllerReference(gs, sec, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconcile curseforge key Secret %s/%s: %w", gs.Namespace, sec.Name, err)
	}
	return nil
}

// modpackKeyEnv returns the secretKeyRef env var for the applied CurseForge
// key, or nil unless the key is currently shared. The value is never inlined.
func modpackKeyEnv(gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) *corev1.EnvVar {
	if gs.Status.Modpack == nil || !gs.Status.Modpack.KeyShared {
		return nil
	}
	name := modpackKeyEnvName(gs, tmpl)
	if name == "" {
		return nil
	}
	return &corev1.EnvVar{
		Name: name,
		ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: curseforgeKeySecretName(gs)},
				Key:                  curseforgeKeyDataKey,
				// Optional: the pod still starts if the copy Secret is briefly
				// missing during a share toggle.
				Optional: ptr.To(true),
			},
		},
	}
}
