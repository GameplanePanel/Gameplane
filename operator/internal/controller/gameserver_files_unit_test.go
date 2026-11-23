package controller

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func filesTestServer(stamp string) *gameplanev1alpha1.GameServer {
	gs := &gameplanev1alpha1.GameServer{ObjectMeta: metav1.ObjectMeta{Name: "gs", Namespace: "ns"}}
	if stamp != "" {
		gs.Annotations = map[string]string{FilesRequestedAtAnnotation: stamp}
	}
	return gs
}

func TestFilesRequestFresh(t *testing.T) {
	now := time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)
	const timeout = 10 * time.Minute
	stamp := func(d time.Duration) string { return now.Add(d).Format(time.RFC3339) }

	cases := []struct {
		name      string
		stamp     string
		wantFresh bool
		wantLeft  time.Duration
	}{
		{"absent", "", false, 0},
		{"unparseable", "yesterday", false, 0},
		{"non-RFC3339", "2026-11-23 12:00:00", false, 0},
		{"just requested", stamp(0), true, timeout},
		{"half elapsed", stamp(-5 * time.Minute), true, 5 * time.Minute},
		{"one second before expiry", stamp(-timeout + time.Second), true, time.Second},
		{"exactly at timeout", stamp(-timeout), false, 0},
		{"expired", stamp(-time.Hour), false, 0},
		{"slightly future", stamp(time.Second), true, timeout},
		{"future beyond timeout", stamp(timeout + time.Minute), false, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fresh, left := filesRequestFresh(filesTestServer(tc.stamp), now, timeout)
			if fresh != tc.wantFresh {
				t.Fatalf("fresh = %v, want %v", fresh, tc.wantFresh)
			}
			if left != tc.wantLeft {
				t.Fatalf("remaining = %s, want %s", left, tc.wantLeft)
			}
		})
	}
}

func TestWithAgentTransferFlags(t *testing.T) {
	base := corev1.Container{Name: "agent", Args: []string{"--tls-cert=/x"}}

	unchanged := withAgentTransferFlags(base, 0, false)
	if len(unchanged.Args) != 1 {
		t.Fatalf("zero limit and not files-only added args: %v", unchanged.Args)
	}

	got := withAgentTransferFlags(base, 1<<30, true)
	want := []string{"--tls-cert=/x", "--max-upload-bytes=1073741824", "--files-only"}
	if len(got.Args) != len(want) {
		t.Fatalf("args = %v, want %v", got.Args, want)
	}
	for i := range want {
		if got.Args[i] != want[i] {
			t.Fatalf("args = %v, want %v", got.Args, want)
		}
	}
}

func TestFilesTimeoutDefaults(t *testing.T) {
	if got := (&GameServerReconciler{}).filesTimeout(); got != DefaultFilesIdleTimeout {
		t.Fatalf("zero timeout = %s, want default %s", got, DefaultFilesIdleTimeout)
	}
	if got := (&GameServerReconciler{FilesIdleTimeout: 3 * time.Second}).filesTimeout(); got != 3*time.Second {
		t.Fatalf("configured timeout = %s, want 3s", got)
	}
}
