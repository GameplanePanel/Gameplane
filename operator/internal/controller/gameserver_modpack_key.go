package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// Spec 035 key sharing (rule 5, specs/035-curseforge-modpacks/contracts/
// operator-modpack.md item 5/7, data-model.md "Secrets").
//
// The API owns the managed Secret gameplane-modreg-curseforge in the
// control-plane namespace. When it is shared, each modpack server with an
// applied CurseForge pack gets a copy Secret <gs>-curseforge-key in its own
// namespace and the game container reads the key through a secretKeyRef.
// The key value never reaches the pod template, the config hash or the agent.
const (
	curseforgeKeySecretSuffix = "-curseforge-key"
	curseforgeKeyCopyLabel    = "gameplane.local/curseforge-key-copy"
	curseforgeManagedSecret   = "gameplane-modreg-curseforge"
	curseforgeShareAnnotation = "gameplane.local/share-with-game-servers"
	modRegistryLabel          = "gameplane.local/mod-registry"
	curseforgeKeyDataKey      = "apiKey"
)

func curseforgeKeySecretName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + curseforgeKeySecretSuffix
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
	var sec corev1.Secret
	if err := r.Get(ctx, client.ObjectKey{Namespace: r.ControlPlaneNamespace, Name: curseforgeManagedSecret}, &sec); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("get managed key Secret %s/%s: %w", r.ControlPlaneNamespace, curseforgeManagedSecret, err)
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
	want := modpackKeyEnvName(gs, tmpl) != "" && shared
	sec := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: curseforgeKeySecretName(gs), Namespace: gs.Namespace},
	}
	if !want {
		if err := r.deleteIfControlledBy(ctx, gs, sec); err != nil {
			return fmt.Errorf("delete curseforge key Secret %s/%s: %w", gs.Namespace, sec.Name, err)
		}
	} else {
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
	}
	if gs.Status.Modpack != nil && gs.Status.Modpack.KeyShared != want {
		next := gs.Status.Modpack.DeepCopy()
		next.KeyShared = want
		return r.patchModpackStatus(ctx, gs, next)
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
			},
		},
	}
}

// mapManagedKeyToGameServers enqueues the CurseForge modpack servers when the
// managed key Secret changes. Other Secrets map to nothing.
func (r *GameServerReconciler) mapManagedKeyToGameServers(ctx context.Context, obj client.Object) []reconcile.Request {
	if r.ControlPlaneNamespace == "" || obj.GetNamespace() != r.ControlPlaneNamespace || obj.GetName() != curseforgeManagedSecret {
		return nil
	}
	var servers gameplanev1alpha1.GameServerList
	if err := r.List(ctx, &servers); err != nil {
		log.FromContext(ctx).Error(err, "list servers for mod-registry key change", "secret", obj.GetName())
		return nil
	}
	var requests []reconcile.Request
	for i := range servers.Items {
		gs := &servers.Items[i]
		specCF := gs.Spec.Modpack != nil && gs.Spec.Modpack.Provider == "curseforge"
		appliedCF := gs.Status.Modpack != nil && gs.Status.Modpack.Applied != nil &&
			gs.Status.Modpack.Applied.Provider == "curseforge"
		if specCF || appliedCF {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(gs)})
		}
	}
	return requests
}
