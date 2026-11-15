package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterSpec describes a remote Kubernetes cluster.
type ClusterSpec struct {
	// DisplayName is an optional human-readable name for the cluster.
	// +optional
	DisplayName string `json:"displayName,omitempty"`

	// KubeconfigSecret references a Secret holding the kubeconfig.
	// +kubebuilder:validation:Required
	KubeconfigSecret KubeconfigSecretRef `json:"kubeconfigSecret"`

	// AgentGateway enables interactive agent operations in this cluster.
	// Kubernetes configuration and pod streams still use KubeconfigSecret.
	// +optional
	AgentGateway *AgentGatewaySpec `json:"agentGateway,omitempty"`

	// ControlPlaneNamespace is the namespace of this cluster's operator
	// control plane. The API reads the cluster's security settings ConfigMap
	// (gameplane-security-settings) in this namespace. Empty or absent means
	// "gameplane". Must match the namespace of the remote install.
	// +kubebuilder:default=gameplane
	// +kubebuilder:validation:MaxLength=63
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +optional
	ControlPlaneNamespace string `json:"controlPlaneNamespace,omitempty"`
}

// AgentGatewaySpec configures the optional remote agent gateway.
type AgentGatewaySpec struct {
	// URL is the HTTPS origin of the gateway, without a path or query.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MaxLength=2048
	// +kubebuilder:validation:Pattern=`^https://[^/?#@]+/?$`
	URL string `json:"url"`

	// TLSSecretRef names credentials in the central API namespace. The Secret
	// must carry gameplane.local/agent-gateway-credentials=true and contain
	// ca.crt, tls.crt and tls.key. Values are never returned by the API.
	// +kubebuilder:validation:Required
	TLSSecretRef AgentGatewayTLSSecretRef `json:"tlsSecretRef"`
}

// AgentGatewayTLSSecretRef references an explicitly labeled credential Secret.
type AgentGatewayTLSSecretRef struct {
	// Name is the Secret name in the central API namespace.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// KubeconfigSecretRef names a Secret and the key within it that holds the kubeconfig.
type KubeconfigSecretRef struct {
	// Name is the Secret's name.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// Key is the Secret's data key where the kubeconfig lives.
	// Defaults to "kubeconfig".
	// +kubebuilder:default=kubeconfig
	// +optional
	Key string `json:"key,omitempty"`
}

// ClusterStatus reports the health and observed state of a remote cluster.
type ClusterStatus struct {
	// ObservedGeneration tracks the spec generation this status represents.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// Phase summarizes the cluster's health.
	//   - Unknown: not yet checked
	//   - Healthy: last check succeeded
	//   - Unhealthy: last check failed
	// +kubebuilder:validation:Enum=Unknown;Healthy;Unhealthy
	// +optional
	Phase string `json:"phase,omitempty"`

	// LastCheckTime is when the cluster's health was last assessed.
	// +optional
	LastCheckTime *metav1.Time `json:"lastCheckTime,omitempty"`

	// Message provides details about the current phase.
	// +optional
	Message string `json:"message,omitempty"`

	// ServerVersion is the Kubernetes server version of the remote cluster
	// (e.g., "v1.28.0"), set on successful health check.
	// +optional
	ServerVersion string `json:"serverVersion,omitempty"`

	// Conditions tracks the cluster's health and other observed state.
	// +optional
	// +patchMergeKey=type
	// +patchStrategy=merge
	Conditions []metav1.Condition `json:"conditions,omitempty" patchMergeKey:"type" patchStrategy:"merge"`
}

// Cluster phases.
const (
	ClusterPhaseUnknown   = "Unknown"
	ClusterPhaseHealthy   = "Healthy"
	ClusterPhaseUnhealthy = "Unhealthy"
)

// Cluster condition types.
const (
	ClusterConditionHealthy = "Healthy"
)

// LabelClusterKubeconfig marks a Secret as holding a cluster kubeconfig (value "true").
// Cluster CRs can only reference Secrets with this label.
const LabelClusterKubeconfig = "gameplane.local/cluster-kubeconfig"

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=cls
// +kubebuilder:printcolumn:name="DisplayName",type=string,JSONPath=`.spec.displayName`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:subresource:status

// Cluster represents a remote Kubernetes cluster managed by this Gameplane instance.
type Cluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterSpec   `json:"spec,omitempty"`
	Status ClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterList is a list of Clusters.
type ClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Cluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Cluster{}, &ClusterList{})
}
