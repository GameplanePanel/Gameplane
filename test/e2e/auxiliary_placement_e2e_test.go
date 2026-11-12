//go:build e2e

package e2e

import (
	"os/exec"
	"testing"
)

func TestAuxiliaryWorkersAndWakeHandoff(t *testing.T) {
	path, err := findRepoRel("test/e2e/auxiliary-placement.sh")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "bash", path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("auxiliary worker drill: %v\n%s", err, out)
	} else {
		t.Logf("%s", out)
	}
}
