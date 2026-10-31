package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

const sentinelIsolationLabel = "gameplane.local/sentinel-isolation"

// The chart's legacy policies exclude opted-in pods on both sides. Policies
// are additive: these per-server grants cannot subtract a namespace-wide grant.
func (r *GameServerReconciler) reconcileSentinelIsolation(ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate) error {
	if !tmpl.Spec.SentinelIsolation {
		// Workload reconciliation removes the opt-in labels by rolling both
		// pod templates. Keep both grants until every live pod has switched
		// back to the legacy policies, including during a partial rollout.
		reader := r.APIReader
		if reader == nil {
			reader = r.Client
		}
		// An empty Pod list alone is insufficient: an old workload template
		// could still create labeled replacements after that observation.
		var game appsv1.StatefulSet
		if err := reader.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name}, &game); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		} else if game.Spec.Template.Labels[sentinelIsolationLabel] == "true" || game.Status.ObservedGeneration < game.Generation || game.Status.CurrentRevision != game.Status.UpdateRevision {
			return nil
		}
		var sentinel appsv1.Deployment
		if err := reader.Get(ctx, client.ObjectKey{Namespace: gs.Namespace, Name: gs.Name + "-waker"}, &sentinel); err != nil {
			if !apierrors.IsNotFound(err) {
				return err
			}
		} else if sentinel.Spec.Template.Labels[sentinelIsolationLabel] == "true" || sentinel.Status.ObservedGeneration < sentinel.Generation || sentinel.Status.UpdatedReplicas != sentinel.Status.Replicas {
			return nil
		}
		var pods corev1.PodList
		if err := reader.List(ctx, &pods, client.InNamespace(gs.Namespace), client.MatchingLabels{
			"app.kubernetes.io/instance": gs.Name, sentinelIsolationLabel: "true",
		}); err != nil {
			return err
		}
		for i := range pods.Items {
			name := pods.Items[i].Labels["app.kubernetes.io/name"]
			if name == "gameplane-game" || name == "gameplane-waker" {
				return nil
			}
		}
	}
	ports := networkPolicyPortsFromTemplate(tmpl)
	for _, ingress := range []bool{true, false} {
		suffix, target, peer := "egress", "gameplane-waker", "gameplane-game"
		if ingress {
			suffix, target, peer = "ingress", "gameplane-game", "gameplane-waker"
		}
		np := &networkingv1.NetworkPolicy{ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-sentinel-" + suffix, Namespace: gs.Namespace}}
		if !tmpl.Spec.SentinelIsolation || len(ports) == 0 {
			if err := r.Get(ctx, client.ObjectKeyFromObject(np), np); err != nil {
				if client.IgnoreNotFound(err) != nil {
					return err
				}
				continue
			}
			if metav1.IsControlledBy(np, gs) {
				uid := np.UID
				if err := client.IgnoreNotFound(r.Delete(ctx, np, &client.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &uid}})); err != nil {
					return err
				}
			}
			continue
		}
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
			if np.ResourceVersion != "" && !metav1.IsControlledBy(np, gs) {
				return fmt.Errorf("sentinel policy %s is not owned by this GameServer", np.Name)
			}
			np.Spec = networkingv1.NetworkPolicySpec{PodSelector: metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": target, "app.kubernetes.io/instance": gs.Name,
			}}}
			peers := []networkingv1.NetworkPolicyPeer{{PodSelector: &metav1.LabelSelector{MatchLabels: map[string]string{
				"app.kubernetes.io/name": peer, "app.kubernetes.io/instance": gs.Name,
			}}}}
			if ingress {
				np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
				np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{From: peers, Ports: ports}}
			} else {
				np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeEgress}
				np.Spec.Egress = []networkingv1.NetworkPolicyEgressRule{{To: peers, Ports: ports}}
			}
			return controllerutil.SetControllerReference(gs, np, r.Scheme)
		})
		if err != nil {
			return err
		}
	}
	return nil
}
