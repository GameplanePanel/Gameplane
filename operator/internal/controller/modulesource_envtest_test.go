//go:build envtest

package controller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
	"github.com/GameplanePanel/gameplane/operator/internal/modsrc"
)

// fakeOCI is an in-process modsrc.OCIClient used by the ModuleSource
// and Module reconcilers through the real OCI fetcher. Tests
// pre-populate tags + bundle files by reference, and inspect call
// counts to verify the reconciler's caching/back-off behavior.
type fakeOCI struct {
	mu      sync.Mutex
	tags    map[string][]string
	bundles map[string]map[string]fakeArtifact // ref → version → artifact
	pulls   int
	errOn   map[string]error
}

type fakeArtifact struct {
	digest string
	files  map[string][]byte
}

func newFakeOCI() *fakeOCI {
	return &fakeOCI{
		tags:    map[string][]string{},
		bundles: map[string]map[string]fakeArtifact{},
		errOn:   map[string]error{},
	}
}

func (f *fakeOCI) ListTags(_ context.Context, ref string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err, ok := f.errOn["tags:"+ref]; ok {
		return nil, err
	}
	// Mirror Client.ListTags: descending semver order.
	out := append([]string(nil), f.tags[ref]...)
	sortSemverDescending(out)
	return out, nil
}

func sortSemverDescending(tags []string) {
	for i := 0; i < len(tags); i++ {
		for j := i + 1; j < len(tags); j++ {
			if !semverDescending(tags[i], tags[j]) {
				tags[i], tags[j] = tags[j], tags[i]
			}
		}
	}
}

func (f *fakeOCI) Pull(_ context.Context, ref, version string) (string, map[string][]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pulls++
	if err, ok := f.errOn["pull:"+ref+":"+version]; ok {
		return "", nil, err
	}
	if m, ok := f.bundles[ref]; ok {
		if a, ok := m[version]; ok {
			return a.digest, a.files, nil
		}
	}
	return "", nil, fmt.Errorf("fakeOCI: no bundle at %s:%s", ref, version)
}

func (f *fakeOCI) pullCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pulls
}

func (f *fakeOCI) putBundle(ref, version string, a fakeArtifact) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.bundles[ref]; !ok {
		f.bundles[ref] = map[string]fakeArtifact{}
	}
	f.bundles[ref][version] = a
	// Append tag if new.
	for _, t := range f.tags[ref] {
		if t == version {
			return
		}
	}
	f.tags[ref] = append(f.tags[ref], version)
}

// fakeOCIFetcher wires the fake transport through the real OCI fetcher
// so envtests exercise the production index/pull logic.
func fakeOCIFetcher(fake *fakeOCI) func(context.Context, *gameplanev1alpha1.ModuleSource) (modsrc.Fetcher, error) {
	return func(_ context.Context, src *gameplanev1alpha1.ModuleSource) (modsrc.Fetcher, error) {
		names := make([]string, 0, len(src.Spec.OCI.Modules))
		for _, m := range src.Spec.OCI.Modules {
			names = append(names, m.Name)
		}
		return modsrc.NewOCI(fake, src.Spec.OCI.URL, names), nil
	}
}

func withModuleSourceReconciler(fake *fakeOCI) setupReconciler {
	return func(mgr manager.Manager) error {
		return (&ModuleSourceReconciler{
			Client:     mgr.GetClient(),
			Scheme:     mgr.GetScheme(),
			NewFetcher: fakeOCIFetcher(fake),
		}).SetupWithManager(mgr)
	}
}

// fixtureBundle constructs bundle files whose metadata matches the
// given name+version. Just enough to populate ModuleEntry status fields.
func fixtureBundle(name, version, displayName string) fakeArtifact {
	return fakeArtifact{
		digest: "sha256:" + name + "-" + version,
		files: map[string][]byte{
			modsrc.FileMetadata: []byte("apiVersion: gameplane.local/module/v1\n" +
				"name: " + name + "\n" +
				"displayName: " + displayName + "\n" +
				"version: " + version + "\n" +
				"game: " + name + "\n" +
				"summary: " + displayName + " — test fixture\n"),
			modsrc.FileTemplate: []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n" +
				"spec:\n  displayName: " + displayName + "\n  game: " + name +
				"\n  version: " + version + "\n  image: ghcr.io/test/" + name + ":" + version + "\n"),
		},
	}
}

// TestModuleSource_IndexesCatalog — happy path: status.modules
// reflects the bundles the registry has, sorted descending.
func TestModuleSource_IndexesCatalog(t *testing.T) {
	_ = newNamespace(t) // doesn't matter; ModuleSource is cluster-scoped
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleSourceReconciler(fake))

	// Pre-populate the fake registry with two modules, two versions each.
	fake.putBundle("local/test/minecraft-java", "1.0.0", fixtureBundle("minecraft-java", "1.0.0", "Minecraft (Java)"))
	fake.putBundle("local/test/minecraft-java", "1.1.0", fixtureBundle("minecraft-java", "1.1.0", "Minecraft (Java)"))
	fake.putBundle("local/test/valheim", "0.9.0", fixtureBundle("valheim", "0.9.0", "Valheim"))

	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("indexed")},
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type: gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI: &gameplanev1alpha1.OCISourceSpec{
				URL: "local/test",
				Modules: []gameplanev1alpha1.ModuleRef{
					{Name: "minecraft-java"},
					{Name: "valheim"},
				},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), src); err != nil {
		t.Fatalf("create modulesource: %v", err)
	}
	deleteCleanup(t, src)

	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		if len(got.Status.Modules) != 2 {
			return false, fmt.Sprintf("got %d modules", len(got.Status.Modules))
		}
		mc := byName(got.Status.Modules, "minecraft-java")
		if mc == nil || mc.LatestVersion != "1.1.0" {
			return false, fmt.Sprintf("minecraft-java latest = %v", mc)
		}
		if len(mc.Versions) != 2 || mc.Versions[0] != "1.1.0" || mc.Versions[1] != "1.0.0" {
			return false, fmt.Sprintf("minecraft-java versions = %v", mc.Versions)
		}
		if mc.DisplayName != "Minecraft (Java)" {
			return false, fmt.Sprintf("minecraft-java displayName = %q", mc.DisplayName)
		}
		vh := byName(got.Status.Modules, "valheim")
		if vh == nil || vh.LatestVersion != "0.9.0" {
			return false, fmt.Sprintf("valheim latest = %v", vh)
		}
		return conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced),
			"Synced condition not True yet"
	})
}

// TestModuleSource_KeepsPartialCatalogOnError — when one module fails
// to index, the others still appear in status.
func TestModuleSource_KeepsPartialCatalogOnError(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleSourceReconciler(fake))

	fake.putBundle("local/test/good", "1.0.0", fixtureBundle("good", "1.0.0", "Good"))
	fake.errOn["tags:local/test/broken"] = fmt.Errorf("simulated registry error")

	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("partial")},
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type: gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI: &gameplanev1alpha1.OCISourceSpec{
				URL:     "local/test",
				Modules: []gameplanev1alpha1.ModuleRef{{Name: "good"}, {Name: "broken"}},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), src); err != nil {
		t.Fatalf("create: %v", err)
	}
	deleteCleanup(t, src)

	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		good := byName(got.Status.Modules, "good")
		broken := byName(got.Status.Modules, "broken")
		if good == nil || good.LatestVersion != "1.0.0" {
			return false, fmt.Sprintf("good = %+v", good)
		}
		if broken == nil || len(broken.Versions) != 0 {
			return false, fmt.Sprintf("broken = %+v", broken)
		}
		return true, ""
	})
}

// TestModuleSource_LocalDirectory exercises the REAL local fetcher
// (no fake): module dirs dropped under the operator's local root are
// indexed into the catalog, and a Module install materializes a
// GameTemplate from them.
func TestModuleSource_LocalDirectory(t *testing.T) {
	_ = newNamespace(t)
	root := t.TempDir()
	writeLocalModule(t, filepath.Join(root, "bundles", "terraria"), "terraria", "1.4.0")

	opts := modsrc.Options{LocalRoot: root}
	startMgr(t, "gameplane-system",
		func(mgr manager.Manager) error {
			return (&ModuleSourceReconciler{
				Client:       mgr.GetClient(),
				Scheme:       mgr.GetScheme(),
				FetchOptions: opts,
			}).SetupWithManager(mgr)
		},
		func(mgr manager.Manager) error {
			return (&ModuleReconciler{
				Client:       mgr.GetClient(),
				Scheme:       mgr.GetScheme(),
				FetchOptions: opts,
			}).SetupWithManager(mgr)
		},
	)

	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("local")},
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type:  gameplanev1alpha1.ModuleSourceTypeLocal,
			Local: &gameplanev1alpha1.LocalSourceSpec{Path: "bundles"},
		},
	}
	if err := k8sClient.Create(context.Background(), src); err != nil {
		t.Fatalf("create modulesource: %v", err)
	}
	deleteCleanup(t, src)

	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		entry := byName(got.Status.Modules, "terraria")
		if entry == nil {
			return false, fmt.Sprintf("catalog = %+v", got.Status.Modules)
		}
		if entry.LatestVersion != "1.4.0" || entry.Digest == "" {
			return false, fmt.Sprintf("entry = %+v", entry)
		}
		return conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced),
			"Synced condition not True yet"
	})

	modName := uniqueName("local-mod")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: src.Name},
			Name:   "terraria",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.Module
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &got); err != nil {
			return false, err.Error()
		}
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady {
			return false, "phase=" + got.Status.Phase + " err=" + got.Status.LastError
		}
		if got.Status.AppliedVersion != "1.4.0" || got.Status.AppliedDigest == "" {
			return false, fmt.Sprintf("applied %q digest %q", got.Status.AppliedVersion, got.Status.AppliedDigest)
		}
		return true, ""
	})

	var tmpl gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &tmpl); err != nil {
		t.Fatalf("get materialized template: %v", err)
	}
	if tmpl.Spec.Game != "terraria" {
		t.Errorf("template spec.game = %q", tmpl.Spec.Game)
	}
}

// TestModuleSource_UploadConfigMap is the kubectl-apply-parity check:
// a hand-applied labeled ConfigMap must index into an upload-type
// source and install exactly like a dashboard upload would.
func TestModuleSource_UploadConfigMap(t *testing.T) {
	// The test namespace stands in for the operator namespace where
	// upload ConfigMaps live.
	ns := newNamespace(t)
	startMgr(t, ns,
		func(mgr manager.Manager) error {
			return (&ModuleSourceReconciler{
				Client:    mgr.GetClient(),
				Scheme:    mgr.GetScheme(),
				Namespace: ns,
			}).SetupWithManager(mgr)
		},
		func(mgr manager.Manager) error {
			return (&ModuleReconciler{
				Client:    mgr.GetClient(),
				Scheme:    mgr.GetScheme(),
				Namespace: ns,
			}).SetupWithManager(mgr)
		},
	)

	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("uploads")},
		Spec:       gameplanev1alpha1.ModuleSourceSpec{Type: gameplanev1alpha1.ModuleSourceTypeUpload},
	}
	if err := k8sClient.Create(context.Background(), src); err != nil {
		t.Fatalf("create modulesource: %v", err)
	}
	deleteCleanup(t, src)

	// The source starts healthy but empty.
	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		return conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced),
			"Synced not True yet"
	})

	// Apply the bundle ConfigMap; the watch should index it without
	// waiting for the refresh interval.
	cmName := uniqueName("bundle-factorio")
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      cmName,
			Namespace: ns,
			Labels:    map[string]string{gameplanev1alpha1.LabelModuleUpload: "true"},
		},
		BinaryData: map[string][]byte{
			"module.yaml": []byte("apiVersion: gameplane.local/module/v1\nname: factorio\n" +
				"displayName: Factorio\nversion: 2.0.0\ngame: factorio\nsummary: upload fixture\n"),
			"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n" +
				"  displayName: Factorio\n  game: factorio\n  version: 2.0.0\n  image: factoriotools/factorio:stable\n"),
		},
	}
	if err := k8sClient.Create(context.Background(), cm); err != nil {
		t.Fatalf("create upload configmap: %v", err)
	}
	deleteCleanup(t, cm)

	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		entry := byName(got.Status.Modules, "factorio")
		if entry == nil {
			return false, fmt.Sprintf("catalog = %+v", got.Status.Modules)
		}
		if entry.LatestVersion != "2.0.0" || entry.Reference != "upload:"+cmName || entry.Digest == "" {
			return false, fmt.Sprintf("entry = %+v", entry)
		}
		return true, ""
	})

	// Install from the upload and confirm materialization.
	modName := uniqueName("upload-mod")
	mod := &gameplanev1alpha1.Module{
		ObjectMeta: metav1.ObjectMeta{Name: modName},
		Spec: gameplanev1alpha1.ModuleSpec{
			Source: corev1.LocalObjectReference{Name: src.Name},
			Name:   "factorio",
		},
	}
	if err := k8sClient.Create(context.Background(), mod); err != nil {
		t.Fatalf("create module: %v", err)
	}
	deleteCleanup(t, mod)

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.Module
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &got); err != nil {
			return false, err.Error()
		}
		if got.Status.Phase != gameplanev1alpha1.ModulePhaseReady {
			return false, "phase=" + got.Status.Phase + " err=" + got.Status.LastError
		}
		return got.Status.AppliedVersion == "2.0.0", "appliedVersion=" + got.Status.AppliedVersion
	})

	var tmpl gameplanev1alpha1.GameTemplate
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &tmpl); err != nil {
		t.Fatalf("get materialized template: %v", err)
	}
	if tmpl.Spec.Image != "factoriotools/factorio:stable" {
		t.Errorf("template image = %q", tmpl.Spec.Image)
	}

	var digestBefore string
	{
		var got gameplanev1alpha1.Module
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &got); err != nil {
			t.Fatalf("get module: %v", err)
		}
		digestBefore = got.Status.AppliedDigest
	}

	// Re-upload changed content under the SAME version: the digest
	// change alone must drive a re-apply.
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: cmName, Namespace: ns}, cm); err != nil {
		t.Fatalf("re-get configmap: %v", err)
	}
	cm.BinaryData["template.yaml"] = []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n" +
		"  displayName: Factorio\n  game: factorio\n  version: 2.0.0\n  image: factoriotools/factorio:1.1.110\n")
	if err := k8sClient.Update(context.Background(), cm); err != nil {
		t.Fatalf("update configmap: %v", err)
	}

	eventually(t, func() (bool, string) {
		var got gameplanev1alpha1.Module
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &got); err != nil {
			return false, err.Error()
		}
		if got.Status.AppliedDigest == digestBefore {
			return false, "appliedDigest unchanged"
		}
		var tmpl gameplanev1alpha1.GameTemplate
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: modName}, &tmpl); err != nil {
			return false, err.Error()
		}
		return tmpl.Spec.Image == "factoriotools/factorio:1.1.110",
			"template image=" + tmpl.Spec.Image
	})
}

// writeLocalModule drops a minimal module dir (module.yaml +
// template.yaml) on disk for local-source tests.
func writeLocalModule(t *testing.T, dir, name, version string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	meta := "apiVersion: gameplane.local/module/v1\nname: " + name +
		"\ndisplayName: " + name + "\nversion: " + version +
		"\ngame: " + name + "\nsummary: local fixture\n"
	tmplYAML := "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\nspec:\n" +
		"  displayName: " + name + "\n  game: " + name + "\n  version: " + version +
		"\n  image: ghcr.io/test/" + name + ":" + version + "\n"
	for file, content := range map[string]string{
		"module.yaml":   meta,
		"template.yaml": tmplYAML,
	} {
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
}

func getModuleSource(t *testing.T, name string) *gameplanev1alpha1.ModuleSource {
	t.Helper()
	var src gameplanev1alpha1.ModuleSource
	if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &src); err != nil {
		t.Fatalf("get modulesource: %v", err)
	}
	return &src
}

func byName(entries []gameplanev1alpha1.ModuleEntry, name string) *gameplanev1alpha1.ModuleEntry {
	for i := range entries {
		if entries[i].Name == name {
			return &entries[i]
		}
	}
	return nil
}

func conditionTrue(conds []metav1.Condition, t string) bool {
	for _, c := range conds {
		if c.Type == t {
			return c.Status == metav1.ConditionTrue
		}
	}
	return false
}

func hasCondition(conds []metav1.Condition, t string) bool {
	for _, c := range conds {
		if c.Type == t {
			return true
		}
	}
	return false
}

// patchSourceAnnotation mutates an annotation to force the controller to
// re-reconcile a source immediately instead of at its refresh interval.
func patchSourceAnnotation(t *testing.T, name, key, val string) {
	t.Helper()
	// Retry on conflict — the reconciler patches status concurrently,
	// racing a bare Get+Update's resourceVersion.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		var src gameplanev1alpha1.ModuleSource
		if err := k8sClient.Get(context.Background(), types.NamespacedName{Name: name}, &src); err != nil {
			return err
		}
		if src.Annotations == nil {
			src.Annotations = map[string]string{}
		}
		src.Annotations[key] = val
		return k8sClient.Update(context.Background(), &src)
	}); err != nil {
		t.Fatalf("patch source: %v", err)
	}
}

// TestModuleSource_ReportsFailureWhenAllModulesError — when a source that
// has never indexed fails (registry unreachable), it must not publish a
// catalog of empty stubs as if it were healthy: modules stay empty, lastSync
// stays unset (it tracks the last *successful* index), and Synced/Ready go
// False.
func TestModuleSource_ReportsFailureWhenAllModulesError(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleSourceReconciler(fake))

	fake.errOn["tags:unreachable/test/ghost"] = fmt.Errorf("dial tcp: no such host")

	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("unreachable")},
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type: gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI: &gameplanev1alpha1.OCISourceSpec{
				URL:     "unreachable/test",
				Modules: []gameplanev1alpha1.ModuleRef{{Name: "ghost"}},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), src); err != nil {
		t.Fatalf("create: %v", err)
	}
	deleteCleanup(t, src)

	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		if conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced) {
			return false, "Synced should be False"
		}
		// Synced=False only counts once the failure was actually observed.
		if !hasCondition(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced) {
			return false, "Synced condition not yet set"
		}
		if got.Status.LastSync != nil {
			return false, "lastSync set despite never indexing successfully"
		}
		if len(got.Status.Modules) != 0 {
			return false, fmt.Sprintf("modules unexpectedly populated: %d", len(got.Status.Modules))
		}
		if conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionReady) {
			return false, "Ready should be False"
		}
		return true, ""
	})
}

// TestModuleSource_PreservesStaleCatalogOnFailure — once a source has
// indexed, a later transient failure must keep the cached catalog (so
// installs of known versions still resolve), flip Synced=False but hold
// Ready=True (ServingStaleCatalog), and leave LastSync at the last success.
func TestModuleSource_PreservesStaleCatalogOnFailure(t *testing.T) {
	_ = newNamespace(t)
	fake := newFakeOCI()
	startMgr(t, "gameplane-system", withModuleSourceReconciler(fake))

	fake.putBundle("local/test/good", "1.0.0", fixtureBundle("good", "1.0.0", "Good Mod"))

	src := &gameplanev1alpha1.ModuleSource{
		ObjectMeta: metav1.ObjectMeta{Name: uniqueName("stale")},
		Spec: gameplanev1alpha1.ModuleSourceSpec{
			Type: gameplanev1alpha1.ModuleSourceTypeOCI,
			OCI: &gameplanev1alpha1.OCISourceSpec{
				URL:     "local/test",
				Modules: []gameplanev1alpha1.ModuleRef{{Name: "good"}},
			},
		},
	}
	if err := k8sClient.Create(context.Background(), src); err != nil {
		t.Fatalf("create: %v", err)
	}
	deleteCleanup(t, src)

	// Wait for the first successful index and capture LastSync.
	var firstSync *metav1.Time
	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		if len(got.Status.Modules) != 1 {
			return false, fmt.Sprintf("modules = %d", len(got.Status.Modules))
		}
		if !conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced) {
			return false, "Synced not yet True"
		}
		firstSync = got.Status.LastSync
		return firstSync != nil, "lastSync nil after success"
	})

	// Break the registry, then nudge the source so it re-reconciles now
	// instead of an hour from now.
	fake.errOn["tags:local/test/good"] = fmt.Errorf("dial tcp: connection refused")
	patchSourceAnnotation(t, src.Name, "gameplane.local/test-nudge", "1")

	eventually(t, func() (bool, string) {
		got := getModuleSource(t, src.Name)
		if conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionSynced) {
			return false, "Synced should have flipped False"
		}
		if len(got.Status.Modules) != 1 {
			return false, fmt.Sprintf("stale catalog dropped: %d modules", len(got.Status.Modules))
		}
		if !conditionTrue(got.Status.Conditions, gameplanev1alpha1.ModuleSourceConditionReady) {
			return false, "Ready should stay True (serving stale catalog)"
		}
		if got.Status.LastSync == nil || !got.Status.LastSync.Time.Equal(firstSync.Time) {
			return false, fmt.Sprintf("lastSync moved: %v want %v", got.Status.LastSync, firstSync)
		}
		return true, ""
	})
}
