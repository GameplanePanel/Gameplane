//go:build e2e

package e2e

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// Exercise rollout, storage and failure containment with real operator, kubelet
// and Restic workers. The Minecraft bot independently qualifies the game join.
func TestGameServer_RestoredRolloutRehearsal(t *testing.T) {
	t.Parallel()
	ensureResticRepo(t)
	name := fmt.Sprintf("rollout-%x", time.Now().UnixNano())
	join := filepath.Join(t.TempDir(), "join-once")
	build := exec.CommandContext(t.Context(), "go", "build", "-o", join, "./cmd/join-once")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build protocol client: %v\n%s", err, out)
	}
	cmd := exec.CommandContext(t.Context(), "python3", "rollout_rehearsal.py", envInstance.Context, name, join)
	out, err := cmd.CombinedOutput()
	t.Logf("restored rollout receipts:\n%s", out)
	if err != nil {
		t.Fatalf("restored rollout qualification: %v", err)
	}
}
