package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GameServerPhase is a high-level state for a GameServer, derived from
// the underlying StatefulSet/Pod + agent heartbeat.
// +kubebuilder:validation:Enum=Pending;Starting;Running;Stopping;Stopped;Suspended;Failed
type GameServerPhase string

// GameServerPhase constants define the possible values for a GameServer's phase.
const (
	GameServerPhasePending   GameServerPhase = "Pending"
	GameServerPhaseStarting  GameServerPhase = "Starting"
	GameServerPhaseRunning   GameServerPhase = "Running"
	GameServerPhaseStopping  GameServerPhase = "Stopping"
	GameServerPhaseStopped   GameServerPhase = "Stopped"
	GameServerPhaseSuspended GameServerPhase = "Suspended"
	GameServerPhaseFailed    GameServerPhase = "Failed"
)

// GameServer condition types.
const (
	// GameServerConditionAddressAssignment reports the outcome of a
	// spec.networking.addressPool / .address request: True once the address
	// manager has assigned the address, False while it is pending, was
	// ignored for the expose mode, or failed.
	GameServerConditionAddressAssignment = "AddressAssignment"

	// GameServerConditionDataWipe reports the outcome of the most recent
	// data-wipe request (spec via the wipe-data-requested annotation):
	// False with Reason "JobFailed" when the wipe Job could not empty the
	// volume (e.g. a permission error on a subdirectory it doesn't own).
	// Absent once a wipe has succeeded and been acked.
	GameServerConditionDataWipe = "DataWipe"
)

// GameServerSpec is the desired state of a single game server instance.
type GameServerSpec struct {
	// TemplateRef references a GameTemplate that provides defaults for
	// image, ports, probes, etc. Required. Immutable after creation: the
	// StatefulSet fields derived from a template (image, selector labels)
	// cannot be repointed at another template in place, so changing this
	// after create wedges the StatefulSet reconcile with only an operator
	// log to explain why (F-047). Delete and recreate the GameServer to
	// switch templates.
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="templateRef is immutable; delete and recreate the GameServer to switch templates"
	TemplateRef GameTemplateRef `json:"templateRef"`

	// Suspend, when true, scales the underlying StatefulSet to zero.
	// Data is preserved. Transitions the GameServer to Suspended.
	// +kubebuilder:default=false
	// +optional
	Suspend bool `json:"suspend,omitempty"`

	// StopGracePeriodSeconds bounds the soft-stop: when Suspend flips true
	// and the template declares a Lifecycle.Stop sequence, the operator runs
	// it over RCON and waits up to this long for the game to go not-ready
	// before scaling the StatefulSet to zero (it scales early once the game
	// has actually stopped). It has no effect when the template declares no
	// stop sequence. Defaults to 30s.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=600
	// +kubebuilder:default=30
	// +optional
	StopGracePeriodSeconds *int32 `json:"stopGracePeriodSeconds,omitempty"`

	// Image, when set, overrides GameTemplate.Spec.Image (and any image
	// resolved from Version). Useful for pinning a specific build or
	// running a fork.
	// +optional
	Image string `json:"image,omitempty"`

	// Version selects a GameTemplate.spec.versions[].id, pinning that
	// entry's image and appending its env (and provisioning its per-loader
	// mod volume). When the template declares versions and this is set, it
	// must match a catalog id or the server fails. Empty selects the
	// template's default version (the entry with default=true, else the
	// first); when the template declares no versions, this is ignored and
	// the server runs spec.image or the template Image.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=40
	// +optional
	Version string `json:"version,omitempty"`

	// Env is appended to (and overrides) the template's env vars.
	// +optional
	Env []corev1.EnvVar `json:"env,omitempty"`

	// Config holds wizard-driven values keyed by ConfigField.Name from
	// the referenced GameTemplate. The operator materializes these as
	// env vars or files per the template's ConfigSchema.
	// +kubebuilder:validation:MaxProperties=64
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k].size() <= 4096)",message="config values must be at most 4096 characters"
	// +optional
	Config map[string]string `json:"config,omitempty"`

	// Probes overrides the template's readiness/liveness/startup probes.
	// Each set probe replaces the corresponding template probe; unset
	// probes fall back to the template.
	// +optional
	Probes *GameProbesSpec `json:"probes,omitempty"`

	// Resources overrides compute resources from the template.
	// +optional
	Resources *corev1.ResourceRequirements `json:"resources,omitempty"`

	// Storage overrides the template's storage layout.
	// +optional
	Storage *GameStorageSpec `json:"storage,omitempty"`

	// Networking controls how the game is exposed outside the cluster.
	// +optional
	Networking GameServerNetworking `json:"networking,omitempty"`

	// NodeSelector / Tolerations / Affinity are passed through to the
	// pod spec unchanged.
	// +kubebuilder:validation:MaxProperties=32
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k].size() <= 253)",message="nodeSelector values must be at most 253 characters"
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// BackupPolicy inlines a BackupSchedule for this server. When set,
	// the operator creates/maintains a BackupSchedule owned by this
	// GameServer. Remove to stop scheduled backups.
	// +optional
	BackupPolicy *InlineBackupPolicy `json:"backupPolicy,omitempty"`

	// ServiceAccountName, when set, overrides the SA the pod runs as.
	// By default the operator creates a per-GameServer ServiceAccount
	// (`<name>-agent`) whose only grant is patching this GameServer's
	// status — the agent sidecar's heartbeat needs it. Overriding with
	// an SA that lacks that grant disables heartbeats (the server then
	// never reports Running).
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`

	// Mods pins the mods this server loads, for games whose server downloads
	// its own mods by id (see GameTemplate capabilities.mods.idList). Ignored
	// by file-drop games.
	// +optional
	Mods *GameServerModsSpec `json:"mods,omitempty"`

	// Idle configures automatic sleep while nobody is playing. Opt-in.
	// +optional
	Idle *IdleSpec `json:"idle,omitempty"`

	// Capture configures optional packet-capture capability for this GameServer.
	// +optional
	Capture *CaptureConfiguration `json:"capture,omitempty"`
}

// IdleSpec configures idle auto-sleep: the operator scales the server down
// once it has reported no online players for AfterMinutes, and brings it back
// on a WakeWindows cron tick or an explicit wake request.
//
// Sleeping reuses the same graceful path as spec.suspend — the module-declared
// stop sequence runs first, so the world is saved — and retains the data
// volume. It is deliberately *not* expressed as spec.suspend: that field is
// the user's own power switch, and conflating the two would let a wake window
// resurrect a server its owner had deliberately turned off.
//
// A server whose game reports no player count can never satisfy the trigger
// (unknown is not zero — see AgentStatus.PlayersOnline). Rather than sleeping
// forever without explanation, the controller records why on
// IdleStatus.Reason, which the dashboard surfaces on the server's Overview.
type IdleSpec struct {
	// Enabled turns idle auto-sleep on for this server.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// AfterMinutes is how long the server must continuously report zero
	// online players before it is put to sleep. The floor of 5 minutes keeps
	// a server from flapping between the last player leaving and the next
	// arriving.
	// +kubebuilder:validation:Minimum=5
	// +kubebuilder:validation:Maximum=1440
	// +kubebuilder:default=30
	// +optional
	AfterMinutes *int32 `json:"afterMinutes,omitempty"`

	// WakeWindows are cron expressions, in the cluster's timezone, at which a
	// sleeping server is woken back up — the scheduled counterpart to the
	// dashboard's Wake button, so a server can be up before players arrive
	// without anyone holding write access.
	//
	// Same five-field form and structural guard as BackupScheduleSpec.Schedule;
	// the controller parses with robfig/cron/v3 and reports an unparseable
	// entry as a failed condition. A window never wakes a server the user
	// suspended by hand.
	//
	// Bounded (and each entry length-capped) deliberately: an unbounded list
	// carrying a per-item rule blows the apiserver's CEL cost budget and gets
	// the whole CRD rejected at install time.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MinLength=9
	// +kubebuilder:validation:items:MaxLength=64
	// +kubebuilder:validation:items:Pattern=`^\S+\s+\S+\s+\S+\s+\S+\s+\S+(\s+\S+)?$`
	// +listType=atomic
	// +optional
	WakeWindows []string `json:"wakeWindows,omitempty"`

	// WakeOnConnect arms the wake sentinel for this server: while it is
	// asleep, a small pod holds its advertised ports and wakes it when a
	// player genuinely tries to join, instead of the player getting a
	// connection error. Costs one small pod per sleeping server, so it is
	// opt-in; without it, waking needs a wakeWindows tick, the dashboard,
	// or POST /servers/{name}:wake.
	// +kubebuilder:default=false
	// +optional
	WakeOnConnect bool `json:"wakeOnConnect,omitempty"`
}

// CaptureConfiguration configures optional packet-capture capability for a GameServer.
// The opt-in Enabled flag controls whether the capture sidecar is injected as an ephemeral
// container; RetentionSeconds allows per-server override of the cluster-default retention window.
type CaptureConfiguration struct {
	// Enabled: opt-in flag for packet-capture capability on this GameServer.
	// If false or omitted, no capture sidecar is injected; the game container is unchanged.
	// In the legacy profile, the operator injects the capture sidecar via
	// pods/ephemeralcontainers, live, without restarting the game container.
	// Disabling (true→false) does not remove the sidecar from a running pod — Kubernetes
	// has no API to remove ephemeral containers — but stops accepting new captures.
	// Restricted workloads require NodeDiagnostic instead and never inject a sidecar.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Type=boolean
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// NodeDiagnostic permits the separately deployed node capture worker for
	// this server. Application administrators must explicitly opt in. The
	// worker has host-level capabilities; it never weakens the game Pod.
	// Required for capture on Restricted workloads. Off by default.
	// +optional
	NodeDiagnostic bool `json:"nodeDiagnostic,omitempty"`

	// RetentionSeconds: optional per-GameServer override of the cluster-default retention window
	// after a capture completes. Unit: seconds. If omitted, the cluster default
	// (Helm value capture.defaultRetentionSeconds) is used. Must not exceed the cluster
	// maximum (capture.maxRetentionSeconds); the API validates this at NetworkCapture creation.
	// The maximum of 604800 seconds (7 days) is a storage-limitation-informed default,
	// per GDPR Art. 5(1)(e), not a legal mandate — implementations may enforce different policies.
	// Examples: 86400 (24 hours), 2592000 (30 days).
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:Minimum=60
	// +kubebuilder:validation:Maximum=604800
	// +optional
	RetentionSeconds *int32 `json:"retentionSeconds,omitempty"`
}

// GameServerModsSpec is the set of mods selected for a server whose game
// installs mods by id (see GameTemplate capabilities.mods.idList).
type GameServerModsSpec struct {
	// IDs are the provider-native mod ids, in order.
	// +kubebuilder:validation:MaxItems=200
	// +optional
	IDs []ModRef `json:"ids,omitempty"`
}

// ModRef is one provider-native mod id, optionally labeled for display.
type ModRef struct {
	// ID is the provider-native mod id (e.g. a CurseForge project id).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=64
	// +kubebuilder:validation:Pattern=`^[A-Za-z0-9._-]+$`
	ID string `json:"id"`
	// Name is a display label only. It is never projected into the game —
	// it exists so the dashboard can render the list without a registry
	// round-trip.
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Name string `json:"name,omitempty"`
}

// GameTemplateRef identifies a GameTemplate by name.
type GameTemplateRef struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// GameServerNetworking controls service + ingress for the game.
type GameServerNetworking struct {
	// Expose controls the Service type fronting the game pod.
	// - "ClusterIP": reachable only from within the cluster (default)
	// - "NodePort": exposed on a high port on every node
	// - "LoadBalancer": request an external LB (cloud)
	// - "Hostport": advertise a specific host port via HostPort pod spec
	// +kubebuilder:validation:Enum=ClusterIP;NodePort;LoadBalancer;Hostport
	// +kubebuilder:default=ClusterIP
	// +optional
	Expose string `json:"expose,omitempty"`

	// Hostname is an optional DNS name the operator may advertise via
	// ingress / external-dns annotations on the Service. Must be an
	// RFC 1123 hostname: dotted labels of 1-63 alphanumeric characters
	// or hyphens, no leading/trailing hyphen, at most 253 characters
	// total. The operator does not create the DNS record itself.
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)(\.[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)*$`
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// ServiceAnnotations are merged into the fronting Service's
	// annotations (useful for LoadBalancer-specific config, external-dns
	// hooks, etc.).
	// +kubebuilder:validation:MaxProperties=32
	// +kubebuilder:validation:XValidation:rule="self.all(k, self[k].size() <= 4096)",message="serviceAnnotations values must be at most 4096 characters"
	// +optional
	ServiceAnnotations map[string]string `json:"serviceAnnotations,omitempty"`

	// PortOverrides lets the user pin a specific NodePort or override
	// the Service port for a named template port.
	// +optional
	PortOverrides []PortOverride `json:"portOverrides,omitempty"`

	// SourceRanges is an IP allow-list (CIDRs) for the fronting Service. It
	// maps to service.spec.loadBalancerSourceRanges and therefore only takes
	// effect when Expose=LoadBalancer (cloud LBs that honor it); it is
	// ignored for ClusterIP/NodePort. Empty allows all clients.
	// +kubebuilder:validation:MaxItems=20
	// +kubebuilder:validation:XValidation:rule="self.all(c, c.contains('/'))",message="each sourceRange must be a CIDR, e.g. 203.0.113.0/24"
	// +listType=atomic
	// +optional
	SourceRanges []string `json:"sourceRanges,omitempty"`

	// AddressPool names the load-balancer address pool the operator should
	// request this server's external address from. It only takes effect when
	// Expose=LoadBalancer; other expose modes ignore it (and the operator
	// flags the ignored preference on the AddressAssignment condition). The
	// operator translates the name per the cluster's address-manager flavor —
	// a metallb.io/address-pool annotation for MetalLB, a
	// gameplane.local/lb-pool label for Cilium — so the same spec works on
	// either. The value is the address manager's own pool object name, so it
	// must be an RFC 1123 subdomain — dotted labels of 1-63 alphanumeric
	// characters or hyphens, no leading/trailing hyphen. A label-only pattern
	// would reject a legitimately named pool such as "pool.us-east" at
	// admission, with no operator-side escape hatch. MaxLength 63 is the real
	// bound; the pattern only constrains the shape.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)(\.[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)*$`
	// +optional
	AddressPool string `json:"addressPool,omitempty"`

	// Address requests one specific external address for this server. Like
	// AddressPool it only takes effect when Expose=LoadBalancer. When both
	// are set, Address wins for the assignment itself: the operator asks the
	// address manager for exactly this address and reports a failure rather
	// than silently falling back to any address in the pool.
	//
	// The format is deliberately not validated here: no CEL rule and no
	// Pattern, because unbounded CEL on this CRD has been rejected by the
	// apiserver on its cost budget. The operator parses the value during
	// reconciliation and reports a bad one through the AddressAssignment
	// condition instead. MaxLength 45 is the longest textual IPv6 form
	// (an IPv4-mapped address with a zone id).
	// +kubebuilder:validation:MaxLength=45
	// +optional
	Address string `json:"address,omitempty"`

	// Tunnel routes players through a relay instead of relying on cluster
	// ingress, for installs with no public IP and no ability to port-forward.
	// It layers over the backing Service rather than replacing it, so it
	// composes with idle auto-sleep and wake-on-connect.
	// +optional
	Tunnel *GameServerTunnel `json:"tunnel,omitempty"`
}

// PortOverride pins or remaps one of the template's declared ports.
type PortOverride struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// ServicePort overrides the external Service port.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ServicePort int32 `json:"servicePort,omitempty"`

	// NodePort pins the NodePort when Networking.Expose is NodePort.
	// +kubebuilder:validation:Minimum=30000
	// +kubebuilder:validation:Maximum=32767
	// +optional
	NodePort int32 `json:"nodePort,omitempty"`
}

// GameServerTunnel configures relay-based connectivity for a server.
// +kubebuilder:validation:XValidation:rule="!self.enabled || has(self.credentialsSecretRef)",message="credentialsSecretRef is required when tunnel is enabled"
// +kubebuilder:validation:XValidation:rule="!self.enabled || self.provider != 'frp' || has(self.frp)",message="frp config is required when provider is frp"
type GameServerTunnel struct {
	// Enabled turns the tunnel on for this server.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Provider selects which tunnel backend to use.
	// +kubebuilder:validation:Enum=frp;tailscale;playit
	Provider string `json:"provider"`

	// CredentialsSecretRef references a Secret in the GameServer's
	// namespace containing provider-specific credentials. The Secret's key
	// shape is documented in docs/tunnels.md per provider.
	// +optional
	CredentialsSecretRef *SecretNameRef `json:"credentialsSecretRef,omitempty"`

	// Frp configures an frp relay tunnel (static public address).
	// +optional
	Frp *FrpTunnelSpec `json:"frp,omitempty"`

	// Tailscale configures a Tailscale tunnel (private tailnet only).
	// +optional
	Tailscale *TailscaleTunnelSpec `json:"tailscale,omitempty"`

	// Playit configures a Playit tunnel (dynamic public address).
	// +optional
	Playit *PlayitTunnelSpec `json:"playit,omitempty"`
}

// FrpTunnelSpec configures frp relay tunnel settings.
type FrpTunnelSpec struct {
	// ServerAddr is the hostname or IP of the frps server.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^([a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.)*[a-zA-Z0-9]([a-zA-Z0-9\-]{0,61}[a-zA-Z0-9])?\.?$|^((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)$|^(([0-9a-fA-F]{1,4}:){7}[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,7}:|([0-9a-fA-F]{1,4}:){1,6}:[0-9a-fA-F]{1,4}|([0-9a-fA-F]{1,4}:){1,5}(:[0-9a-fA-F]{1,4}){1,2}|([0-9a-fA-F]{1,4}:){1,4}(:[0-9a-fA-F]{1,4}){1,3}|([0-9a-fA-F]{1,4}:){1,3}(:[0-9a-fA-F]{1,4}){1,4}|([0-9a-fA-F]{1,4}:){1,2}(:[0-9a-fA-F]{1,4}){1,5}|[0-9a-fA-F]{1,4}:(:[0-9a-fA-F]{1,4}){1,6}|:((:[0-9a-fA-F]{1,4}){1,7}|:)|([0-9a-fA-F]{1,4}:){6}((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)|([0-9a-fA-F]{1,4}:){1,5}:((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)|::([0-9a-fA-F]{1,4}:){0,5}((25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?)\.){3}(25[0-5]|2[0-4][0-9]|[01]?[0-9][0-9]?))$`
	ServerAddr string `json:"serverAddr"`

	// ServerPort is the frps listening port.
	// +kubebuilder:default=7000
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	// +optional
	ServerPort int32 `json:"serverPort,omitempty"`

	// RemotePorts maps advertised template ports to ports on the frps host.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:MaxItems=16
	RemotePorts []RemotePortMapping `json:"remotePorts"`
}

// RemotePortMapping associates a template port name with its remote port.
type RemotePortMapping struct {
	// Name is the advertised port name from the GameTemplate.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`
	Name string `json:"name"`

	// RemotePort is the port on the frps host.
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	RemotePort int32 `json:"remotePort"`
}

// TailscaleTunnelSpec configures Tailscale tunnel settings.
type TailscaleTunnelSpec struct {
	// Hostname is the MagicDNS device name. If empty, defaults to the
	// GameServer name. Must be an RFC 1123 label: 1-63 alphanumeric
	// characters or hyphens, no leading/trailing hyphen.
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^([a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?)?$`
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// Tags are Tailscale ACL tags requested for the device when it
	// registers, via `tailscale up --advertise-tags` (tailscaled's
	// declarative config has no field for tags, and changing the tags of a
	// registered node needs a fresh login). Each entry is "tag:<name>" or a bare
	// name, which gets the "tag:" prefix. The tailnet ACL must grant
	// tagOwners for these tags to the auth key's owner; if the request is
	// refused, the tunnel pod logs the error and the tunnel runs untagged.
	// If any entry is invalid, the whole list is logged and ignored and the
	// device registers untagged. See
	// tunnel/specs.md.
	// +kubebuilder:validation:MaxItems=8
	// +optional
	Tags []string `json:"tags,omitempty"`
}

// PlayitTunnelSpec configures Playit tunnel settings.
// The assigned public address is reported into status.endpoints by the
// tunnel pod, not configured here.
type PlayitTunnelSpec struct {
	// TunnelName is an optional label used when creating the tunnel via
	// the Playit API.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	TunnelName string `json:"tunnelName,omitempty"`
}

// InlineBackupPolicy is the subset of BackupScheduleSpec a user can
// configure when enabling backups directly from GameServer. The
// operator materializes this into a managed BackupSchedule.
type InlineBackupPolicy struct {
	// Schedule is a standard cron expression; same structural guard as
	// BackupScheduleSpec.Schedule so typos are rejected at admission
	// instead of failing inside the reconcile loop of the materialized
	// BackupSchedule.
	// +kubebuilder:validation:MinLength=9
	// +kubebuilder:validation:Pattern=`^\S+\s+\S+\s+\S+\s+\S+\s+\S+(\s+\S+)?$`
	Schedule string `json:"schedule"`

	// RepoRef points at a BackupRepo (cluster-scoped resource, TBD) or
	// inline secret with restic repo + credentials.
	RepoRef SecretKeySelector `json:"repoRef"`

	// +optional
	Retention *BackupRetention `json:"retention,omitempty"`

	// +optional
	Suspend bool `json:"suspend,omitempty"`
}

// GameServerStatus is the observed state of a GameServer.
type GameServerStatus struct {
	// +optional
	Phase GameServerPhase `json:"phase,omitempty"`

	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Conditions carry detailed state transitions. Standard Ready,
	// Progressing, and Healthy conditions are surfaced in the UI.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`

	// ConditionArgs carries, per condition Type, the values interpolated into
	// that condition's English message, so clients can render the localized
	// message for the condition's current Reason (translation id
	// condition.<type>.<reason>, lower-cased). The English Message is
	// unchanged and stays the fallback. An entry exists only while the
	// condition's current Reason has arguments; it is removed when the Reason
	// changes to one without.
	// +optional
	ConditionArgs map[string]map[string]string `json:"conditionArgs,omitempty"`

	// Endpoints lists the externally reachable addresses advertised for
	// this server (populated once the Service is reconciled).
	// +optional
	Endpoints []GameServerEndpoint `json:"endpoints,omitempty"`

	// Agent reports runtime info sourced from the in-pod agent sidecar.
	// +optional
	Agent *AgentStatus `json:"agent,omitempty"`

	// StartedAt is the wall-clock time the game container was last
	// observed as Ready.
	// +optional
	StartedAt *metav1.Time `json:"startedAt,omitempty"`

	// LastBackupTime is the completion time of the most recent
	// successful backup of this server.
	// +optional
	LastBackupTime *metav1.Time `json:"lastBackupTime,omitempty"`

	// Idle reports the observed state of idle auto-sleep (spec.idle).
	// +optional
	Idle *IdleStatus `json:"idle,omitempty"`

	// Capture reports the observed state of the capture system for this GameServer.
	// +optional
	Capture *CaptureStatus `json:"capture,omitempty"`

	// TunnelEndpoints are the per-port addresses reported by the tunnel pod.
	// For frp/tailscale these are computed from the spec at reconcile time;
	// for playit they arrive asynchronously as the pod discovers assignments.
	// This field is read-only; only the tunnel pod (via its per-server
	// ServiceAccount) may patch it.
	// +kubebuilder:validation:MaxItems=32
	// +optional
	TunnelEndpoints []GameServerEndpoint `json:"tunnelEndpoints,omitempty"`
}

// IdleStatus is the observed state of idle auto-sleep. It is a read model:
// the authoritative sleep marker is an operator-owned annotation, so the
// decision survives a status subresource being reset.
type IdleStatus struct {
	// EmptySince is when the server first reported a *fresh* zero player
	// count. It is cleared the moment a player appears or the count goes
	// unknown, so the elapsed time since it is the true continuous-empty
	// duration. Nil while the server is not accumulating idle time.
	// +optional
	EmptySince *metav1.Time `json:"emptySince,omitempty"`

	// Asleep reports whether the operator has scaled this server down for
	// idleness. While true the phase reads Suspended with reason IdleAsleep;
	// it is distinct from spec.suspend, which is the user's own stop.
	// +optional
	Asleep bool `json:"asleep,omitempty"`

	// AsleepSince is when the server was put to sleep.
	// +optional
	AsleepSince *metav1.Time `json:"asleepSince,omitempty"`

	// LastWakeTime is when it was last woken, by a wake window or an
	// explicit request.
	// +optional
	LastWakeTime *metav1.Time `json:"lastWakeTime,omitempty"`

	// Reason explains the current idle state in one short phrase — why the
	// server is not accumulating idle time, or what woke it.
	// +kubebuilder:validation:MaxLength=256
	// +optional
	Reason string `json:"reason,omitempty"`
}

// CaptureStatus is the observed state of the capture system for one GameServer.
// It tracks capture readiness, the currently active capture (if any), and metadata about
// recent capture activity.
type CaptureStatus struct {
	// Ready: whether the capture sidecar is currently running and able to accept new capture requests.
	// True only while the ephemeral container is Running and listening on its capture port.
	// False if the sidecar has not been injected, has crashed, or spec.capture.enabled is false.
	// +optional
	Ready bool `json:"ready,omitempty"`

	// ActiveCapture: name of the NetworkCapture currently in Pending or Running phase for this GameServer, if any.
	// Cleared (set to nil) when that capture transitions to Completed, Failed, or Expired.
	// Allows finding the in-progress capture without listing all NetworkCaptures in the namespace.
	// +kubebuilder:validation:Optional
	// +kubebuilder:validation:MaxLength=255
	// +optional
	ActiveCapture *string `json:"activeCapture,omitempty"`

	// LastCaptureTime: timestamp of the most recent capture reaching a terminal phase
	// (Completed or Failed) on this GameServer. Not updated for Pending/Running/Expired transitions.
	// +optional
	LastCaptureTime *metav1.Time `json:"lastCaptureTime,omitempty"`

	// SidecarRestarts: count of container restarts observed for the capture ephemeral container.
	// NOTE: ephemeral containers cannot be restarted by Kubernetes (RestartPolicy does not apply).
	// This field is expected to stay 0 in practice but is retained to surface any unexpected
	// restarts (e.g. via node-level mechanisms) and as a stable field name if the implementation
	// later adds a genuinely restartable variant.
	// +optional
	SidecarRestarts int32 `json:"sidecarRestarts,omitempty"`
}

// GameServerEndpoint is a single externally reachable (host, port)
// associated with a named template port.
type GameServerEndpoint struct {
	Name string `json:"name"`
	Host string `json:"host"`
	Port int32  `json:"port"`
	// +optional
	Protocol corev1.Protocol `json:"protocol,omitempty"`

	// Private indicates the endpoint is reachable only from a private mesh
	// (e.g. Tailscale), not the public internet. The dashboard must label
	// it accordingly.
	// +optional
	Private bool `json:"private,omitempty"`

	// TunnelProvider is set when this endpoint is served by a tunnel
	// provider (frp, tailscale, playit) rather than the backing Service.
	// +optional
	TunnelProvider string `json:"tunnelProvider,omitempty"`

	// Pool is the load-balancer address pool this endpoint's address was
	// requested from. It is reconciler-owned — users express the preference
	// through spec.networking.addressPool, never here — and is empty when no
	// pool was requested or the cluster has no address manager configured.
	// +optional
	Pool string `json:"pool,omitempty"`
}

// AgentStatus is runtime state the sidecar reports via status updates.
type AgentStatus struct {
	// +optional
	Version string `json:"version,omitempty"`

	// +optional
	LastHeartbeat *metav1.Time `json:"lastHeartbeat,omitempty"`

	// PlayersOnline is the count reported by the game protocol (RCON /
	// server query). Null (absent) means "unknown" — the game exposes no
	// player source, or the query failed. Consumers must not treat unknown
	// as zero: idle auto-sleep depends on exactly that distinction, and the
	// dashboard sums this field across servers, which is why the agent
	// patches null rather than a -1 sentinel (see
	// agent/internal/heartbeat.sendOnce).
	// +optional
	PlayersOnline *int32 `json:"playersOnline,omitempty"`

	// PlayersMax is the configured max player count, if known.
	// +optional
	PlayersMax *int32 `json:"playersMax,omitempty"`

	// GameVersion is the version string the running game reports.
	// +optional
	GameVersion string `json:"gameVersion,omitempty"`

	// CPUMillicores is the agent's own cgroup CPU usage averaged over the
	// last heartbeat interval, in millicores. nil means "unknown".
	// +optional
	CPUMillicores *int64 `json:"cpuMillicores,omitempty"`

	// CPULimitMillicores is the pod's cgroup CPU limit in millicores, or
	// nil when unlimited / unknown.
	// +optional
	CPULimitMillicores *int64 `json:"cpuLimitMillicores,omitempty"`

	// MemoryBytes is the pod's current cgroup memory usage in bytes. nil
	// means "unknown".
	// +optional
	MemoryBytes *int64 `json:"memoryBytes,omitempty"`

	// MemoryLimitBytes is the pod's cgroup memory limit in bytes, or nil
	// when unlimited / unknown.
	// +optional
	MemoryLimitBytes *int64 `json:"memoryLimitBytes,omitempty"`

	// DiskUsedBytes is the used space on the game data volume (statfs).
	// nil means "unknown".
	// +optional
	DiskUsedBytes *int64 `json:"diskUsedBytes,omitempty"`

	// DiskTotalBytes is the total size of the game data volume (statfs).
	// nil means "unknown".
	// +optional
	DiskTotalBytes *int64 `json:"diskTotalBytes,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:shortName=gs
// +kubebuilder:printcolumn:name="Template",type=string,JSONPath=`.spec.templateRef.name`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Players",type=integer,JSONPath=`.status.agent.playersOnline`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// Asleep distinguishes an idle auto-sleep from a user's own stop, which the
// reused Suspended phase cannot. priority=1 keeps it out of the default table
// (it only shows under `-o wide`) so existing `kubectl get gs` output is
// unchanged.
// +kubebuilder:printcolumn:name="Asleep",type=boolean,JSONPath=`.status.idle.asleep`,priority=1
// +kubebuilder:subresource:status

// GameServer is a single running game server instance.
type GameServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GameServerSpec   `json:"spec,omitempty"`
	Status GameServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// GameServerList is a list of GameServers.
type GameServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GameServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GameServer{}, &GameServerList{})
}
