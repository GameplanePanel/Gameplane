package controller

import (
	"encoding/json"
	"errors"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// The API stamps this reserved annotation with a clone's inherited password
// keys in the same Create as its config. Keep the format in step with api/kube.
const protectedConfigKeysAnnotation = "gameplane.local/protected-config-keys"

func protectedConfigKeys(gs *gameplanev1alpha1.GameServer) ([]string, error) {
	raw, found := gs.Annotations[protectedConfigKeysAnnotation]
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

// Validate before materializeConfig: validation of a retyped int/bool/enum
// includes its value in errors that become public GameServer status.
func validateTemplatePasswordTransition(gs *gameplanev1alpha1.GameServer, previous, target *gameplanev1alpha1.GameTemplate) error {
	protected, err := protectedConfigKeys(gs)
	if err != nil {
		return err
	}
	if previous != nil {
		for _, field := range previous.Spec.ConfigSchema {
			if field.Type == "password" {
				protected = append(protected, field.Name)
			}
		}
	}
	passwords := make(map[string]bool)
	for _, field := range target.Spec.ConfigSchema {
		previousPassword, seen := passwords[field.Name]
		passwords[field.Name] = field.Type == "password" && (!seen || previousPassword)
	}
	for _, key := range protected {
		if gs.Spec.Config[key] != "" && !passwords[key] {
			return errors.New("template revision would expose a stored password; clear or remove the protected value before adopting this revision")
		}
	}
	return nil
}

// Release inherited classification only when the same optimistic update commits
// the target pin with an empty/absent value. A concurrent secret edit conflicts.
func pruneEmptyProtectedConfigKeys(gs *gameplanev1alpha1.GameServer) error {
	keys, err := protectedConfigKeys(gs)
	if err != nil {
		return err
	}
	var retained []string
	for _, key := range keys {
		if gs.Spec.Config[key] != "" {
			retained = append(retained, key)
		}
	}
	if len(retained) == 0 {
		delete(gs.Annotations, protectedConfigKeysAnnotation)
		return nil
	}
	data, err := json.Marshal(retained)
	if err != nil {
		return err
	}
	gs.Annotations[protectedConfigKeysAnnotation] = string(data)
	return nil
}
