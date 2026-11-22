package handlers

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Read-only pod and Kubernetes event routes. They back the MCP server and
// the dashboard's pod views. They never write to the cluster.
const (
	// serverInstanceLabel is set on every server pod by the operator.
	serverInstanceLabel = "app.kubernetes.io/instance"
	// maxKubeEvents caps the /kube-events response.
	maxKubeEvents = 100
	// maxLogTailBytes caps the bytes returned by the pod log tail.
	maxLogTailBytes = 256 << 10
	// defaultLogTailLines and maxLogTailLines bound tailLines.
	defaultLogTailLines = 200
	maxLogTailLines     = 5000
	logTruncatedNotice  = "[gameplane: log output truncated to the newest 256 KiB]\n"
)

// MountPods registers the read-only pod and event routes. controlPlaneNS is
// the namespace the operator runs in. Its pods and events are readable only
// by cluster administrators on the local cluster.
func MountPods(r chi.Router, reg *kube.Registry, controlPlaneNS string) {
	h := &podsHandler{reg: reg, controlPlaneNS: controlPlaneNS}
	r.Get("/pods", h.listPods)
	r.Get("/pods/{name}", h.getPod)
	r.Get("/kube-events", h.listKubeEvents)
	r.Get("/servers/{name}/pods", h.listServerPods)
	r.Get("/servers/{name}/logs/pod/tail", h.tailServerPodLog)
}

type podsHandler struct {
	reg            *kube.Registry
	controlPlaneNS string
}

// kubeEventList is the /kube-events response body.
type kubeEventList struct {
	APIVersion string         `json:"apiVersion"`
	Kind       string         `json:"kind"`
	Items      []corev1.Event `json:"items"`
	Truncated  bool           `json:"truncated"`
	Total      int            `json:"total"`
}

// podsNamespace resolves the namespace for /pods, /pods/{name} and
// /kube-events. The control-plane namespace is reachable only by a
// cluster administrator on the local cluster. Every other namespace goes
// through scope.Resolve.
func (h *podsHandler) podsNamespace(w http.ResponseWriter, req *http.Request) (string, bool) {
	if h.isControlPlane(req) {
		if !h.allowControlPlane(req) {
			httperr.WriteMessage(w, req, messages.New("server.forbidden", nil))
			return "", false
		}
		return h.controlPlaneNS, true
	}
	return resolveNS(w, req)
}

// isControlPlane reports whether the request names the control-plane
// namespace in its namespace query parameter.
func (h *podsHandler) isControlPlane(req *http.Request) bool {
	return h.controlPlaneNS != "" && strings.TrimSpace(req.URL.Query().Get("namespace")) == h.controlPlaneNS
}

// allowControlPlane reports whether the caller may read control-plane
// objects: an administrator ("*") on the local cluster.
func (h *podsHandler) allowControlPlane(req *http.Request) bool {
	if scope.RequestedCluster(req) != scope.DefaultCluster {
		return false
	}
	u := auth.UserFromContext(req.Context())
	return u != nil && u.Can("*", true, scope.DefaultCluster, h.controlPlaneNS)
}

func (h *podsHandler) listPods(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := h.podsNamespace(w, req)
	if !ok {
		return
	}
	list, err := k.Typed.CoreV1().Pods(ns).List(req.Context(), metav1.ListOptions{
		LabelSelector: req.URL.Query().Get("labelSelector"),
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	for i := range list.Items {
		list.Items[i].ManagedFields = nil
	}
	writeJSON(w, list)
}

func (h *podsHandler) getPod(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if !validDNSName(w, req, name) {
		return
	}
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := h.podsNamespace(w, req)
	if !ok {
		return
	}
	pod, err := k.Typed.CoreV1().Pods(ns).Get(req.Context(), name, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	pod.ManagedFields = nil
	writeJSON(w, pod)
}

func (h *podsHandler) listKubeEvents(w http.ResponseWriter, req *http.Request) {
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := h.podsNamespace(w, req)
	if !ok {
		return
	}
	q := req.URL.Query()
	list, err := k.Typed.CoreV1().Events(ns).List(req.Context(), metav1.ListOptions{
		FieldSelector: q.Get("fieldSelector"),
		LabelSelector: q.Get("labelSelector"),
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	items := list.Items
	sort.SliceStable(items, func(i, j int) bool { return eventNewer(&items[i], &items[j]) })
	total := len(items)
	truncated := total > maxKubeEvents
	if truncated {
		items = items[:maxKubeEvents]
	}
	for i := range items {
		items[i].ManagedFields = nil
	}
	if items == nil {
		items = []corev1.Event{}
	}
	writeJSON(w, kubeEventList{
		APIVersion: "v1",
		Kind:       "EventList",
		Items:      items,
		Truncated:  truncated,
		Total:      total,
	})
}

// eventNewer orders events newest first: LastTimestamp, then EventTime, then
// CreationTimestamp. Each later key breaks a tie in the one before it.
func eventNewer(a, b *corev1.Event) bool {
	if !a.LastTimestamp.Equal(&b.LastTimestamp) {
		return b.LastTimestamp.Before(&a.LastTimestamp)
	}
	if !a.EventTime.Equal(&b.EventTime) {
		return b.EventTime.Before(&a.EventTime)
	}
	return b.CreationTimestamp.Before(&a.CreationTimestamp)
}

func (h *podsHandler) listServerPods(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if !validDNSName(w, req, name) {
		return
	}
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	list, err := k.Typed.CoreV1().Pods(ns).List(req.Context(), metav1.ListOptions{
		LabelSelector: serverInstanceLabel + "=" + name,
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	for i := range list.Items {
		list.Items[i].ManagedFields = nil
	}
	writeJSON(w, list)
}

func (h *podsHandler) tailServerPodLog(w http.ResponseWriter, req *http.Request) {
	name := chi.URLParam(req, "name")
	if !validDNSName(w, req, name) {
		return
	}
	q := req.URL.Query()
	pod := strings.TrimSpace(q.Get("pod"))
	if !validDNSName(w, req, pod) {
		return
	}
	tail, ok := parseTailLines(w, req, q.Get("tailLines"))
	if !ok {
		return
	}
	previous := false
	if raw := strings.TrimSpace(q.Get("previous")); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
			return
		}
		previous = v
	}
	k, ok := resolveCluster(w, req, h.reg)
	if !ok {
		return
	}
	ns, ok := resolveNS(w, req)
	if !ok {
		return
	}
	// The pod must belong to this server. Otherwise a caller could read the
	// logs of another server's pod in the same namespace.
	obj, err := k.Typed.CoreV1().Pods(ns).Get(req.Context(), pod, metav1.GetOptions{})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if obj.Labels[serverInstanceLabel] != name {
		httperr.WriteMessage(w, req, messages.New("server.not_found", nil))
		return
	}
	body, truncated, err := readLogTail(req.Context(), k, ns, pod, corev1.PodLogOptions{
		Container: strings.TrimSpace(q.Get("container")),
		TailLines: &tail,
		Previous:  previous,
	})
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if truncated {
		_, _ = io.WriteString(w, logTruncatedNotice)
	}
	_, _ = io.Copy(w, bytes.NewReader(body))
}

// parseTailLines reads tailLines: default 200, clamped to 1..5000. A value
// that is not an integer writes 400.
func parseTailLines(w http.ResponseWriter, req *http.Request, raw string) (int64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultLogTailLines, true
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return 0, false
	}
	return min(max(n, 1), maxLogTailLines), true
}

// readLogTail streams a pod's log and keeps only the newest maxLogTailBytes.
// truncated reports whether older bytes were dropped.
func readLogTail(ctx context.Context, k *kube.Client, ns, pod string, opts corev1.PodLogOptions) ([]byte, bool, error) {
	rc, err := k.Typed.CoreV1().Pods(ns).GetLogs(pod, &opts).Stream(ctx)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rc.Close() }()

	// The buffer may grow to twice the cap before it is trimmed back, so the
	// copy cost stays amortized over the stream.
	var buf bytes.Buffer
	truncated := false
	chunk := make([]byte, 32<<10)
	for {
		n, readErr := rc.Read(chunk)
		if n > 0 {
			buf.Write(chunk[:n])
			if buf.Len() > 2*maxLogTailBytes {
				truncated = true
				keep := buf.Bytes()[buf.Len()-maxLogTailBytes:]
				out := append([]byte(nil), keep...)
				buf.Reset()
				buf.Write(out)
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, false, readErr
		}
	}
	if buf.Len() > maxLogTailBytes {
		data := buf.Bytes()
		return append([]byte(nil), data[len(data)-maxLogTailBytes:]...), true, nil
	}
	return append([]byte(nil), buf.Bytes()...), truncated, nil
}

// validDNSName writes 400 and reports false unless v is a DNS-1123 subdomain.
func validDNSName(w http.ResponseWriter, req *http.Request, v string) bool {
	if len(validation.IsDNS1123Subdomain(v)) > 0 {
		httperr.WriteMessage(w, req, messages.New("server.bad_request", nil))
		return false
	}
	return true
}
