package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// maxTerminationBytes is the Kubernetes limit for a termination message.
const maxTerminationBytes = 4096

// WriteTermination JSON-encodes v and writes it to path, the termination
// message file the kubelet reports back on the pod status.
func WriteTermination(path string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("marshal termination message: %w", err)
	}
	if len(b) > maxTerminationBytes {
		return errors.New("termination message too large")
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write termination message: %w", err)
	}
	return nil
}
