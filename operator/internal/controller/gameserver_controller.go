package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// GameServerReconciler reconciles a GameServer object into a StatefulSet,
// Service, and PVC. The agent sidecar is injected at pod-spec build time.
type GameServerReconciler struct {
	client.Client
	APIReader client.Reader
	Scheme    *runtime.Scheme

	// AgentImage is the container image used for the sidecar agent
	// injected into every game pod. Set from an operator flag so the
	// deployer can pin the agent version independently of the game.
	AgentImage string

	// AgentImagePullPolicy, when non-empty, is set on the agent sidecar
	// container. Set from the --agent-image-pull-policy operator flag.
	// Empty leaves ImagePullPolicy unset, so Kubernetes applies its usual
	// default (Always for a ":latest" tag, IfNotPresent otherwise) —
	// preserving pre-flag behavior. That default is exactly the footgun
	// this exists to let deployers override: a floating tag like ":edge"
	// defaults to IfNotPresent, so a node reuses whatever agent image it
	// already cached and game pods silently run a stale agent forever.
	// The chart sets this to the same policy it uses for its own images
	// (image.pullPolicy), so "Always" for :edge installs also rolls the
	// agent sidecar.
	AgentImagePullPolicy string

	// AgentLogLevel, when non-empty, is injected into every agent sidecar
	// as GAMEPLANE_LOG_LEVEL. Empty injects nothing — the agent defaults
	// to info and existing StatefulSets don't roll on operator upgrade.
	AgentLogLevel string

	// ConfigInitImage is the image for the init container that copies
	// operator-rendered config files onto the data volume. Set from an
	// operator flag so air-gapped installs can point it at a private
	// registry mirror instead of Docker Hub. Empty falls back to
	// DefaultConfigInitImage.
	ConfigInitImage string

	// SentinelImage is the image for the wake sentinel pod that holds
	// advertised ports while the server is asleep. Set from an operator flag
	// so air-gapped installs can point it at a private registry mirror.
	// Empty falls back to DefaultSentinelImage.
	SentinelImage string

	// TunnelFrpImage, TunnelTailscaleImage, TunnelPlayitImage are the images
	// for the tunnel relay pods. Set from operator flags so air-gapped installs
	// can point at a private registry mirror. Empty falls back to the per-
	// provider defaults.
	TunnelFrpImage       string
	TunnelTailscaleImage string
	TunnelPlayitImage    string

	// AgentCASecretName / AgentCASecretNamespace point at the cluster-
	// wide Secret holding `ca.crt` + `ca.key` used to sign the
	// per-GameServer agent server cert. Provisioned by the chart
	// (charts/gameplane/templates/mtls.yaml).
	AgentCASecretName      string
	AgentCASecretNamespace string

	// AgentClient runs the module-declared in-game stop sequence over RCON
	// during a soft stop. May be nil (or a disabled client) in dev clusters,
	// in which case the operator falls back to a timed scale-to-zero.
	AgentClient AgentStopper

	// PodAttacher runs the module-declared stop sequence over a pod attach
	// to the game container's stdin, for consoleMode: pty games that
	// declare no RCON. May be nil (dev clusters, tests that don't wire
	// it), in which case softStop treats a pty-only template the same as
	// one with no usable transport at all.
	PodAttacher PodStopAttacher

	// AddressManager names the cluster's load-balancer address manager
	// ("metallb", "cilium" or "none"), set from the operator's
	// --address-manager flag and validated at startup. It selects how a
	// GameServer's spec.networking.addressPool / .address preference is
	// translated onto the game Service (see planAddressPreference). Empty
	// is treated exactly as "none": mutate nothing and report the
	// unhonored request, never silently fall back to the default pool.
	AddressManager string

	// MetalLBNamespace is the namespace MetalLB's IPAddressPool custom
	// resources (metallb.io/v1beta1, namespaced) live in. Used only when
	// AddressManager == "metallb" and a pool was requested: reconcileStatus
	// GETs the requested pool directly here so a missing pool can be
	// reported as PoolNotFound without waiting for a MetalLB event. Set
	// from the operator's --metallb-namespace flag. "metallb-system" is
	// MetalLB's own install convention, not a contract, hence configurable.
	// Empty is treated as "metallb-system" (see metalLBNamespace()) — this
	// keeps envtest/unit-test reconcilers that construct the struct
	// directly, without setting every field, working unchanged.
	MetalLBNamespace string

	// GameIngressPolicyEnabled controls whether the operator reconciles a
	// per-GameServer ingress NetworkPolicy admitting player traffic to the
	// template's advertised ports (see reconcileNetworkPolicy). Set from
	// the operator's --game-ingress-policy flag, default true. When false,
	// reconcileNetworkPolicy ensures the policy is absent rather than
	// merely skipping, so toggling the flag off converges existing
	// GameServers instead of only affecting new ones.
	GameIngressPolicyEnabled bool

	// GameIngressFromCIDRs are the source CIDRs admitted to advertised
	// game ports by the ingress NetworkPolicy. Set from the operator's
	// repeatable --game-ingress-from-cidr flag, which defaults to
	// ["0.0.0.0/0"] (games are meant to be publicly reachable) when not
	// supplied. Each entry is validated as a CIDR at operator startup.
	GameIngressFromCIDRs []string

	// CaptureEnabled is the cluster-wide on/off switch for the network
	// capture feature. Set from the operator's --capture-enabled flag,
	// default false. When false, the capture capability cannot be enabled
	// per-GameServer; when true, it can be toggled on/off per server.
	CaptureEnabled bool

	// CaptureSidecarImage is the container image for the network capture sidecar
	// injected when capture is enabled on a GameServer. Set from the
	// --capture-sidecar-image operator flag so air-gapped installs can point it
	// at a private registry mirror. Empty falls back to DefaultCaptureSidecarImage.
	CaptureSidecarImage string

	// DefaultStorageClassName is the install-time default StorageClass name for
	// game server data volumes. Set from the --game-data-storage-class CLI flag.
	// Empty string means use the cluster's default StorageClass.
	// Precedence: GameServer.Spec.Storage.StorageClassName (explicit override) >
	// GameTemplate.Spec.Storage.StorageClassName (template default) >
	// DefaultStorageClassName (install-time default) > cluster default.
	DefaultStorageClassName string
}

// AgentStopper issues the module-declared graceful stop sequence to a game's
// agent. Satisfied by *operator/internal/agent.Client.
type AgentStopper interface {
	Stop(ctx context.Context, namespace, server string) error
	// Enabled reports whether this stopper can actually reach an agent.
	// agent.New returns a non-nil *Client with Disabled: true when no
	// mTLS material is configured (dev clusters, tests) — its Stop is
	// then a silent no-op, so selectStopTransport must check Enabled(),
	// not just r.AgentClient == nil, or it picks stopTransportRCON for a
	// client that will never actually stop anything and sits out the
	// full grace period for nothing.
	Enabled() bool
}

const (
	// stopRequestedAtAnnotation records (RFC3339) when the operator issued the
	// in-game stop sequence, so the soft-stop wait survives reconciles and the
	// command is issued only once.
	stopRequestedAtAnnotation = "gameserver.gameplane.local/stop-requested-at"

	// defaultStopGracePeriod bounds the soft-stop wait when the GameServer
	// leaves spec.stopGracePeriodSeconds unset.
	defaultStopGracePeriod = 30 * time.Second
)

// RBAC markers below describe only the CLUSTER-wide permissions the
// operator needs. Writes to workload primitives (StatefulSets,
// Deployments, Services, PVCs, Secrets, ConfigMaps, Jobs,
// NetworkPolicies, ServiceAccounts, Roles/RoleBindings), to the
// namespaced Gameplane CRDs and their finalizers, and to
// pods/ephemeralcontainers are scoped per-namespace via a
// hand-managed Role bound in the games namespace(s) — see
// operator/config/rbac/role_namespace.yaml and the Helm chart. This
// keeps a compromised operator token from mutating workloads, policies
// or RBAC outside the games namespace(s).
//
// pods/attach create (softStop's stdin pod-attach for consoleMode: pty
// games, see gameserver_stop_attach.go) is likewise namespace-scoped
// ONLY, not listed here: game pods only ever exist in the games
// namespace, and pods/attach create is exec-equivalent — a cluster-wide
// grant would let the operator attach to stdin of any pod in any
// namespace. It's granted in operator/config/rbac/role_namespace.yaml
// and the Helm chart's namespaced Role, matching every other write verb
// in this list.
//
// +kubebuilder:rbac:groups=gameplane.local,resources=gameservers,verbs=get;list;watch
// +kubebuilder:rbac:groups=gameplane.local,resources=gametemplates,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=services;persistentvolumeclaims;configmaps;secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods;pods/log,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=pods/ephemeralcontainers,verbs=get;list;watch
// +kubebuilder:rbac:groups=gameplane.local,resources=networkcaptures,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=serviceaccounts,verbs=get;list;watch
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch
// +kubebuilder:rbac:groups=networking.k8s.io,resources=networkpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
// +kubebuilder:rbac:groups=core,resources=events,verbs=get;list;watch
//
// checkMetalLBPoolExists GETs MetalLB's namespaced IPAddressPool CR by name
// (see poolExistence) — read-only, and deliberately narrow (no list/delete)
// since the operator only ever needs to know whether one named pool exists.
// +kubebuilder:rbac:groups=metallb.io,resources=ipaddresspools,verbs=get;list;watch
// +kubebuilder:rbac:groups=storage.k8s.io,resources=storageclasses,verbs=get;list;watch

func (r *GameServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var gs gameplanev1alpha1.GameServer
	if err := r.Get(ctx, req.NamespacedName, &gs); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	// Refresh and recover wipe ownership before deriving config or idle state.
	// A later refresh would mix an older idle calculation with a newer status
	// patch base and could overwrite a concurrent idle-clock update.
	if changed, err := r.ensureWipeGuard(ctx, &gs); err != nil {
		return requeueOnConflict(ctrl.Result{}, err)
	} else if changed {
		return ctrl.Result{Requeue: true}, nil
	}

	// Resolve the template this GameServer points at. Templates are
	// cluster-scoped so no namespace is needed.
	var tmpl gameplanev1alpha1.GameTemplate
	if err := r.Get(ctx, types.NamespacedName{Name: gs.Spec.TemplateRef.Name}, &tmpl); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{RequeueAfter: 15 * time.Second}, r.setPhase(ctx, &gs,
				fmt.Sprintf("GameTemplate %q not found", gs.Spec.TemplateRef.Name))
		}
		return ctrl.Result{}, err
	}

	// Resolve spec.config against the template's configSchema before
	// touching any children: invalid config must fail loudly (mirroring
	// the missing-template path) instead of materializing a pod that
	// silently ignores what the user asked for.
	mc, err := materializeConfig(&gs, &tmpl)
	if err != nil {
		return ctrl.Result{}, r.setPhase(ctx, &gs,
			fmt.Sprintf("invalid config: %v", err))
	}

	// Resolve the selected game version (image/env + per-loader mod
	// volume). A spec.version that names no catalog entry fails loudly,
	// like an invalid config, rather than silently falling back.
	ver, err := resolveVersion(&gs, &tmpl)
	if err != nil {
		return ctrl.Result{}, r.setPhase(ctx, &gs, err.Error())
	}

	if err := r.reconcilePVC(ctx, &gs, &tmpl); err != nil {
		logger.Error(err, "reconcile PVC")
		return ctrl.Result{}, err
	}
	if err := r.reconcileExtraPVCs(ctx, &gs, &tmpl); err != nil {
		logger.Error(err, "reconcile extra PVCs")
		return ctrl.Result{}, err
	}
	if err := r.reconcileModPVC(ctx, &gs, &tmpl, ver); err != nil {
		logger.Error(err, "reconcile mod PVC")
		return ctrl.Result{}, err
	}
	if err := r.reconcileAgentService(ctx, &gs); err != nil {
		logger.Error(err, "reconcile agent Service")
		return ctrl.Result{}, err
	}
	if err := r.reconcileNetworkPolicy(ctx, &gs, &tmpl); err != nil {
		logger.Error(err, "reconcile ingress NetworkPolicy")
		return ctrl.Result{}, err
	}
	if err := r.reconcileAgentTLS(ctx, &gs); err != nil {
		logger.Error(err, "reconcile agent TLS")
		return ctrl.Result{}, err
	}
	if err := r.reconcileAgentRBAC(ctx, &gs); err != nil {
		logger.Error(err, "reconcile agent RBAC")
		return ctrl.Result{}, err
	}
	// Tunnel RBAC is only needed for playit, which requires a grant to patch
	// status with the assigned address. Scope it to playit to keep the API
	// surface minimal when playit is not in use.
	needPlayitRBAC := gs.Spec.Networking.Tunnel != nil &&
		gs.Spec.Networking.Tunnel.Enabled &&
		gs.Spec.Networking.Tunnel.Provider == "playit"
	if err := r.reconcileTunnelRBAC(ctx, &gs, needPlayitRBAC); err != nil {
		logger.Error(err, "reconcile tunnel RBAC")
		return ctrl.Result{}, err
	}
	if err := r.reconcileConfigSecret(ctx, &gs, mc); err != nil {
		logger.Error(err, "reconcile config Secret")
		return ctrl.Result{}, err
	}
	if err := r.reconcileFilesSecret(ctx, &gs, mc); err != nil {
		logger.Error(err, "reconcile files Secret")
		return ctrl.Result{}, err
	}
	if err := r.reconcileRCONSecret(ctx, &gs, &tmpl); err != nil {
		logger.Error(err, "reconcile rcon Secret")
		return ctrl.Result{}, err
	}
	// Idle policy runs before the replica computation so a sleep or wake
	// decided this pass is already reflected in the count below. It writes
	// only annotations; its status read model is folded into reconcileStatus'
	// single status patch further down.
	idle, idleStatus, idleRequeue, idleScheduleErr, err := r.reconcileIdle(ctx, &gs)
	if err != nil {
		logger.Error(err, "reconcile idle")
		return ctrl.Result{}, err
	}

	// planSentinel is the single source of truth for whether the wake
	// sentinel should exist this pass and whether the game Service should
	// route to it instead of the game pod — see its doc comment for why
	// that decision must live in exactly one place. Deliberately NOT an
	// early-return-and-skip-everything-else when the sentinel isn't ready
	// yet: a sentinel that cannot start (ImagePullBackOff, quota) must
	// degrade to "no wake-on-connect this pass", never to "this GameServer
	// stops reconciling" — so every step below still runs regardless.
	plan, err := r.planSentinel(ctx, &gs, &tmpl, idle)
	if err != nil {
		logger.Error(err, "plan wake sentinel")
		return ctrl.Result{}, err
	}
	if err := r.reconcileSentinelRBAC(ctx, &gs); err != nil {
		logger.Error(err, "reconcile sentinel RBAC")
		return ctrl.Result{}, err
	}
	if err := r.reconcileSentinel(ctx, &gs, &tmpl, plan.wantSentinel); err != nil {
		logger.Error(err, "reconcile sentinel")
		return ctrl.Result{}, err
	}
	if err := r.reconcileGameDirectServiceFromTemplate(ctx, &gs, &tmpl); err != nil {
		logger.Error(err, "reconcile game-direct Service")
		return ctrl.Result{}, err
	}

	// planTunnel decides whether the tunnel pod should exist and what endpoints
	// it will advertise. Like planSentinel, this is computed once per pass so
	// there is a single source of truth (see planTunnel's doc comment).
	tunnelPlan := r.planTunnel(ctx, &gs, &tmpl)
	if err := r.reconcileTunnel(ctx, &gs, &tmpl, tunnelPlan.wantTunnel); err != nil {
		logger.Error(err, "reconcile tunnel")
		return ctrl.Result{}, err
	}
	if err := r.reconcileTunnelNetworkPolicy(ctx, &gs, &tmpl, tunnelPlan); err != nil {
		logger.Error(err, "reconcile tunnel NetworkPolicy")
		return ctrl.Result{}, err
	}
	if err := r.reconcileService(ctx, &gs, &tmpl, plan.routeToSentinel); err != nil {
		logger.Error(err, "reconcile Service")
		return ctrl.Result{}, err
	}

	replicas, stopRequeue, err := r.desiredReplicas(ctx, &gs, &tmpl, idle)
	if err != nil {
		logger.Error(err, "compute desired replicas")
		return ctrl.Result{}, err
	}
	if err := r.reconcileStatefulSet(ctx, &gs, &tmpl, ver, mc, replicas); err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Error(err, "reconcile StatefulSet")
		return ctrl.Result{}, err
	}
	// Node placement is a cosmetic annotation for the dashboard; a transient
	// pod-get hiccup shouldn't stall the rest of reconciliation, so log and go.
	if err := r.reconcileNodePlacement(ctx, &gs); err != nil {
		logger.Error(err, "reconcile node placement")
	}
	if err := r.reconcileCapture(ctx, &gs); err != nil {
		logger.Error(err, "reconcile capture")
		return ctrl.Result{}, err
	}
	if err := r.reconcileBackupSchedule(ctx, &gs); err != nil {
		logger.Error(err, "reconcile BackupSchedule")
		return ctrl.Result{}, err
	}
	// Preflight already recovered legacy workers. An ordinary pass needs only
	// finished-Job cleanup; avoid repeating namespace inventories here.
	var wipeErr error
	if gs.Annotations[wipeGuardAnnotation] != "" || pendingWipe(&gs) {
		wipeErr = r.reconcileWipe(ctx, &gs, &tmpl)
	} else {
		wipeErr = r.deleteWipeJob(ctx, &gs, gs.Name+"-wipe")
	}
	if err := wipeErr; err != nil {
		if apierrors.IsConflict(err) {
			return ctrl.Result{Requeue: true}, nil
		}
		logger.Error(err, "reconcile data wipe")
		return ctrl.Result{}, err
	}

	// Fetch Service events for address-manager failure reason derivation.
	// Errors are logged but not fatal — missing events should not stall reconciliation.
	var svcEvents []corev1.Event
	if gs.Spec.Networking.AddressPool != "" || gs.Spec.Networking.Address != "" {
		if err := r.listServiceEvents(ctx, &gs, &svcEvents); err != nil {
			logger.Error(err, "list service events")
		}
	}

	// Detect address conflicts: if an explicit address was requested,
	// check if another GameServer in the cluster already holds it.
	conflictingServer := ""
	if gs.Spec.Networking.Address != "" {
		if conflict, err := r.findAddressConflict(ctx, &gs); err != nil {
			logger.Error(err, "check address conflicts")
		} else if conflict != "" {
			conflictingServer = conflict
		}
	}

	requeue, err := r.reconcileStatus(ctx, &gs, idle, idleStatus, idleScheduleErr, tunnelPlan, &tmpl, svcEvents, conflictingServer)
	if err != nil {
		return ctrl.Result{}, err
	}
	// While a soft stop is mid-flight, requeue at the (sooner) grace deadline
	// so we scale to zero even if no pod event arrives first. The idle hint is
	// the sleep deadline or the next wake window; take whichever comes first,
	// or nothing fires the transition until an unrelated event happens to
	// wake the reconciler.
	hints := []time.Duration{stopRequeue, idleRequeue}
	if plan.wantSentinel {
		// Bounded backstop for planSentinel's wait on the sentinel or the
		// game pod. Both are owned (Owns(&appsv1.Deployment{}),
		// Owns(&appsv1.StatefulSet{})) so a readiness transition normally
		// retriggers Reconcile on its own; this only bounds how long a
		// missed/coalesced event could otherwise leave things stale. It is
		// NOT a substitute for the early-return-and-skip-everything this
		// replaced — every step above still ran this pass regardless.
		hints = append(hints, sentinelBackstopRequeue)
	}
	for _, hint := range hints {
		if hint > 0 && (requeue == 0 || hint < requeue) {
			requeue = hint
		}
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// SetupWithManager configures the controller manager to watch GameServer and its owned resources.
func (r *GameServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&gameplanev1alpha1.GameServer{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&networkingv1.NetworkPolicy{}).
		Owns(&gameplanev1alpha1.BackupSchedule{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(&corev1.Pod{}, handler.EnqueueRequestsFromMapFunc(r.mapPodToGameServer)).
		Watches(&gameplanev1alpha1.GameTemplate{}, handler.EnqueueRequestsFromMapFunc(r.mapTemplateToGameServers),
			builder.WithPredicates(predicate.GenerationChangedPredicate{})).
		Complete(r)
}

// Template creation and spec changes must wake servers that failed before any
// child workload existed. Status-only template updates need no reconciliation.
func (r *GameServerReconciler) mapTemplateToGameServers(ctx context.Context, obj client.Object) []reconcile.Request {
	var servers gameplanev1alpha1.GameServerList
	if err := r.List(ctx, &servers); err != nil {
		log.FromContext(ctx).Error(err, "list servers for template change", "template", obj.GetName())
		return nil // Missing-template servers also have a bounded retry.
	}
	var requests []reconcile.Request
	for i := range servers.Items {
		gs := &servers.Items[i]
		if gs.Spec.TemplateRef.Name == obj.GetName() {
			requests = append(requests, reconcile.Request{NamespacedName: client.ObjectKeyFromObject(gs)})
		}
	}
	return requests
}

// mapPodToGameServer maps a game Pod event to its owning GameServer, so
// reconcileCapture notices both a kubelet-reported ephemeral-container status
// change (status.capture.ready needs to follow it) and a pod recreation
// (spec.capture.enabled staying true needs the ephemeral container
// re-injected into the new pod) without waiting for an unrelated event or the
// next periodic resync. The game pod is owned by its StatefulSet, not by the
// GameServer directly (Owns(&appsv1.StatefulSet{}) above only watches the
// StatefulSet object itself), so Owns(&corev1.Pod{}) would never fire —
// matching the same label-based lookup NetworkCaptureReconciler already uses
// for the identical problem (mapPodToNetworkCaptures).
func (r *GameServerReconciler) mapPodToGameServer(_ context.Context, obj client.Object) []reconcile.Request {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	serverName := pod.Labels["app.kubernetes.io/instance"]
	if serverName == "" {
		return nil
	}
	return []reconcile.Request{
		{NamespacedName: types.NamespacedName{Namespace: pod.Namespace, Name: serverName}},
	}
}

// --- sub-reconcilers (skeletons) ---

func (r *GameServerReconciler) reconcilePVC(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	size := resource.MustParse("10Gi")
	if gs.Spec.Storage != nil && !gs.Spec.Storage.Size.IsZero() {
		size = gs.Spec.Storage.Size
	} else if !tmpl.Spec.Storage.Size.IsZero() {
		size = tmpl.Spec.Storage.Size
	}

	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-data", Namespace: gs.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, pvc, func() error {
		if pvc.CreationTimestamp.IsZero() {
			pvc.Spec.AccessModes = []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce}
			pvc.Spec.Resources.Requests = corev1.ResourceList{corev1.ResourceStorage: size}
			if gs.Spec.Storage != nil && gs.Spec.Storage.StorageClassName != nil {
				pvc.Spec.StorageClassName = gs.Spec.Storage.StorageClassName
			} else if tmpl.Spec.Storage.StorageClassName != nil {
				pvc.Spec.StorageClassName = tmpl.Spec.Storage.StorageClassName
			} else if r.DefaultStorageClassName != "" {
				pvc.Spec.StorageClassName = &r.DefaultStorageClassName
			}
			// Seed the volume from a CSI VolumeSnapshot when requested
			// (this is how volume-snapshot Restores stand up a new server).
			// DataSource is immutable once the PVC binds, so it only ever
			// takes effect on this first-creation path. The copied storage
			// size is >= the snapshot's restoreSize by construction (the
			// snapshot came from a PVC of that size).
			if gs.Spec.Storage != nil && gs.Spec.Storage.DataSource != nil {
				apiGroup := "snapshot.storage.k8s.io"
				pvc.Spec.DataSource = &corev1.TypedLocalObjectReference{
					APIGroup: &apiGroup,
					Kind:     gs.Spec.Storage.DataSource.Kind,
					Name:     gs.Spec.Storage.DataSource.Name,
				}
			}
		}
		return controllerutil.SetControllerReference(gs, pvc, r.Scheme)
	})
	return err
}

// reconcileService maintains the main game Service. routeToSentinel is
// planSentinel's decision for this pass (see gameserver_sentinel.go): this
// is the ONLY place spec.selector is written. It used to be written here
// and, separately, by updateServiceSelector — two CreateOrUpdate calls
// racing to set the same field, with this one always winning last and
// silently undoing the flip to the sentinel. Do not reintroduce a second
// writer of svc.Spec.Selector.
func (r *GameServerReconciler) reconcileService(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	routeToSentinel bool,
) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		switch gs.Spec.Networking.Expose {
		case "NodePort":
			svc.Spec.Type = corev1.ServiceTypeNodePort
		case "LoadBalancer":
			svc.Spec.Type = corev1.ServiceTypeLoadBalancer
		}
		// loadBalancerSourceRanges is only valid on LoadBalancer Services;
		// clear it otherwise so a later Expose change doesn't leave a stale
		// (and rejected) allow-list behind.
		if svc.Spec.Type == corev1.ServiceTypeLoadBalancer {
			svc.Spec.LoadBalancerSourceRanges = gs.Spec.Networking.SourceRanges
		} else {
			svc.Spec.LoadBalancerSourceRanges = nil
		}
		if routeToSentinel {
			svc.Spec.Selector = map[string]string{
				sentinelLabel:                sentinelValue,
				"app.kubernetes.io/instance": gs.Name,
			}
		} else {
			svc.Spec.Selector = map[string]string{
				"app.kubernetes.io/name":     "gameplane-game",
				"app.kubernetes.io/instance": gs.Name,
			}
		}
		svc.Spec.Ports = svcPortsFromTemplate(tmpl, gs)
		// Address-pool translation. NOTE: svc.Spec.LoadBalancerIP is never
		// written here or anywhere else — Kubernetes deprecated it in 1.24
		// and both address managers take the request through metadata.
		// The plan's metadata is applied last so an address-manager key
		// wins over a same-key entry in spec.networking.serviceAnnotations,
		// for the same reason the typed hostname field does: it is the
		// explicit, validated, UI-backed field.
		plan := r.addressPlanFor(gs)
		desired := desiredServiceAnnotations(gs)
		for k, v := range plan.serviceAnnotations() {
			desired[k] = v
		}
		applyManagedServiceAnnotations(svc, desired)
		// After applyManagedServiceAnnotations: the managed-label bookkeeping
		// lives in an annotation, and applying it second keeps that annotation
		// from being pruned as an unmanaged leftover.
		applyManagedServiceLabels(svc, plan.serviceLabels())
		return controllerutil.SetControllerReference(gs, svc, r.Scheme)
	})
	return err
}

// managedServiceAnnotationsKey records which annotation keys the operator
// applied from spec.networking.serviceAnnotations on the previous reconcile.
const managedServiceAnnotationsKey = "gameplane.local/managed-service-annotations"

// externalDNSHostnameAnnotation is the de-facto-standard external-dns key the
// operator stamps onto the game Service from spec.networking.hostname so an
// installed external-dns controller publishes the record. The operator does
// not create the DNS record itself (see GameServerNetworking.Hostname).
const externalDNSHostnameAnnotation = "external-dns.alpha.kubernetes.io/hostname"

// desiredServiceAnnotations is the full set of annotations the operator wants
// to manage on the game Service: the user's spec.networking.serviceAnnotations
// plus, when spec.networking.hostname is set, the external-dns hostname hint.
// Set unconditionally on hostname (not gated on Expose type) — external-dns
// decides what to publish from its own source config. The typed hostname field
// is applied last so it wins over a same-key entry in serviceAnnotations: it is
// the explicit, validated, UI-backed field and therefore authoritative.
func desiredServiceAnnotations(gs *gameplanev1alpha1.GameServer) map[string]string {
	desired := make(map[string]string, len(gs.Spec.Networking.ServiceAnnotations)+1)
	for k, v := range gs.Spec.Networking.ServiceAnnotations {
		desired[k] = v
	}
	if h := gs.Spec.Networking.Hostname; h != "" {
		desired[externalDNSHostnameAnnotation] = h
	}
	return desired
}

// applyManagedServiceAnnotations reconciles the user's desired
// serviceAnnotations onto svc so the Service converges when keys are removed
// from spec, without clobbering annotations written by other controllers
// (cloud load balancer, external-dns). It prunes keys the operator set last
// time but that are gone from spec now, applies the desired set, and records
// the managed keys in a sentinel annotation for the next pass. (Merging
// alone, as before, left removed annotations active on the Service.)
func applyManagedServiceAnnotations(svc *corev1.Service, desired map[string]string) {
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	if prev := svc.Annotations[managedServiceAnnotationsKey]; prev != "" {
		for _, k := range strings.Split(prev, ",") {
			if _, keep := desired[k]; !keep {
				delete(svc.Annotations, k)
			}
		}
	}
	keys := make([]string, 0, len(desired))
	for k, v := range desired {
		svc.Annotations[k] = v
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		delete(svc.Annotations, managedServiceAnnotationsKey)
		return
	}
	sort.Strings(keys)
	svc.Annotations[managedServiceAnnotationsKey] = strings.Join(keys, ",")
}

// managedServiceLabelsKey records which *label* keys the operator applied to
// the game Service on the previous reconcile, so a preference that goes away
// takes its label with it instead of leaving, say, a Cilium pool selector
// orphaned on the Service forever — pointing a live server at a pool nobody
// asked for any more.
//
// It is deliberately an annotation, not a label: the value is a comma-joined
// key list, and commas and slashes are not legal in a label value.
const managedServiceLabelsKey = "gameplane.local/managed-service-labels"

// applyManagedServiceLabels is applyManagedServiceAnnotations' twin for
// labels, with the same prune-then-apply-then-record shape and the same
// reason for existing: merging alone leaves a removed key active on the
// Service. Labels on the game Service are otherwise not ours (the selector
// side is spec.selector, written above), so only the keys we recorded last
// time are ever deleted.
//
// It is a no-op when nothing is desired and nothing was managed before, which
// is what keeps a GameServer with no pool preference producing byte-identical
// Service metadata to the pre-feature operator.
func applyManagedServiceLabels(svc *corev1.Service, desired map[string]string) {
	prev := svc.Annotations[managedServiceLabelsKey]
	if prev == "" && len(desired) == 0 {
		return
	}
	if prev != "" {
		for _, k := range strings.Split(prev, ",") {
			if _, keep := desired[k]; !keep {
				delete(svc.Labels, k)
			}
		}
	}
	if len(desired) > 0 && svc.Labels == nil {
		svc.Labels = map[string]string{}
	}
	keys := make([]string, 0, len(desired))
	for k, v := range desired {
		svc.Labels[k] = v
		keys = append(keys, k)
	}
	if svc.Annotations == nil {
		svc.Annotations = map[string]string{}
	}
	if len(keys) == 0 {
		delete(svc.Annotations, managedServiceLabelsKey)
		return
	}
	sort.Strings(keys)
	svc.Annotations[managedServiceLabelsKey] = strings.Join(keys, ",")
}

// Cluster address-manager flavors the operator has a translation branch for,
// mirroring the --address-manager flag in operator/cmd/main.go (which
// validates the value at startup, so an unknown flavor never reaches here).
// The third accepted flavor, "none", has no constant because it has no
// branch: it is every value that is not one of these two.
const (
	addressManagerMetalLB = "metallb"
	addressManagerCilium  = "cilium"
)

// Service metadata keys each address manager reads a pool/address request
// from. MetalLB takes both as annotations; Cilium takes the address as an
// annotation but selects a pool by *label*, which is the whole reason
// applyManagedServiceLabels exists.
//
// ciliumPoolLabel is a Gameplane convention, not a key Cilium knows: the
// cluster admin must mirror it in CiliumLoadBalancerIPPool's
// spec.serviceSelector for pool selection to bind. Where they have not, the
// label is inert — which is why the AddressAssignment condition reports the
// request rather than letting it pass for honored.
//
// The deprecated metallb.universe.tf/* prefixes are still honored by MetalLB
// but are not written for new servers.
const (
	metalLBAddressPoolAnnotation     = "metallb.io/address-pool"
	metalLBLoadBalancerIPsAnnotation = "metallb.io/loadBalancerIPs"
	ciliumAddressAnnotation          = "lbipam.cilium.io/ips"
	ciliumPoolLabel                  = "gameplane.local/lb-pool"
)

// metalLBIPAddressPoolGVK identifies MetalLB's namespaced IPAddressPool
// custom resource (metallb.io/v1beta1). Read via an unstructured GET
// (checkMetalLBPoolExists) rather than a vendored MetalLB Go type — the
// operator needs to know only whether a named pool exists, not to decode its
// spec.
var metalLBIPAddressPoolGVK = schema.GroupVersionKind{
	Group:   "metallb.io",
	Version: "v1beta1",
	Kind:    "IPAddressPool",
}

// poolExistence is the tri-state result of checkMetalLBPoolExists.
type poolExistence int

const (
	// poolExistenceUnknown means the check could not determine an answer:
	// the IPAddressPool kind isn't registered with the API server (MetalLB
	// not installed) or the GET was forbidden by RBAC. Both are legitimate
	// cluster states — a cluster admin can select the metallb flavor before
	// MetalLB itself is installed — so callers MUST fail soft on this value:
	// fall through to the event-derived path rather than report
	// PoolNotFound, which would be a false positive.
	poolExistenceUnknown poolExistence = iota
	// poolExistenceFound means the GET succeeded: the pool exists.
	poolExistenceFound
	// poolExistenceMissing means the GET returned NotFound: the pool does
	// not exist in the configured namespace, so PoolNotFound can be reported
	// directly without waiting for a MetalLB event.
	poolExistenceMissing
)

// checkMetalLBPoolExists GETs the named IPAddressPool in the configured
// MetalLB namespace (r.metalLBNamespace()). It fails SOFT: any error other
// than a clean NotFound — the CRD not being registered, an RBAC Forbidden, a
// transient apiserver error — is reported as poolExistenceUnknown rather than
// treated as evidence the pool is missing. See poolExistence's doc comment
// for why that distinction matters to callers.
func (r *GameServerReconciler) checkMetalLBPoolExists(ctx context.Context, name string) poolExistence {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(metalLBIPAddressPoolGVK)
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: r.metalLBNamespace()}, u)
	switch {
	case err == nil:
		return poolExistenceFound
	case apierrors.IsNotFound(err):
		return poolExistenceMissing
	default:
		return poolExistenceUnknown
	}
}

// metalLBNamespace returns r.MetalLBNamespace, defaulting to MetalLB's own
// "metallb-system" install convention when the field was left unset — e.g.
// by tests that construct a GameServerReconciler directly without going
// through main.go's flag wiring.
func (r *GameServerReconciler) metalLBNamespace() string {
	if r.MetalLBNamespace == "" {
		return "metallb-system"
	}
	return r.MetalLBNamespace
}

// addressPlanOutcome is what the operator did with a pool/address preference
// on this pass. gameserver_status.go maps it onto the AddressAssignment
// condition's reason; the zero value means nothing was requested, so there is
// nothing to report.
type addressPlanOutcome string

const (
	addressPlanNotRequested               addressPlanOutcome = ""
	addressPlanTranslated                 addressPlanOutcome = "Translated"
	addressPlanIgnoredForExposureMode     addressPlanOutcome = "IgnoredForExposureMode"
	addressPlanNoAddressManagerConfigured addressPlanOutcome = "NoAddressManagerConfigured"
	// addressPlanInvalidAddress: spec.networking.address was set but is not a
	// parseable IP address (netip.ParseAddr). The CRD accepts any non-empty
	// string, so this is caught here rather than left to the address manager,
	// which would otherwise pass the raw value straight through to a
	// MetalLB/Cilium annotation and report a generic pending reason instead
	// of a clear format error.
	addressPlanInvalidAddress addressPlanOutcome = "InvalidAddress"
)

// addressPlan is the decision reconcileService reached about a GameServer's
// spec.networking.addressPool / .address preference: what was asked for, and
// whether this cluster can honor it.
type addressPlan struct {
	// Manager is the flavor the plan was made against.
	Manager string
	// Pool and Address are the request as spec'd, verbatim and unvalidated —
	// they are what the status reports back to the user, so they must not be
	// normalized here.
	Pool    string
	Address string
	Outcome addressPlanOutcome
}

// requested reports whether the user asked for a pool or an address at all.
func (p addressPlan) requested() bool {
	return p.Pool != "" || p.Address != ""
}

// planAddressPreference decides how to translate a GameServer's pool/address
// preference for the given address-manager flavor.
//
// It is a pure function of the GameServer and the flavor so gameserver_status.go
// can recompute the identical decision when building the AddressAssignment
// condition, instead of the reconcile threading it through every call.
//
// An unrequestable preference is never silently dropped: it comes back as
// IgnoredForExposureMode (the request is meaningless outside
// Expose=LoadBalancer) or NoAddressManagerConfigured (nothing in this cluster
// can act on it, so leaving it unreported would let the server come up on a
// default-pool address that looks assigned but honors nothing). Exposure mode
// is checked first because it is the more actionable of the two — the user
// owns the expose mode, the cluster admin owns the flavor.
func planAddressPreference(gs *gameplanev1alpha1.GameServer, manager string) addressPlan {
	p := addressPlan{
		Manager: manager,
		Pool:    gs.Spec.Networking.AddressPool,
		Address: gs.Spec.Networking.Address,
	}
	if !p.requested() {
		p.Outcome = addressPlanNotRequested
		return p
	}
	switch {
	case gs.Spec.Networking.Expose != "LoadBalancer":
		p.Outcome = addressPlanIgnoredForExposureMode
	case p.Address != "" && !isValidAddress(p.Address):
		// Caught before the manager-flavor branch so an invalid address is
		// reported the same way regardless of which address manager (or
		// none) is configured.
		p.Outcome = addressPlanInvalidAddress
	case manager == addressManagerMetalLB, manager == addressManagerCilium:
		p.Outcome = addressPlanTranslated
	default:
		// Flavor "none", the empty flag value that means the same, and —
		// unreachable, since main.go validates the flag at startup — any
		// unknown flavor. All fail closed: the request is reported, never
		// quietly allowed to land on a default-pool address.
		p.Outcome = addressPlanNoAddressManagerConfigured
	}
	return p
}

// isValidAddress reports whether addr parses as an IP address (v4 or v6).
// spec.networking.address is a free-form string at the CRD level, so this is
// the operator's own format check before handing the value to an address
// manager (F-056).
func isValidAddress(addr string) bool {
	_, err := netip.ParseAddr(addr)
	return err == nil
}

// addressPlanFor plans this reconciler's configured flavor against gs.
func (r *GameServerReconciler) addressPlanFor(gs *gameplanev1alpha1.GameServer) addressPlan {
	return planAddressPreference(gs, r.AddressManager)
}

// serviceAnnotations is the address-manager metadata this plan wants carried
// on the game Service as annotations. Nil for every non-translated outcome:
// flavor "none" and the ignored expose modes mutate the Service not at all,
// and an unset preference leaves no trace.
func (p addressPlan) serviceAnnotations() map[string]string {
	if p.Outcome != addressPlanTranslated {
		return nil
	}
	out := make(map[string]string, 2)
	switch p.Manager {
	case addressManagerMetalLB:
		if p.Pool != "" {
			out[metalLBAddressPoolAnnotation] = p.Pool
		}
		if p.Address != "" {
			out[metalLBLoadBalancerIPsAnnotation] = p.Address
		}
	case addressManagerCilium:
		// Cilium's pool preference is a label, not an annotation; see
		// serviceLabels.
		if p.Address != "" {
			out[ciliumAddressAnnotation] = p.Address
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// serviceLabels is the address-manager metadata this plan wants carried on
// the game Service as labels — today only Cilium's pool selector. Nil
// otherwise, which prunes the label back off the Service when the preference
// is unset or the flavor changes away from Cilium.
func (p addressPlan) serviceLabels() map[string]string {
	if p.Outcome != addressPlanTranslated || p.Manager != addressManagerCilium || p.Pool == "" {
		return nil
	}
	return map[string]string{ciliumPoolLabel: p.Pool}
}

// reconcileAgentService maintains a dedicated ClusterIP Service
// (`<gs>-agent`) fronting the agent sidecar on port 8090. The game's
// own Service follows spec.networking.expose and may be NodePort or
// LoadBalancer; the agent must never ride along on an externally
// exposed Service, so it gets its own, always cluster-internal one.
// The API and operator dial the agent through this Service
// (api/internal/ws/dialer.go, operator/internal/agent/client.go) —
// per-pod DNS only resolves under headless Services, which the game
// Service is not.
func (r *GameServerReconciler) reconcileAgentService(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) error {
	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name + "-agent", Namespace: gs.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		// The agent must be reachable while the game container is still
		// starting (console/files/logs during long world generation), so
		// don't gate endpoints on whole-pod readiness — the game's
		// readiness probe would otherwise hold the agent hostage.
		svc.Spec.PublishNotReadyAddresses = true
		svc.Spec.Selector = map[string]string{
			"app.kubernetes.io/name":     "gameplane-game",
			"app.kubernetes.io/instance": gs.Name,
		}
		svc.Spec.Ports = []corev1.ServicePort{
			{
				Name:       "agent",
				Port:       8090,
				TargetPort: intstr.FromInt32(8090),
				Protocol:   corev1.ProtocolTCP,
			},
			{
				// Capture sidecar control endpoint (:9091), reachable via the
				// existing <gs>-agent Service DNS name and mTLS cert SANs.
				// Ephemeral containers cannot declare a named containerPort,
				// so the target must be numeric. A Service selects pods, not
				// containers, so the ephemeral container's port is correctly
				// fronted once the container exists.
				Name:       "capture",
				Port:       9091,
				TargetPort: intstr.FromInt32(9091),
				Protocol:   corev1.ProtocolTCP,
			},
		}
		return controllerutil.SetControllerReference(gs, svc, r.Scheme)
	})
	return err
}

// gameIngressPolicyName is the name of the per-GameServer ingress
// NetworkPolicy admitting player traffic (see reconcileNetworkPolicy).
func gameIngressPolicyName(gs *gameplanev1alpha1.GameServer) string {
	return gs.Name + "-game-ingress"
}

// reconcileNetworkPolicy maintains a per-GameServer ingress NetworkPolicy
// (`<gs>-game-ingress`) admitting player traffic to the template's
// advertised ports. The chart ships a default-deny-ingress NetworkPolicy
// with podSelector: {} across the games namespace, and today the only thing
// letting players through is allow-kubelet-probes defaulting probePorts to
// [] (= all ports) with a broad source CIDR — narrowing probePorts would
// silently cut players off. This gives player traffic its own precise,
// per-server rule instead of relying on that side effect.
//
// The PodSelector matches this GameServer's pods only (name+instance
// labels, same pair used by the Service and StatefulSet), so the policy
// never widens to select another server's pods. Ports are read from the
// GameTemplate's advertised ports at their ContainerPort/Protocol —
// GameServer.Spec.Networking.PortOverrides can only remap the *Service*
// port, never the container port a NetworkPolicy actually matches against,
// so overrides are deliberately not consulted here (mirrors
// svcPortsFromTemplate's advertised-port filter, applied to the container
// side instead of the Service side).
//
// CRITICAL: a Kubernetes NetworkPolicy ingress rule with a From but an
// empty/absent Ports list allows ALL ports, not none. So a template with
// zero advertised ports must never get a rule with an empty Ports list —
// that would fling every port, including the agent's 8090 and any
// non-advertised port (e.g. RCON), open to the configured source CIDRs. In
// that case (and whenever the feature is disabled via
// --game-ingress-policy=false) the policy is deleted instead, so toggling
// the flag off — or a template that advertises nothing — converges to no
// policy rather than an accidentally-permissive one.
func (r *GameServerReconciler) reconcileNetworkPolicy(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) error {
	np := &networkingv1.NetworkPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: gameIngressPolicyName(gs), Namespace: gs.Namespace},
	}

	ports := networkPolicyPortsFromTemplate(tmpl)
	if !r.GameIngressPolicyEnabled || len(ports) == 0 {
		// Get (served from the informer cache) instead of an unconditional
		// Delete (always a live apiserver write): the agent's heartbeat
		// patches gameservers/status, which retriggers Reconcile, so an
		// unconditional Delete would fire a DELETE -> 404 for every server
		// on every heartbeat whenever the feature is disabled or the
		// template advertises no ports. Only delete when the policy exists
		// and we actually own it — never remove one a human or another
		// controller created out-of-band.
		var existing networkingv1.NetworkPolicy
		if err := r.Get(ctx, client.ObjectKeyFromObject(np), &existing); err != nil {
			return client.IgnoreNotFound(err)
		}
		if !metav1.IsControlledBy(&existing, gs) {
			return nil
		}
		return client.IgnoreNotFound(r.Delete(ctx, &existing))
	}

	from := make([]networkingv1.NetworkPolicyPeer, 0, len(r.GameIngressFromCIDRs))
	for _, cidr := range r.GameIngressFromCIDRs {
		from = append(from, networkingv1.NetworkPolicyPeer{
			IPBlock: &networkingv1.IPBlock{CIDR: cidr},
		})
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, np, func() error {
		np.Spec.PodSelector = metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/name":     "gameplane-game",
				"app.kubernetes.io/instance": gs.Name,
			},
		}
		np.Spec.PolicyTypes = []networkingv1.PolicyType{networkingv1.PolicyTypeIngress}
		np.Spec.Ingress = []networkingv1.NetworkPolicyIngressRule{{
			From:  from,
			Ports: ports,
		}}
		return controllerutil.SetControllerReference(gs, np, r.Scheme)
	})
	return err
}

// networkPolicyPortsFromTemplate returns one NetworkPolicyPort per
// advertised template port, at its ContainerPort/Protocol (defaulting to
// TCP when unset, mirroring svcPortsFromTemplate). Non-advertised ports
// (e.g. RCON, query) are skipped, same as the Service.
func networkPolicyPortsFromTemplate(tmpl *gameplanev1alpha1.GameTemplate) []networkingv1.NetworkPolicyPort {
	out := make([]networkingv1.NetworkPolicyPort, 0, len(tmpl.Spec.Ports))
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise {
			continue
		}
		proto := p.Protocol
		if proto == "" {
			proto = corev1.ProtocolTCP
		}
		port := intstr.FromInt32(p.ContainerPort)
		out = append(out, networkingv1.NetworkPolicyPort{
			Protocol: &proto,
			Port:     &port,
		})
	}
	return out
}

// svcPortsFromTemplate extracts Kubernetes Service ports declared by the GameTemplate.
func svcPortsFromTemplate(
	tmpl *gameplanev1alpha1.GameTemplate, gs *gameplanev1alpha1.GameServer,
) []corev1.ServicePort {
	overrides := map[string]gameplanev1alpha1.PortOverride{}
	for _, o := range gs.Spec.Networking.PortOverrides {
		overrides[o.Name] = o
	}
	out := make([]corev1.ServicePort, 0, len(tmpl.Spec.Ports))
	for _, p := range tmpl.Spec.Ports {
		if !p.Advertise {
			continue
		}
		port := p.ContainerPort
		nodePort := int32(0)
		if o, ok := overrides[p.Name]; ok {
			if o.ServicePort != 0 {
				port = o.ServicePort
			}
			nodePort = o.NodePort
		}
		sp := corev1.ServicePort{
			Name:       p.Name,
			Port:       port,
			TargetPort: intstr.FromInt32(p.ContainerPort),
			Protocol:   p.Protocol,
			NodePort:   nodePort,
		}
		if sp.Protocol == "" {
			sp.Protocol = corev1.ProtocolTCP
		}
		out = append(out, sp)
	}
	return out
}

// desiredReplicas decides the StatefulSet replica count. It brings the server
// down (gracefully, via the soft stop) on a spec.suspend, while a restart
// drains, or when the idle policy has parked it, and back up otherwise. A
// restart is an operator-owned scale-down → scale-up: the pod is recycled only
// once it is confirmed gone, so the request survives coalesced reconciles. The
// second return value is a requeue hint (>0 while a soft stop or restart drain
// is in progress).
//
// Idle sleep deliberately routes through the very same softStop path as an
// explicit stop: the module-declared stop sequence runs, the game saves, and
// only then does the pod go away. A slept world is not a killed world.
func (r *GameServerReconciler) desiredReplicas(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	idle idleState,
) (int32, time.Duration, error) {
	if gs.Annotations[wipeGuardAnnotation] != "" || pendingWipe(gs) {
		replicas, poll, err := r.softStop(ctx, gs, tmpl)
		if poll == 0 || poll > wipeDrainPoll {
			poll = wipeDrainPoll
		}
		return replicas, poll, err
	}
	// Restore owns the data PVC until its workers have drained. Keep the
	// workload stopped even if a concurrent power request clears suspend.
	if gs.Annotations[restoreGuardAnnotation] != "" {
		return r.softStop(ctx, gs, tmpl)
	}
	// A pending restart (stamped by the API) drives a transient scale-down →
	// scale-up entirely operator-side, so the request can't be lost to a
	// coalesced reconcile the way a client-issued suspend/resume pair can.
	restart, err := r.restartPhase(ctx, gs)
	if err != nil {
		return 1, 0, err
	}
	if restart == restartComplete {
		// The pod is gone (Status.Replicas == 0 / StatefulSet absent), so a
		// scale back to 1 yields a fresh pod identity. Ack so the same token
		// never re-runs, then return to the spec'd power state.
		if err := r.ackRestart(ctx, gs); err != nil {
			return 1, 0, err
		}
		// The power state outlives the restart — whether the user set it or the
		// idle policy did. Without the idle arm here, restarting a sleeping
		// server would quietly bring it back up and leave it running.
		if gs.Spec.Suspend || idle == idleAsleep {
			return 0, 0, nil
		}
		return 1, 0, nil
	}

	// A player joining mid-drain cancels the sleep: the next reconcile reads a
	// non-zero count, reconcileIdle clears the marker, and this falls back to
	// the running branch — which also drops the soft-stop bookkeeping.
	stopping := gs.Spec.Suspend || restart == restartDraining || idle == idleAsleep
	if !stopping {
		// Running: drop any stale soft-stop bookkeeping from a prior stop.
		return 1, 0, r.clearStopAnnotation(ctx, gs)
	}

	replicas, requeue, err := r.softStop(ctx, gs, tmpl)
	// While a restart drains, keep requeuing until the pod is actually gone —
	// the StatefulSet watch already wakes us on pod deletion; this is a backstop.
	if err == nil && restart == restartDraining && requeue == 0 {
		requeue = restartDrainPoll
	}
	return replicas, requeue, err
}

// stopTransport identifies how softStop delivers the module-declared stop
// sequence.
type stopTransport int

const (
	// stopTransportNone means there's no usable way to run the sequence —
	// either the template declares none, or it declares one but exposes
	// neither RCON nor a pty console (or the corresponding client wasn't
	// wired). softStop scales straight to zero in this case rather than
	// waiting out the grace period for nothing.
	stopTransportNone stopTransport = iota
	// stopTransportRCON delivers the sequence over the agent's existing
	// /lifecycle/stop call, which runs it over the game's RCON connection.
	stopTransportRCON
	// stopTransportPTY delivers the sequence over a pod attach to the game
	// container's stdin (consoleMode: pty, no RCON).
	stopTransportPTY
)

// String implements fmt.Stringer so stopTransport reads as a name (not a
// bare int) wherever it's logged or formatted.
func (t stopTransport) String() string {
	switch t {
	case stopTransportNone:
		return "none"
	case stopTransportRCON:
		return "rcon"
	case stopTransportPTY:
		return "pty"
	default:
		return fmt.Sprintf("stopTransport(%d)", int(t))
	}
}

// selectStopTransport decides how to run tmpl's declared stop sequence:
// over RCON when the template exposes it, over a pod-attach to stdin when
// it instead uses consoleMode: pty, or not at all when neither applies.
func selectStopTransport(tmpl *gameplanev1alpha1.GameTemplate) stopTransport {
	switch {
	case templateHasRCON(tmpl):
		return stopTransportRCON
	case EffectiveConsoleMode(tmpl) == "pty":
		return stopTransportPTY
	default:
		return stopTransportNone
	}
}

// softStop computes the replica count while a server is being brought down —
// via spec.suspend or a draining restart. It drives the module-declared
// graceful stop over the transport the template supports (RCON or, for
// consoleMode: pty games with no RCON, a stdin pod-attach) and holds the pod
// up while the game saves, then scales to zero once the game goes not-ready
// (or the grace deadline elapses). Templates with no stop sequence — or a
// sequence but no usable transport — scale straight to zero.
func (r *GameServerReconciler) softStop(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) (int32, time.Duration, error) {
	declared := tmpl.Spec.Capabilities != nil &&
		tmpl.Spec.Capabilities.Lifecycle != nil &&
		len(tmpl.Spec.Capabilities.Lifecycle.Stop) > 0
	if !declared {
		return 0, 0, nil // hard scale-down (no graceful stop available)
	}

	transport := selectStopTransport(tmpl)
	if transport == stopTransportRCON && (r.AgentClient == nil || !r.AgentClient.Enabled()) {
		transport = stopTransportNone
	}
	if transport == stopTransportPTY && r.PodAttacher == nil {
		transport = stopTransportNone
	}
	if transport == stopTransportNone {
		// A sequence is declared but there's nothing to run it over — don't
		// sit through the full grace period for nothing.
		return 0, 0, nil
	}

	// Is the game still up? If the StatefulSet is gone or has no ready
	// replica, there's nothing to gracefully stop — finish the scale-down.
	var ss appsv1.StatefulSet
	switch err := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name}, &ss); {
	case apierrors.IsNotFound(err):
		return 0, 0, nil
	case err != nil:
		return 0, 0, err
	}
	if ss.Status.ReadyReplicas == 0 {
		return 0, 0, nil
	}

	grace := defaultStopGracePeriod
	if gs.Spec.StopGracePeriodSeconds != nil {
		grace = time.Duration(*gs.Spec.StopGracePeriodSeconds) * time.Second
	}

	// First pass: stamp the start of the grace clock, then issue the stop
	// sequence. Stamping first means an update conflict retries cleanly
	// without re-issuing the command.
	if _, ok := gs.Annotations[stopRequestedAtAnnotation]; !ok {
		if err := r.setStopAnnotation(ctx, gs, time.Now().UTC().Format(time.RFC3339)); err != nil {
			return 1, 0, err
		}
		r.issueStopSequence(ctx, gs, tmpl, transport)
		return 1, grace, nil // keep running; requeue at the grace deadline
	}

	// Backstop: the game is still ready, so wait out the remaining grace
	// (readiness going to zero, handled above, scales us down sooner).
	requestedAt, perr := time.Parse(time.RFC3339, gs.Annotations[stopRequestedAtAnnotation])
	if perr != nil {
		// unparseable stamp — don't hang, just scale down
		ctrl.LoggerFrom(ctx).Error(perr, "unparseable stop-requested-at annotation; scaling down immediately")
		return 0, 0, nil
	}
	if remaining := grace - time.Since(requestedAt); remaining > 0 {
		return 1, remaining, nil
	}
	return 0, 0, nil
}

// issueStopSequence runs tmpl's declared stop sequence over the selected
// transport. Best-effort: a failed/unreachable transport must not wedge
// the stop — softStop's readiness check and grace deadline remain the
// authority on when the server actually scales down.
func (r *GameServerReconciler) issueStopSequence(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, transport stopTransport,
) {
	var err error
	switch transport {
	case stopTransportRCON:
		err = r.AgentClient.Stop(ctx, gs.Namespace, gs.Name)
	case stopTransportPTY:
		err = r.PodAttacher.Stop(ctx, gs.Namespace, gs.Name+"-0", gameContainerName, tmpl.Spec.Capabilities.Lifecycle.Stop)
	case stopTransportNone:
		return // unreachable: softStop already filtered this out
	}
	if err != nil {
		log.FromContext(ctx).Info("soft stop: stop sequence call failed; falling back to timed scale-down",
			"transport", transport, "err", err)
	}
}

// setStopAnnotation records the stop request timestamp on the GameServer.
func (r *GameServerReconciler) setStopAnnotation(ctx context.Context, gs *gameplanev1alpha1.GameServer, val string) error {
	if gs.Annotations == nil {
		gs.Annotations = map[string]string{}
	}
	gs.Annotations[stopRequestedAtAnnotation] = val
	return r.Update(ctx, gs)
}

// clearStopAnnotation removes the stop request annotation from the GameServer.
func (r *GameServerReconciler) clearStopAnnotation(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	if _, ok := gs.Annotations[stopRequestedAtAnnotation]; !ok {
		return nil
	}
	delete(gs.Annotations, stopRequestedAtAnnotation)
	return r.Update(ctx, gs)
}

// reconcileStatefulSet ensures the workload StatefulSet matches the desired server spec.
func (r *GameServerReconciler) reconcileStatefulSet(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	ver *gameplanev1alpha1.GameVersion, mc *materializedConfig, replicas int32,
) error {
	image := resolveImage(gs, tmpl, ver)

	// Validate gs.Spec.Env secret and configmap references: any SecretKeyRef
	// or ConfigMapKeyRef must target a server-owned resource. Referencing
	// resources belonging to other subsystems (destinations, auth, other servers)
	// is refused to prevent data exfiltration.
	if err := r.validateServerEnvSources(ctx, gs); err != nil {
		return err
	}

	ss := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: gs.Name, Namespace: gs.Namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		labels := map[string]string{
			"app.kubernetes.io/name":     "gameplane-game",
			"app.kubernetes.io/instance": gs.Name,
			"gameplane.local/template":   tmpl.Name,
		}
		actualReplicas := replicas
		if ss.Annotations[restoreGuardAnnotation] != "" || ss.Annotations[wipeGuardAnnotation] != "" {
			actualReplicas = 0
		}
		// Both creates and updates can originate from stale cache snapshots.
		// Read live ownership even after a marker was cleared for release: a
		// conflicting GameServer release must still keep this workload fenced.
		var live gameplanev1alpha1.GameServer
		if err := r.wipeReader().Get(ctx, client.ObjectKeyFromObject(gs), &live); err != nil {
			return err
		}
		if live.UID != gs.UID {
			return fmt.Errorf("GameServer changed identity before StatefulSet mutation")
		}
		if owner := metav1.GetControllerOf(ss); owner != nil && owner.UID != live.UID {
			return fmt.Errorf("StatefulSet belongs to a different GameServer identity")
		}
		if live.Annotations[restoreGuardAnnotation] == "" {
			// Restore cleanup can race a callback that restamps the workload
			// before the checked GameServer release. That release guarantees
			// worker drain, so the same live owner's absent guard permits
			// removing the stale marker without changing requested replicas.
			delete(ss.Annotations, restoreGuardAnnotation)
		}
		for _, annotation := range []string{restoreGuardAnnotation, wipeGuardAnnotation} {
			if guard := live.Annotations[annotation]; guard != "" {
				actualReplicas = 0
				if ss.Annotations == nil {
					ss.Annotations = make(map[string]string)
				}
				ss.Annotations[annotation] = guard
			}
		}
		if pendingWipe(&live) {
			actualReplicas = 0
		}
		if live.Annotations[wipeGuardAnnotation] == "" && !pendingWipe(&live) {
			workers, err := wipeWorkers(ctx, r.wipeReader(), &live)
			if err != nil {
				return err
			}
			if workers.busy {
				actualReplicas = 0
			} else {
				// A callback may have restamped the marker between workload
				// cleanup and the checked GameServer release. Recover that
				// interrupted release once the live guard and workers are gone.
				delete(ss.Annotations, wipeGuardAnnotation)
				if ss.Annotations[restoreGuardAnnotation] == "" {
					actualReplicas = replicas
				}
			}
		}
		ss.Spec.Replicas = &actualReplicas
		ss.Spec.ServiceName = gs.Name
		ss.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
		ss.Spec.Template.Labels = labels
		// Stamp (or clear) the config fingerprint without touching
		// annotations other actors may have set on the pod template.
		ann := ss.Spec.Template.Annotations
		if mc.hash != "" {
			if ann == nil {
				ann = map[string]string{}
			}
			ann[configHashAnnotation] = mc.hash
		} else {
			delete(ann, configHashAnnotation)
		}
		ss.Spec.Template.Annotations = ann
		ss.Spec.Template.Spec.Containers = []corev1.Container{
			buildGameContainer(gs, tmpl, image, ver, mc),
			buildAgentContainer(gs, tmpl, ver, r.AgentImage, r.AgentLogLevel, r.AgentImagePullPolicy),
		}
		volumes := []corev1.Volume{
			{
				Name: "data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: gs.Name + "-data",
					},
				},
			},
			{
				// Per-GameServer Secret with tls.crt, tls.key, ca.crt.
				// Reconciled by reconcileAgentTLS before this StatefulSet.
				Name: "agent-tls",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: agentTLSSecretName(gs),
					},
				},
			},
			captureVolume(),
		}
		// Extra volumes (spec.storage.extra / template's), one PVC each,
		// mounted only on the game container (see buildGameContainer) — not
		// nested under the data volume, and never on the agent (see
		// agentVolumeMounts' doc comment for why). Assigned wholesale with
		// the rest, so an empty/removed extra list is a no-op here.
		volumes = append(volumes, extraVolumes(gs, tmpl)...)
		// Mount the resolved RCON password (operator-generated or the
		// template's referenced Secret) so the agent sidecar can read it
		// via --rcon-password-file. Added only when the game exposes RCON
		// and doesn't use a game-managed password file.
		if rc := resolveRCON(gs, tmpl); rc.enabled && rc.passwordFile == "" && rc.secretName != "" {
			volumes = append(volumes, corev1.Volume{
				Name: "rcon-password",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: rc.secretName,
						Items:      []corev1.KeyToPath{{Key: rc.secretKey, Path: "password"}},
					},
				},
			})
		}
		// Volumes and InitContainers are assigned wholesale so removing
		// configFiles from the template strips them on the next reconcile.
		if len(mc.files) > 0 {
			items := make([]corev1.KeyToPath, 0, len(mc.files))
			for _, f := range mc.files {
				items = append(items, corev1.KeyToPath{Key: f.key, Path: f.path})
			}
			volumes = append(volumes, corev1.Volume{
				Name: "config-files",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: filesSecretName(gs),
						Items:      items,
					},
				},
			})
			ss.Spec.Template.Spec.InitContainers = []corev1.Container{buildConfigInitContainer(r.ConfigInitImage, tmpl)}
		} else {
			ss.Spec.Template.Spec.InitContainers = nil
		}
		// Per-(version+loader) mod volume, nested at storage.mountPath/<path>
		// so the game image reads mods from its usual dir while they persist
		// on their own PVC. Assigned wholesale with the rest, so switching to
		// a loaderless version drops the mount on the next reconcile.
		if v := modVolume(gs, tmpl, ver); v != nil {
			volumes = append(volumes, *v)
		}
		// Mod-portal credential volumes for each configured provider. When a
		// provider's CredentialsSecretRef is set, its Secret is mounted
		// read-only at /etc/gameplane/mod-creds/<provider>/ so the agent can
		// inject credentials on install. Assigned wholesale so removing the
		// secret ref drops the mount on the next reconcile.
		volumes = append(volumes, modCredsVolumes(resolveModCreds(tmpl))...)
		ss.Spec.Template.Spec.Volumes = volumes
		// Assign unconditionally so clearing spec.nodeSelector also clears the
		// pod template's selector (nil resets it); the previous nil-guard left
		// a removed scheduling pin active on the StatefulSet.
		ss.Spec.Template.Spec.NodeSelector = gs.Spec.NodeSelector
		ss.Spec.Template.Spec.Tolerations = gs.Spec.Tolerations
		ss.Spec.Template.Spec.Affinity = gs.Spec.Affinity
		// Same unconditional-assign reasoning: removing template.spec.security
		// must clear a previously-set pod-level FSGroup, not leave it pinned.
		ss.Spec.Template.Spec.SecurityContext = gamePodSecurityContext(tmpl)
		// Default to the per-GameServer SA so the agent's heartbeat can
		// patch gameservers/status (see reconcileAgentRBAC); an explicit
		// spec.serviceAccountName still wins.
		ss.Spec.Template.Spec.ServiceAccountName = agentServiceAccountName(gs)
		if gs.Spec.ServiceAccountName != "" {
			ss.Spec.Template.Spec.ServiceAccountName = gs.Spec.ServiceAccountName
		}
		// Share the pod's PID namespace so the agent sidecar can read the
		// game process's CPU/memory from /proc. cgroup v2 files are
		// per-container, so without this the agent only sees its own
		// (idle) usage. The pod stays non-privileged; /proc/<pid>/stat and
		// /statm are world-readable, so no extra capabilities are needed.
		shareProcess := true
		ss.Spec.Template.Spec.ShareProcessNamespace = &shareProcess
		return controllerutil.SetControllerReference(gs, ss, r.Scheme)
	})
	return err
}

// effectiveMountPath is where the game's data volume is mounted,
// defaulting to /data when the template doesn't say.
func effectiveMountPath(tmpl *gameplanev1alpha1.GameTemplate) string {
	if tmpl.Spec.Storage.MountPath != "" {
		return tmpl.Spec.Storage.MountPath
	}
	return "/data"
}

// configFilesStagingPath is where the `<gs>-files` Secret is mounted
// inside the config-init container before being copied onto the data
// volume.
const configFilesStagingPath = "/etc/gameplane/config-files"

// DefaultConfigInitImage is the small shell image the operator uses for the
// utility containers it injects into game workloads: the config-init container
// that seeds rendered config files, and the wipe Job that clears a data volume.
// Pinned like the restic image in backup_controller.go; the agent image can't do
// either job (distroless, no shell or cp). Overridable via the operator's
// --config-init-image flag for air-gapped installs.
const DefaultConfigInitImage = "busybox:1.37.0"

// DefaultSentinelImage is the image for the wake sentinel pod that holds
// advertised ports while a server is asleep, waking it when a player connects.
// Overridable via the operator's --sentinel-image flag for air-gapped installs.
const DefaultSentinelImage = "ghcr.io/gameplanepanel/gameplane/sentinel:dev"

// DefaultCaptureSidecarImage is the image for the network capture sidecar
// ephemeral container injected when capture is enabled on a GameServer.
// Overridable via the operator's --capture-sidecar-image flag for air-gapped installs.
const DefaultCaptureSidecarImage = "ghcr.io/gameplanepanel/gameplane/capture-sidecar:dev"

// captureVolumeSizeLimitBytes is the "captures" emptyDir's kubelet-enforced
// SizeLimit (see the "captures" Volume above). It is the single source of
// truth for that number: buildCaptureEphemeralContainer derives
// captureVolumeBudgetBytes from it below, so the sidecar's own
// admission-time budget check (F-187) can never drift from the volume the
// kubelet is actually watching. Matches the 1Gi documented in
// charts/gameplane/values.yaml's capture.defaultMaxSizeBytes comment.
const captureVolumeSizeLimitBytes int64 = 1 * 1024 * 1024 * 1024

// captureVolume returns the pre-provisioned "captures" emptyDir Volume added
// UNCONDITIONALLY to every game pod's StatefulSet template by
// reconcileStatefulSet, regardless of spec.capture.enabled. This is required
// because ephemeral containers cannot add a volume via
// pods/ephemeralcontainers, and pod.spec.volumes is immutable on a running
// pod — the volume must already exist in the StatefulSet pod template before
// the capture sidecar can be injected restart-free. This volume is mounted
// ONLY on the capture sidecar ephemeral container when capture is enabled;
// it is never mounted on the agent or game container (see
// agentVolumeMounts' doc comment for why agents cannot have multiple
// roots). As a consequence, every existing game pod will roll once on the
// release that ships this feature, regardless of whether capture is ever
// used — this is documented in the release upgrade notes.
//
// Extracted to its own function (rather than an inline literal in
// reconcileStatefulSet) so a unit test can assert its SizeLimit against
// captureVolumeSizeLimitBytes without exercising the full reconcile path.
func captureVolume() corev1.Volume {
	return corev1.Volume{
		Name: "captures",
		VolumeSource: corev1.VolumeSource{
			EmptyDir: &corev1.EmptyDirVolumeSource{
				SizeLimit: resource.NewQuantity(captureVolumeSizeLimitBytes, resource.BinarySI), // 1Gi
			},
		},
	}
}

// captureVolumeBudgetBytes is the total bytes of retained (not-yet-deleted)
// capture files plus one new capture's own maxSizeBytes that the sidecar
// will allow on the "captures" volume, passed to it via the
// CAPTURE_VOLUME_BUDGET_BYTES env var below. It reserves a 10% margin under
// captureVolumeSizeLimitBytes: the kubelet's eviction check and the
// sidecar's own accounting (a directory scan taken at admission time, not a
// live byte counter) are not perfectly synchronized, so a start admitted
// right at the hard limit could still tip the kubelet into eviction before
// the new capture writes its first byte.
const captureVolumeBudgetBytes = captureVolumeSizeLimitBytes - captureVolumeSizeLimitBytes/10

// captureContainerName is the capture sidecar's fixed ephemeral-container
// name on every game pod. Both GameServerReconciler's eager injection (on
// spec.capture.enabled) and NetworkCaptureReconciler's idempotent fallback
// injection (on first capture start) check/set this exact name, so neither
// path ever double-injects or fights the other.
const captureContainerName = "capture"

// buildCaptureEphemeralContainer returns the capture sidecar's ephemeral
// container spec. Single definition shared by GameServerReconciler (eager
// injection when spec.capture.enabled transitions to true) and
// NetworkCaptureReconciler (idempotent fallback injection at first capture
// start) so the two paths can never drift apart. image falls back to
// DefaultCaptureSidecarImage when empty.
func buildCaptureEphemeralContainer(image, serverUID string) corev1.EphemeralContainer {
	if image == "" {
		image = DefaultCaptureSidecarImage
	}
	return corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:  captureContainerName,
			Image: image,
			SecurityContext: &corev1.SecurityContext{
				// Matches the capture-sidecar Dockerfile's fixed `USER
				// 65532:65532` on its distroless/static:nonroot final
				// stage.
				RunAsNonRoot: ptrTo(true),
				RunAsUser:    ptrTo(int64(65532)),
				// AllowPrivilegeEscalation must stay true: the capture
				// binary relies on Linux file capabilities (CAP_NET_RAW,
				// granted via `setcap` on the executable in the
				// Dockerfile) to open a raw socket, and the kernel only
				// honors a file capability grant on exec when no_new_privs
				// is off. Drop ALL alone would also empty the process's
				// bounding set, and execve() of a binary carrying
				// cap_net_raw+ep computes the new permitted set as
				// (bounding & fP) | (inheritable & fI): with NET_RAW
				// missing from bounding, that computed permitted set can't
				// contain the file's (effective) NET_RAW, so the kernel
				// fails the exec itself with EPERM — the sidecar would
				// never start, not merely start without the capability.
				// Re-adding NET_RAW keeps it in the bounding set so exec
				// succeeds and the file capability can be granted. This
				// Add does NOT itself grant NET_RAW — Kubernetes sets no
				// ambient capabilities, so the process's own effective set
				// is still empty at container start; only the setcap'd
				// binary's file-capability grant at execve makes NET_RAW
				// effective.
				AllowPrivilegeEscalation: ptrTo(true),
				ReadOnlyRootFilesystem:   ptrTo(true),
				Capabilities: &corev1.Capabilities{
					Drop: []corev1.Capability{"ALL"},
					Add:  []corev1.Capability{"NET_RAW"},
				},
				// Matches buildAgentContainer's fixed distroless
				// SecurityContext — the same seccomp posture every other
				// operator-injected sidecar in this repo runs under.
				SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
			},
			VolumeMounts: []corev1.VolumeMount{
				{
					// Matches the "captures" emptyDir volume
					// reconcileStatefulSet provisions unconditionally on
					// every game pod template.
					Name:      "captures",
					MountPath: "/tmp/captures",
				},
				{
					Name:      "agent-tls",
					MountPath: "/etc/tls",
					ReadOnly:  true,
				},
			},
			Env: []corev1.EnvVar{
				{Name: "TLS_CERT_FILE", Value: "/etc/tls/tls.crt"},
				{Name: "TLS_KEY_FILE", Value: "/etc/tls/tls.key"},
				{Name: "TLS_CA_FILE", Value: "/etc/tls/ca.crt"},
				// See captureVolumeBudgetBytes' doc comment: derived from the
				// "captures" emptyDir's own SizeLimit, with a safety margin,
				// so HandleStart (capture-sidecar/internal/httpserver) can
				// refuse a start that would push retained files plus the new
				// capture past the volume's real limit (F-187).
				{Name: "CAPTURE_VOLUME_BUDGET_BYTES", Value: strconv.FormatInt(captureVolumeBudgetBytes, 10)},
				{Name: "GAMEPLANE_SERVER_UID", Value: serverUID},
			},
		},
		// Targets the game container for a shared pid/network/ipc namespace.
		TargetContainerName: gameContainerName,
	}
}

// hasCaptureEphemeralContainer reports whether the pod's spec already lists
// the capture ephemeral container. Ephemeral containers are append-only —
// this is the idempotency check both injection paths rely on before
// patching pods/ephemeralcontainers.
func hasCaptureEphemeralContainer(pod *corev1.Pod) bool {
	for _, ec := range pod.Spec.EphemeralContainers {
		if ec.Name == captureContainerName {
			return true
		}
	}
	return false
}

// captureEphemeralContainerStatus returns the pod's observed status entry
// for the capture ephemeral container, or nil if the container has never
// been injected or the kubelet hasn't reported a status yet.
func captureEphemeralContainerStatus(pod *corev1.Pod) *corev1.ContainerStatus {
	for i := range pod.Status.EphemeralContainerStatuses {
		if pod.Status.EphemeralContainerStatuses[i].Name == captureContainerName {
			return &pod.Status.EphemeralContainerStatuses[i]
		}
	}
	return nil
}

// reconcileCapture reacts to spec.capture.enabled and keeps status.capture
// converged with the pod's observed ephemeral-container state.
//
// Enabling injects the capture sidecar as an ephemeral container
// immediately, live, without restarting the game container (spec.md's US2
// acceptance scenario 2: "the capture sidecar is injected ... When an admin
// enables capture" — the sidecar must appear at enable time, not only once
// a capture is first requested). This is eager injection, not lazy:
// NetworkCaptureReconciler's own idempotent injection at first-capture-start
// stays in place as a fallback (e.g. a NetworkCapture created for a
// GameServer whose ephemeral container injection from a prior enable hasn't
// landed in this reconciler's cache yet), but the common path is that this
// function has already added it well before any capture is requested.
//
// Disabling stops routing new captures and terminates anything already
// running immediately (US2 acceptance scenario 4), and reports
// status.capture.ready=false / activeCapture=nil right away — but, per
// CaptureConfiguration.Enabled's doc comment, it can NEVER remove the
// already-injected ephemeral container: Kubernetes exposes no API to remove
// one. The container lingers in the pod's spec and
// status.ephemeralContainerStatuses until the pod is next recreated (US2
// acceptance scenario 4's "removed on the next pod recreation"). A reconcile
// of a disabled GameServer with that lingering entry must never error or
// loop — this function only ever reads it to report status, never tries to
// strip it.
func (r *GameServerReconciler) reconcileCapture(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	base := gs.DeepCopy()
	if gs.Status.Capture == nil {
		gs.Status.Capture = &gameplanev1alpha1.CaptureStatus{}
	}

	enabled := gs.Spec.Capture != nil && gs.Spec.Capture.Enabled

	// Cluster-wide kill switch overrides any per-server request: never
	// inject when the operator was started with --capture-enabled=false.
	// Surfaced in status rather than silently doing nothing, so the
	// dashboard/API can explain why a server stays not-ready even with
	// spec.capture.enabled=true.
	clusterAllows := r.CaptureEnabled

	if !enabled {
		// Stop routing new captures and terminate anything already running,
		// before ever touching the (unremovable) ephemeral container.
		if err := r.stopActiveCaptures(ctx, gs); err != nil {
			return fmt.Errorf("stop active captures on disable: %w", err)
		}
		gs.Status.Capture.Ready = false
		return r.patchCaptureStatus(ctx, gs, base)
	}

	if !clusterAllows {
		gs.Status.Capture.Ready = false
		return r.patchCaptureStatus(ctx, gs, base)
	}

	var pod corev1.Pod
	podErr := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: gs.Name + "-0"}, &pod)
	switch {
	case apierrors.IsNotFound(podErr):
		// No pod yet (still provisioning) — nothing to inject into and
		// nothing running to report on.
		gs.Status.Capture.Ready = false
		return r.patchCaptureStatus(ctx, gs, base)
	case podErr != nil:
		return fmt.Errorf("get game pod for capture reconciliation: %w", podErr)
	}

	if !hasCaptureEphemeralContainer(&pod) {
		image := r.CaptureSidecarImage
		pod.Spec.EphemeralContainers = append(pod.Spec.EphemeralContainers, buildCaptureEphemeralContainer(image, string(gs.UID)))
		if err := r.SubResource("ephemeralcontainers").Update(ctx, &pod); err != nil {
			// A requeue racing ahead of the manager cache's propagation of
			// the previous injection can see hasCaptureEphemeralContainer
			// == false a second time and retry — the apiserver reports
			// that as Invalid (duplicate ephemeral container name) or
			// Conflict (stale resourceVersion), not a real failure. The
			// injection has already happened (or is landing); don't error.
			if !apierrors.IsInvalid(err) && !apierrors.IsConflict(err) {
				return fmt.Errorf("inject capture ephemeral container: %w", err)
			}
		}
	}

	// Ready reflects the ephemeral container's own observed state, not
	// merely whether injection was attempted: a crashed/terminated sidecar
	// still has a spec entry and a status entry, but cannot accept capture
	// requests.
	ecStatus := captureEphemeralContainerStatus(&pod)
	gs.Status.Capture.Ready = ecStatus != nil && ecStatus.State.Running != nil
	if ecStatus != nil {
		gs.Status.Capture.SidecarRestarts = ecStatus.RestartCount
	}

	return r.patchCaptureStatus(ctx, gs, base)
}

// stopActiveCaptures asks every NetworkCapture owned by gs that is still
// Pending or Running to stop, and clears gs.Status.Capture.ActiveCapture in
// memory (folded into the caller's single status patch) — used when
// spec.capture.enabled transitions to false, per US2 acceptance scenario 4:
// "any active capture is stopped immediately."
//
// It never writes a capture's phase itself (F-259). Writing Completed or
// Failed here, before the sidecar was stopped, raced the
// NetworkCaptureReconciler: a capture it was just starting would conflict
// on its status write, then be seen as terminal, and the sidecar kept
// capturing. Instead each non-terminal capture gets the same
// stopRequestedAnnotation the API's :capture-stop sets, and
// NetworkCaptureReconciler.completeRequestedStop stops the sidecar first
// and only then marks the capture Completed. The annotation is only added
// when absent, so repeated reconciles (and an earlier user stop) are
// no-ops; LastCaptureTime is likewise only touched the reconcile that adds
// the annotation, so replaying an already-stopped capture on every
// reconcile can't keep bumping it and churning the status patch forever.
func (r *GameServerReconciler) stopActiveCaptures(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	var captures gameplanev1alpha1.NetworkCaptureList
	if err := r.List(ctx, &captures, client.InNamespace(gs.Namespace)); err != nil {
		return fmt.Errorf("list network captures for %s: %w", gs.Name, err)
	}

	now := metav1.Now()
	for i := range captures.Items {
		nc := &captures.Items[i]
		if nc.Spec.ServerRef.Name != gs.Name || isTerminalCapturePhase(nc.Status.Phase) {
			continue
		}
		if _, requested := nc.Annotations[stopRequestedAnnotation]; !requested {
			base := nc.DeepCopy()
			if nc.Annotations == nil {
				nc.Annotations = map[string]string{}
			}
			nc.Annotations[stopRequestedAnnotation] = now.UTC().Format(time.RFC3339)
			if err := r.Patch(ctx, nc, client.MergeFrom(base)); err != nil {
				return fmt.Errorf("request stop of capture %s: %w", nc.Name, err)
			}
			gs.Status.Capture.LastCaptureTime = &now
		}
	}

	gs.Status.Capture.ActiveCapture = nil
	return nil
}

// patchCaptureStatus issues a JSON merge patch of only status.capture,
// matching reconcileStatus' MergeFrom pattern (gameserver_status.go) so this
// function never clobbers fields other reconcile steps concurrently own
// (e.g. the agent sidecar's status.agent heartbeat).
func (r *GameServerReconciler) patchCaptureStatus(ctx context.Context, gs, base *gameplanev1alpha1.GameServer) error {
	// Patch responses contain the full GameServer, even for a no-op patch.
	// Keep the caller's snapshot coherent with its already-derived idle state
	// so a fresh response cannot turn an older idle clock into a stale write.
	if err := r.Status().Patch(ctx, gs.DeepCopy(), client.MergeFrom(base)); err != nil {
		return fmt.Errorf("patch capture status for %s: %w", gs.Name, err)
	}
	return nil
}

// configInitImageOrDefault resolves the configured shell image, falling back to
// the pin when the operator wasn't given a --config-init-image.
func configInitImageOrDefault(image string) string {
	if image == "" {
		return DefaultConfigInitImage
	}
	return image
}

// buildConfigInitContainer copies the rendered config files onto the
// data volume on every pod start — operator-rendered files always win
// over in-place edits (e.g. via the dashboard Files tab). image is the
// operator-configured config-init image; empty falls back to the pin.
// When the template sets security.fsGroup, config-init makes each file
// and directory it copied group-writable so a non-root game sharing that
// fsGroup can update them.
func buildConfigInitContainer(image string, tmpl *gameplanev1alpha1.GameTemplate) corev1.Container {
	image = configInitImageOrDefault(image)
	mountPath := effectiveMountPath(tmpl)
	cpCmd := "cp -RL " + configFilesStagingPath + "/* '" + mountPath + "/'"

	// If fsGroup is set, append a chmod step to make each file and directory
	// config-init copied group-writable (entries that came from the staging tree;
	// other files already on the volume keep their modes).
	if tmpl.Spec.Security != nil && tmpl.Spec.Security.FSGroup != nil {
		cpCmd += " && cd " + configFilesStagingPath + " && find ./* -follow \\( -type f -o -type d \\) | while IFS= read -r p; do chmod g+w '" + mountPath + "/'\"$p\"; done"
	}

	return corev1.Container{
		Name:    "config-init",
		Image:   image,
		Command: []string{"/bin/sh", "-c"},
		// -L dereferences the kubelet's per-key symlinks; the * glob
		// skips the ..data/..<timestamp> dot-entries of the Secret mount.
		Args: []string{cpCmd},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "config-files", MountPath: configFilesStagingPath, ReadOnly: true},
			{Name: "data", MountPath: mountPath},
		},
	}
}

// effectiveResources resolves the game container's compute resources:
// the template's defaults, replaced wholesale by spec.resources when set.
func effectiveResources(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
) corev1.ResourceRequirements {
	if gs.Spec.Resources != nil {
		return *gs.Spec.Resources
	}
	return tmpl.Spec.Resources
}

// buildGameContainer constructs the primary game server container definition.
func buildGameContainer(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate, image string,
	ver *gameplanev1alpha1.GameVersion, mc *materializedConfig,
) corev1.Container {
	mountPath := effectiveMountPath(tmpl)
	// expose: Hostport binds each container port directly on the node so the
	// game is reachable at <node>:<port> without a NodePort/LoadBalancer
	// Service (the Service stays ClusterIP). Suits single-node k3s/homelab
	// installs where one pod owns the host port.
	hostPort := gs.Spec.Networking.Expose == "Hostport"
	ports := make([]corev1.ContainerPort, 0, len(tmpl.Spec.Ports))
	for _, p := range tmpl.Spec.Ports {
		cp := corev1.ContainerPort{Name: p.Name, ContainerPort: p.ContainerPort, Protocol: p.Protocol}
		// Only bind advertised ports on the node — non-advertised admin
		// ports (RCON, query, telnet) must stay pod-local, mirroring the
		// Advertise filter svcPortsFromTemplate/networkPolicyPortsFromTemplate
		// already apply.
		if hostPort && p.Advertise {
			cp.HostPort = p.ContainerPort
		}
		ports = append(ports, cp)
	}
	// Later entries win on duplicate names: template defaults, then the
	// selected version's env (e.g. itzg TYPE/VERSION), then schema-resolved
	// config, then the mods-by-id projection, then explicit spec.env
	// overrides.
	env := append([]corev1.EnvVar{}, tmpl.Spec.Env...)
	if ver != nil {
		env = append(env, ver.Env...)
	}
	env = append(env, mc.env...)
	// capabilities.mods.idList projection: for games whose server
	// downloads its own mods given a list of ids (ARK's -mods=<id>,<id>
	// launch flag, Project Zomboid's MOD_IDS) rather than the agent
	// dropping files into a mods directory. Runs after the config-schema
	// env above so append mode can extend a config-schema-provided value
	// (e.g. ARK's ASA_START_PARAMS), but before spec.env so an explicit
	// user override of the same env var still wins — same precedence
	// spec.env already holds over everything else. modIDListEnv mutates an
	// existing plain-Value entry named idList.Env in place (returning nil)
	// when one is already present above, rather than appending a duplicate
	// that would only be resolved by the kubelet's last-wins rule; it
	// returns a new entry to append here only when there's nothing to
	// merge into (or the only match is ValueFrom-based and must not be
	// clobbered — see modIDListEnv's doc comment).
	if e := modIDListEnv(tmpl, gs, env); e != nil {
		env = append(env, *e)
	}
	env = append(env, gs.Spec.Env...)
	// The operator-managed RCON password wins, so the game and the agent
	// sidecar always agree on it.
	if e := rconGameEnv(gs, tmpl); e != nil {
		env = append(env, *e)
	}

	res := effectiveResources(gs, tmpl)

	mounts := []corev1.VolumeMount{{Name: "data", MountPath: mountPath}}
	if m := modVolumeMount(tmpl, ver); m != nil {
		mounts = append(mounts, *m)
	}
	mounts = append(mounts, extraVolumeMounts(gs, tmpl)...)

	c := corev1.Container{
		Name:         gameContainerName,
		Image:        image,
		Command:      tmpl.Spec.Command,
		Args:         tmpl.Spec.Args,
		Env:          env,
		Ports:        ports,
		VolumeMounts: mounts,
		Resources:    res,
	}
	if tmpl.Spec.Probes != nil {
		c.ReadinessProbe = tmpl.Spec.Probes.Readiness
		c.LivenessProbe = tmpl.Spec.Probes.Liveness
		c.StartupProbe = tmpl.Spec.Probes.Startup
	}
	// Per-server probe overrides win over the template, one probe at a time.
	if p := gs.Spec.Probes; p != nil {
		if p.Readiness != nil {
			c.ReadinessProbe = p.Readiness
		}
		if p.Liveness != nil {
			c.LivenessProbe = p.Liveness
		}
		if p.Startup != nil {
			c.StartupProbe = p.Startup
		}
	}
	// PTY console mode requires the kubelet to allocate a TTY for the
	// container at start time. These fields are immutable once the pod
	// exists, so changing ConsoleMode forces a pod recreate (handled by
	// StatefulSet's normal rollout when the template hash changes).
	if EffectiveConsoleMode(tmpl) == "pty" {
		c.TTY = true
		c.Stdin = true
	}
	c.SecurityContext = gameContainerSecurityContext(tmpl)
	return c
}

// gameContainerSecurityContext returns the GAME container's
// SecurityContext derived from the template's optional Security block, or
// nil when the template sets neither RunAsUser nor RunAsGroup — so a
// template that doesn't opt in renders a pod spec byte-identical to
// before this field existed (no empty `securityContext: {}`). Unlike
// buildAgentContainer's fixed distroless SecurityContext, this only sets
// what the template asks for: most game images need nothing beyond the
// uid/gid override to run non-root (e.g. ARK's uid 25000), and forcing
// readOnlyRootFilesystem/capabilities drops on arbitrary third-party game
// images would break ones that don't expect it.
func gameContainerSecurityContext(tmpl *gameplanev1alpha1.GameTemplate) *corev1.SecurityContext {
	sec := tmpl.Spec.Security
	if sec == nil || (sec.RunAsUser == nil && sec.RunAsGroup == nil) {
		return nil
	}
	return &corev1.SecurityContext{
		RunAsUser:  sec.RunAsUser,
		RunAsGroup: sec.RunAsGroup,
	}
}

// validateServerEnvSources ensures any SecretKeyRef or ConfigMapKeyRef in gs.Spec.Env targets
// an existing Secret or ConfigMap owned by gs via a controller OwnerReference matching
// gs.Name and gs.UID. Referencing unowned Secrets or ConfigMaps (such as restic credentials,
// auth provider secrets, or other servers' secrets/configmaps) is refused.
func (r *GameServerReconciler) validateServerEnvSources(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	for _, ev := range gs.Spec.Env {
		if ev.ValueFrom == nil {
			continue
		}
		if ev.ValueFrom.SecretKeyRef != nil {
			secName := ev.ValueFrom.SecretKeyRef.Name
			var sec corev1.Secret
			if err := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: secName}, &sec); err != nil {
				return fmt.Errorf("secret reference %q in spec.env: %w", secName, err)
			}
			if !isServerOwnedSecret(&sec, gs) {
				return fmt.Errorf("secret reference %q in spec.env is not owned by GameServer %s/%s",
					secName, gs.Namespace, gs.Name)
			}
		}
		if ev.ValueFrom.ConfigMapKeyRef != nil {
			cmName := ev.ValueFrom.ConfigMapKeyRef.Name
			var cm corev1.ConfigMap
			if err := r.Get(ctx, types.NamespacedName{Namespace: gs.Namespace, Name: cmName}, &cm); err != nil {
				return fmt.Errorf("configmap reference %q in spec.env: %w", cmName, err)
			}
			if !isServerOwnedConfigMap(&cm, gs) {
				return fmt.Errorf("configmap reference %q in spec.env is not owned by GameServer %s/%s",
					cmName, gs.Namespace, gs.Name)
			}
		}
	}
	return nil
}

// validateServerEnvSecrets is retained for backward compatibility and delegates to validateServerEnvSources.
func (r *GameServerReconciler) validateServerEnvSecrets(ctx context.Context, gs *gameplanev1alpha1.GameServer) error {
	return r.validateServerEnvSources(ctx, gs)
}

// isServerOwnedSecret reports whether the given Secret belongs to gs by checking
// for an OwnerReference matching the GameServer's Kind, Name, and UID.
// Canonical name suffixes and user-settable labels are NOT treated as proof of ownership.
func isServerOwnedSecret(sec *corev1.Secret, gs *gameplanev1alpha1.GameServer) bool {
	if sec == nil || gs == nil || gs.Name == "" {
		return false
	}
	for _, ref := range sec.OwnerReferences {
		if ref.Kind == "GameServer" && ref.Name == gs.Name {
			if gs.UID != "" && ref.UID != gs.UID {
				continue
			}
			if gs.UID == "" && ref.UID != "" {
				continue
			}
			return true
		}
	}
	return false
}

// isServerOwnedConfigMap reports whether the given ConfigMap belongs to gs by checking
// for an OwnerReference matching the GameServer's Kind, Name, and UID.
// Canonical name suffixes and user-settable labels are NOT treated as proof of ownership.
func isServerOwnedConfigMap(cm *corev1.ConfigMap, gs *gameplanev1alpha1.GameServer) bool {
	if cm == nil || gs == nil || gs.Name == "" {
		return false
	}
	for _, ref := range cm.OwnerReferences {
		if ref.Kind == "GameServer" && ref.Name == gs.Name {
			if gs.UID != "" && ref.UID != gs.UID {
				continue
			}
			if gs.UID == "" && ref.UID != "" {
				continue
			}
			return true
		}
	}
	return false
}

// gamePodSecurityContext returns the pod-level SecurityContext carrying
// FSGroup, or nil when the template's Security block doesn't set one —
// mirroring gameContainerSecurityContext's byte-identical-when-unset
// behavior at the pod level.
func gamePodSecurityContext(tmpl *gameplanev1alpha1.GameTemplate) *corev1.PodSecurityContext {
	sec := tmpl.Spec.Security
	if sec == nil || sec.FSGroup == nil {
		return nil
	}
	return &corev1.PodSecurityContext{FSGroup: sec.FSGroup}
}

// buildAgentContainer constructs the sidecar management agent container definition.
func buildAgentContainer(
	gs *gameplanev1alpha1.GameServer, tmpl *gameplanev1alpha1.GameTemplate,
	ver *gameplanev1alpha1.GameVersion, fallbackImage, logLevel, pullPolicy string,
) corev1.Container {
	image := fallbackImage
	res := corev1.ResourceRequirements{}
	if tmpl.Spec.Agent != nil {
		if tmpl.Spec.Agent.Image != "" {
			image = tmpl.Spec.Agent.Image
		}
		res = tmpl.Spec.Agent.Resources
	}
	mountPath := effectiveMountPath(tmpl)
	nonRoot := true
	roRootFS := true
	noPrivEsc := false
	uid := int64(65532)
	args := []string{
		"--tls-cert=/etc/gameplane/agent-tls/tls.crt",
		"--tls-key=/etc/gameplane/agent-tls/tls.key",
		"--tls-client-ca=/etc/gameplane/agent-tls/ca.crt",
		// Must match the "data" VolumeMount below (agentVolumeMounts also takes
		// mountPath) so the agent's file ops, mods dir, and disk-usage stats are
		// rooted at the same path the game container's data volume is mounted at
		// — not the agent's own /data default, which is only correct when the
		// template happens to mount storage at /data.
		"--data-root=" + mountPath,
	}
	if tmpl.Spec.LogPath != "" {
		args = append(args, "--game-log-path="+tmpl.Spec.LogPath)
	}
	rc := resolveRCON(gs, tmpl)
	if rc.enabled {
		if rc.passwordFile != "" {
			args = append(args, "--rcon-password-file="+path.Join(mountPath, rc.passwordFile))
		} else if rc.secretName != "" {
			args = append(args, "--rcon-password-file="+rconAuthMountPath+"/password")
		}
		args = append(args, "--rcon-port="+strconv.FormatInt(int64(rc.port), 10))
	}
	env := []corev1.EnvVar{
		{Name: "GAMEPLANE_SERVER_NAME", Value: gs.Name},
		{Name: "GAMEPLANE_SERVER_UID", Value: string(gs.UID)},
		{Name: "GAMEPLANE_TEMPLATE", Value: tmpl.Name},
		{Name: "GAMEPLANE_GAME", Value: tmpl.Spec.Game},
		// Games without RCON (consoleMode pty/none) must not have the
		// agent dialing a console port that doesn't exist — players
		// and moderation endpoints degrade instead.
		{Name: "GAMEPLANE_RCON_ENABLED", Value: strconv.FormatBool(templateHasRCON(tmpl))},
		// Selects which wire protocol the agent speaks when RCON is
		// enabled above; ignored otherwise. Always set (not just when
		// non-default) so the agent's own back-compat default and the
		// operator's stay in one place instead of two.
		{Name: "GAMEPLANE_RCON_PROTOCOL", Value: rconProtocol(tmpl)},
		// The pod shares its PID namespace (ShareProcessNamespace), so the
		// agent reports the GAME process's CPU/memory from /proc rather than
		// its own per-container cgroup (which shows only the idle sidecar).
		{Name: "GAMEPLANE_USAGE_PROC", Value: "1"},
	}
	// Only when explicitly configured — the env change rolls every game
	// StatefulSet, so an unset flag must not differ from the old pod spec.
	if logLevel != "" {
		env = append(env, corev1.EnvVar{Name: "GAMEPLANE_LOG_LEVEL", Value: logLevel})
	}
	// In proc mode the agent can't read the game container's cgroup limit, so
	// pass the resolved limits through as the denominator for the dashboard's
	// usage bars. Mirrors buildGameContainer's resource resolution.
	gameRes := effectiveResources(gs, tmpl)
	if cpu := gameRes.Limits.Cpu(); cpu != nil && !cpu.IsZero() {
		env = append(env, corev1.EnvVar{
			Name: "GAMEPLANE_CPU_LIMIT_MILLICORES", Value: strconv.FormatInt(cpu.MilliValue(), 10),
		})
	}
	if mem := gameRes.Limits.Memory(); mem != nil && !mem.IsZero() {
		env = append(env, corev1.EnvVar{
			Name: "GAMEPLANE_MEM_LIMIT_BYTES", Value: strconv.FormatInt(mem.Value(), 10),
		})
	}
	// Declared capability commands travel to the agent as one JSON blob;
	// the env change rolls the StatefulSet, so capability edits apply on the
	// next pod rollout like every other template change. resolveCapabilities
	// collapses the per-loader mods map into the active version's concrete
	// Mods.Path, so the agent stays loader-agnostic (no agent code change).
	if caps := resolveCapabilities(tmpl, ver); caps != nil {
		if b, err := json.Marshal(caps); err == nil {
			env = append(env, corev1.EnvVar{Name: "GAMEPLANE_CAPABILITIES", Value: string(b)})
		}
	}
	c := corev1.Container{
		Name:         "agent",
		Image:        image,
		Args:         args,
		Env:          env,
		VolumeMounts: agentVolumeMounts(gs, tmpl, ver, mountPath),
		// "agent" (8090) is the mTLS control port. "metrics" (9090) is the
		// separate plain-HTTP Prometheus listener (agent/cmd/main.go's
		// --metrics-addr); declaring it as a named containerPort lets the
		// chart's agent PodMonitor target it by name (podMetricsEndpoints[].port)
		// instead of a bare portNumber, which needs a recent
		// Prometheus-Operator CRD version to exist at all.
		Ports: []corev1.ContainerPort{
			{Name: "agent", ContainerPort: 8090},
			{Name: "metrics", ContainerPort: 9090},
		},
		Resources: res,
		SecurityContext: &corev1.SecurityContext{
			RunAsNonRoot:             &nonRoot,
			RunAsUser:                &uid,
			ReadOnlyRootFilesystem:   &roRootFS,
			AllowPrivilegeEscalation: &noPrivEsc,
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			SeccompProfile:           &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
		},
	}
	// Only when explicitly configured — an empty value leaves
	// ImagePullPolicy unset so Kubernetes applies its own default, matching
	// pre-flag pod specs and avoiding a surprise rollout on operator
	// upgrade for deployers who haven't set the chart's image.pullPolicy.
	if pullPolicy != "" {
		c.ImagePullPolicy = corev1.PullPolicy(pullPolicy)
	}
	return c
}

// reconcileBackupSchedule synchronizes automated backup schedules with the server backup policy.
func (r *GameServerReconciler) reconcileBackupSchedule(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) error {
	name := gs.Name + "-auto"
	bs := &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: gs.Namespace},
	}

	if gs.Spec.BackupPolicy == nil {
		return r.deleteIfControlledBy(ctx, gs, bs)
	}

	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, bs, func() error {
		bs.Spec.ServerRef = gameplanev1alpha1.LocalObjectRef{Name: gs.Name}
		bs.Spec.Schedule = gs.Spec.BackupPolicy.Schedule
		bs.Spec.RepoRef = &gs.Spec.BackupPolicy.RepoRef
		bs.Spec.Retention = gs.Spec.BackupPolicy.Retention
		bs.Spec.Suspend = gs.Spec.BackupPolicy.Suspend
		// InlineBackupPolicy exposes no quiesce field, so this is the only
		// place that sets it. bs.Spec.Quiesce has no `omitempty` (see its
		// doc comment), so leaving it unset here would send an explicit
		// `false` on every Create/Update via the typed client and defeat
		// the CRD's `default: true` (F-045) — always send the true the CRD
		// documents as the default.
		bs.Spec.Quiesce = true
		return controllerutil.SetControllerReference(gs, bs, r.Scheme)
	})
	return err
}

// setPhase updates the GameServer status phase and ready condition message.
func (r *GameServerReconciler) setPhase(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, msg string,
) error {
	// Patch (not Update) so we don't carry/revert the agent's concurrently
	// written status.agent — see reconcileStatus for the full rationale.
	base := gs.DeepCopy()
	phase := gameplanev1alpha1.GameServerPhaseFailed
	gs.Status.Phase = phase
	gs.Status.Conditions = upsertCondition(gs.Status.Conditions, metav1.Condition{
		Type:               "Ready",
		Status:             metav1.ConditionFalse,
		Reason:             string(phase),
		Message:            msg,
		ObservedGeneration: gs.Generation,
	})
	return r.Status().Patch(ctx, gs, client.MergeFrom(base))
}

// listServiceEvents fetches the recent events on the game Service into the
// provided slice. Errors are logged but not fatal — if event fetching fails,
// the reconciliation continues without event-derived failure reasons.
//
// The namespace is the only server-side filter: controller-runtime's cached
// client can only serve client.MatchingFields for keys registered with
// mgr.GetFieldIndexer().IndexField, and this operator registers no index on
// involvedObject. Filtering on involvedObject there would make every List
// fail, silently disabling the whole feature, so the involvedObject match is
// done in Go over the returned items instead. Per-namespace event volume is
// small enough for that to be cheap.
// listServiceEvents fetches the recent events on the game Service into the
// provided events slice. To reduce apiserver load (namespace Event LIST is
// O(Events)), this skips the read when the AddressAssignment condition is
// already in a terminal state that won't be affected by events:
//   - Condition True (Assigned): address was successfully assigned
//   - Condition False with terminal reason: IgnoredForExposureMode,
//     NoAddressManagerConfigured, AddressInUse, PoolNotFound (re-derived
//     live from a direct IPAddressPool GET every pass regardless of this
//     skip — see reconcileStatus — so skipping the event LIST here never
//     staleness-locks it), or AllocationFailed (an event-derived failure,
//     latched by addressAssignmentCondition once observed — see its doc
//     comment — so skipping the re-LIST here is exactly what the latch is
//     for: it need not survive on a fresh event every pass)
//
// Special case: if the condition was True but the address has since
// disappeared (regression), the read resumes to detect a new failure reason.
// This is checked by verifying the address still exists in status.endpoints.
func (r *GameServerReconciler) listServiceEvents(
	ctx context.Context, gs *gameplanev1alpha1.GameServer, events *[]corev1.Event,
) error {
	// Check if the AddressAssignment condition already signals a stable state.
	addrCond := meta.FindStatusCondition(gs.Status.Conditions, gameplanev1alpha1.GameServerConditionAddressAssignment)
	if addrCond != nil {
		if addrCond.Status == metav1.ConditionTrue {
			// Condition is True (Assigned). Check if the address is still present
			// in status to detect regression.
			if len(gs.Status.Endpoints) > 0 {
				// Address still assigned; no need to read events.
				*events = nil
				return nil
			}
			// Address is gone (regression); fall through to read events.
		} else {
			// Condition is False. Check if the reason is terminal.
			// Terminal reasons: those derived from spec (IgnoredForExposureMode,
			// NoAddressManagerConfigured, AddressInUse), from a direct pool
			// check (PoolNotFound), or from an event (AllocationFailed).
			// Non-terminal reasons: ServiceNotReady, AssignmentPending.
			switch addrCond.Reason {
			case "IgnoredForExposureMode", "NoAddressManagerConfigured", "AddressInUse",
				"PoolNotFound", "AllocationFailed":
				// Terminal failure; no need to read events.
				*events = nil
				return nil
			case "ServiceNotReady", "AssignmentPending":
				// Non-terminal; fall through to read events.
			default:
				// Unknown reason; assume it came from events and fall through.
			}
		}
	}

	var svc corev1.Service
	svcKnown := r.Get(ctx, types.NamespacedName{Name: gs.Name, Namespace: gs.Namespace}, &svc) == nil

	var eventList corev1.EventList
	reader := r.APIReader
	if reader == nil {
		reader = r.Client
	}
	if err := reader.List(ctx, &eventList, client.InNamespace(gs.Namespace)); err != nil {
		return fmt.Errorf("list events in namespace %s: %w", gs.Namespace, err)
	}

	matched := make([]corev1.Event, 0, len(eventList.Items))
	for i := range eventList.Items {
		ev := eventList.Items[i]
		if ev.InvolvedObject.Kind != "Service" || ev.InvolvedObject.Name != gs.Name {
			continue
		}
		// When the live Service is readable, pin the match to its UID so
		// events left over from a previous Service of the same name are not
		// attributed to the current one. If it is not readable, fall back to
		// the name match rather than dropping every event.
		if svcKnown && ev.InvolvedObject.UID != "" && ev.InvolvedObject.UID != svc.UID {
			continue
		}
		matched = append(matched, ev)
	}
	*events = matched
	return nil
}

// findAddressConflict checks whether another GameServer in the cluster
// already has the same explicit address assigned (in status.endpoints) or
// requested (in spec.networking.address).
//
// Returns the conflicting GameServer's name, or empty string if none found.
// Errors are logged but not fatal.
//
// The whole candidate set — every other server that either holds the
// address or requests it — is resolved as ONE ordered comparison rather
// than two independent branches. Splitting "holds" and "requests" into
// separate branches (one of them unconditional) used to make the ordering
// non-total: a server B that merely requested an address, created before a
// server A that already held it, could report A as a conflict while A
// simultaneously reported B as a conflict back — a permanent livelock, and
// with 3+ contenders the *reported name* also flapped because it was
// whichever candidate the cached List happened to yield first (List order
// from controller-runtime is not stable).
//
// The fix: gather every holder/requester of the address into one slice,
// sort it by a strict total order — holds ranks before merely requests,
// then creationTimestamp, namespace, name — and report a conflict iff the
// minimum of that sorted set sorts strictly before gs itself. At least one
// server in any contending set is told "no conflict" (see addressConflictLess
// below for why it is "at least one" and not "exactly one"), and the
// reported name is deterministic regardless of List's return order.
//
// A candidate can join the set two independent ways: it "requests" the
// address (spec.networking.address matches) or it "holds" the address
// (status.endpoints carries it). "Holds" is judged independently of
// "requests" — a pool-assigned server with no explicit spec address must
// still be caught when another server explicitly requests the address it
// was assigned. An actual holder must outrank a mere requester regardless
// of creation order — the address was already handed to the holder by the
// address manager, so a requester created earlier has no real claim over
// it — which is why "holds" is a leading key in the total order, not a
// second-order tiebreak. See addressConflictLess for the precedence rule
// and why it cannot produce a mutual pair between the two.
//
// Two discriminators keep "holds" from false-positiving on endpoints that
// were never a real LoadBalancer address assignment:
//   - GameServerEndpoint.TunnelProvider is set for every tunnel-sourced
//     endpoint that reaches status.endpoints through a code path that
//     stamps it (frp, tailscale, and both branches — success and
//     validation-failure — of playit); only a TunnelProvider == "" endpoint
//     is eligible.
//   - endpointsFromService falls back to svc.Spec.ClusterIP as Host when the
//     Service has no LoadBalancer ingress yet, so a Host match only counts
//     as holding when the candidate's own expose mode is LoadBalancer.
//     This narrows, but does not close, the false-positive: an
//     LB-exposed candidate awaiting ingress assignment still carries its
//     ClusterIP as Host, so if a requested address happens to fall inside
//     the Service CIDR (not the LB pool's range — an unlikely but not
//     impossible operator misconfiguration) that candidate would still
//     read as "holding" it. GameServerEndpoint.Pool cannot be used to
//     close this: it is stamped only for a translated pool request
//     (addressPlanTranslated), never for an explicit spec.networking.address
//     request served directly off the Service's real LB ingress, so an
//     empty Pool does not distinguish a genuine explicit-address holder
//     from a pending ClusterIP fallback. Closing it fully would need a
//     new discriminator field on GameServerEndpoint (a CRD type change),
//     which is out of scope here.
//
// Candidates being deleted (a non-nil metadata.deletionTimestamp) are
// skipped entirely: a terminating server cannot legitimately hold or claim
// an address going forward, and letting it win the tiebreak would
// permanently block a live server behind a server that is already on its
// way out.
//
// Caveats: the no-mutual-pair property holds only under these conditions.
//
// Cross-address pair: this assumes every contender's spec.networking.address is
// either empty or the contended address. Two servers with swapped explicit
// addresses while their status shows the old ones can name each other
// permanently. Impact is bounded to the status condition; it does not block
// reconciliation.
//
// Unreported duplicate hold: an older server both holding and requesting, plus
// a younger pure holder of the same address, is reported by neither. The older
// server is the minimum and returns "no conflict"; the pure holder hits the
// early return. This is a known gap.
//
// Cache staleness: the invariant assumes every contender observes the same
// holds flags. Reconciles read through informer caches, so a transient mutual
// pair is possible while caches disagree. It self-heals on the next status
// transition, unlike a permanent livelock.
func (r *GameServerReconciler) findAddressConflict(
	ctx context.Context, gs *gameplanev1alpha1.GameServer,
) (string, error) {
	if gs.Spec.Networking.Address == "" {
		return "", nil
	}

	// Listed cluster-wide, not namespace-scoped: load-balancer addresses are a
	// cluster-wide resource, so a conflicting holder can live in any namespace.
	// The operator's ClusterRole already grants cluster-wide list on gameservers.
	var gsList gameplanev1alpha1.GameServerList
	if err := r.List(ctx, &gsList); err != nil {
		return "", fmt.Errorf("list gameservers: %w", err)
	}

	requestedAddr := gs.Spec.Networking.Address

	var candidates []addressCandidate
	for i := range gsList.Items {
		other := &gsList.Items[i]
		// Skip self — matched on namespace *and* name, so a same-named server
		// in another namespace is still considered a genuine candidate.
		if other.Namespace == gs.Namespace && other.Name == gs.Name {
			continue
		}
		// A terminating server cannot legitimately hold or claim an address
		// going forward.
		if other.GetDeletionTimestamp() != nil {
			continue
		}

		requests := other.Spec.Networking.Address == requestedAddr
		holds := candidateHoldsAddress(other, requestedAddr)

		if holds || requests {
			candidates = append(candidates, addressCandidate{gs: other, holds: holds})
		}
	}
	if len(candidates) == 0 {
		return "", nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		return addressConflictLess(candidates[i], candidates[j])
	})

	winner := candidates[0]
	// gs's own hold status matters too: a requester (gs) that has not yet
	// been granted the address it asked for must not outrank an actual
	// holder just because gs asked first — see the precedence-order note
	// on addressConflictLess.
	self := addressCandidate{gs: gs, holds: candidateHoldsAddress(gs, requestedAddr)}
	if !addressConflictLess(winner, self) {
		// gs itself is the minimum of the full contending set: no conflict.
		return "", nil
	}

	// Namespace-qualify a cross-namespace holder so the reported name is
	// unambiguous; a same-namespace one stays bare.
	if winner.gs.Namespace != gs.Namespace {
		return winner.gs.Namespace + "/" + winner.gs.Name, nil
	}
	return winner.gs.Name, nil
}

// candidateHoldsAddress reports whether gs is a genuine LoadBalancer holder
// of addr — its expose mode is LoadBalancer and one of its status.endpoints
// carries addr as Host with no TunnelProvider. See the discriminator notes
// on findAddressConflict for what this deliberately excludes (tunnel-sourced
// endpoints, and the residual ClusterIP-fallback edge case).
func candidateHoldsAddress(gs *gameplanev1alpha1.GameServer, addr string) bool {
	if gs.Spec.Networking.Expose != "LoadBalancer" {
		return false
	}
	for _, endpoint := range gs.Status.Endpoints {
		if endpoint.TunnelProvider == "" && endpoint.Host == addr {
			return true
		}
	}
	return false
}

// addressCandidate pairs a contending GameServer with its precomputed hold
// status against the address in question, so the total order below can rank
// "holds" without recomputing it (recomputing it against the wrong address
// would be an easy bug once gs's own status is folded into the same
// comparison as the listed candidates').
type addressCandidate struct {
	gs    *gameplanev1alpha1.GameServer
	holds bool
}

// addressConflictLess reports whether a sorts strictly before b under the
// address-conflict precedence order: a genuine holder always sorts before a
// mere requester, regardless of creation order; among two candidates with
// the same hold status, the tiebreak is earlier metadata.creationTimestamp,
// then namespace (lexicographic), then name (lexicographic). This is a
// strict total order over (GameServer, holds) pairs.
//
// It is NOT true that exactly one server in a contending set is ever told
// "no conflict" — a pure holder (spec.networking.address == "") never
// reaches this comparison at all, because findAddressConflict's own
// early-return sends it home with "no conflict" before it lists anyone.
// So a set with both a pure holder and a requester can produce two "no
// conflict" outcomes: the pure holder's from the early return, and the
// requester's if it happens to be the set's minimum too. At least one
// server is always told "no conflict" (the set is never left with every
// member reporting), which is the livelock-breaking property that matters.
//
// Folding "holds" in as the leading key — rather than leaving the order as
// pure (creationTimestamp, namespace, name) and special-casing holders
// separately — is also what keeps this precedence rule from reintroducing
// the mutual-pair livelock it replaces. Case walk, for a holder-vs-requester
// pair (H holds+requests addr, R only requests addr, R older than H):
//   - From R's reconcile: R's own hold status is computed fresh against
//     addr (false, since R never got it) and compared against H (holds).
//     H sorts first regardless of R being older, so R reports H as the
//     conflict.
//   - From H's reconcile: H's own hold status is computed fresh (true) and
//     compared against R (requests only, no hold). H sorts first, so H
//     reports "no conflict" — it never names R.
//     Exactly one direction fires; never both. The reason this differs from
//     naively saying "if any candidate holds, always report it" (which WOULD
//     produce a mutual pair here, since that naive rule ignores gs's own hold
//     status) is that gs's own holds flag is folded into the same comparison
//     as every candidate's, making the order symmetric no matter which of the
//     pair is doing the asking.
//   - A pure holder (spec address == "") never runs this comparison at all
//     (early return), so it can never be the R or H side of a mutual pair
//     either — it can only ever be a silent winner named by someone else's
//     reconcile.
func addressConflictLess(a, b addressCandidate) bool {
	if a.holds != b.holds {
		return a.holds
	}
	at := a.gs.GetCreationTimestamp()
	bt := b.gs.GetCreationTimestamp()
	if !at.Time.Equal(bt.Time) {
		return at.Time.Before(bt.Time)
	}
	if a.gs.Namespace != b.gs.Namespace {
		return a.gs.Namespace < b.gs.Namespace
	}
	return a.gs.Name < b.gs.Name
}
