package notify

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SinkSecretLabel must be set to "true" on a Secret before the notifier
// will read it. The label is the guard that stops a config:manage user from
// aiming a sink's configRef at an arbitrary control-plane Secret — the same
// contract backup destinations use in the games namespace.
const SinkSecretLabel = "gameplane.local/notification-sink"

// Sink mirrors the notifSink schema of the "notifications" config section
// (api/internal/handlers/config.go); the validator there is what bounds
// these fields, so this side just decodes.
type Sink struct {
	Name      string   `json:"name"`
	Kind      string   `json:"kind"` // "discord" | "slack" | "smtp" | "webhook" | "ntfy"
	Enabled   bool     `json:"enabled"`
	ConfigRef string   `json:"configRef"`
	Events    []string `json:"events"`
}

// loadSinks reads the persisted notifications config. A missing row means
// no sinks were ever configured — not an error.
func (n *Notifier) loadSinks(ctx context.Context) ([]Sink, error) {
	raw, ok, err := n.store.ConfigValue(ctx, "notifications")
	if err != nil {
		return nil, fmt.Errorf("read notifications config: %w", err)
	}
	if !ok {
		return nil, nil
	}
	var c struct {
		Sinks []Sink `json:"sinks"`
	}
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		return nil, fmt.Errorf("parse notifications config: %w", err)
	}
	return c.Sinks, nil
}

// sinkMatches reports whether an event of type t should go to s: disabled
// sinks never match, an explicit events list is authoritative, and an empty
// list falls back to the DefaultOn set.
func sinkMatches(s Sink, t EventType) bool {
	if !s.Enabled {
		return false
	}
	if len(s.Events) == 0 {
		return DefaultOn(t)
	}
	for _, ev := range s.Events {
		if ev == string(t) {
			return true
		}
	}
	return false
}

// sinkSecret fetches the sink's credential Secret from the control-plane
// namespace, refusing any Secret not labelled SinkSecretLabel=true.
func (n *Notifier) sinkSecret(ctx context.Context, name string) (map[string][]byte, error) {
	sec, err := n.k.Secrets(n.controlNS).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("get sink secret %s/%s: %w", n.controlNS, name, err)
	}
	if sec.Labels[SinkSecretLabel] != "true" {
		return nil, fmt.Errorf("secret %s/%s is not labelled %s=true", n.controlNS, name, SinkSecretLabel)
	}
	return sec.Data, nil
}
