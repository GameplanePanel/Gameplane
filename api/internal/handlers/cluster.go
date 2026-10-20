package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8srest "k8s.io/client-go/rest"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// MountCluster exposes read-only cluster observability the dashboard's
// Cluster page and header rely on. Everything here reads live Kubernetes
// state via the selected cluster's client — no caching, no side effects —
// keeping the operator authoritative. All routes are GETs (viewer+ under
// the RBAC rules in api/internal/rbac).
func MountCluster(r chi.Router, reg *kube.Registry, store *db.Store, gameplaneVersion string, clusterOps bool, updateChannel string) {
	h := &clusterHandler{reg: reg, store: store, gameplaneVersion: gameplaneVersion, clusterOps: clusterOps, updateChannel: updateChannel, podCounts: &podCountCache{entries: map[string]podCountEntry{}}}
	r.Get("/cluster", h.view)
	r.Get("/cluster/info", h.info)
	r.Get("/cluster/stats", h.stats)
}

type podCountEntry struct {
	counts map[string]int64
	exp    time.Time
}

// podCountCache is shared by every per-request copy of clusterHandler (held by pointer, so no lock is copied).
type podCountCache struct {
	mu      sync.Mutex
	entries map[string]podCountEntry // keyed by clusterID
}

type clusterHandler struct {
	reg              *kube.Registry
	k                *kube.Client
	clusterID        string
	store            *db.Store
	gameplaneVersion string
	clusterOps       bool
	updateChannel    string
	podCounts        *podCountCache
}

func (h *clusterHandler) forRequest(w http.ResponseWriter, req *http.Request) (*clusterHandler, bool) {
	id := scope.RequestedCluster(req)
	if !dnsLabelRE.MatchString(id) {
		httperr.Write(w, req, scope.ErrForbiddenCluster)
		return nil, false
	}
	if h.reg == nil {
		httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("cluster registry unavailable"))
		return nil, false
	}
	k, ok := h.reg.Get(id)
	if !ok {
		// Registration metadata lives centrally, but inventory never does.
		// Keep a disconnected registration distinguishable from an unknown ID.
		if id != h.reg.DefaultID() {
			home := h.reg.Management()
			if home != nil && home.Clusters() != nil {
				_, err := home.Clusters().Get(req.Context(), id, metav1.GetOptions{})
				if apierrors.IsNotFound(err) {
					httperr.Write(w, req, scope.ErrForbiddenCluster)
					return nil, false
				}
				if err != nil {
					httperr.WriteCode(w, req, http.StatusServiceUnavailable, err)
					return nil, false
				}
			} else {
				httperr.Write(w, req, scope.ErrForbiddenCluster)
				return nil, false
			}
		}
		httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("selected cluster client unavailable"))
		return nil, false
	}
	if k == nil || k.Typed == nil {
		httperr.WriteCode(w, req, http.StatusServiceUnavailable, errors.New("selected cluster client unavailable"))
		return nil, false
	}
	selected := *h
	selected.k, selected.clusterID = k, id
	if id != h.reg.DefaultID() {
		selected.clusterOps = false
		selected.updateChannel = ""
	}
	return &selected, true
}

func (h *clusterHandler) writeError(w http.ResponseWriter, req *http.Request, err error) {
	if h.clusterID != h.reg.DefaultID() && !apierrors.IsForbidden(err) && !apierrors.IsUnauthorized(err) && !apierrors.IsNotFound(err) {
		httperr.WriteCode(w, req, http.StatusServiceUnavailable, err)
		return
	}
	httperr.Write(w, req, err)
}

type clusterNode struct {
	Name      string        `json:"name"`
	Roles     []string      `json:"roles,omitempty"`
	Status    string        `json:"status"`
	StartedAt string        `json:"startedAt,omitempty"`
	Pods      *resourcePair `json:"pods,omitempty"`
	CPU       *resourcePair `json:"cpu,omitempty"`
	Memory    *resourcePair `json:"memory,omitempty"`
}

// resourcePair mirrors the dashboard's {used?, capacity?} shape. `used` is
// populated from metrics-server (the metrics.k8s.io aggregated API) when
// it's installed and reachable; it's omitted — not zero — when unknown, so
// the UI can tell "measured idle" apart from "no metrics pipeline" instead
// of rendering a false 0% bar either way. Capacity and used always share a
// unit per resource: CPU is whole/fractional cores (matching how capacity
// was already rendered — "N cores" — so used can carry metrics-server's
// sub-core precision without a wider unit change rippling through the
// dashboard), memory is bytes, pods is a plain count.
type resourcePair struct {
	Used     *float64 `json:"used,omitempty"`
	Capacity int64    `json:"capacity"`
}

type clusterView struct {
	Nodes   []clusterNode `json:"nodes"`
	Version string        `json:"version,omitempty"`
	Name    string        `json:"name,omitempty"`
	Ready   int           `json:"ready"`
	Total   int           `json:"total"`
}

type clusterInfo struct {
	ClusterName      string `json:"clusterName,omitempty"`
	Version          string `json:"version,omitempty"`          // Kubernetes server version
	GameplaneVersion string `json:"gameplaneVersion,omitempty"` // Gameplane control-plane build
	// ClusterOps mirrors the --cluster-ops flag so the dashboard can
	// grey out node-join / kubeconfig actions instead of letting every
	// click run into the handlers' 501. Never omitted: the client must
	// distinguish "off" from "server too old to report it".
	ClusterOps bool `json:"clusterOps"`
	// UpdateChannel is the chart's informational updates.channel label.
	// Nothing consumes it server-side — upgrades happen via Helm — it
	// exists so the dashboard can show which channel this install tracks.
	UpdateChannel string `json:"updateChannel,omitempty"`
}

type clusterStats struct {
	Nodes int `json:"nodes"`
	// TotalStorageBytes is the physical disk the cluster's nodes report
	// (ephemeral-storage capacity) — the denominator the web client
	// compares provisioned storage against.
	TotalStorageBytes int64 `json:"totalStorageBytes"`
	// UsedStorageBytes is NOT disk usage — Kubernetes exposes no such
	// metric without a metrics/monitoring pipeline. It is storage
	// *provisioned*: the capacity of Bound PersistentVolumes, i.e. what has
	// been handed out to workloads regardless of how full those volumes
	// actually are. With networked storage (Ceph, EBS, …) PVs don't come
	// off node disk, so this can legitimately exceed TotalStorageBytes —
	// an honest overcommit signal, not a bug. The JSON field name stays
	// "usedStorageBytes" for API compatibility; the web client is
	// responsible for labeling it "provisioned" and presenting the
	// overcommit case explicitly rather than as a >100% bar.
	UsedStorageBytes int64 `json:"usedStorageBytes"`
}

func (h *clusterHandler) view(w http.ResponseWriter, req *http.Request) {
	h, ok := h.forRequest(w, req)
	if !ok {
		return
	}
	nodes, err := h.k.Typed.CoreV1().Nodes().List(req.Context(), metav1.ListOptions{})
	if err != nil {
		h.writeError(w, req, err)
		return
	}
	usage := h.fetchNodeUsage(req.Context())
	podCounts := h.countNodePods(req.Context())
	version, _ := h.serverVersion()
	out := clusterView{
		Nodes:   make([]clusterNode, 0, len(nodes.Items)),
		Version: version,
		Name:    h.clusterName(req.Context()),
		Total:   len(nodes.Items),
	}
	for i := range nodes.Items {
		n := mapNode(&nodes.Items[i])
		if u, ok := usage[n.Name]; ok {
			if n.CPU != nil {
				cpuUsed := u.cpuCores
				n.CPU.Used = &cpuUsed
			}
			if n.Memory != nil {
				memUsed := u.memoryBytes
				n.Memory.Used = &memUsed
			}
		}
		if n.Pods != nil && podCounts != nil {
			podsUsed := float64(podCounts[n.Name])
			n.Pods.Used = &podsUsed
		}
		if n.Status == "Ready" {
			out.Ready++
		}
		out.Nodes = append(out.Nodes, n)
	}
	writeJSON(w, out)
}

// nodeUsage is one node's live CPU/memory consumption as reported by
// metrics-server, already converted to the same units resourcePair.Capacity
// uses (cores, bytes) so callers can assign it straight to `Used`.
type nodeUsage struct {
	cpuCores    float64
	memoryBytes float64
}

// metricsNodeList is the subset of the metrics.k8s.io/v1beta1 NodeMetrics
// list response this handler needs. Defined locally instead of importing
// k8s.io/metrics for one read-only GET.
type metricsNodeList struct {
	Items []struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
		Usage struct {
			CPU    string `json:"cpu"`
			Memory string `json:"memory"`
		} `json:"usage"`
	} `json:"items"`
}

// fetchNodeUsage reads live per-node CPU/memory usage from the
// metrics.k8s.io aggregated API (metrics-server) through the existing typed
// client's raw REST path — this repo avoids adding k8s.io/metrics as a
// dependency for a single GET.
//
// Returns nil, never an error: a cluster without metrics-server installed
// is a normal, supported deployment (the operator doesn't require one), so
// any failure to reach or parse the endpoint — 404 because the API group
// isn't registered, a network error, a malformed body — must leave the
// caller with "no usage data" rather than fail the whole /cluster request.
// Logged at debug, not warn/error, since this is an expected steady state
// on many installs.
func (h *clusterHandler) fetchNodeUsage(ctx context.Context) map[string]nodeUsage {
	rc := h.k.Typed.CoreV1().RESTClient()
	// Guards against a typed-nil interface: fake Kubernetes clientsets
	// (used throughout this package's tests) return a nil *rest.RESTClient
	// wrapped in a non-nil rest.Interface, which would otherwise panic on
	// the first field access inside Get(). A real cluster's client never
	// returns nil here.
	if rc == nil {
		return nil
	}
	if typed, ok := rc.(*k8srest.RESTClient); ok && typed == nil {
		return nil
	}

	raw, err := rc.Get().AbsPath("/apis/metrics.k8s.io/v1beta1/nodes").DoRaw(ctx)
	if err != nil {
		slog.Debug("metrics-server unavailable, cluster CPU/memory usage omitted", "err", err)
		return nil
	}

	var parsed metricsNodeList
	if err := json.Unmarshal(raw, &parsed); err != nil {
		slog.Debug("metrics-server response unparsable, cluster CPU/memory usage omitted", "err", err)
		return nil
	}

	usage := make(map[string]nodeUsage, len(parsed.Items))
	for _, item := range parsed.Items {
		cpu, err := resource.ParseQuantity(item.Usage.CPU)
		if err != nil {
			continue
		}
		mem, err := resource.ParseQuantity(item.Usage.Memory)
		if err != nil {
			continue
		}
		// MilliValue() first (millicores), then down to cores: matches the
		// unit resourcePair.Capacity already uses for CPU while keeping
		// metrics-server's sub-core precision instead of rounding a small
		// usage figure up to a whole core.
		usage[item.Metadata.Name] = nodeUsage{
			cpuCores:    float64(cpu.MilliValue()) / 1000,
			memoryBytes: float64(mem.Value()),
		}
	}
	return usage
}

// countNodePods counts non-terminal pods (Pending and Running phases) per
// node name. Unscheduled pods (empty spec.nodeName) are ignored. It returns
// nil on any list error: pod counts are informational and the "used" value
// is simply omitted. Results are cached per cluster for ~15 seconds.
func (h *clusterHandler) countNodePods(ctx context.Context) map[string]int64 {
	const cacheTTL = 15 * time.Second

	// Check cache.
	if h.podCounts != nil {
		h.podCounts.mu.Lock()
		e, ok := h.podCounts.entries[h.clusterID]
		h.podCounts.mu.Unlock()
		if ok && time.Now().Before(e.exp) {
			return e.counts
		}
	}

	// Paginate: list 500 pods at a time.
	counts := make(map[string]int64)
	opts := metav1.ListOptions{
		Limit:         500,
		FieldSelector: "status.phase!=Succeeded,status.phase!=Failed",
	}

	for {
		pods, err := h.k.Typed.CoreV1().Pods("").List(ctx, opts)
		if err != nil {
			slog.Debug("count node pods: list failed", "err", err)
			return nil
		}

		// Process this page: count Pending and Running pods.
		for i := range pods.Items {
			pod := &pods.Items[i]
			if pod.Spec.NodeName == "" {
				continue
			}
			switch pod.Status.Phase {
			case corev1.PodPending, corev1.PodRunning:
				counts[pod.Spec.NodeName]++
			}
		}

		// Stop if no continue token.
		if pods.Continue == "" {
			break
		}
		opts.Continue = pods.Continue
	}

	// Cache the result.
	if h.podCounts != nil {
		h.podCounts.mu.Lock()
		h.podCounts.entries[h.clusterID] = podCountEntry{counts: counts, exp: time.Now().Add(cacheTTL)}
		h.podCounts.mu.Unlock()
	}
	return counts
}

func (h *clusterHandler) info(w http.ResponseWriter, req *http.Request) {
	h, ok := h.forRequest(w, req)
	if !ok {
		return
	}
	version, err := h.serverVersion()
	if err != nil && h.clusterID != h.reg.DefaultID() {
		h.writeError(w, req, err)
		return
	}
	writeJSON(w, clusterInfo{
		ClusterName:      h.clusterName(req.Context()),
		Version:          version,
		GameplaneVersion: h.gameplaneVersion,
		ClusterOps:       h.clusterOps,
		UpdateChannel:    h.updateChannel,
	})
}

func (h *clusterHandler) stats(w http.ResponseWriter, req *http.Request) {
	h, ok := h.forRequest(w, req)
	if !ok {
		return
	}
	nodes, err := h.k.Typed.CoreV1().Nodes().List(req.Context(), metav1.ListOptions{})
	if err != nil {
		h.writeError(w, req, err)
		return
	}
	pvs, err := h.k.Typed.CoreV1().PersistentVolumes().List(req.Context(), metav1.ListOptions{})
	if err != nil {
		h.writeError(w, req, err)
		return
	}
	writeJSON(w, clusterStats{
		Nodes:             len(nodes.Items),
		TotalStorageBytes: nodeStorageCapacity(nodes.Items),
		UsedStorageBytes:  boundVolumeBytes(pvs.Items),
	})
}

// nodeStorageCapacity sums the ephemeral-storage capacity each node reports:
// the physical disk behind the cluster.
//
// Measuring "total" as the sum of *all* PersistentVolume capacity — as this
// once did — makes the storage meter degenerate. Every PV that exists is
// almost always Bound, so used == total and the meter reads 100% on every
// install. Nodes give an independent denominator.
//
// Caveat: with networked storage (Ceph, EBS) the volumes do not come off the
// node disks, so provisioned storage can exceed this figure. That reads as
// >100% — an honest signal of overcommit rather than a silent 100%.
func nodeStorageCapacity(nodes []corev1.Node) int64 {
	var total int64
	for i := range nodes {
		q := nodes[i].Status.Capacity[corev1.ResourceEphemeralStorage]
		total += q.Value()
	}
	return total
}

// boundVolumeBytes sums the capacity of Bound PersistentVolumes. An unbound
// volume is spare capacity, not consumption.
func boundVolumeBytes(pvs []corev1.PersistentVolume) int64 {
	var used int64
	for i := range pvs {
		if pvs[i].Status.Phase != corev1.VolumeBound {
			continue
		}
		q := pvs[i].Spec.Capacity[corev1.ResourceStorage]
		used += q.Value()
	}
	return used
}

// serverVersion returns the selected cluster's Kubernetes version. The view
// tolerates unavailable discovery as partial metadata; remote /cluster/info
// propagates the failure rather than reporting a healthy-looking empty result.
func (h *clusterHandler) serverVersion() (string, error) {
	v, err := h.k.Typed.Discovery().ServerVersion()
	if err != nil {
		return "", err
	}
	if v == nil {
		return "", errors.New("cluster version unavailable")
	}
	return v.GitVersion, nil
}

// clusterName uses the remote registration ID, or the configured local instance
// name when reading the home cluster. Remote display labels come from /clusters.
func (h *clusterHandler) clusterName(ctx context.Context) string {
	if h.clusterID != h.reg.DefaultID() {
		return h.clusterID
	}
	if h.store == nil {
		return ""
	}
	raw, ok, err := h.store.ConfigValue(ctx, "general")
	if err != nil || !ok {
		return ""
	}
	var g struct {
		InstanceName string `json:"instanceName"`
	}
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return ""
	}
	return g.InstanceName
}

func mapNode(n *corev1.Node) clusterNode {
	out := clusterNode{
		Name:      n.Name,
		Roles:     nodeRoles(n),
		Status:    "NotReady",
		StartedAt: n.CreationTimestamp.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			out.Status = "Ready"
			break
		}
	}
	if cpu, ok := n.Status.Capacity[corev1.ResourceCPU]; ok {
		out.CPU = &resourcePair{Capacity: cpu.Value()}
	}
	if mem, ok := n.Status.Capacity[corev1.ResourceMemory]; ok {
		out.Memory = &resourcePair{Capacity: mem.Value()}
	}
	if pods, ok := n.Status.Capacity[corev1.ResourcePods]; ok {
		out.Pods = &resourcePair{Capacity: pods.Value()}
	}
	return out
}

// nodeRoles extracts roles from the standard node-role.kubernetes.io/<r>
// labels.
func nodeRoles(n *corev1.Node) []string {
	var roles []string
	const prefix = "node-role.kubernetes.io/"
	for k := range n.Labels {
		if strings.HasPrefix(k, prefix) {
			if r := strings.TrimPrefix(k, prefix); r != "" {
				roles = append(roles, r)
			}
		}
	}
	sort.Strings(roles)
	return roles
}
