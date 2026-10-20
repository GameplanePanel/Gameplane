package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ModuleSource type discriminator values.
const (
	ModuleSourceTypeOCI    = "oci"
	ModuleSourceTypeGit    = "git"
	ModuleSourceTypeHTTP   = "http"
	ModuleSourceTypeLocal  = "local"
	ModuleSourceTypeUpload = "upload"
)

// ModuleSourceSpec configures one store that hosts Gameplane modules. The
// spec is a discriminated union: exactly the nested config matching
// spec.type must be set (enforced by CEL rules below). The operator
// periodically indexes the source and caches the resulting catalog into
// status.modules.
//
// +kubebuilder:validation:XValidation:rule="self.type != 'oci' || has(self.oci)",message="spec.oci is required when spec.type is oci"
// +kubebuilder:validation:XValidation:rule="self.type != 'git' || has(self.git)",message="spec.git is required when spec.type is git"
// +kubebuilder:validation:XValidation:rule="self.type != 'http' || has(self.http)",message="spec.http is required when spec.type is http"
// +kubebuilder:validation:XValidation:rule="self.type != 'local' || has(self.local)",message="spec.local is required when spec.type is local"
// +kubebuilder:validation:XValidation:rule="!has(self.oci) || self.type == 'oci'",message="spec.oci is only valid when spec.type is oci"
// +kubebuilder:validation:XValidation:rule="!has(self.git) || self.type == 'git'",message="spec.git is only valid when spec.type is git"
// +kubebuilder:validation:XValidation:rule="!has(self.http) || self.type == 'http'",message="spec.http is only valid when spec.type is http"
// +kubebuilder:validation:XValidation:rule="!has(self.local) || self.type == 'local'",message="spec.local is only valid when spec.type is local"
// +kubebuilder:validation:XValidation:rule="!has(self.verify) || self.type == 'oci'",message="spec.verify is only valid when spec.type is oci"
type ModuleSourceSpec struct {
	// Type selects where this source pulls modules from:
	//   - oci:    an OCI registry holding module bundle artifacts
	//   - git:    a git repository containing module directories
	//   - http:   an http(s) URL to a tar.gz/zip of module directories
	//   - local:  a directory mounted into the operator pod
	//   - upload: bundles uploaded through the API, stored as labeled
	//             ConfigMaps in the operator namespace (no config)
	// +kubebuilder:validation:Enum=oci;git;http;local;upload
	// +kubebuilder:default=oci
	// +optional
	Type string `json:"type,omitempty"`

	// OCI configures an oci-type source.
	// +optional
	OCI *OCISourceSpec `json:"oci,omitempty"`

	// Git configures a git-type source.
	// +optional
	Git *GitSourceSpec `json:"git,omitempty"`

	// HTTP configures an http-type source.
	// +optional
	HTTP *HTTPSourceSpec `json:"http,omitempty"`

	// Local configures a local-type source.
	// +optional
	Local *LocalSourceSpec `json:"local,omitempty"`

	// Allow restricts which discovered module names this source exposes.
	// Entries are exact names or path.Match globs (e.g. "minecraft-*").
	// Empty means every discovered module. For oci sources the explicit
	// spec.oci.modules list is authoritative; allow filters on top of it.
	// +optional
	Allow []string `json:"allow,omitempty"`

	// RefreshInterval is how often the controller re-indexes the
	// source. Defaults to 1h. Minimum 1m.
	// +kubebuilder:default="1h"
	// +optional
	RefreshInterval metav1.Duration `json:"refreshInterval,omitempty"`

	// Verify, when set, requires every module bundle installed from this
	// source to carry a valid cosign signature. Only valid for oci sources —
	// cosign signatures are an OCI-registry concept; git/http/local/upload
	// sources rely on the content digest plus a Module.spec.digest pin.
	// +optional
	Verify *VerifySpec `json:"verify,omitempty"`
}

// VerifySpec configures cosign signature verification for an oci source.
// Exactly one of key or keyless must be set.
//
// +kubebuilder:validation:XValidation:rule="has(self.key) != has(self.keyless)",message="exactly one of verify.key or verify.keyless must be set"
type VerifySpec struct {
	// Key references a Secret in the operator namespace holding a PEM cosign
	// public key under the "cosign.pub" data key, for keyed verification.
	// +optional
	Key *corev1.LocalObjectReference `json:"key,omitempty"`

	// Keyless verifies a Fulcio-issued (keyless) signature against a pinned
	// OIDC issuer and certificate identity.
	// +optional
	Keyless *KeylessVerifySpec `json:"keyless,omitempty"`

	// RequireTransparencyLog additionally requires each signature to carry
	// valid Sigstore transparency-log (Rekor) inclusion evidence, proving the
	// signing event was publicly recorded and making misuse of a stolen key
	// detectable.
	//
	// Only meaningful with key: keyless verification always enforces the log.
	// Defaults to false, because a keyed signature is complete without a log
	// entry and requiring one would break air-gapped clusters and any private
	// registry whose bundles are signed offline. The operator must be able to
	// reach the Sigstore TUF root to load the Rekor public keys when this is
	// enabled; the inclusion proof bundled with the signature is then checked
	// offline, without a live log query per verification.
	// +optional
	RequireTransparencyLog bool `json:"requireTransparencyLog,omitempty"`
}

// KeylessVerifySpec pins the OIDC issuer and certificate identity a keyless
// cosign signature must carry. Both are matched exactly.
type KeylessVerifySpec struct {
	// Issuer is the OIDC issuer URL embedded in the signing certificate
	// (e.g. "https://token.actions.githubusercontent.com").
	// +kubebuilder:validation:MinLength=1
	Issuer string `json:"issuer"`

	// Identity is the certificate SAN identity that must have produced the
	// signature (e.g. a GitHub Actions workflow URL or a signer email).
	// +kubebuilder:validation:MinLength=1
	Identity string `json:"identity"`
}

// OCISourceSpec points at an OCI registry prefix that holds module
// bundle artifacts. Registries cannot be enumerated portably, so the
// module list is explicit; each module is pushed under
// "<url>/<name>:<version>".
type OCISourceSpec struct {
	// URL is the registry/repository prefix that holds module bundles
	// (e.g. "ghcr.io/gameplanepanel/gameplane-modules").
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Modules is the explicit list of modules served by this source.
	// +kubebuilder:validation:MinItems=1
	Modules []ModuleRef `json:"modules"`

	// PullSecretRef references a kubernetes.io/dockerconfigjson Secret
	// in the operator namespace. Used for private registries.
	// +optional
	PullSecretRef *corev1.LocalObjectReference `json:"pullSecretRef,omitempty"`

	// Insecure allows plain HTTP (no TLS) to the registry. TLS
	// verification is never skipped — for self-signed registries mount
	// the CA into the operator's trust store. Intended for local
	// kind/k3d registries; do not enable on production clusters.
	// +optional
	Insecure bool `json:"insecure,omitempty"`
}

// GitSourceSpec points at a git repository containing module
// directories (each holding a module.yaml + template.yaml). Modules
// are auto-discovered by scanning the checkout; the source publishes
// one version stream — whatever each module.yaml declares at the
// configured ref.
type GitSourceSpec struct {
	// URL is the clone URL (https://... or ssh://git@...).
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// Ref is the branch or tag to index. Defaults to main.
	// +kubebuilder:default=main
	// +optional
	Ref string `json:"ref,omitempty"`

	// SubPath limits scanning to a subdirectory of the repository
	// (e.g. "modules"). Must be relative and must not contain "..".
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.contains('..')",message="subPath must be relative and must not contain '..'"
	// +optional
	SubPath string `json:"subPath,omitempty"`

	// SecretRef references a Secret in the operator namespace holding
	// credentials: either "token" (or "username"+"password") for https,
	// or "ssh-privatekey" (+ optional "known_hosts") for ssh.
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`
}

// HTTPSourceSpec points at an http(s) URL serving a tar.gz or zip
// archive of module directories. Modules are auto-discovered by
// scanning the extracted archive.
type HTTPSourceSpec struct {
	// URL of the archive (.tar.gz/.tgz/.zip).
	// +kubebuilder:validation:MinLength=1
	URL string `json:"url"`

	// SecretRef references a Secret in the operator namespace holding
	// "token" (sent as a bearer token) or "username"+"password" (basic
	// auth).
	// +optional
	SecretRef *corev1.LocalObjectReference `json:"secretRef,omitempty"`

	// Insecure allows plain http URLs. TLS verification is never
	// skipped — for self-signed registries mount the CA into the
	// operator's trust store.
	// +optional
	Insecure bool `json:"insecure,omitempty"`
}

// LocalSourceSpec points at a directory mounted into the operator pod
// (the mount and the operator's --module-local-root flag are wired via
// Helm values). Modules are auto-discovered by scanning the directory.
type LocalSourceSpec struct {
	// Path is resolved relative to the operator's --module-local-root.
	// Empty scans the root itself. Must be relative and must not
	// contain "..".
	// +kubebuilder:validation:XValidation:rule="!self.startsWith('/') && !self.contains('..')",message="path must be relative and must not contain '..'"
	// +optional
	Path string `json:"path,omitempty"`
}

// ModuleRef identifies a module within a ModuleSource by its logical
// name. For oci sources the full reference is reconstructed as
// "<oci.url>/<name>".
type ModuleRef struct {
	// Name is the module identifier within this source. Must match the
	// "name" field in the bundle's module.yaml.
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// ModuleSourceStatus is populated by the operator from a successful
// source index.
type ModuleSourceStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// LastSync is the time of the most recent successful index pull.
	// +optional
	LastSync *metav1.Time `json:"lastSync,omitempty"`

	// Conditions tracks Synced (last index attempt succeeded) and Ready
	// (a complete catalog is available).
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchMergeKey:"type" patchStrategy:"merge"`

	// Modules is the cached catalog for this source.
	// +optional
	Modules []ModuleEntry `json:"modules,omitempty"`
}

// ModuleEntry is one module's catalog snapshot from the source. The
// operator fills these from each module's module.yaml (+ tag list for
// oci sources).
type ModuleEntry struct {
	// Name is the logical module name.
	Name string `json:"name"`

	// DisplayName is the human-readable label from module.yaml.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// Summary is the one-line catalog blurb from module.yaml.
	// +optional
	Summary string `json:"summary,omitempty"`

	// Game is the game-family identifier from module.yaml.
	// +optional
	Game string `json:"game,omitempty"`

	// Categories are the catalog groupings from module.yaml (e.g.
	// ["Survival", "Sandbox"]). Surfaced so the dashboard can build its
	// module-catalog filter from author-declared values instead of a
	// hardcoded heuristic. A bundle that declares the legacy scalar
	// `category:` is normalized into a one-element list on parse.
	// +kubebuilder:validation:MaxItems=8
	// +kubebuilder:validation:items:MaxLength=32
	// +kubebuilder:validation:items:MinLength=1
	// +optional
	Categories []string `json:"categories,omitempty"`

	// Icon is either a relative filename (resolved against the bundle)
	// or a URL/data URI passed straight through to the UI.
	// +optional
	Icon string `json:"icon,omitempty"`

	// Reference locates this module within its source: the registry
	// repo path for oci (e.g. "ghcr.io/gameplanepanel/gameplane-modules/minecraft-java"),
	// or "<type>:<location>/<dir>" for the other source types.
	Reference string `json:"reference"`

	// Versions lists the available versions, semver-sorted descending.
	// For oci these are the registry tags; non-oci sources publish a
	// single version — whatever module.yaml declares.
	// +optional
	Versions []string `json:"versions,omitempty"`

	// LatestVersion is Versions[0] when populated.
	// +optional
	LatestVersion string `json:"latestVersion,omitempty"`

	// Digest identifies the exact content backing LatestVersion: the
	// OCI manifest digest, "git:<commit sha>", or a sha256 content hash
	// over the module directory. Lets installs detect content drift
	// when a version string is reused.
	// +optional
	Digest string `json:"digest,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=msrc;modsource
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.type`
// +kubebuilder:printcolumn:name="Synced",type=string,JSONPath=`.status.conditions[?(@.type=="Synced")].status`
// +kubebuilder:printcolumn:name="Last Sync",type=date,JSONPath=`.status.lastSync`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:subresource:status

// ModuleSource is a store that Gameplane pulls module bundles from.
type ModuleSource struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ModuleSourceSpec   `json:"spec,omitempty"`
	Status ModuleSourceStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ModuleSourceList is a list of ModuleSources.
type ModuleSourceList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ModuleSource `json:"items"`
}

// Condition types reported on ModuleSource.status.
const (
	// ModuleSourceConditionSynced is True when the most recent index
	// pull succeeded.
	ModuleSourceConditionSynced = "Synced"
	// ModuleSourceConditionReady is True when at least one full catalog
	// pull has completed successfully.
	ModuleSourceConditionReady = "Ready"
)

func init() {
	SchemeBuilder.Register(&ModuleSource{}, &ModuleSourceList{})
}
