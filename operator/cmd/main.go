// Package main is the entry point for the Gameplane operator.
package main

import (
	"errors"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"strings"
	"time"

	snapshotv1 "github.com/kubernetes-csi/external-snapshotter/client/v8/apis/volumesnapshot/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/config"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/agent"
	"github.com/GameplanePanel/gameplane/operator/internal/controller"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
)

var scheme = runtime.NewScheme()

// cidrListFlag implements flag.Value for a repeatable
// --game-ingress-from-cidr flag: each occurrence appends a CIDR to the
// list, rather than the stdlib flag package's default last-one-wins
// behavior for a flag registered more than once.
type cidrListFlag []string

func (f *cidrListFlag) String() string {
	return strings.Join(*f, ",")
}

func (f *cidrListFlag) Set(value string) error {
	*f = append(*f, value)
	return nil
}

// errUnknownAddressManager is returned for an --address-manager value outside
// the supported set. Startup fails on it rather than silently degrading to
// "none": a cluster admin who names a pool expects the request translated, and
// a typo'd flavor would otherwise leave every pool preference unapplied while
// the address manager quietly hands out default-pool addresses.
var errUnknownAddressManager = errors.New("unknown cluster address-manager flavor")

// validateAddressManager accepts only the flavors reconcileService knows how
// to translate a pool/address preference into.
func validateAddressManager(flavor string) error {
	switch flavor {
	case "metallb", "cilium", "none":
		return nil
	default:
		return fmt.Errorf("%w %q: want metallb, cilium or none", errUnknownAddressManager, flavor)
	}
}

// errInt32Range is returned by boundedInt32 when v falls outside int32's
// representable range.
var errInt32Range = errors.New("value out of int32 range")

// boundedInt32 narrows v (an int64, the type flag.Int64Var naturally
// produces) to int32, the type NetworkCaptureSpec.TTLSecondsAfterFinished
// and NetworkCaptureReconciler's retention fields use. This is the single
// int64→int32 conversion in the capture retention path — every caller of
// NetworkCaptureReconciler downstream is int32 end to end, and every other
// int64 capture flag (captureDefaultMaxDurationSeconds, captureDefaultMaxSizeBytes)
// stays int64 with no cast, passed straight to NetworkCaptureReconciler's own
// int64 fields.
//
// The literal `v > math.MaxInt32` comparison immediately guarding the
// `int32(v)` conversion below, in the same function body and straight-line
// control flow (an ordinary `return`, not a closure boundary or os.Exit),
// is the exact shape gosec's G115 SSA range analysis
// (github.com/securego/gosec/v2/analyzers/conversion_overflow.go,
// isSafeConversion → hasRangeCheck → validateRangeLimits) resolves as safe:
// it walks the dominating `if`'s branch structure and, for the
// out-of-range branch a plain `if v < 0 || v > math.MaxInt32 { return ... }`
// compiles to (two chained blocks from the `||`), confirms the narrowed
// value on the fallthrough edge satisfies dstInt.Min/dstInt.Max before
// accepting the conversion as guarded. A prior attempt guarded the same
// cast inside a closure over a captured variable; the closure's FreeVar
// indirection breaks the analyzer's SSA value identity tracking between the
// checked value and the converted one, which a real function parameter (as
// here) does not.
func boundedInt32(v int64) (int32, error) {
	if v < 0 || v > math.MaxInt32 {
		return 0, fmt.Errorf("%w: %d", errInt32Range, v)
	}
	return int32(v), nil
}

// Version is the operator build version, overridden at build time via
// -ldflags. Compared against a module bundle's gameplaneMinVersion to refuse
// modules that need a newer operator. Mirrors api/cmd and agent/cmd.
var Version = "dev"

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(gameplanev1alpha1.AddToScheme(scheme))
	// CSI VolumeSnapshot types — backed by the volume-snapshot backup
	// strategy (BackupReconciler creates VolumeSnapshots; RestoreReconciler
	// reads them to seed a new server's data PVC).
	utilruntime.Must(snapshotv1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr                      string
		probeAddr                        string
		enableLeaderElection             bool
		agentImage                       string
		agentImagePullPolicy             string
		configInitImage                  string
		resticImage                      string
		sentinelImage                    string
		tunnelFrpImage                   string
		tunnelTailscaleImage             string
		tunnelPlayitImage                string
		agentLogLevel                    string
		agentCABundle                    string
		agentClientCert                  string
		agentClientKey                   string
		agentCASecretName                string
		agentCASecretNamespace           string
		moduleNamespace                  string
		moduleLocalRoot                  string
		controlPlaneNamespace            string
		addressManager                   string
		metalLBNamespace                 string
		gameIngressPolicy                bool
		gameIngressFromCIDR              cidrListFlag
		gameDataStorageClass             string
		captureEnabled                   bool
		restrictedWorkloads              bool
		agentTokenExpirationSeconds      int64
		diagnosticsNamespace             string
		diagnosticsStorageClass          string
		captureDefaultRetention          int64
		captureMaxRetention              int64
		captureSidecarImage              string
		captureDefaultMaxDurationSeconds int64
		captureDefaultMaxSizeBytes       int64
		backupJobBackoffLimit            int64
		backupJobActiveDeadlineSeconds   int64
		backupPreparationTimeout         time.Duration
		backupQuiesceTimeout             time.Duration
		backupCSICaptureTimeout          time.Duration
		backupHardSameNode               bool
		agentResourcesJSON               string
		sentinelResourcesJSON            string
		tunnelResourcesJSON              string
		backupResourcesJSON              string
	)
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "Address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false, "Enable leader election.")
	flag.StringVar(&agentResourcesJSON, "agent-resources", "", "JSON ResourceRequirements for agents and config init containers; default requests 25m/64Mi, {} clears defaults.")
	flag.StringVar(&sentinelResourcesJSON, "sentinel-resources", "", "JSON ResourceRequirements for sentinels; default requests 25m/32Mi, {} clears defaults.")
	flag.StringVar(&tunnelResourcesJSON, "tunnel-resources", "", "JSON ResourceRequirements for tunnel relays; default requests 25m/64Mi, {} clears defaults.")
	flag.StringVar(&backupResourcesJSON, "backup-resources", "", "JSON ResourceRequirements for restic backup/init, restore and cleanup workers; default backup/restore requests 100m/128Mi, {} clears defaults.")
	flag.StringVar(&agentImage, "agent-image", "ghcr.io/gameplanepanel/gameplane/agent:dev",
		"Image to use for the Gameplane agent sidecar injected into game pods.")
	flag.StringVar(&agentImagePullPolicy, "agent-image-pull-policy", "",
		"ImagePullPolicy for the agent sidecar container (Always, IfNotPresent, or Never). "+
			"Empty (default) leaves it unset so Kubernetes applies its usual default, preserving "+
			"today's behavior. Set explicitly when --agent-image tracks a floating tag (e.g. :edge): "+
			"Kubernetes defaults ImagePullPolicy to IfNotPresent for any non-\":latest\" tag, so without "+
			"this flag a node reuses whatever agent image it already cached and game pods run a stale "+
			"agent indefinitely.")
	flag.StringVar(&configInitImage, "config-init-image", controller.DefaultConfigInitImage,
		"Image for the init container that copies rendered config files onto the data volume. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.StringVar(&resticImage, "restic-image", controller.DefaultResticImage,
		"Image for the restic backup/restore Jobs. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.Int64Var(&backupJobBackoffLimit, "backup-job-backoff-limit", int64(controller.DefaultBackupJobBackoffLimit),
		"BackoffLimit for the restic backup and restore Jobs (retries before the Job is marked Failed). Must be >= 0. Defaults to 2.")
	flag.Int64Var(&backupJobActiveDeadlineSeconds, "backup-job-active-deadline-seconds", controller.DefaultBackupJobActiveDeadlineSeconds,
		"ActiveDeadlineSeconds for the restic backup and restore Jobs. Must be > 0. Defaults to 86400 (24 hours).")
	flag.DurationVar(&backupPreparationTimeout, "backup-preparation-timeout", controller.DefaultBackupPreparationTimeout, "Maximum worker preparation time before copy authorization; must be positive.")
	flag.DurationVar(&backupQuiesceTimeout, "backup-quiesce-timeout", controller.DefaultBackupQuiesceTimeout, "Maximum live quiesced Restic copy duration; expiry cancels and drains workers before save-on.")
	flag.DurationVar(&backupCSICaptureTimeout, "backup-csi-capture-timeout", controller.DefaultBackupCSICaptureTimeout, "Maximum CSI capture duration before invalidation and save-on; must be positive.")
	flag.BoolVar(&backupHardSameNode, "backup-hard-same-node", false, "Require Restic workers on the observed game node instead of the default preferred placement.")
	flag.StringVar(&sentinelImage, "sentinel-image", controller.DefaultSentinelImage,
		"Image for the wake sentinel pod that holds advertised ports while a server is asleep. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.StringVar(&tunnelFrpImage, "tunnel-frp-image", controller.DefaultTunnelFrpImage,
		"Image for the frp tunnel relay pod that routes players through an frp server. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.StringVar(&tunnelTailscaleImage, "tunnel-tailscale-image", controller.DefaultTunnelTailscaleImage,
		"Image for the Tailscale tunnel relay pod that routes players through a Tailscale tailnet. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.StringVar(&tunnelPlayitImage, "tunnel-playit-image", controller.DefaultTunnelPlayitImage,
		"Image for the Playit tunnel relay pod that routes players through a Playit tunnel. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.BoolVar(&restrictedWorkloads, "restricted-workloads", false, "Require compatible nonroot templates and render Restricted game workloads")
	flag.Int64Var(&agentTokenExpirationSeconds, "agent-token-expiration-seconds", 3600, "Projected agent token lifetime (600-3600 seconds)")
	flag.StringVar(&diagnosticsNamespace, "diagnostics-namespace", "", "Opt in to node diagnostics in this dedicated privileged namespace")
	flag.StringVar(&diagnosticsStorageClass, "diagnostics-storage-class", "", "StorageClass for bounded node capture artifact PVCs")
	flag.BoolVar(&captureEnabled, "capture-enabled", false,
		"Enable the network capture feature cluster-wide. When false (the default), "+
			"the capture capability is disabled and cannot be enabled per-GameServer.")
	flag.Int64Var(&captureDefaultRetention, "capture-default-retention-seconds", 86400,
		"Default retention period for completed network captures, in seconds. "+
			"Defaults to 86400 (24 hours). Applies when a GameServer's spec.capture.retentionSeconds is not set.")
	flag.Int64Var(&captureMaxRetention, "capture-max-retention-seconds", 604800,
		"Maximum retention period for network captures, in seconds. "+
			"Defaults to 604800 (7 days). Clamps any higher retention request to this value.")
	flag.StringVar(&captureSidecarImage, "capture-sidecar-image", controller.DefaultCaptureSidecarImage,
		"Image for the network capture sidecar container injected when capture is enabled on a GameServer. "+
			"Point at a private registry mirror for air-gapped installs.")
	flag.Int64Var(&captureDefaultMaxDurationSeconds, "capture-default-max-duration-seconds", 300,
		"Default maximum duration for a single network capture, in seconds. "+
			"A capture requested without an explicit maxDuration uses this limit; the sidecar stops the capture "+
			"automatically when the duration is reached. Defaults to 300 (5 minutes).")
	flag.Int64Var(&captureDefaultMaxSizeBytes, "capture-default-max-size-bytes", 5368709120,
		"Default maximum size for a single network capture file, in bytes. "+
			"A capture requested without an explicit maxSize uses this limit; the sidecar stops the capture "+
			"automatically when the file reaches this size. Defaults to 5368709120 (5 GiB).")
	flag.StringVar(&agentLogLevel, "agent-log-level", "",
		"Log level (debug, info, warn, or error) injected into agent sidecars as GAMEPLANE_LOG_LEVEL. "+
			"Empty injects nothing (the agent defaults to info) and avoids rolling existing pods.")
	flag.StringVar(&moduleNamespace, "module-namespace", "gameplane-system",
		"Namespace where ModuleSource credential Secrets, uploaded module bundles and module translation ConfigMaps live.")
	flag.StringVar(&moduleLocalRoot, "module-local-root", "",
		"Base directory that local-type ModuleSources resolve their paths under. Empty disables local sources.")
	flag.StringVar(&agentCABundle, "agent-ca-bundle", "",
		"CA bundle that signs agent server certs (for operator → agent calls).")
	flag.StringVar(&agentClientCert, "agent-client-cert", "",
		"Client cert presented when calling the agent over mTLS.")
	flag.StringVar(&agentClientKey, "agent-client-key", "",
		"Client key for the agent client cert.")
	flag.StringVar(&agentCASecretName, "agent-ca-secret-name", "gameplane-agent-ca",
		"Name of the Secret holding the agent CA cert+key used to sign per-GameServer agent server certs.")
	flag.StringVar(&agentCASecretNamespace, "agent-ca-secret-namespace", "gameplane-system",
		"Namespace of the agent CA Secret.")
	controlPlaneNamespaceDefault := os.Getenv("POD_NAMESPACE")
	if controlPlaneNamespaceDefault == "" {
		controlPlaneNamespaceDefault = "gameplane-system"
	}
	flag.StringVar(&controlPlaneNamespace, "control-plane-namespace", controlPlaneNamespaceDefault,
		"Namespace where the operator runs and where cluster kubeconfig Secrets are stored.")
	addressManagerDefault := os.Getenv("GAMEPLANE_ADDRESS_MANAGER")
	if addressManagerDefault == "" {
		addressManagerDefault = "none"
	}
	flag.StringVar(&addressManager, "address-manager", addressManagerDefault,
		"Cluster load-balancer address manager (metallb, cilium, or none) the operator translates a "+
			"GameServer's spec.networking.addressPool/address preference for. metallb writes the "+
			"metallb.io annotations; cilium writes the gameplane.local/lb-pool label plus the "+
			"lbipam.cilium.io/ips annotation. none (the default) mutates no Service and instead reports "+
			"the unhonored request on the GameServer's AddressAssignment condition, so a pool preference "+
			"never silently falls back to the default pool. Also settable as GAMEPLANE_ADDRESS_MANAGER.")
	flag.StringVar(&metalLBNamespace, "metallb-namespace", "metallb-system",
		"Namespace MetalLB's IPAddressPool custom resources (metallb.io/v1beta1, namespaced) live in. "+
			"Only used when --address-manager=metallb: the operator GETs a requested pool directly there "+
			"to report PoolNotFound without waiting for a MetalLB event. \"metallb-system\" is MetalLB's "+
			"own install convention, not a contract, hence configurable.")
	flag.BoolVar(&gameIngressPolicy, "game-ingress-policy", true,
		"Reconcile a per-GameServer ingress NetworkPolicy admitting player traffic to the template's "+
			"advertised ports. When false, the operator ensures the policy is absent instead of merely "+
			"skipping it, so toggling this off converges existing GameServers.")
	flag.Var(&gameIngressFromCIDR, "game-ingress-from-cidr",
		"Source CIDR admitted to advertised game ports by the ingress NetworkPolicy. Repeatable. "+
			"Defaults to 0.0.0.0/0 (games are meant to be publicly reachable) when not supplied.")
	flag.StringVar(&gameDataStorageClass, "game-data-storage-class", os.Getenv("GAMEPLANE_GAME_DATA_STORAGE_CLASS"),
		"StorageClass for game server data volumes (GameServer spec.storage.storageClassName). "+
			"Empty (default) uses the cluster's default StorageClass. "+
			"Applies when a GameServer does not explicitly set spec.storage.storageClassName.")

	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	if agentTokenExpirationSeconds < 600 || agentTokenExpirationSeconds > 3600 {
		fmt.Fprintln(os.Stderr, "agent-token-expiration-seconds must be between 600 and 3600")
		os.Exit(1)
	}
	if diagnosticsNamespace != "" && diagnosticsNamespace == controlPlaneNamespace {
		fmt.Fprintln(os.Stderr, "diagnostics-namespace must be separate from the control-plane namespace")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))
	setupLog := ctrl.Log.WithName("setup")

	agentResources, err := controller.ParseAuxiliaryResources(agentResourcesJSON)
	if err != nil {
		setupLog.Error(err, "invalid --agent-resources")
		os.Exit(1)
	}
	sentinelResources, err := controller.ParseAuxiliaryResources(sentinelResourcesJSON)
	if err != nil {
		setupLog.Error(err, "invalid --sentinel-resources")
		os.Exit(1)
	}
	tunnelResources, err := controller.ParseAuxiliaryResources(tunnelResourcesJSON)
	if err != nil {
		setupLog.Error(err, "invalid --tunnel-resources")
		os.Exit(1)
	}
	backupResources, err := controller.ParseAuxiliaryResources(backupResourcesJSON)
	if err != nil {
		setupLog.Error(err, "invalid --backup-resources")
		os.Exit(1)
	}
	backupJobBackoff32, err := boundedInt32(backupJobBackoffLimit) // rejects <0 and >MaxInt32
	if err != nil {
		setupLog.Error(err, "invalid --backup-job-backoff-limit value (must be >= 0)", "value", backupJobBackoffLimit)
		os.Exit(1)
	}
	if backupJobActiveDeadlineSeconds <= 0 {
		setupLog.Error(nil, "invalid --backup-job-active-deadline-seconds value (must be > 0)", "value", backupJobActiveDeadlineSeconds)
		os.Exit(1)
	}
	if backupPreparationTimeout <= 0 || backupQuiesceTimeout <= 0 || backupCSICaptureTimeout <= 0 {
		setupLog.Error(nil, "backup preparation, quiesce and CSI capture timeouts must be positive")
		os.Exit(1)
	}

	// Validate capture retention flags while they are still the int64 type
	// flag.Int64Var naturally produces. The minimum of 60 seconds aligns with
	// the CRD's own validation. The maximum of 604800 (7 days) is the
	// ratified ceiling — comfortably inside int32, so the boundedInt32 calls
	// just below (which narrow to the type NetworkCaptureReconciler actually
	// uses) cannot fail in practice, but check the error anyway.
	const captureRetentionMin int64 = 60
	const captureRetentionMax int64 = 604800

	if captureDefaultRetention < captureRetentionMin || captureDefaultRetention > captureRetentionMax {
		setupLog.Error(nil, "invalid --capture-default-retention-seconds value",
			"value", captureDefaultRetention, "min", captureRetentionMin, "max", captureRetentionMax)
		os.Exit(1)
	}

	if captureMaxRetention < captureRetentionMin || captureMaxRetention > captureRetentionMax {
		setupLog.Error(nil, "invalid --capture-max-retention-seconds value",
			"value", captureMaxRetention, "min", captureRetentionMin, "max", captureRetentionMax)
		os.Exit(1)
	}

	// Narrow to int32 once here, for NetworkCaptureReconciler's int32 fields
	// (see boundedInt32's doc comment for why this exact shape satisfies
	// gosec G115). captureRetentionMax above (604800) is already well
	// within int32 range, so these calls cannot fail given the bounds
	// checks just above — but boundedInt32 reports the error rather than
	// silently wrapping, on the off chance the bounds check above is ever
	// relaxed without updating this cast.
	captureDefaultRetention32, err := boundedInt32(captureDefaultRetention)
	if err != nil {
		setupLog.Error(err, "--capture-default-retention-seconds does not fit in int32",
			"value", captureDefaultRetention)
		os.Exit(1)
	}
	captureMaxRetention32, err := boundedInt32(captureMaxRetention)
	if err != nil {
		setupLog.Error(err, "--capture-max-retention-seconds does not fit in int32",
			"value", captureMaxRetention)
		os.Exit(1)
	}

	if captureDefaultRetention > captureMaxRetention {
		setupLog.Error(nil, "--capture-default-retention-seconds cannot exceed --capture-max-retention-seconds",
			"default", captureDefaultRetention, "max", captureMaxRetention)
		os.Exit(1)
	}

	if err := validateAddressManager(addressManager); err != nil {
		setupLog.Error(err, "invalid --address-manager value")
		os.Exit(1)
	}

	// Games are meant to be publicly reachable, so an unset
	// --game-ingress-from-cidr defaults to wide-open rather than an empty
	// (and therefore permission-less) list.
	if len(gameIngressFromCIDR) == 0 {
		gameIngressFromCIDR = cidrListFlag{"0.0.0.0/0"}
	}
	for i, cidr := range gameIngressFromCIDR {
		_, ipnet, err := net.ParseCIDR(cidr)
		if err != nil {
			setupLog.Error(err, "invalid --game-ingress-from-cidr value", "cidr", cidr)
			os.Exit(1)
		}
		// ParseCIDR succeeds even when the address has host bits set (e.g.
		// "10.0.0.1/8"), returning the parsed network alongside it — use
		// that canonical form (e.g. "10.0.0.0/8") rather than the raw flag
		// value, so IPBlock.CIDR never carries host bits the apiserver
		// might reject or interpret surprisingly.
		gameIngressFromCIDR[i] = ipnet.String()
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "gameplane-operator.gameplane.local",
		// On a resource-constrained node (the homelab target, and the CI
		// runner) a hammered apiserver can push an informer's initial sync —
		// e.g. the backup controller's VolumeSnapshot watch — past the default
		// 2m CacheSyncTimeout. The manager then exits ("problem running
		// manager: failed to wait for ... caches to sync") and crash-loops,
		// stalling all reconciliation. A larger window lets it ride out the
		// slowness and start cleanly.
		Controller: config.Controller{CacheSyncTimeout: 5 * time.Minute},
	})
	if err != nil {
		setupLog.Error(err, "unable to create manager")
		os.Exit(1)
	}

	agentClient, err := agent.New(agent.Config{
		CABundle:   agentCABundle,
		ClientCert: agentClientCert,
		ClientKey:  agentClientKey,
	})
	if err != nil {
		setupLog.Error(err, "unable to build agent client")
		os.Exit(1)
	}

	if err := (&controller.GameServerReconciler{
		Client:                 mgr.GetClient(),
		APIReader:              mgr.GetAPIReader(),
		Scheme:                 mgr.GetScheme(),
		AgentImage:             agentImage,
		AgentResources:         agentResources,
		SentinelResources:      sentinelResources,
		TunnelResources:        tunnelResources,
		AgentImagePullPolicy:   agentImagePullPolicy,
		ConfigInitImage:        configInitImage,
		SentinelImage:          sentinelImage,
		TunnelFrpImage:         tunnelFrpImage,
		TunnelTailscaleImage:   tunnelTailscaleImage,
		TunnelPlayitImage:      tunnelPlayitImage,
		AgentLogLevel:          agentLogLevel,
		AgentCASecretName:      agentCASecretName,
		AgentCASecretNamespace: agentCASecretNamespace,
		AgentClient:            agentClient,
		OperationQuiescer:      agentClient,
		PodAttacher: &controller.StopAttachClient{
			Config:    mgr.GetConfig(),
			Clientset: kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		},
		AddressManager:              addressManager,
		MetalLBNamespace:            metalLBNamespace,
		GameIngressPolicyEnabled:    gameIngressPolicy,
		GameIngressFromCIDRs:        gameIngressFromCIDR,
		CaptureEnabled:              captureEnabled,
		RestrictedWorkloads:         restrictedWorkloads,
		AgentTokenExpirationSeconds: agentTokenExpirationSeconds,
		DiagnosticsNamespace:        diagnosticsNamespace,
		CaptureSidecarImage:         captureSidecarImage,
		DefaultStorageClassName:     gameDataStorageClass,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "GameServer")
		os.Exit(1)
	}
	if err := (&controller.GameTemplateReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "GameTemplate")
		os.Exit(1)
	}

	if err := (&controller.BackupReconciler{
		Client:                   mgr.GetClient(),
		Scheme:                   mgr.GetScheme(),
		Clientset:                kubernetes.NewForConfigOrDie(mgr.GetConfig()),
		AgentClient:              agentClient,
		ResticImage:              resticImage,
		BackupResources:          backupResources,
		JobBackoffLimit:          &backupJobBackoff32,
		JobActiveDeadlineSeconds: backupJobActiveDeadlineSeconds,
		EventRecorder:            mgr.GetEventRecorder("backup-controller"),
		PreparationTimeout:       backupPreparationTimeout,
		QuiesceTimeout:           backupQuiesceTimeout,
		CSICaptureTimeout:        backupCSICaptureTimeout,
		HardSameNode:             backupHardSameNode,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "Backup")
		os.Exit(1)
	}
	if err := (&controller.BackupScheduleReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "BackupSchedule")
		os.Exit(1)
	}
	if err := (&controller.RestoreReconciler{
		Client:                   mgr.GetClient(),
		APIReader:                mgr.GetAPIReader(),
		Scheme:                   mgr.GetScheme(),
		OperationQuiescer:        agentClient,
		ResticImage:              resticImage,
		BackupResources:          backupResources,
		JobBackoffLimit:          &backupJobBackoff32,
		JobActiveDeadlineSeconds: backupJobActiveDeadlineSeconds,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "Restore")
		os.Exit(1)
	}

	// Network capture reconciler (manages NetworkCapture CRD lifecycle and sidecar interaction).
	captureClient := agent.NewCaptureClient(agentClient)
	if err := (&controller.NetworkCaptureReconciler{
		Client:                           mgr.GetClient(),
		Scheme:                           mgr.GetScheme(),
		SidecarClient:                    captureClient,
		APIReader:                        mgr.GetAPIReader(),
		RestrictedWorkloads:              restrictedWorkloads,
		DiagnosticsNamespace:             diagnosticsNamespace,
		DiagnosticsStorageClass:          diagnosticsStorageClass,
		AgentCASecretName:                agentCASecretName,
		AgentCASecretNamespace:           agentCASecretNamespace,
		CaptureEnabled:                   captureEnabled,
		CaptureSidecarImage:              captureSidecarImage,
		CaptureDefaultMaxDurationSeconds: captureDefaultMaxDurationSeconds,
		CaptureDefaultMaxSizeBytes:       captureDefaultMaxSizeBytes,
		CaptureDefaultRetentionSeconds:   captureDefaultRetention32,
		CaptureMaxRetentionSeconds:       captureMaxRetention32,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "NetworkCapture")
		os.Exit(1)
	}

	fetchOptions := modsrc.Options{LocalRoot: moduleLocalRoot}
	if err := (&controller.ModuleSourceReconciler{
		Client:       mgr.GetClient(),
		Scheme:       mgr.GetScheme(),
		Namespace:    moduleNamespace,
		FetchOptions: fetchOptions,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "ModuleSource")
		os.Exit(1)
	}
	if err := (&controller.ModuleReconciler{
		Client:          mgr.GetClient(),
		Scheme:          mgr.GetScheme(),
		Namespace:       moduleNamespace,
		OperatorVersion: Version,
		FetchOptions:    fetchOptions,
		EventRecorder:   mgr.GetEventRecorder("module-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "Module")
		os.Exit(1)
	}
	if err := (&controller.ClusterStatusReconciler{
		Client:    mgr.GetClient(),
		Scheme:    mgr.GetScheme(),
		Namespace: controlPlaneNamespace,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to set up controller", "controller", "Cluster")
		os.Exit(1)
	}

	// Fleet metrics: report how many GameServers and Backups sit in each phase,
	// served on the manager's existing /metrics endpoint. The collectors read
	// the shared cache (populated by the controllers' watches above) at scrape
	// time, so registration order relative to Start doesn't matter.
	metrics.Registry.MustRegister(
		controller.NewGameServerCollector(mgr.GetClient()),
		controller.NewGameServerIdleCollector(mgr.GetClient()),
		controller.NewBackupCollector(mgr.GetClient()),
	)

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}
