package kube

import (
	"encoding/json"
	"errors"
)

// ProtectedConfigKeysAnnotation records inherited password classification for
// clones before template admission. Generic API mutations reserve this domain.
const ProtectedConfigKeysAnnotation = "gameplane.local/protected-config-keys"

// ProtectedConfigKeys reads the names only. Invalid metadata must fail closed.
// Keep the wire format in step with operator/controller/template_passwords.go.
func ProtectedConfigKeys(annotations map[string]string) ([]string, error) {
	raw, found := annotations[ProtectedConfigKeysAnnotation]
	if !found {
		return nil, nil
	}
	var keys []string
	if err := json.Unmarshal([]byte(raw), &keys); err != nil || keys == nil {
		return nil, errors.New("invalid protected config metadata")
	}
	for _, key := range keys {
		if key == "" {
			return nil, errors.New("invalid protected config metadata")
		}
	}
	return keys, nil
}
