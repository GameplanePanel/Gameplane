package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

func TestMountSystemLogs(t *testing.T) {
	fakeClientset := fake.NewSimpleClientset()
	k8sClient := &kube.Client{
		Typed: fakeClientset,
	}

	namespace := "gameplane-system"

	// Create test pods
	runningAPI := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gameplane-api-0",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "gameplane-api",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	runningOperator := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gameplane-operator-0",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "gameplane-operator",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-10 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	pendingAPI := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gameplane-api-1",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "gameplane-api",
			},
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	_, err := fakeClientset.CoreV1().Pods(namespace).Create(context.Background(), runningAPI, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create running api pod: %v", err)
	}

	_, err = fakeClientset.CoreV1().Pods(namespace).Create(context.Background(), runningOperator, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create running operator pod: %v", err)
	}

	_, err = fakeClientset.CoreV1().Pods(namespace).Create(context.Background(), pendingAPI, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pending api pod: %v", err)
	}

	tests := []struct {
		name           string
		component      string
		pod            string
		tailLines      string
		follow         string
		expectedStatus int
		expectedHeader string // X-Gameplane-Pod
		shouldFail     bool
	}{
		{
			name:           "api component running pod",
			component:      "api",
			expectedStatus: http.StatusOK,
			expectedHeader: "gameplane-api-0",
			shouldFail:     false,
		},
		{
			name:           "operator component running pod",
			component:      "operator",
			expectedStatus: http.StatusOK,
			expectedHeader: "gameplane-operator-0",
			shouldFail:     false,
		},
		{
			name:           "specific pod request",
			component:      "api",
			pod:            "gameplane-api-0",
			expectedStatus: http.StatusOK,
			expectedHeader: "gameplane-api-0",
			shouldFail:     false,
		},
		{
			name:           "invalid component",
			component:      "invalid",
			expectedStatus: http.StatusBadRequest,
			shouldFail:     true,
		},
		{
			name:           "pod not in set",
			component:      "api",
			pod:            "nonexistent",
			expectedStatus: http.StatusNotFound,
			shouldFail:     true,
		},
		{
			name:           "tailLines clamped high",
			component:      "api",
			tailLines:      "999999",
			expectedStatus: http.StatusOK,
			expectedHeader: "gameplane-api-0",
			shouldFail:     false,
		},
		{
			name:           "tailLines clamped low",
			component:      "api",
			tailLines:      "-5",
			expectedStatus: http.StatusOK,
			expectedHeader: "gameplane-api-0",
			shouldFail:     false,
		},
		{
			name:           "follow mode",
			component:      "api",
			follow:         "true",
			expectedStatus: http.StatusOK,
			expectedHeader: "gameplane-api-0",
			shouldFail:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := chi.NewRouter()
			MountSystemLogs(r, k8sClient, namespace)

			// Build query params
			query := ""
			if tt.pod != "" {
				query += "?pod=" + tt.pod
			}
			if tt.tailLines != "" {
				sep := "?"
				if query != "" {
					sep = "&"
				}
				query += sep + "tailLines=" + tt.tailLines
			}
			if tt.follow != "" {
				sep := "?"
				if query != "" {
					sep = "&"
				}
				query += sep + "follow=" + tt.follow
			}

			req := httptest.NewRequestWithContext(t.Context(), "GET", "/admin/system-logs/"+tt.component+query, nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			if !tt.shouldFail {
				if header := w.Header().Get("X-Gameplane-Pod"); header != tt.expectedHeader {
					t.Errorf("expected X-Gameplane-Pod header %s, got %s", tt.expectedHeader, header)
				}
				if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
					t.Errorf("expected Content-Type text/plain; charset=utf-8, got %s", ct)
				}
			}
		})
	}
}

func TestMountSystemLogsNoPods(t *testing.T) {
	fakeClientset := fake.NewSimpleClientset()
	k8sClient := &kube.Client{
		Typed: fakeClientset,
	}

	namespace := "gameplane-system"

	r := chi.NewRouter()
	MountSystemLogs(r, k8sClient, namespace)

	req := httptest.NewRequestWithContext(t.Context(), "GET", "/admin/system-logs/api", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("expected status 404 for no pods, got %d", w.Code)
	}
}

func TestMountSystemLogsPreferRunning(t *testing.T) {
	fakeClientset := fake.NewSimpleClientset()
	k8sClient := &kube.Client{
		Typed: fakeClientset,
	}

	namespace := "gameplane-system"

	// Create pending pod (newest by timestamp)
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gameplane-api-pending",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "gameplane-api",
			},
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	// Create running pod (older by timestamp)
	running := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gameplane-api-running",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "gameplane-api",
			},
			CreationTimestamp: metav1.NewTime(time.Now().Add(-5 * time.Minute)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	_, err := fakeClientset.CoreV1().Pods(namespace).Create(context.Background(), pending, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pending pod: %v", err)
	}

	_, err = fakeClientset.CoreV1().Pods(namespace).Create(context.Background(), running, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create running pod: %v", err)
	}

	r := chi.NewRouter()
	MountSystemLogs(r, k8sClient, namespace)

	req := httptest.NewRequestWithContext(t.Context(), "GET", "/admin/system-logs/api", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if header := w.Header().Get("X-Gameplane-Pod"); header != "gameplane-api-running" {
		t.Errorf("expected to select running pod gameplane-api-running, got %s", header)
	}
}

func TestMountSystemLogsFallbackNoRunning(t *testing.T) {
	fakeClientset := fake.NewSimpleClientset()
	k8sClient := &kube.Client{
		Typed: fakeClientset,
	}

	namespace := "gameplane-system"

	// The only pod is Pending — the handler should fall back to the
	// newest pod overall when no Running pod exists.
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gameplane-api-pending",
			Namespace: namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name": "gameplane-api",
			},
			CreationTimestamp: metav1.NewTime(time.Now()),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}

	_, err := fakeClientset.CoreV1().Pods(namespace).Create(context.Background(), pending, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("create pending pod: %v", err)
	}

	r := chi.NewRouter()
	MountSystemLogs(r, k8sClient, namespace)

	req := httptest.NewRequestWithContext(t.Context(), "GET", "/admin/system-logs/api", nil)
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	if header := w.Header().Get("X-Gameplane-Pod"); header != "gameplane-api-pending" {
		t.Errorf("expected fallback to pending pod gameplane-api-pending, got %s", header)
	}
}
