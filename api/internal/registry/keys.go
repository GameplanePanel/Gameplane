package registry

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// RegistryKeySecretLabel must be set to "true" on a Secret before a
// mod-registry KeyFunc will read an apiKey from it — the same
// labelled-Secret contract as the auth-provider (auth.ProviderSecretLabel)
// and notification-sink Secrets, under its own label so a config:manage
// user can't point a registry-key lookup at an unrelated Secret.
const RegistryKeySecretLabel = "gameplane.local/mod-registry"

// registryKeyPrefix derives the default Secret name for a
// provider's key when the ConfigSectionModRegistries row carries no
// explicit configRef.
const registryKeyPrefix = "gameplane-modreg-"

// ConfigSectionModRegistries is the /admin/config section name that holds
// the (secret-free) list of which mod-registry providers are configured
// and their configRef Secret name override.
const ConfigSectionModRegistries = "modRegistries"

// KeyedProviders is the closed set of mod-registry providers that take an
// API key. curseforge, steam, and nexus all have engines wired into a Set;
// each stays hidden in the Mods browser until its key is configured (via the
// /admin/registries/{provider}/secret endpoint). Unknown providers are
// rejected rather than silently getting a Secret nobody will ever read.
var KeyedProviders = map[string]bool{
	"curseforge": true,
	"steam":      true,
	"nexus":      true,
}

// DefaultKeySecretName returns the conventional Secret name for a
// provider's key. Both the secret endpoint (always writes here) and the
// KeyFunc below (reads here absent a configRef override) use this.
func DefaultKeySecretName(provider string) string {
	return registryKeyPrefix + provider
}

// SecretReader fetches a named credential Secret's data. Production wires
// NewK8sSecretReader; tests substitute a map-backed stub.
type SecretReader func(ctx context.Context, name string) (map[string][]byte, error)

// NewK8sSecretReader reads Secrets from ns, refusing any not labelled
// RegistryKeySecretLabel=true.
func NewK8sSecretReader(k *kube.Client, ns string) SecretReader {
	return func(ctx context.Context, name string) (map[string][]byte, error) {
		sec, err := k.Secrets(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return nil, fmt.Errorf("get registry-key secret %s/%s: %w", ns, name, err)
		}
		if sec.Labels[RegistryKeySecretLabel] != "true" {
			return nil, fmt.Errorf("secret %s/%s is not labelled %s=true", ns, name, RegistryKeySecretLabel)
		}
		return sec.Data, nil
	}
}

// modRegistryEntry mirrors one entry of the admin-managed modRegistries
// config row (see api/internal/handlers/config.go's validateModRegistries).
// It carries no secret material — just an optional Secret-name override.
type modRegistryEntry struct {
	Provider  string `json:"provider"`
	ConfigRef string `json:"configRef,omitempty"`
}

// configRefFor looks up provider's configRef override from the
// modRegistries config row. "" means no override on file (or the row is
// missing/unparsable, which is not an error here — the caller falls back
// to the default naming convention).
func configRefFor(ctx context.Context, store *db.Store, provider string) string {
	raw, ok, err := store.ConfigValue(ctx, ConfigSectionModRegistries)
	if err != nil || !ok {
		return ""
	}
	var c struct {
		Registries []modRegistryEntry `json:"registries"`
	}
	if json.Unmarshal([]byte(raw), &c) != nil {
		return ""
	}
	for _, e := range c.Registries {
		if e.Provider == provider {
			return e.ConfigRef
		}
	}
	return ""
}

// DBKeyFunc resolves a provider's API key from the admin-managed
// modRegistries config row plus its backing Secret: the config row
// (optional) supplies a configRef override, defaulting to
// DefaultKeySecretName(provider) when absent; the Secret's "apiKey" field
// is the key material. The config row and Secret are both re-read per
// call — no local cache here — because the KeyFunc call itself already
// sits behind Set's TTL + key-hash cache (curseforgeLazy), which is what
// bounds how often the *expensive* work (engine construction) actually
// happens; this mirrors auth.Registry's per-resolve config re-read.
//
// Missing config, missing Secret, an unlabelled Secret, or an unkeyed
// provider all resolve to "" (provider unconfigured) rather than an
// error, since KeyFunc has no error channel and the caller (Set) already
// treats "" as "not available" — the exact behavior a startup flag with
// no key produces today.
func DBKeyFunc(store *db.Store, secrets SecretReader) KeyFunc {
	return func(ctx context.Context, provider string) string {
		if !KeyedProviders[provider] {
			return ""
		}
		name := configRefFor(ctx, store, provider)
		if name == "" {
			name = DefaultKeySecretName(provider)
		}
		data, err := secrets(ctx, name)
		if err != nil {
			return ""
		}
		return string(data["apiKey"])
	}
}

// FallbackKeys composes two KeyFuncs: primary wins whenever it returns a
// non-empty key; fallback is consulted only when primary reports the
// provider unconfigured.
//
// Precedence: a runtime (DB-configured) key always wins over the startup
// --*-api-key flag, so an admin's dashboard/API change takes effect
// without a redeploy; the flag remains a working fallback for
// air-gapped/GitOps installs that never touch admin config.
func FallbackKeys(primary, fallback KeyFunc) KeyFunc {
	return func(ctx context.Context, provider string) string {
		if key := primary(ctx, provider); key != "" {
			return key
		}
		return fallback(ctx, provider)
	}
}
