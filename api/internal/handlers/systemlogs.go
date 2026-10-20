package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// MountSystemLogs registers the system logs API handlers.
func MountSystemLogs(r chi.Router, k *kube.Client, namespace string) {
	r.Get("/admin/system-logs/{component}", func(w http.ResponseWriter, req *http.Request) {
		if k == nil || k.Typed == nil {
			httperr.WriteCode(w, req, http.StatusNotImplemented, errors.New("standalone panel logs are available from the container runtime"))
			return
		}
		component := chi.URLParam(req, "component")

		// Component must be exactly "api" or "operator"
		var labelValue string
		switch component {
		case "api":
			labelValue = "gameplane-api"
		case "operator":
			labelValue = "gameplane-operator"
		default:
			httperr.WriteCode(w, req, http.StatusBadRequest,
				errors.New("component must be 'api' or 'operator'"))
			return
		}

		// Parse query parameters
		tailLines := 500
		if tl := req.URL.Query().Get("tailLines"); tl != "" {
			if n, err := strconv.Atoi(tl); err == nil {
				tailLines = n
			}
		}
		// Clamp to [1, 5000]
		if tailLines < 1 {
			tailLines = 1
		} else if tailLines > 5000 {
			tailLines = 5000
		}

		follow := req.URL.Query().Get("follow") == "true"
		podParam := req.URL.Query().Get("pod")

		// List pods with the label selector
		selector := fmt.Sprintf("app.kubernetes.io/name=%s", labelValue)
		pods, err := k.Typed.CoreV1().Pods(namespace).List(req.Context(), metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			httperr.Write(w, req, fmt.Errorf("list pods: %w", err))
			return
		}

		if len(pods.Items) == 0 {
			httperr.WriteCode(w, req, http.StatusNotFound,
				fmt.Errorf("no pods found for component %s", component))
			return
		}

		// Select which pod to tail
		var targetPod *corev1.Pod
		if podParam != "" {
			// Validate pod param is in the list
			for i := range pods.Items {
				if pods.Items[i].Name == podParam {
					targetPod = &pods.Items[i]
					break
				}
			}
			if targetPod == nil {
				httperr.WriteCode(w, req, http.StatusNotFound,
					fmt.Errorf("pod %s not found in component %s", podParam, component))
				return
			}
		} else {
			// Prefer newest Running pod by CreationTimestamp; fall back to newest overall
			sort.Slice(pods.Items, func(i, j int) bool {
				return pods.Items[i].CreationTimestamp.After(pods.Items[j].CreationTimestamp.Time)
			})

			for i := range pods.Items {
				if pods.Items[i].Status.Phase == corev1.PodRunning {
					targetPod = &pods.Items[i]
					break
				}
			}
			if targetPod == nil {
				// Fall back to newest overall
				targetPod = &pods.Items[0]
			}
		}

		// Log stream start
		slog.Debug("system logs stream start",
			"component", component,
			"pod", targetPod.Name,
			"follow", follow)

		// Set response headers
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("X-Gameplane-Pod", targetPod.Name)

		// Set up context for streaming
		streamCtx := req.Context()
		if follow {
			// Wrap with 50s timeout to avoid global 60s middleware timeout killing mid-write
			var cancel context.CancelFunc
			streamCtx, cancel = context.WithTimeout(streamCtx, 50*time.Second)
			defer cancel()
		}

		// Get logs
		tail := int64(tailLines)
		logStream, err := k.Typed.CoreV1().Pods(namespace).GetLogs(targetPod.Name, &corev1.PodLogOptions{
			TailLines:  &tail,
			Follow:     follow,
			Timestamps: true,
		}).Stream(streamCtx)
		if err != nil {
			httperr.Write(w, req, fmt.Errorf("open log stream: %w", err))
			return
		}
		defer func() { _ = logStream.Close() }()

		// Stream logs to client with flushing
		flusher, ok := w.(http.Flusher)
		if !ok {
			httperr.Write(w, req, fmt.Errorf("response writer does not support flushing"))
			return
		}

		buf := make([]byte, 32*1024) // 32 KiB chunks
		for {
			n, err := logStream.Read(buf)
			if n > 0 {
				if _, writeErr := w.Write(buf[:n]); writeErr != nil {
					slog.Warn("system logs write failed",
						"component", component,
						"pod", targetPod.Name,
						"err", writeErr)
					return
				}
				flusher.Flush()
			}
			if err != nil {
				// Treat context deadline/cancellation as normal termination
				// (the follow window ending, or the client going away).
				if errors.Is(err, io.EOF) ||
					errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
					return
				}
				slog.Warn("system logs stream error",
					"component", component,
					"pod", targetPod.Name,
					"err", err)
				return
			}
		}
	})
}
