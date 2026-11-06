package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"

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
	entries map[configRuleKey]configRules
}

// A revision is content-addressed, but snapshot ownership belongs to one
// server. Never reuse a verified snapshot for a replacement or another server.
type configRuleKey struct {
	template, namespace, name, uid, revision, protected string
	protectedPresent                                    bool
}

func newConfigRuleCache(k *kube.Client) *configRuleCache {
	return &configRuleCache{k: k, entries: map[configRuleKey]configRules{}}
}

// rulesFor uses the server's admitted template, failing closed when that
// template cannot be read or its pinned snapshot cannot be verified.
func (c *configRuleCache) rulesFor(ctx context.Context, gs *unstructured.Unstructured) configRules {
	if gs == nil || c == nil || c.k == nil {
		return configRules{failClosed: true}
	}
	templateName, _, _ := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
	protectedJSON, protectedPresent := gs.GetAnnotations()[kube.ProtectedConfigKeysAnnotation]
	key := configRuleKey{template: templateName, namespace: gs.GetNamespace(), name: gs.GetName(), uid: string(gs.GetUID()), revision: gs.GetAnnotations()[kube.EffectiveTemplateRevisionAnnotation], protected: protectedJSON, protectedPresent: protectedPresent}
	c.mu.Lock()
	cached, ok := c.entries[key]
	c.mu.Unlock()
	if ok {
		return cached
	}
	tmpl, err := c.k.EffectiveGameTemplate(ctx, gs)
	if err != nil {
		return configRules{failClosed: true}
	}
	rules := rulesFromTemplate(tmpl)
	if rules.failClosed {
		return rules
	}
	protected, err := kube.ProtectedConfigKeys(gs.GetAnnotations())
	if err != nil {
		return configRules{failClosed: true}
	}
	for _, key := range protected {
		rules.password[key] = true
	}
	c.mu.Lock()
	c.entries[key] = rules
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
	rules := c.rulesFor(ctx, gs)
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

// protectUnpinnedConfig retains password classification until the operator can
// safely pin a template. PUT passes the exact rules already used by
// restoreRedactedConfig, including a failed lookup. Failed schema reads protect
// every value, matching redaction's fail-closed behavior without changing write errors.
func (c *configRuleCache) protectUnpinnedConfig(ctx context.Context, gs *unstructured.Unstructured, writeRules *configRules) error {
	if gs.GetAnnotations()[kube.EffectiveTemplateRevisionAnnotation] != "" {
		return nil
	}
	cfg, protected, eligible := unpinnedConfigProtectionInput(gs)
	if !eligible {
		return nil
	}
	var rules configRules
	if writeRules != nil {
		rules = *writeRules
	} else {
		rules = c.rulesFor(ctx, gs)
	}
	for key, value := range cfg {
		if s, ok := value.(string); ok && s == "" {
			continue
		}
		if rules.redacts(key) {
			protected = append(protected, key)
		}
	}
	return setProtectedConfigKeys(gs, protected)
}

// unpinnedConfigProtectionInput separates write eligibility from write errors.
// Malformed config remains subject to Kubernetes validation; malformed protected
// metadata must remain unchanged so response redaction and admission fail closed.
func unpinnedConfigProtectionInput(gs *unstructured.Unstructured) (map[string]any, []string, bool) {
	cfg, _, err := unstructured.NestedMap(gs.Object, "spec", "config")
	if err != nil || len(cfg) == 0 {
		return nil, nil, false
	}
	protected, err := kube.ProtectedConfigKeys(gs.GetAnnotations())
	if err != nil {
		return nil, nil, false
	}
	return cfg, protected, true
}

// validateCloneConfig persists source password classification with the clone.
// The catalog can change after validation and before the operator's first pin,
// so every response and admission must retain this protected metadata.
func (c *configRuleCache) validateCloneConfig(ctx context.Context, source, clone *unstructured.Unstructured) error {
	cfg, _, err := unstructured.NestedMap(source.Object, "spec", "config")
	if err != nil {
		return errors.New("cannot clone: source config is malformed")
	}
	if len(cfg) == 0 {
		return nil
	}
	sourceRules, targetRules := c.rulesFor(ctx, source), c.rulesFor(ctx, clone)
	var protected []string
	for key, value := range cfg {
		if s, ok := value.(string); ok && s == "" {
			continue
		}
		if sourceRules.redacts(key) && (targetRules.failClosed || !targetRules.password[key]) {
			return errors.New("cannot clone: the current template does not protect copied passwords; restore its password field types or create a new server with fresh configuration")
		}
		if sourceRules.redacts(key) {
			protected = append(protected, key)
		}
	}
	return setProtectedConfigKeys(clone, protected)
}

func setProtectedConfigKeys(gs *unstructured.Unstructured, protected []string) error {
	if len(protected) != 0 {
		slices.Sort(protected)
		protected = slices.Compact(protected)
		data, err := json.Marshal(protected)
		if err != nil {
			return err
		}
		annotations := gs.GetAnnotations()
		if annotations == nil {
			annotations = map[string]string{}
		}
		annotations[kube.ProtectedConfigKeysAnnotation] = string(data)
		gs.SetAnnotations(annotations)
	}
	return nil
}

// restoreRedactedConfig rewrites desired.spec.config before a PUT so redacted
// values the client sent back do not overwrite stored passwords. live is the
// stored object (nil when it does not exist). The rules are documented in
// api/specs.md ("GameServer config password redaction").
func restoreRedactedConfig(ctx context.Context, c *configRuleCache, desired, live *unstructured.Unstructured) configRules {
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
		return rules
	}
	if live != nil {
		rules = c.rulesFor(ctx, live)
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
		return rules
	}
	_ = unstructured.SetNestedField(desired.Object, out, "spec", "config")
	return rules
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
