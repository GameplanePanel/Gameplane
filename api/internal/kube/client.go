// Package kube wraps the dynamic and typed Kubernetes clients that the
// API uses to read/write Gameplane CRDs.
package kube

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Client wraps the Kubernetes dynamic and typed clients, providing access
// to both custom CRDs and standard Kubernetes resources.
type Client struct {
	Dynamic dynamic.Interface
	Typed   kubernetes.Interface
	Config  *rest.Config
	// Management stores are set only for a standalone panel. Such a client
	// deliberately has no Kubernetes transport and is never a workload target.
	SecretStore      func(string) SecretStore
	ClusterStore     ClusterStore
	RegisterCluster  ClusterRegistrar
	RemoteAccess     *RemoteAccessPolicy
	kubeconfigDigest [32]byte
}

// GetServer fetches a GameServer by namespace and name. Returns nil if
// not found (does not distinguish from error for the RBAC fallback use case).
func (c *Client) GetServer(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
	return c.Dynamic.Resource(GVRs["servers"]).
		Namespace(ns).
		Get(ctx, name, metav1.GetOptions{})
}

// GVRs exposed to the REST layer. Keyed by resource-path segment so
// handlers can turn /servers into the right GVR without reflection.
var GVRs = map[string]schema.GroupVersionResource{
	"servers":   {Group: "gameplane.local", Version: "v1alpha1", Resource: "gameservers"},
	"templates": {Group: "gameplane.local", Version: "v1alpha1", Resource: "gametemplates"},
	"backups":   {Group: "gameplane.local", Version: "v1alpha1", Resource: "backups"},
	"schedules": {Group: "gameplane.local", Version: "v1alpha1", Resource: "backupschedules"},
	"restores":  {Group: "gameplane.local", Version: "v1alpha1", Resource: "restores"},
}

// ClusterGVRs lists the cluster-scoped CRDs the clusters handler manages.
// Kept separate from GVRs because /clusters is not a generic CRUD route —
// it serves the cluster registry and mTLS kubeconfig distribution surface.
var (
	GVRCluster        = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "clusters"}
	GVRModule         = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "modules"}
	GVRModuleSource   = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "modulesources"}
	GVRNetworkCapture = schema.GroupVersionResource{Group: "gameplane.local", Version: "v1alpha1", Resource: "networkcaptures"}
)

// New creates a Kubernetes client from the given rest.Config.
func New(cfg *rest.Config) (*Client, error) {
	dyn, err := dynamic.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	typed, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Client{Dynamic: dyn, Typed: typed, Config: cfg}, nil
}
