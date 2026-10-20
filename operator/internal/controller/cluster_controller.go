package controller

import (
	"context"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/discovery"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/kubeconfig"
)

// ClusterStatusReconciler performs periodic health checks on remote clusters.
type ClusterStatusReconciler struct {
	client.Client
	Scheme    *runtime.Scheme
	Namespace string // namespace where kubeconfig Secrets live (control-plane namespace)
}

// +kubebuilder:rbac:groups=gameplane.local,resources=clusters,verbs=get;list;watch
// +kubebuilder:rbac:groups=gameplane.local,resources=clusters/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *ClusterStatusReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var cluster gameplanev1alpha1.Cluster
	if err := r.Get(ctx, req.NamespacedName, &cluster); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Reserved-name guard: "local" is reserved for the local operator cluster.
	if req.Name == "local" {
		if err := r.markUnhealthy(ctx, &cluster, "NameReserved", `cluster name "local" is reserved`, `cluster name "local" is reserved`); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Read the kubeconfig Secret.
	secretRef := cluster.Spec.KubeconfigSecret
	secretKey := secretRef.Key
	if secretKey == "" {
		secretKey = "kubeconfig"
	}

	var secret corev1.Secret
	if err := r.Get(ctx, types.NamespacedName{Namespace: r.Namespace, Name: secretRef.Name}, &secret); err != nil {
		// Secret not found or other read error.
		if err := r.markUnhealthy(ctx, &cluster, "SecretNotFound",
			fmt.Sprintf("kubeconfig secret %s/%s not found", r.Namespace, secretRef.Name),
			fmt.Sprintf("kubeconfig secret not found: %v", err)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Verify the secret has the gameplane kubeconfig label.
	if secret.Labels[gameplanev1alpha1.LabelClusterKubeconfig] != "true" {
		if err := r.markUnhealthy(ctx, &cluster, "InvalidSecret",
			fmt.Sprintf("kubeconfig secret %s/%s missing label gameplane.local/cluster-kubeconfig=true", r.Namespace, secretRef.Name),
			"kubeconfig secret missing required label"); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Read the kubeconfig data from the secret key.
	kubeconfigData, ok := secret.Data[secretKey]
	if !ok {
		if err := r.markUnhealthy(ctx, &cluster, "MissingKey",
			fmt.Sprintf("kubeconfig secret %s/%s missing key %q", r.Namespace, secretRef.Name, secretKey),
			fmt.Sprintf("kubeconfig secret missing key %q", secretKey)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Validate embedded credentials before building the remote client.
	restCfg, err := kubeconfig.RESTConfig(kubeconfigData)
	if err != nil {
		if err := r.markUnhealthy(ctx, &cluster, "BadKubeconfig",
			fmt.Sprintf("failed to parse kubeconfig: %v", err),
			fmt.Sprintf("failed to parse kubeconfig: %v", err)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Set a timeout for discovery operations.
	restCfg.Timeout = 10 * time.Second

	// Get the server version to verify connectivity.
	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		if err := r.markUnhealthy(ctx, &cluster, "DiscoveryClientFailed",
			fmt.Sprintf("failed to create discovery client: %v", err),
			fmt.Sprintf("failed to create discovery client: %v", err)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Get the server version.
	serverVersion, err := discoveryClient.ServerVersion()
	if err != nil {
		if err := r.markUnhealthy(ctx, &cluster, "HealthCheckFailed",
			fmt.Sprintf("cluster health check failed: %v", err),
			fmt.Sprintf("cluster health check failed: %v", err)); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
	}

	// Cluster is healthy.
	if err := r.markHealthy(ctx, &cluster, serverVersion.String()); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 2 * time.Minute}, nil
}

// markUnhealthy updates the cluster status to indicate an unhealthy state.
// It sets the phase, message, last check time, observed generation, and adds
// a condition. The caller is responsible for the requeue decision.
func (r *ClusterStatusReconciler) markUnhealthy(ctx context.Context, c *gameplanev1alpha1.Cluster, reason, statusMsg, condMsg string) error {
	now := metav1.Now()
	c.Status.Phase = gameplanev1alpha1.ClusterPhaseUnhealthy
	c.Status.Message = statusMsg
	c.Status.LastCheckTime = &now
	c.Status.ObservedGeneration = c.Generation
	c.Status.Conditions = upsertCondition(c.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ClusterConditionHealthy,
		Status:             metav1.ConditionFalse,
		Reason:             reason,
		Message:            condMsg,
		ObservedGeneration: c.Generation,
	})
	return r.Status().Update(ctx, c)
}

// markHealthy updates the cluster status to indicate a healthy state.
// It sets the phase, message, server version, last check time, observed generation,
// and adds a condition. The caller is responsible for the requeue decision.
func (r *ClusterStatusReconciler) markHealthy(ctx context.Context, c *gameplanev1alpha1.Cluster, version string) error {
	now := metav1.Now()
	c.Status.Phase = gameplanev1alpha1.ClusterPhaseHealthy
	c.Status.Message = ""
	c.Status.ServerVersion = version
	c.Status.LastCheckTime = &now
	c.Status.ObservedGeneration = c.Generation
	c.Status.Conditions = upsertCondition(c.Status.Conditions, metav1.Condition{
		Type:               gameplanev1alpha1.ClusterConditionHealthy,
		Status:             metav1.ConditionTrue,
		Reason:             "HealthCheckPassed",
		Message:            fmt.Sprintf("cluster is healthy, version %s", version),
		ObservedGeneration: c.Generation,
	})
	return r.Status().Update(ctx, c)
}

func (r *ClusterStatusReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		// Health results update LastCheckTime. Watching those status writes
		// would immediately repeat slow probes, bypassing the two-minute timer.
		For(&gameplanev1alpha1.Cluster{}, builder.WithPredicates(predicate.Or(
			predicate.GenerationChangedPredicate{}, predicate.AnnotationChangedPredicate{},
		))).
		Complete(r)
}
