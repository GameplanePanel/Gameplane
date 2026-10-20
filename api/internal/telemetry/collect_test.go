package telemetry

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/version"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	clienttesting "k8s.io/client-go/testing"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

const testInstallID = "3f1c2a9e-8b4d-4e57-9a61-0c2d7e5b9f10"

// collectOpts describes the fake cluster collectKubeClients builds.
type collectOpts struct {
	nodes      []corev1.Node
	serverInfo *version.Info
	dynamic    []runtime.Object
	// nodesFail, versionFail and dynamicFail make the named calls error:
	// dynamicFail keys are fake verbs ("list", "get").
	nodesFail   bool
	versionFail bool
	dynamicFail map[string]bool
}

// collectKubeClients builds a kube.Client over the typed and dynamic fakes.
func collectKubeClients(t *testing.T, o collectOpts) *kube.Client {
	t.Helper()
	cs := fake.NewSimpleClientset()
	for i := range o.nodes {
		if _, err := cs.CoreV1().Nodes().Create(context.Background(), &o.nodes[i], metav1.CreateOptions{}); err != nil {
			t.Fatalf("create node: %v", err)
		}
	}
	if o.serverInfo != nil {
		cs.Discovery().(*fakediscovery.FakeDiscovery).FakedServerVersion = o.serverInfo
	}
	boom := func(clienttesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("boom")
	}
	if o.nodesFail {
		cs.PrependReactor("list", "nodes", boom)
	}
	if o.versionFail {
		cs.PrependReactor("get", "version", boom)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			kube.GVRs["servers"]:   "GameServerList",
			kube.GVRs["templates"]: "GameTemplateList",
			kube.GVRs["schedules"]: "BackupScheduleList",
			kube.GVRCluster:        "ClusterList",
			kube.GVRModuleSource:   "ModuleSourceList",
		}, o.dynamic...)
	for verb := range o.dynamicFail {
		dyn.PrependReactor(verb, "*", boom)
	}
	return &kube.Client{Typed: cs, Dynamic: dyn}
}

// collectStore returns a migrated in-memory store. withID also stores the
// test install ID.
func collectStore(t *testing.T, withID bool) *db.Store {
	t.Helper()
	s, err := db.Open(t.Context(), "sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if err := s.Migrate(t.Context()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	if withID {
		if err := s.SetInstallID(t.Context(), testInstallID); err != nil {
			t.Fatalf("set install id: %v", err)
		}
	}
	return s
}

func putConfig(t *testing.T, s *db.Store, key, value string) {
	t.Helper()
	if _, err := s.DB.ExecContext(t.Context(),
		`INSERT INTO config(key, value, updated_at) VALUES (?, ?, ?)
		 ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, "2026-01-01 00:00:00"); err != nil {
		t.Fatalf("put config %q: %v", key, err)
	}
}

func gpObject(kind, name, ns string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetAPIVersion("gameplane.local/v1alpha1")
	o.SetKind(kind)
	o.SetName(name)
	if ns != "" {
		o.SetNamespace(ns)
	}
	return o
}

// serverObj builds a GameServer using template tmpl. wake sets
// spec.idle.wakeOnConnect; tunnelProvider (when non-empty) sets spec.tunnel
// with the given enabled value.
func serverObj(name, tmpl string, wake bool, tunnelProvider string, tunnelEnabled bool) *unstructured.Unstructured {
	o := gpObject("GameServer", name, "gameplane-games")
	_ = unstructured.SetNestedField(o.Object, tmpl, "spec", "templateRef", "name")
	if wake {
		_ = unstructured.SetNestedField(o.Object, true, "spec", "idle", "wakeOnConnect")
	}
	if tunnelProvider != "" {
		_ = unstructured.SetNestedField(o.Object, tunnelEnabled, "spec", "tunnel", "enabled")
		_ = unstructured.SetNestedField(o.Object, tunnelProvider, "spec", "tunnel", "provider")
	}
	return o
}

// templateObj builds a cluster-scoped GameTemplate with the given labels.
func templateObj(name string, labels map[string]string) *unstructured.Unstructured {
	o := gpObject("GameTemplate", name, "")
	if labels != nil {
		o.SetLabels(labels)
	}
	return o
}

// moduleTemplate builds a GameTemplate stamped the way the operator stamps a
// module-managed one.
func moduleTemplate(name, source, module string) *unstructured.Unstructured {
	return templateObj(name, map[string]string{
		labelManagedBy:    managedByModule,
		labelModuleSource: source,
		labelModuleName:   module,
	})
}

func moduleSourceObj(name string, ociModules, statusModules []string) *unstructured.Unstructured {
	o := gpObject("ModuleSource", name, "")
	names := func(in []string) []any {
		out := make([]any, 0, len(in))
		for _, n := range in {
			out = append(out, map[string]any{"name": n})
		}
		return out
	}
	if ociModules != nil {
		_ = unstructured.SetNestedSlice(o.Object, names(ociModules), "spec", "oci", "modules")
	}
	if statusModules != nil {
		_ = unstructured.SetNestedSlice(o.Object, names(statusModules), "status", "modules")
	}
	return o
}

func fixedNow() time.Time { return time.Date(2026, 10, 6, 9, 12, 44, 123456789, time.UTC) }

func TestCollect_BasicCountsLocalClusterOnly(t *testing.T) {
	k := collectKubeClients(t, collectOpts{dynamic: []runtime.Object{
		serverObj("a", "t1", false, "", false),
		serverObj("b", "t1", false, "", false),
		templateObj("t1", nil),
	}})
	rep, err := Collect(t.Context(), Deps{Kube: k, Store: collectStore(t, true), Version: "v1.2.3"})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Version != "v1.2.3" || rep.Servers != 2 || rep.Templates != 1 {
		t.Fatalf("basic report = %+v, want {v1.2.3 2 1}", rep)
	}
	if rep.Ext != nil {
		t.Fatal("the extended part must be absent when Extended is false")
	}
}

func TestCollect_BasicListFailureIsZeroNotError(t *testing.T) {
	k := collectKubeClients(t, collectOpts{
		dynamic:     []runtime.Object{serverObj("a", "t1", false, "", false)},
		dynamicFail: map[string]bool{"list": true},
	})
	rep, err := Collect(t.Context(), Deps{Kube: k, Store: collectStore(t, false), Version: "v1"})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Servers != 0 || rep.Templates != 0 {
		t.Fatalf("failed lists must count as 0, got %+v", rep)
	}
}

func TestCollect_NilClients(t *testing.T) {
	for name, k := range map[string]*kube.Client{"nil client": nil, "empty client": {}} {
		rep, err := Collect(t.Context(), Deps{Kube: k, Store: collectStore(t, true), Version: "v1", Extended: true, Now: fixedNow})
		if err != nil {
			t.Fatalf("%s: collect: %v", name, err)
		}
		if rep.Servers != 0 || rep.Templates != 0 || rep.Ext == nil {
			t.Fatalf("%s: report = %+v", name, rep)
		}
		if !reflect.DeepEqual(rep.Ext.Env.K8s, []string{"other"}) || rep.Ext.Env.Nodes != "other" || !reflect.DeepEqual(rep.Ext.Env.Arch, []string{"other"}) {
			t.Errorf("%s: env = %+v, want other fallbacks", name, rep.Ext.Env)
		}
	}
}

func TestCollect_ExtendedFullReport(t *testing.T) {
	const official = "default"
	nodes := []corev1.Node{
		testNode("n1", nil, "", "Ubuntu 24.04", "amd64"),
		testNode("n2", nil, "", "Ubuntu 24.04", "arm64"),
	}
	k := collectKubeClients(t, collectOpts{
		nodes:      nodes,
		serverInfo: &version.Info{Major: "1", Minor: "31+", GitVersion: "v1.31.2+k3s1"},
		dynamic: []runtime.Object{
			moduleSourceObj(official, []string{"minecraft-java", "terraria", "minecraft-java-evil"}, nil),
			moduleTemplate("mc", official, "minecraft-java"),
			moduleTemplate("tr", official, "terraria"),
			moduleTemplate("upload-mc", "uploads", "minecraft-java"),
			moduleTemplate("lookalike", official, "minecraft-java-evil"),
			templateObj("manual", nil),
			serverObj("s1", "mc", true, "", false),
			serverObj("s2", "mc", false, "", false),
			serverObj("s3", "tr", false, "playit", true),
			serverObj("s4", "manual", false, "", false),
			serverObj("s5", "ghost", false, "", false), // template does not exist
			serverObj("s6", "mc", false, "frp", false), // tunnel configured but off
			serverObj("s7", "upload-mc", false, "", false),
			serverObj("s8", "lookalike", false, "", false),
			gpObject("BackupSchedule", "nightly", "gameplane-games"),
			gpObject("Cluster", "remote-a", ""),
			gpObject("Cluster", "remote-b", ""),
		},
	})
	store := collectStore(t, true)
	putConfig(t, store, "auth", `{"providers":[{"name":"local","kind":"local","enabled":true},{"name":"g","kind":"google","enabled":true}]}`)
	secret, err := store.EnsureSigningSecret(t.Context())
	if err != nil {
		t.Fatalf("secret: %v", err)
	}
	priv, err := telemetryschema.DeriveKey(secret, testInstallID)
	if err != nil {
		t.Fatalf("derive: %v", err)
	}

	rep, err := Collect(t.Context(), Deps{
		Kube: k, Store: store, Version: "v0.3.0", Extended: true, Now: fixedNow,
		Flags: Flags{
			CaptureEnabled: true, AuditS3: true, DBDriver: "sqlite", OfficialModuleSource: official,
		},
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if rep.Version != "v0.3.0" || rep.Servers != 8 || rep.Templates != 5 {
		t.Fatalf("basic part = %+v, want {v0.3.0 8 5}", rep)
	}
	ext := rep.Ext
	if ext == nil {
		t.Fatal("missing extended part")
	}
	if ext.Schema != 2 || ext.InstallID != testInstallID {
		t.Errorf("schema/id = %d/%q", ext.Schema, ext.InstallID)
	}
	wantEnv := telemetryschema.Env{K8s: []string{"1.31"}, Distro: []string{"k3s"}, Arch: []string{"amd64", "arm64"}, Nodes: telemetryschema.NodeBand(2), Deployment: "cluster"}
	if !reflect.DeepEqual(ext.Env, wantEnv) {
		t.Errorf("env = %+v, want %+v", ext.Env, wantEnv)
	}
	wantGames := telemetryschema.Games{Official: map[string]int{"minecraft-java": 3, "terraria": 1}, Custom: 4}
	if !reflect.DeepEqual(ext.Games, wantGames) {
		t.Errorf("games = %+v, want %+v", ext.Games, wantGames)
	}
	wantFeatures := telemetryschema.Features{
		WakeOnConnect: true, Tunnels: []string{"playit"}, Capture: true, Backups: true, SSO: true,
		AuditForwarding: true, Clusters: telemetryschema.ClusterBand(3), DB: "sqlite", Language: "en",
	}
	if !reflect.DeepEqual(ext.Features, wantFeatures) {
		t.Errorf("features = %+v, want %+v", ext.Features, wantFeatures)
	}
	if want := telemetryschema.PublicKeyString(priv); ext.Key != want {
		t.Errorf("key = %q, want the key derived from the secret and install ID (%q)", ext.Key, want)
	}
	if want := time.Date(2026, 10, 6, 9, 12, 44, 0, time.UTC); !ext.SentAt.Equal(want) {
		t.Errorf("sentAt = %v, want %v (UTC, whole seconds)", ext.SentAt, want)
	}
}

func TestCollect_ExtendedFieldsFallBackWhenSourcesFail(t *testing.T) {
	k := collectKubeClients(t, collectOpts{
		nodesFail:   true,
		versionFail: true,
		dynamicFail: map[string]bool{"list": true, "get": true},
	})
	rep, err := Collect(t.Context(), Deps{
		Kube: k, Store: collectStore(t, true), Version: "v1", Extended: true, Now: fixedNow,
		Flags: Flags{DBDriver: "mysql", OfficialModuleSource: "default"},
	})
	if err != nil {
		t.Fatalf("a failing source must not fail the report: %v", err)
	}
	ext := rep.Ext
	if ext == nil {
		t.Fatal("missing extended part")
	}
	wantEnv := telemetryschema.Env{K8s: []string{"other"}, Distro: []string{"other"}, Arch: []string{"other"}, Nodes: "other", Deployment: "cluster"}
	if !reflect.DeepEqual(ext.Env, wantEnv) {
		t.Errorf("env = %+v, want %+v", ext.Env, wantEnv)
	}
	if len(ext.Games.Official) != 0 || ext.Games.Custom != 0 {
		t.Errorf("games = %+v, want empty", ext.Games)
	}
	f := ext.Features
	if f.WakeOnConnect || f.Backups || f.SSO || f.Capture || f.AuditForwarding || len(f.Tunnels) != 0 {
		t.Errorf("features = %+v, want everything off", f)
	}
	if f.Clusters != telemetryschema.ClusterBand(1) || f.DB != "other" || f.Language != "en" {
		t.Errorf("features = %+v, want cluster band of 1, db other, language en", f)
	}
}

func TestCollect_ExtendedErrors(t *testing.T) {
	ctx := t.Context()
	deps := func(s *db.Store) Deps {
		return Deps{Kube: collectKubeClients(t, collectOpts{}), Store: s, Version: "v1", Extended: true, Now: fixedNow}
	}

	if _, err := Collect(ctx, deps(collectStore(t, false))); err == nil || !strings.Contains(err.Error(), "install id") {
		t.Errorf("no install ID: got %v, want an install id error", err)
	}

	missing := collectStore(t, true)
	if _, err := missing.DB.ExecContext(ctx, `DELETE FROM telemetry_state`); err != nil {
		t.Fatalf("delete state: %v", err)
	}
	if _, err := Collect(ctx, deps(missing)); err == nil {
		t.Error("a missing telemetry_state row must be an error")
	}

	badSecret := collectStore(t, true)
	if _, err := badSecret.DB.ExecContext(ctx, `UPDATE telemetry_state SET signing_secret = '!!not base64!!'`); err != nil {
		t.Fatalf("corrupt secret: %v", err)
	}
	if _, err := Collect(ctx, deps(badSecret)); err == nil {
		t.Error("an unreadable signing secret must be an error")
	}
}

func TestCollect_DefaultClockIsNow(t *testing.T) {
	rep, err := Collect(t.Context(), Deps{
		Kube: collectKubeClients(t, collectOpts{}), Store: collectStore(t, true), Version: "v1", Extended: true,
	})
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	if d := time.Since(rep.Ext.SentAt); d < -time.Minute || d > time.Minute {
		t.Fatalf("sentAt %v is not the current time", rep.Ext.SentAt)
	}
}

func TestServerVersion_MinorForms(t *testing.T) {
	cases := []struct {
		major, minor, want string
	}{
		{"1", "31", "1.31"},
		{"1", "31+", "1.31"},
		{"1", "9", "1.9"},
		{"1", "", "other"},
		{"1", "1234", "other"},
		{"2", "0", "other"},
		{"", "", "other"},
	}
	for _, tc := range cases {
		k := collectKubeClients(t, collectOpts{serverInfo: &version.Info{Major: tc.major, Minor: tc.minor, GitVersion: "v1.0.0"}})
		git, minor := serverVersion(Deps{Kube: k})
		if minor != tc.want || git != "v1.0.0" {
			t.Errorf("major=%q minor=%q: got %q (git %q), want %q", tc.major, tc.minor, minor, git, tc.want)
		}
	}

	failing := collectKubeClients(t, collectOpts{versionFail: true})
	if git, minor := serverVersion(Deps{Kube: failing}); git != "" || minor != "other" {
		t.Errorf("discovery error: got %q/%q, want empty/other", git, minor)
	}
}

func TestNodeArches(t *testing.T) {
	node := func(arch string) corev1.Node { return testNode("n", nil, "", "", arch) }
	cases := []struct {
		name  string
		nodes []corev1.Node
		want  []string
	}{
		{"sorted and unique", []corev1.Node{node("arm64"), node("amd64"), node("arm64")}, []string{"amd64", "arm64"}},
		{"unknown becomes other", []corev1.Node{node("s390x"), node("sparc")}, []string{"other", "s390x"}},
		{"empty architecture is skipped", []corev1.Node{node(""), node("amd64")}, []string{"amd64"}},
		{"no nodes", nil, []string{"other"}},
		{"only blank architectures", []corev1.Node{node("")}, []string{"other"}},
	}
	for _, tc := range cases {
		if got := nodeArches(tc.nodes); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCollectFeatures_TunnelsAreSanitisedAndSorted(t *testing.T) {
	servers := []unstructured.Unstructured{
		*serverObj("a", "t", false, "tailscale", true),
		*serverObj("b", "t", false, "frp", true),
		*serverObj("c", "t", false, "tailscale", true),
		*serverObj("d", "t", false, "my-private-relay", true),
		*serverObj("e", "t", false, "playit", false),
	}
	f := collectFeatures(t.Context(), Deps{Store: collectStore(t, false), Flags: Flags{DBDriver: "postgres"}}, servers)
	if want := []string{"frp", "other", "tailscale"}; !reflect.DeepEqual(f.Tunnels, want) {
		t.Errorf("tunnels = %v, want %v", f.Tunnels, want)
	}
	if f.DB != "postgres" {
		t.Errorf("db = %q, want postgres", f.DB)
	}
}

func TestCollectFeatures_SSOAndAuditSources(t *testing.T) {
	ctx := t.Context()
	store := collectStore(t, false)
	base := func(f Flags) telemetryschema.Features { return collectFeatures(ctx, Deps{Store: store, Flags: f}, nil) }

	if f := base(Flags{}); f.SSO || f.AuditForwarding {
		t.Errorf("nothing configured: got %+v", f)
	}
	if f := base(Flags{OIDCConfigured: true}); !f.SSO {
		t.Error("the Helm OIDC flag must count as SSO")
	}
	if f := base(Flags{AuditWebhook: true}); !f.AuditForwarding {
		t.Error("the audit webhook must count as audit forwarding")
	}
	if f := base(Flags{AuditS3: true}); !f.AuditForwarding {
		t.Error("the S3 audit sink must count as audit forwarding")
	}
}

func TestHasSSOProvider(t *testing.T) {
	ctx := t.Context()
	if hasSSOProvider(ctx, nil) {
		t.Error("a nil store has no providers")
	}
	cases := []struct {
		name string
		raw  string // empty = no row
		want bool
	}{
		{"no row", "", false},
		{"malformed", `{not json`, false},
		{"no providers", `{"providers":[]}`, false},
		{"local only", `{"providers":[{"name":"local","kind":"local","enabled":true}]}`, false},
		{"oidc disabled", `{"providers":[{"name":"o","kind":"oidc","enabled":false}]}`, false},
		{"oidc enabled", `{"providers":[{"name":"o","kind":"oidc","enabled":true}]}`, true},
		{"google enabled", `{"providers":[{"name":"g","kind":"google","enabled":true}]}`, true},
		{"github enabled", `{"providers":[{"name":"h","kind":"github","enabled":true}]}`, true},
		{"unknown kind", `{"providers":[{"name":"x","kind":"saml","enabled":true}]}`, false},
	}
	for _, tc := range cases {
		store := collectStore(t, false)
		if tc.raw != "" {
			putConfig(t, store, "auth", tc.raw)
		}
		if got := hasSSOProvider(ctx, store); got != tc.want {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}

	closed := collectStore(t, false)
	_ = closed.Close()
	if hasSSOProvider(ctx, closed) {
		t.Error("a store read error must count as no provider")
	}
}
