package handlers

import (
	"context"
	"sync"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// configRedactedMarker replaces the value of every password-type
// GameServer spec.config field in API responses. A PUT that sends the marker
// back keeps the stored value (the dashboard returns draft.spec wholesale).
const configRedactedMarker = "__gameplane_redacted__"

// configRules says which spec.config keys are secret for one GameTemplate.
// failClosed means the template could not be read: every key is then treated
// as secret and optionality is unknown.
type configRules struct {
	failClosed bool
	password   map[string]bool
	required   map[string]bool
}

func (r configRules) redacts(key string) bool { return r.failClosed || r.password[key] }

// rulesFromTemplate extracts the password-type fields of tmpl.spec.configSchema.
// A malformed schema fails closed.
func rulesFromTemplate(tmpl *unstructured.Unstructured) configRules {
	rules := configRules{password: map[string]bool{}, required: map[string]bool{}}
	fields, _, err := unstructured.NestedSlice(tmpl.Object, "spec", "configSchema")
	if err != nil {
		return configRules{failClosed: true}
	}
	for _, f := range fields {
		m, ok := f.(map[string]any)
		if !ok {
			continue
		}
		name, _ := m["name"].(string)
		if name == "" || m["type"] != "password" {
			continue
		}
		rules.password[name] = true
		if req, _ := m["required"].(bool); req {
			rules.required[name] = true
		}
	}
	return rules
}

// configRuleCache memoises GameTemplate lookups for ONE request or ONE SSE
// event. It must never outlive that unit: rules read for an earlier response
// could predate a template edit that turned a field into a password. It is
// safe for concurrent use; failed lookups are never cached.
type configRuleCache struct {
	k       *kube.Client
	mu      sync.Mutex
	entries map[string]configRules
}

func newConfigRuleCache(k *kube.Client) *configRuleCache {
	return &configRuleCache{k: k, entries: map[string]configRules{}}
}

// rulesFor returns the rules for the named template, failing closed when the
// template cannot be read.
func (c *configRuleCache) rulesFor(ctx context.Context, templateName string) configRules {
	if templateName == "" || c == nil || c.k == nil || c.k.Dynamic == nil {
		return configRules{failClosed: true}
	}
	c.mu.Lock()
	cached, ok := c.entries[templateName]
	c.mu.Unlock()
	if ok {
		return cached
	}
	tmpl, err := c.k.Dynamic.Resource(kube.GVRs["templates"]).Get(ctx, templateName, metav1.GetOptions{})
	if err != nil {
		return configRules{failClosed: true}
	}
	rules := rulesFromTemplate(tmpl)
	c.mu.Lock()
	c.entries[templateName] = rules
	c.mu.Unlock()
	return rules
}

// redact replaces password-type spec.config values of gs, in place, with
// configRedactedMarker. Empty values are left empty. Callers that do not own gs
// (watch events, objects returned by a client) must pass a copy.
func (c *configRuleCache) redact(ctx context.Context, gs *unstructured.Unstructured) {
	// kubectl apply stores a complete older manifest here, including passwords
	// that may no longer exist in the current config or template schema.
	unstructured.RemoveNestedField(gs.Object, "metadata", "annotations", "kubectl.kubernetes.io/last-applied-configuration")
	raw, found, _ := unstructured.NestedFieldNoCopy(gs.Object, "spec", "config")
	if !found || raw == nil {
		return
	}
	cfg, ok := raw.(map[string]any)
	if !ok {
		unstructured.RemoveNestedField(gs.Object, "spec", "config")
		return
	}
	if len(cfg) == 0 {
		return
	}
	name, _, _ := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
	rules := c.rulesFor(ctx, name)
	for key, val := range cfg {
		if !rules.redacts(key) {
			continue
		}
		if s, isStr := val.(string); isStr && s == "" {
			continue
		}
		cfg[key] = configRedactedMarker
	}
}

// redactList redacts every item of list in place.
func (c *configRuleCache) redactList(ctx context.Context, list *unstructured.UnstructuredList) {
	for i := range list.Items {
		c.redact(ctx, &list.Items[i])
	}
}

// restoreRedactedConfig rewrites desired.spec.config before a PUT so redacted
// values the client sent back do not overwrite stored passwords. live is the
// stored object (nil when it does not exist). The rules are documented in
// api/specs.md ("GameServer config password redaction").
func restoreRedactedConfig(ctx context.Context, c *configRuleCache, desired, live *unstructured.Unstructured) {
	var incoming, stored map[string]any
	rawIn, foundIn, _ := unstructured.NestedFieldNoCopy(desired.Object, "spec", "config")
	if foundIn {
		incoming, _ = rawIn.(map[string]any)
	}
	rules := configRules{failClosed: true}
	if live != nil {
		rawStored, _, _ := unstructured.NestedFieldNoCopy(live.Object, "spec", "config")
		stored, _ = rawStored.(map[string]any)
	}
	if len(incoming) == 0 && len(stored) == 0 {
		return
	}
	if live != nil {
		name, _, _ := unstructured.NestedString(live.Object, "spec", "templateRef", "name")
		rules = c.rulesFor(ctx, name)
	}
	out := make(map[string]any, len(incoming))
	for key, val := range incoming {
		s, isStr := val.(string)
		// The marker is API-emitted (a fail-closed read marks every key), so it
		// is restored for ANY key, whatever the current template says, and
		// whatever type the stored value has. With nothing stored the marker is
		// dropped rather than persisted.
		if isStr && s == configRedactedMarker {
			if storedVal, hasStored := stored[key]; hasStored {
				out[key] = storedVal
			}
			continue
		}
		if !isStr || !rules.redacts(key) {
			out[key] = val
			continue
		}
		storedVal, hasStored := stored[key].(string)
		if s == "" && hasStored && storedVal != "" && (rules.failClosed || rules.required[key]) {
			out[key] = storedVal
			continue
		}
		out[key] = s
	}
	for key := range rules.required {
		if _, present := incoming[key]; present {
			continue
		}
		if storedVal, ok := stored[key].(string); ok {
			out[key] = storedVal
		}
	}
	if len(out) == 0 && !foundIn {
		return
	}
	_ = unstructured.SetNestedField(desired.Object, out, "spec", "config")
}

// stripRedactedConfig drops marker values from a GameServer about to be
// created: there is no stored value for them to keep.
func stripRedactedConfig(obj *unstructured.Unstructured) {
	raw, _, _ := unstructured.NestedFieldNoCopy(obj.Object, "spec", "config")
	cfg, ok := raw.(map[string]any)
	if !ok {
		return
	}
	for key, val := range cfg {
		if s, isStr := val.(string); isStr && s == configRedactedMarker {
			delete(cfg, key)
		}
	}
}
