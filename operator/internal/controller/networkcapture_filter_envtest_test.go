//go:build envtest

package controller

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// TestNetworkCapture_NoFilterUsesTemplateAdvertisedPorts checks that a
// capture created without a filter starts on the sidecar with a filter
// restricted to the template's advertised ports and protocols, and
// reaches Running.
func TestNetworkCapture_NoFilterUsesTemplateAdvertisedPorts(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	var mu sync.Mutex
	var sentFilter *string
	started := false
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, filter *string, _, _ int64) error {
			mu.Lock()
			defer mu.Unlock()
			if filter != nil {
				f := *filter
				sentFilter = &f
			}
			started = true
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			mu.Lock()
			defer mu.Unlock()
			if !started {
				return "", 0, 0, "", fmt.Errorf("capture not found")
			}
			return "running", 0, 0, "capture running", nil
		},
	}
	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	tmpl := buildGameTemplate(uniqueName("capfilter"))
	tmpl.Spec.Ports = append(tmpl.Spec.Ports,
		gameplanev1alpha1.GamePort{Name: "voice", ContainerPort: 24454, Protocol: corev1.ProtocolUDP, Advertise: true},
		gameplanev1alpha1.GamePort{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	)
	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildCaptureGameServer(ns, "cap-default-srv")
	gs.Spec.TemplateRef.Name = tmpl.Name
	if err := k8sClient.Create(ctx, gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(ctx, buildCapturePod(ns, "cap-default-srv")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	nc := buildNetworkCapture(ns, "cap-default-001", "cap-default-srv", "")
	nc.Spec.Filter = nil
	if err := k8sClient.Create(ctx, nc); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getNetworkCapture(t, ns, "cap-default-001")
		if got.Status.Phase != gameplanev1alpha1.CapturePhaseRunning {
			return false, fmt.Sprintf("phase = %s (%s), want Running", got.Status.Phase, got.Status.Message)
		}
		return true, ""
	})

	mu.Lock()
	defer mu.Unlock()
	if sentFilter == nil {
		t.Fatal("sidecar was started with no filter")
	}
	if *sentFilter != "tcp port 25565 or udp port 24454" {
		t.Fatalf("sidecar filter = %q, want %q", *sentFilter, "tcp port 25565 or udp port 24454")
	}
}

// TestNetworkCapture_NoFilterWithoutAdvertisedPortsFails checks that a
// capture with no filter on a template that advertises no port fails with
// a message and never starts on the sidecar.
func TestNetworkCapture_NoFilterWithoutAdvertisedPortsFails(t *testing.T) {
	ns := newNamespace(t)
	ctx := context.Background()

	var mu sync.Mutex
	startCalls := 0
	stub := &StubSidecarClient{
		startCaptureFn: func(_ context.Context, _, _, _ string, _ *string, _, _ int64) error {
			mu.Lock()
			startCalls++
			mu.Unlock()
			return nil
		},
		getCaptureStatusFn: func(_ context.Context, _, _, _ string) (string, int64, int64, string, error) {
			return "", 0, 0, "", fmt.Errorf("capture not found")
		},
	}
	startMgr(t, ns, withNetworkCaptureReconciler(stub))

	tmpl := buildGameTemplate(uniqueName("capquiet"))
	tmpl.Spec.Ports = []gameplanev1alpha1.GamePort{
		{Name: "rcon", ContainerPort: 25575, Protocol: corev1.ProtocolTCP, Advertise: false},
	}
	if err := k8sClient.Create(ctx, tmpl); err != nil {
		t.Fatalf("create template: %v", err)
	}
	deleteCleanup(t, tmpl)

	gs := buildCaptureGameServer(ns, "cap-quiet-srv")
	gs.Spec.TemplateRef.Name = tmpl.Name
	if err := k8sClient.Create(ctx, gs); err != nil {
		t.Fatalf("create gameserver: %v", err)
	}
	if err := k8sClient.Create(ctx, buildCapturePod(ns, "cap-quiet-srv")); err != nil {
		t.Fatalf("create pod: %v", err)
	}
	nc := buildNetworkCapture(ns, "cap-quiet-001", "cap-quiet-srv", "")
	nc.Spec.Filter = nil
	if err := k8sClient.Create(ctx, nc); err != nil {
		t.Fatalf("create network capture: %v", err)
	}

	eventually(t, func() (bool, string) {
		got := getNetworkCapture(t, ns, "cap-quiet-001")
		if got.Status.Phase != gameplanev1alpha1.CapturePhaseFailed {
			return false, fmt.Sprintf("phase = %s, want Failed", got.Status.Phase)
		}
		if !strings.Contains(got.Status.Message, "advertises no ports") {
			return false, "message = " + got.Status.Message
		}
		return true, ""
	})

	mu.Lock()
	defer mu.Unlock()
	if startCalls != 0 {
		t.Fatalf("sidecar StartCapture called %d times, want 0", startCalls)
	}
}
