package kube

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Registry holds a pool of Kubernetes clients keyed by cluster ID, so the API
// can dispatch each request to the right target cluster. A single-cluster
// install has exactly one entry: the default "local" cluster. The zero value
// is not usable; construct with NewRegistry. All methods are safe for
// concurrent use.
type Registry struct {
	mu         sync.RWMutex
	clients    map[string]*Client
	uids       map[string]types.UID
	defaultID  string
	management *Client
	operations map[string]*registrationSequence
	identities map[string]credentialIdentity
}

// Sequence credential loads per name, while the global lock protects only
// memory. Removal/publication invalidates active work using an epoch. Idle
// sequences are discarded so churn cannot retain one lock per historical name.
type registrationSequence struct {
	ready chan struct{}
	refs  int
	epoch uint64
}

type registryOperation struct {
	registry *Registry
	name     string
	sequence *registrationSequence
	epoch    uint64
}

type credentialIdentity struct {
	uid       types.UID
	name, key string
}

func registrationCredentials(u *unstructured.Unstructured) credentialIdentity {
	name, _, _ := unstructured.NestedString(u.Object, "spec", "kubeconfigSecret", "name")
	key, _, _ := unstructured.NestedString(u.Object, "spec", "kubeconfigSecret", "key")
	if key == "" {
		key = "kubeconfig"
	}
	return credentialIdentity{uid: u.GetUID(), name: name, key: key}
}

func (r *Registry) beginOperation(ctx context.Context, name string) (*registryOperation, error) {
	r.mu.Lock()
	sequence := r.operations[name]
	if sequence == nil {
		sequence = &registrationSequence{ready: make(chan struct{}, 1)}
		sequence.ready <- struct{}{}
		r.operations[name] = sequence
	}
	sequence.refs++
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		r.releaseOperation(name, sequence)
		return nil, ctx.Err()
	case <-sequence.ready:
	}
	r.mu.RLock()
	epoch := sequence.epoch
	r.mu.RUnlock()
	return &registryOperation{registry: r, name: name, sequence: sequence, epoch: epoch}, nil
}

func (r *Registry) releaseOperation(name string, sequence *registrationSequence) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sequence.refs--
	if sequence.refs == 0 {
		delete(r.operations, name)
	}
}

func (op *registryOperation) finish() {
	op.sequence.ready <- struct{}{}
	op.registry.releaseOperation(op.name, op.sequence)
}

func (r *Registry) invalidateLocked(name string) {
	if sequence := r.operations[name]; sequence != nil {
		sequence.epoch++
	}
}

func (op *registryOperation) publish(identity credentialIdentity, client *Client) bool {
	r := op.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if op.sequence.epoch != op.epoch {
		return false
	}
	if client == nil {
		delete(r.clients, op.name)
		delete(r.uids, op.name)
		delete(r.identities, op.name)
	} else {
		r.clients[op.name] = client
		r.uids[op.name] = identity.uid
		r.identities[op.name] = identity
	}
	return true
}

func (r *Registry) existingClient(name string, identity credentialIdentity) *Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.identities[name] == identity {
		return r.clients[name]
	}
	return nil
}

// SetManagement sets the panel's credential and registration backend. It does
// not register the panel host as a workload cluster.
func (r *Registry) SetManagement(c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.management = c
}

// Management returns the explicit backend, falling back to the default
// Kubernetes client for existing installations and callers.
func (r *Registry) Management() *Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.management != nil {
		return r.management
	}
	return r.clients[r.defaultID]
}

// NewRegistry returns an empty Registry whose default cluster is defaultID.
func NewRegistry(defaultID string) *Registry {
	return &Registry{
		clients:    map[string]*Client{},
		uids:       map[string]types.UID{},
		defaultID:  defaultID,
		operations: map[string]*registrationSequence{},
		identities: map[string]credentialIdentity{},
	}
}

// Get returns the client for cluster id and true, or (nil, false) if the
// cluster is not registered.
func (r *Registry) Get(id string) (*Client, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[id]
	return c, ok
}

// Default returns the client for the registry's default cluster, or nil if it
// has not been registered.
func (r *Registry) Default() *Client {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.clients[r.defaultID]
}

// DefaultID returns the ID of the default cluster.
func (r *Registry) DefaultID() string {
	return r.defaultID
}

// Set registers (or replaces) the client for cluster id and clears the stored UID.
func (r *Registry) Set(id string, c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[id] = c
	r.invalidateLocked(id)
	delete(r.identities, id)
	delete(r.uids, id)
}

// Remove deletes the client for cluster id, if present, and clears the stored UID.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, id)
	delete(r.uids, id)
	delete(r.identities, id)
	r.invalidateLocked(id)
}

// SetWithUID registers (or replaces) the client for cluster id and stores its UID.
func (r *Registry) SetWithUID(id string, uid types.UID, c *Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[id] = c
	r.uids[id] = uid
	delete(r.identities, id)
	r.invalidateLocked(id)
}

// RemoveIfUID removes the entry (and its UID) when uid is empty, when the stored
// UID is empty, or when they are equal; otherwise it leaves the entry (a newer
// registration with the same name).
func (r *Registry) RemoveIfUID(id string, uid types.UID) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Remove if the argument uid is empty.
	if uid == "" {
		r.invalidateLocked(id)
		delete(r.identities, id)
		delete(r.clients, id)
		delete(r.uids, id)
		return
	}

	// Remove if the stored uid is empty.
	storedUID, ok := r.uids[id]
	if !ok || storedUID == "" {
		r.invalidateLocked(id)
		delete(r.identities, id)
		delete(r.clients, id)
		delete(r.uids, id)
		return
	}

	// Remove if the stored uid matches the argument.
	if storedUID == uid {
		r.invalidateLocked(id)
		delete(r.identities, id)
		delete(r.clients, id)
		delete(r.uids, id)
		return
	}

	// Otherwise, leave the entry (a newer registration with the same name).
}

// IDs returns the registered cluster IDs in sorted order.
func (r *Registry) IDs() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.clients))
	for id := range r.clients {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// GetServer fetches a GameServer from the named cluster, so callers like the
// rbac middleware's owner/collaborator fallback can target the right cluster.
// Returns an error if the cluster is not registered.
func (r *Registry) GetServer(ctx context.Context, cluster, ns, name string) (*unstructured.Unstructured, error) {
	c, ok := r.Get(cluster)
	if !ok {
		return nil, fmt.Errorf("cluster %q not registered", cluster)
	}
	return c.GetServer(ctx, ns, name)
}
