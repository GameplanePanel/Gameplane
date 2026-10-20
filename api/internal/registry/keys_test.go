package registry

import (
	"context"
	"fmt"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
)

// newTestStore opens an in-memory SQLite store and runs migrations,
// mirroring api/internal/handlers' newTestStore helper (unexported there,
// so duplicated here for this package's tests).
func newTestStore(t *testing.T) *db.Store {
	t.Helper()
	store, err := db.Open(context.Background(), "sqlite", "file::memory:?_pragma=journal_mode(WAL)&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Migrate(context.Background()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return store
}

// setConfigRow writes a raw config row directly, bypassing the handlers
// package's PUT /admin/config/{section} validator (which this package
// can't import without a cycle).
func setConfigRow(t *testing.T, store *db.Store, key, value string) {
	t.Helper()
	if _, err := store.DB.ExecContext(context.Background(),
		`INSERT INTO config(key, value, updated_at) VALUES (?, ?, datetime('now'))
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value,
	); err != nil {
		t.Fatalf("set config row: %v", err)
	}
}

// fakeSecrets is a map-backed SecretReader stub keyed by Secret name.
type fakeSecrets map[string]map[string][]byte

func (f fakeSecrets) read(_ context.Context, name string) (map[string][]byte, error) {
	data, ok := f[name]
	if !ok {
		return nil, fmt.Errorf("secret %q not found", name)
	}
	return data, nil
}

func TestDBKeyFunc_DefaultSecretName(t *testing.T) {
	store := newTestStore(t)
	secrets := fakeSecrets{
		DefaultKeySecretName("curseforge"): {"apiKey": []byte("cf-key")},
	}
	kf := DBKeyFunc(store, secrets.read)

	if got := kf(context.Background(), "curseforge"); got != "cf-key" {
		t.Fatalf("got %q, want cf-key", got)
	}
}

func TestDBKeyFunc_NoSecretYieldsEmpty(t *testing.T) {
	store := newTestStore(t)
	secrets := fakeSecrets{}
	kf := DBKeyFunc(store, secrets.read)

	if got := kf(context.Background(), "curseforge"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

func TestDBKeyFunc_UnknownProviderYieldsEmpty(t *testing.T) {
	store := newTestStore(t)
	secrets := fakeSecrets{
		DefaultKeySecretName("modrinth"): {"apiKey": []byte("should-never-be-read")},
	}
	kf := DBKeyFunc(store, secrets.read)

	if got := kf(context.Background(), "modrinth"); got != "" {
		t.Fatalf("got %q, want empty (modrinth is not a keyed provider)", got)
	}
}

func TestDBKeyFunc_ConfigRefOverride(t *testing.T) {
	store := newTestStore(t)
	setConfigRow(t, store, ConfigSectionModRegistries,
		`{"registries":[{"provider":"curseforge","configRef":"custom-cf-secret"}]}`)
	secrets := fakeSecrets{
		"custom-cf-secret":                 {"apiKey": []byte("override-key")},
		DefaultKeySecretName("curseforge"): {"apiKey": []byte("default-key")},
	}
	kf := DBKeyFunc(store, secrets.read)

	if got := kf(context.Background(), "curseforge"); got != "override-key" {
		t.Fatalf("got %q, want override-key (configRef override should win)", got)
	}
}

func TestDBKeyFunc_MalformedConfigRowFallsBackToDefault(t *testing.T) {
	store := newTestStore(t)
	setConfigRow(t, store, ConfigSectionModRegistries, `not json`)
	secrets := fakeSecrets{
		DefaultKeySecretName("curseforge"): {"apiKey": []byte("cf-key")},
	}
	kf := DBKeyFunc(store, secrets.read)

	if got := kf(context.Background(), "curseforge"); got != "cf-key" {
		t.Fatalf("got %q, want cf-key (malformed row should not break resolution)", got)
	}
}

func TestDBKeyFunc_EmptyAPIKeyFieldYieldsEmpty(t *testing.T) {
	store := newTestStore(t)
	secrets := fakeSecrets{
		DefaultKeySecretName("curseforge"): {"someOtherField": []byte("x")},
	}
	kf := DBKeyFunc(store, secrets.read)

	if got := kf(context.Background(), "curseforge"); got != "" {
		t.Fatalf("got %q, want empty (secret has no apiKey field)", got)
	}
}

func TestFallbackKeys_DBWinsOverFlag(t *testing.T) {
	primary := StaticKeys(map[string]string{"curseforge": "db-key"})
	fallback := StaticKeys(map[string]string{"curseforge": "flag-key"})
	kf := FallbackKeys(primary, fallback)

	if got := kf(context.Background(), "curseforge"); got != "db-key" {
		t.Fatalf("got %q, want db-key (DB-configured key must win)", got)
	}
}

func TestFallbackKeys_FallsBackWhenPrimaryEmpty(t *testing.T) {
	primary := StaticKeys(map[string]string{})
	fallback := StaticKeys(map[string]string{"curseforge": "flag-key"})
	kf := FallbackKeys(primary, fallback)

	if got := kf(context.Background(), "curseforge"); got != "flag-key" {
		t.Fatalf("got %q, want flag-key (fallback must apply when primary is unconfigured)", got)
	}
}

func TestFallbackKeys_BothEmpty(t *testing.T) {
	kf := FallbackKeys(StaticKeys(nil), StaticKeys(nil))
	if got := kf(context.Background(), "curseforge"); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

// TestSet_AvailableTogglesWithSecret proves the end-to-end claim: writing
// the Secret makes the provider available without rebuilding the Set or
// restarting the process, and removing it hides the provider again on the
// very next call (the empty-key check in curseforgeLazy runs before the
// TTL cache is consulted, so there's no stale-cache window on removal).
func TestSet_AvailableTogglesWithSecret(t *testing.T) {
	store := newTestStore(t)
	secrets := fakeSecrets{}
	set := NewSet("test", DBKeyFunc(store, secrets.read))
	ctx := context.Background()

	if set.Available(ctx, "curseforge") {
		t.Fatal("expected curseforge unavailable before any key is configured")
	}

	secrets[DefaultKeySecretName("curseforge")] = map[string][]byte{"apiKey": []byte("cf-key")}
	if !set.Available(ctx, "curseforge") {
		t.Fatal("expected curseforge available once the key secret exists")
	}
	if _, ok := set.For(ctx, Config{Provider: "curseforge", CurseForgeGameID: 432}); !ok {
		t.Fatal("expected For to resolve curseforge once the key secret exists")
	}

	delete(secrets, DefaultKeySecretName("curseforge"))
	if set.Available(ctx, "curseforge") {
		t.Fatal("expected curseforge hidden again immediately after the key secret is removed")
	}
}
