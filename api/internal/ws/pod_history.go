package ws

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// Kubelet exposes a line tail and a lower time bound, but no reverse seek.
// Anchor pages to an exact timestamped record. A missing or ambiguous anchor
// is a discontinuity, never permission to substitute a different page.
const (
	podHistoryLines     = 100
	podHistoryBytes     = 64 << 10
	podHistoryReadBytes = 1 << 20
	podHistoryMaxTail   = 20000
)

var errPodHistoryChanged = errors.New("pod log history changed")

type podHistoryCursor struct {
	Pod       string `json:"p"`
	Container string `json:"c"`
	ID        string `json:"i"`
	Restarts  int32  `json:"r"`
	Anchor    string `json:"a,omitempty"`
	Depth     int64  `json:"d,omitempty"`
	Since     int64  `json:"s,omitempty"`
}

func (c podHistoryCursor) encode() string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}

type podHistoryPage struct {
	Text    string `json:"text"`
	Source  string `json:"source,omitempty"`
	Before  string `json:"before,omitempty"`
	Limited bool   `json:"limited,omitempty"`
}

func podRecordID(line string) string {
	digest := sha256.Sum256([]byte(line))
	return hex.EncodeToString(digest[:16])
}

// Locate the unique record including the kubelet timestamp so repeated game
// messages remain distinct. An ambiguous marker cannot be replayed safely.
func podBoundary(rows []string, anchor string) (int, error) {
	if anchor == "" {
		return len(rows), nil
	}
	at := -1
	for i, row := range rows {
		if podRecordID(row) != anchor {
			continue
		}
		if at >= 0 {
			return 0, errPodHistoryChanged
		}
		at = i
	}
	if at < 0 {
		return 0, errPodHistoryChanged
	}
	return at, nil
}

func podPageRows(rows []string, end, limit int) ([]string, int, bool) {
	var bytesUsed int
	start := end
	for start > 0 && end-start < limit {
		_, msg, _ := strings.Cut(rows[start-1], " ")
		if bytesUsed+len(msg)+1 > podHistoryBytes {
			break
		}
		bytesUsed += len(msg) + 1
		start--
	}
	limited := start == end && end > 0
	if limited {
		start--
	}
	lines := make([]string, 0, end-start)
	for _, row := range rows[start:end] {
		_, msg, _ := strings.Cut(row, " ")
		if limited {
			msg = strings.ToValidUTF8(msg[:min(len(msg), podHistoryBytes-8)], "") + "…"
		}
		lines = append(lines, strings.ToValidUTF8(msg, "?"))
	}
	return lines, start, limited
}

func historyContainers(pod *corev1.Pod) []corev1.ContainerStatus {
	states := make(map[string]corev1.ContainerStatus)
	for _, s := range pod.Status.InitContainerStatuses {
		states[s.Name] = s
	}
	for _, s := range pod.Status.ContainerStatuses {
		states[s.Name] = s
	}
	var ordered []corev1.ContainerStatus
	appendStarted := func(name string) {
		s := states[name]
		if s.State.Running != nil || s.State.Terminated != nil {
			ordered = append(ordered, s)
		}
	}
	for _, c := range pod.Spec.InitContainers {
		appendStarted(c.Name)
	}
	appendStarted(gameContainer)
	return ordered
}

func historyIdentity(pod *corev1.Pod, s corev1.ContainerStatus) podHistoryCursor {
	return podHistoryCursor{Pod: string(pod.UID), Container: s.Name, ID: s.ContainerID, Restarts: s.RestartCount}
}

func sameHistoryContainer(c podHistoryCursor, pod *corev1.Pod, s corev1.ContainerStatus) bool {
	return c.Pod == string(pod.UID) && c.Container == s.Name && c.ID == s.ContainerID && c.Restarts == s.RestartCount
}

func mountPodHistory(r chi.Router, reg *kube.Registry) {
	r.Get("/servers/{name}/logs/pod/history", func(w http.ResponseWriter, req *http.Request) {
		k, ok := streamClient(w, req, reg)
		if !ok {
			return
		}
		ns, err := scope.Resolve(req)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		name := chi.URLParam(req, "name")
		if len(validation.IsDNS1123Subdomain(name)) != 0 {
			http.Error(w, "invalid server name", http.StatusBadRequest)
			return
		}
		var cursor podHistoryCursor
		raw := req.URL.Query().Get("before")
		if raw != "" {
			if len(raw) > 2048 {
				http.Error(w, "invalid history cursor", http.StatusBadRequest)
				return
			}
			data, err := base64.RawURLEncoding.DecodeString(raw)
			if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Pod == "" || cursor.Container == "" || cursor.Depth < 0 || cursor.Depth > podHistoryMaxTail {
				http.Error(w, "invalid history cursor", http.StatusBadRequest)
				return
			}
		}
		pod, err := serverPod(req.Context(), k, ns, name)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		h := podLogProxy{k: k, podUID: pod.UID}
		page, err := h.historyPage(req.Context(), ns, pod, cursor)
		if errors.Is(err, errPodHistoryChanged) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(page)
	})
}

func (h *podLogProxy) historyPage(ctx context.Context, ns string, pod *corev1.Pod, cursor podHistoryCursor) (podHistoryPage, error) {
	steps := historyContainers(pod)
	index := len(steps) - 1
	if cursor.Pod != "" {
		for index >= 0 && !sameHistoryContainer(cursor, pod, steps[index]) {
			index--
		}
		if index < 0 {
			return podHistoryPage{}, errPodHistoryChanged
		}
	}
	if index < 0 {
		return podHistoryPage{}, nil
	}
	state := steps[index]
	identity := historyIdentity(pod, state)
	// Leave room for writes between requests. The next page still starts at
	// the exact boundary even if records were appended while it was in flight.
	tail := min(int64(podHistoryMaxTail), cursor.Depth+podHistoryLines+64)
	opts := &corev1.PodLogOptions{Container: state.Name, Timestamps: true, TailLines: &tail}
	rows, capped, err := h.historyWindow(ctx, ns, pod.Name, identity, opts)
	if err != nil {
		return podHistoryPage{}, err
	}
	end, boundaryErr := podBoundary(rows, cursor.Anchor)
	// A burst can move the record outside the line tail. Read again from a
	// timestamp captured in the original page. Appends after the anchor cannot
	// move this lower bound. Both reads have independent hard byte caps.
	usedSince := false
	if cursor.Anchor != "" && cursor.Since > 0 && boundaryErr != nil {
		since := metav1.NewTime(time.Unix(cursor.Since, 0).UTC())
		opts.TailLines = nil
		opts.SinceTime = &since
		rows, capped, err = h.historyWindow(ctx, ns, pod.Name, identity, opts)
		if err != nil {
			return podHistoryPage{}, err
		}
		end, boundaryErr = podBoundary(rows, cursor.Anchor)
		usedSince = true
	}
	// A same-name recreation or a container restart during the read must
	// never return bytes as belonging to the previously authorized instance.
	if err := h.checkHistoryContainer(ctx, ns, pod.Name, identity); err != nil {
		return podHistoryPage{}, err
	}

	page := podHistoryPage{Source: state.Name}
	if index > 0 {
		page.Before = historyIdentity(pod, steps[index-1]).encode()
	}
	if boundaryErr != nil {
		return podHistoryPage{}, boundaryErr
	}
	if capped && cursor.Anchor == "" {
		// The kubelet byte limit cuts the end, so the partial response cannot be
		// presented as the newest complete page. Earlier containers still work.
		page.Limited = true
		return page, nil
	}
	lines, start, clipped := podPageRows(rows, end, podHistoryLines)
	page.Limited = clipped
	if len(lines) == 0 {
		if usedSince {
			page.Limited = true
		}
		return page, nil
	}
	page.Text = strings.Join(lines, "\n") + "\n"
	if start > 0 || (!usedSince && int64(len(rows)) == tail && tail < podHistoryMaxTail) {
		identity.Anchor = podRecordID(rows[start])
		identity.Depth = min(int64(podHistoryMaxTail), max(cursor.Depth, int64(len(rows)-start)))
		stamp, _, _ := strings.Cut(rows[0], " ")
		if t, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
			identity.Since = t.Add(-time.Second).Unix()
		}
		page.Before = identity.encode()
	} else if usedSince || int64(len(rows)) == tail {
		page.Limited = true
	}
	return page, nil
}

func (h *podLogProxy) checkHistoryContainer(ctx context.Context, ns, name string, identity podHistoryCursor) error {
	pod, err := h.readPod(ctx, ns, name)
	if apierrors.IsNotFound(err) {
		return errPodHistoryChanged
	}
	if err != nil {
		return err
	}
	for _, state := range historyContainers(pod) {
		if sameHistoryContainer(identity, pod, state) {
			return nil
		}
	}
	return errPodHistoryChanged
}

// Revalidate the container immediately before every log subrequest. Keep the
// local reader limit even when a proxy or a fake kubelet ignores LimitBytes.
func (h *podLogProxy) historyWindow(ctx context.Context, ns, name string, identity podHistoryCursor, opts *corev1.PodLogOptions) ([]string, bool, error) {
	if err := h.checkHistoryContainer(ctx, ns, name, identity); err != nil {
		return nil, false, err
	}
	nbytes := int64(podHistoryReadBytes + 1)
	opts.LimitBytes = &nbytes
	stream, err := h.k.Typed.CoreV1().Pods(ns).GetLogs(name, opts).Stream(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("open container log: %w", err)
	}
	defer func() { _ = stream.Close() }()
	data, err := io.ReadAll(io.LimitReader(stream, nbytes))
	if err != nil {
		return nil, false, fmt.Errorf("read container log: %w", err)
	}
	capped := len(data) > podHistoryReadBytes
	if capped {
		cut := bytes.LastIndexByte(data[:podHistoryReadBytes], '\n')
		if cut < 0 {
			return nil, true, nil
		}
		data = data[:cut+1]
	}
	if len(data) == 0 {
		return nil, capped, nil
	}
	return strings.Split(string(bytes.TrimSuffix(data, []byte{'\n'})), "\n"), capped, nil
}
