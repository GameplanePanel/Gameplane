package kube

// SetControlPlaneNamespace records the namespace of the default (home)
// cluster's operator control plane. The security settings ConfigMap of the
// home cluster lives there. Remote clusters use the namespace on their own
// registration instead, so this value never applies to them.
func (r *Registry) SetControlPlaneNamespace(ns string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.controlPlaneNamespace = ns
}

// ControlPlaneNamespace returns the home cluster's control-plane namespace, or
// "" when it was never set. Callers treat "" as not configured.
func (r *Registry) ControlPlaneNamespace() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.controlPlaneNamespace
}
