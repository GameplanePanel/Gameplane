package nodecapture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/capture-sidecar/internal/capture"
)

func TestCgroupIdentityMatchesExactPodAndContainer(t *testing.T) {
	pod := "11111111-2222-3333-4444-555555555555"
	id := strings.Repeat("a", 64)
	for _, test := range []struct {
		name, cgroup string
		want         bool
	}{
		{"systemd", "0::/kubepods.slice/kubepods-burstable-pod" + strings.ReplaceAll(pod, "-", "_") + ".slice/cri-containerd-" + id + ".scope\n", true},
		{"cgroupfs", "0::/kubepods/burstable/pod" + pod + "/" + id + "\n", true},
		{"other-pod", "0::/kubepods/pod99999999-2222-3333-4444-555555555555/" + id + "\n", false},
		{"container-prefix", "0::/kubepods/pod" + pod + "/" + id + "a\n", false},
		{"host", "0::/system.slice/containerd.service\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := matchesCgroup(test.cgroup, pod, id); got != test.want {
				t.Fatalf("identity match=%t want %t", got, test.want)
			}
		})
	}
}

type endingSource struct{ err error }

func (s endingSource) ReadPacket(context.Context) (*capture.RawPacket, error) { return nil, s.err }
func (endingSource) Close() error                                             { return nil }

func TestRunCapturePublishesOnlyFinalizedIdentityBoundResult(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "canceled", true: "identity-changed"}[changed], func(t *testing.T) {
			dir := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			cancel()
			c := Config{ServerUID: "server", CaptureUID: "capture", CaptureID: "cap", DurationSeconds: 1, MaxSizeBytes: 4096, Filter: "tcp port 25565"}
			verify := func() error {
				if changed {
					return errors.New("replaced")
				}
				return nil
			}
			if err := runCapture(ctx, c, dir, endingSource{context.Canceled}, verify); err != nil {
				t.Fatal(err)
			}
			raw, err := os.ReadFile(filepath.Join(dir, "capture-cap.result.json"))
			if err != nil {
				t.Fatal(err)
			}
			var result Result
			if err := json.Unmarshal(raw, &result); err != nil {
				t.Fatal(err)
			}
			want := "completed"
			if changed {
				want = "failed"
			}
			if result.Status != want || result.ServerUID != "server" || result.CaptureUID != "capture" || result.BytesWritten <= 0 {
				t.Fatalf("invalid result: %+v", result)
			}
			for _, name := range []string{"capture-cap.identity.json", "capture-cap.pcapng", "capture-cap.result.json"} {
				stat, err := os.Stat(filepath.Join(dir, name))
				if err != nil || stat.Mode().Perm() != 0o640 {
					t.Fatalf("artifact %s mode: %v %v", name, stat, err)
				}
			}
			if err := runCapture(ctx, c, dir, endingSource{context.Canceled}, verify); err == nil {
				t.Fatal("reused existing capture identity")
			}
		})
	}
}

func TestWorkerRejectsUnsafeConfiguration(t *testing.T) {
	c := Config{ServerUID: "server-uid", CaptureUID: "capture-uid", CaptureID: "cap-test", PodUID: "11111111-2222-3333-4444-555555555555", ContainerID: "containerd://" + strings.Repeat("a", 64), Node: "node", Filter: "tcp port 25565", DurationSeconds: 60, MaxSizeBytes: 1024}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Config){func(c *Config) { c.ContainerID = "docker://" + strings.Repeat("a", 64) }, func(c *Config) { c.CaptureID = "../../host" }, func(c *Config) { c.PodUID = "" }, func(c *Config) { c.DurationSeconds = 3601 }, func(c *Config) { c.MaxSizeBytes = 1 << 30 }, func(c *Config) { c.Filter = "" }} {
		bad := c
		mutate(&bad)
		if bad.Validate() == nil {
			t.Fatalf("accepted unsafe config: %+v", bad)
		}
	}
}
