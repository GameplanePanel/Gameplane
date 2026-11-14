//go:build envtest

package controller

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

// These tests assert that the CRD OpenAPI schemas (loaded by the suite
// from operator/config/crd) reject invalid specs at the apiserver
// admission layer. They are the seconds-tier counterpart of the
// kubectl-based cases in test/e2e/crd_validation_e2e_test.go — a schema
// regression should fail here first, without a kind cluster.

func validGameServer(ns, name string) *gameplanev1alpha1.GameServer {
	return &gameplanev1alpha1.GameServer{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec: gameplanev1alpha1.GameServerSpec{
			TemplateRef: gameplanev1alpha1.GameTemplateRef{Name: "some-template"},
		},
	}
}

func TestCRDValidation_GameServerEmptyTemplateRef(t *testing.T) {
	ns := newNamespace(t)
	gs := validGameServer(ns, "v-empty-templateref")
	gs.Spec.TemplateRef.Name = ""
	err := k8sClient.Create(context.Background(), gs)
	if !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestCRDValidation_ProbeHosts(t *testing.T) {
	ns := newNamespace(t)
	port := intstr.FromString("game")
	for _, tc := range []struct {
		field  string
		probes gameplanev1alpha1.GameProbesSpec
	}{
		{"readiness.httpGet", gameplanev1alpha1.GameProbesSpec{Readiness: probeSecHTTP("169.254.169.254", port)}},
		{"readiness.tcpSocket", gameplanev1alpha1.GameProbesSpec{Readiness: probeSecTCPHost("::1", port)}},
		{"startup.httpGet", gameplanev1alpha1.GameProbesSpec{Startup: probeSecHTTP("kubernetes.default.svc", port)}},
		{"startup.tcpSocket", gameplanev1alpha1.GameProbesSpec{Startup: probeSecTCPHost("127.0.0.1", port)}},
		{"liveness.httpGet", gameplanev1alpha1.GameProbesSpec{Liveness: probeSecHTTP("fd00::1", port)}},
		{"liveness.tcpSocket", gameplanev1alpha1.GameProbesSpec{Liveness: probeSecTCPHost("localhost", port)}},
	} {
		check := func(t *testing.T, obj client.Object, probes **gameplanev1alpha1.GameProbesSpec) {
			t.Helper()
			field := "spec.probes." + tc.field + ".host"
			*probes = &tc.probes
			if err := k8sClient.Create(t.Context(), obj); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field) {
				t.Fatalf("create: expected Invalid at %s, got %v", field, err)
			}
			*probes = &gameplanev1alpha1.GameProbesSpec{Readiness: probeSecHTTP("", port), Startup: probeSecTCP(port)}
			if err := k8sClient.Create(t.Context(), obj); err != nil {
				t.Fatalf("create with pod-local named ports: %v", err)
			}
			deleteCleanup(t, obj)
			*probes = &tc.probes
			if err := k8sClient.Update(t.Context(), obj); !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), field) {
				t.Fatalf("update: expected Invalid at %s, got %v", field, err)
			}
		}
		t.Run("GameTemplate/"+tc.field, func(t *testing.T) {
			tmpl := buildGameTemplate(uniqueName("probe-template"))
			check(t, tmpl, &tmpl.Spec.Probes)
		})
		t.Run("GameServer/"+tc.field, func(t *testing.T) {
			gs := validGameServer(ns, uniqueName("probe-server"))
			check(t, gs, &gs.Spec.Probes)
		})
	}
}

func TestCRDValidation_GameServerBadHostname(t *testing.T) {
	ns := newNamespace(t)
	for _, bad := range []string{"Has_Underscores", "-leading.dash", "double..dot"} {
		gs := validGameServer(ns, "v-bad-hostname")
		gs.Spec.Networking.Hostname = bad
		if err := k8sClient.Create(context.Background(), gs); !apierrors.IsInvalid(err) {
			t.Fatalf("hostname %q: expected Invalid, got %v", bad, err)
		}
	}

	gs := validGameServer(ns, "v-good-hostname")
	gs.Spec.Networking.Hostname = "mc.example.com"
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("valid hostname rejected: %v", err)
	}
}

func TestCRDValidation_GameServerConfigValueTooLong(t *testing.T) {
	ns := newNamespace(t)
	gs := validGameServer(ns, "v-config-too-long")
	gs.Spec.Config = map[string]string{"motd": strings.Repeat("x", 4097)}
	if err := k8sClient.Create(context.Background(), gs); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for 4097-char config value, got %v", err)
	}

	gs = validGameServer(ns, "v-config-ok")
	gs.Spec.Config = map[string]string{"motd": strings.Repeat("x", 4096)}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("4096-char config value rejected: %v", err)
	}
}

func TestCRDValidation_GameServerSourceRanges(t *testing.T) {
	ns := newNamespace(t)
	bad := validGameServer(ns, "v-bad-cidr")
	bad.Spec.Networking.SourceRanges = []string{"not-a-cidr"}
	if err := k8sClient.Create(context.Background(), bad); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for non-CIDR sourceRange, got %v", err)
	}

	ok := validGameServer(ns, "v-ok-cidr")
	ok.Spec.Networking.SourceRanges = []string{"10.0.0.0/8", "203.0.113.0/24"}
	if err := k8sClient.Create(context.Background(), ok); err != nil {
		t.Fatalf("valid CIDR sourceRanges rejected: %v", err)
	}
}

func TestCRDValidation_GameServerBadInlineBackupSchedule(t *testing.T) {
	ns := newNamespace(t)
	gs := validGameServer(ns, "v-bad-inline-cron")
	gs.Spec.BackupPolicy = &gameplanev1alpha1.InlineBackupPolicy{
		Schedule: "every-night",
		RepoRef:  gameplanev1alpha1.SecretKeySelector{Name: "creds", Key: "repo"},
	}
	if err := k8sClient.Create(context.Background(), gs); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for non-cron inline schedule, got %v", err)
	}

	gs = validGameServer(ns, "v-good-inline-cron")
	gs.Spec.BackupPolicy = &gameplanev1alpha1.InlineBackupPolicy{
		Schedule: "0 4 * * *",
		RepoRef:  gameplanev1alpha1.SecretKeySelector{Name: "creds", Key: "repo"},
	}
	if err := k8sClient.Create(context.Background(), gs); err != nil {
		t.Fatalf("valid inline schedule rejected: %v", err)
	}
}

func TestCRDValidation_BackupScheduleBadCron(t *testing.T) {
	ns := newNamespace(t)
	bs := &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "v-bad-cron", Namespace: ns},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "any"},
			Schedule:  "not-a-cron",
			RepoRef:   &gameplanev1alpha1.SecretKeySelector{Name: "creds", Key: "repo"},
		},
	}
	if err := k8sClient.Create(context.Background(), bs); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

// TestCRDValidation_BackupScheduleRepoRefRequiredForRestic mirrors the Backup
// rule: restic-snapshot schedules need a repoRef; volume-snapshot ones don't.
func TestCRDValidation_BackupScheduleRepoRefRequiredForRestic(t *testing.T) {
	ns := newNamespace(t)
	restic := &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "v-sched-restic-no-repo", Namespace: ns},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "smp"},
			Schedule:  "0 3 * * *",
		},
	}
	if err := k8sClient.Create(context.Background(), restic); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for restic schedule without repoRef, got %v", err)
	}

	vs := &gameplanev1alpha1.BackupSchedule{
		ObjectMeta: metav1.ObjectMeta{Name: "v-sched-vs-no-repo", Namespace: ns},
		Spec: gameplanev1alpha1.BackupScheduleSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "smp"},
			Schedule:  "0 3 * * *",
			Strategy:  "volume-snapshot",
		},
	}
	if err := k8sClient.Create(context.Background(), vs); err != nil {
		t.Fatalf("volume-snapshot schedule without repoRef rejected: %v", err)
	}
}

func TestCRDValidation_BackupRequiresServerRef(t *testing.T) {
	ns := newNamespace(t)
	bk := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "v-no-serverref", Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			RepoRef: &gameplanev1alpha1.SecretKeySelector{Name: "creds", Key: "repo"},
		},
	}
	if err := k8sClient.Create(context.Background(), bk); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

// TestCRDValidation_BackupRepoRefRequiredForRestic checks the CEL rule:
// restic-snapshot backups must carry a repoRef, while volume-snapshot
// backups may omit it (they capture a CSI snapshot, no restic repo).
func TestCRDValidation_BackupRepoRefRequiredForRestic(t *testing.T) {
	ns := newNamespace(t)

	// restic (default strategy) without repoRef → rejected.
	bk := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "v-restic-no-repo", Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "smp"},
		},
	}
	if err := k8sClient.Create(context.Background(), bk); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for restic backup without repoRef, got %v", err)
	}

	// volume-snapshot without repoRef → accepted.
	vs := &gameplanev1alpha1.Backup{
		ObjectMeta: metav1.ObjectMeta{Name: "v-vs-no-repo", Namespace: ns},
		Spec: gameplanev1alpha1.BackupSpec{
			ServerRef: gameplanev1alpha1.LocalObjectRef{Name: "smp"},
			Strategy:  "volume-snapshot",
		},
	}
	if err := k8sClient.Create(context.Background(), vs); err != nil {
		t.Fatalf("volume-snapshot backup without repoRef rejected: %v", err)
	}
}

func TestCRDValidation_GameTemplateRequiresImage(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "v-no-image"},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			DisplayName: "no image",
			Game:        "busybox",
			Version:     "1",
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid, got %v", err)
	}
}

func TestCRDValidation_DeferredConfigSecretKeys(t *testing.T) {
	for _, tc := range []struct {
		name     string
		deferred bool
		valid    bool
	}{
		{".", true, false},
		{"..", true, false},
		{"..initialHeap", true, false},
		{strings.Repeat("a", 254), true, false},
		{"jvm.initial-heap_1", true, true},
		{".initialHeap", true, true},
		{"-", true, true},
		{strings.Repeat("a", 253), true, true},
		{"jvm/initialHeap", false, true},
		{"..initialHeap", false, true},
		{strings.Repeat("a", 254), false, true},
	} {
		tmpl := buildGameTemplate(uniqueName("deferred-config-key"))
		tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{{
			Name: tc.name, EnvName: "INIT_MEMORY", Type: "string", ApplyOnRestart: tc.deferred,
		}}
		err := k8sClient.Create(t.Context(), tmpl)
		if !tc.valid {
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "Secret key") {
				t.Fatalf("deferred field %q: expected invalid Secret key, got %v", tc.name, err)
			}
			continue
		}
		if err != nil {
			t.Fatalf("field %q (deferred=%t) rejected: %v", tc.name, tc.deferred, err)
		}
		deleteCleanup(t, tmpl)
	}

	// A valid environment alias does not make the original name a Secret key.
	// Exercise updates as well as creation on clusters with existing templates.
	tmpl := buildGameTemplate(uniqueName("deferred-config-alias"))
	tmpl.Spec.ConfigSchema = []gameplanev1alpha1.ConfigField{{
		Name: "jvm/initialHeap", EnvName: "INIT_MEMORY", Type: "string", ApplyOnRestart: true,
	}}
	checkInvalidKey := func(err error) {
		t.Helper()
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "spec.configSchema[0]") {
			t.Fatalf("expected invalid deferred config field, got %v", err)
		}
	}
	checkInvalidKey(k8sClient.Create(t.Context(), tmpl))
	tmpl.Spec.ConfigSchema[0].ApplyOnRestart = false
	if err := k8sClient.Create(t.Context(), tmpl); err != nil {
		t.Fatalf("create with immediate alias: %v", err)
	}
	deleteCleanup(t, tmpl)
	// The typed client omits false on create. Exercise explicit false on the wire too.
	explicitFalse := client.RawPatch(types.JSONPatchType, []byte(`[{"op":"add","path":"/spec/configSchema/0/applyOnRestart","value":false}]`))
	if err := k8sClient.Patch(t.Context(), tmpl, explicitFalse); err != nil {
		t.Fatalf("set applyOnRestart to false on an immediate alias: %v", err)
	}
	tmpl.Spec.ConfigSchema[0].ApplyOnRestart = true
	checkInvalidKey(k8sClient.Update(t.Context(), tmpl))
}

func TestCRDValidation_ModuleSourceUnion(t *testing.T) {
	ociSpec := &gameplanev1alpha1.OCISourceSpec{
		URL:     "ghcr.io/test/modules",
		Modules: []gameplanev1alpha1.ModuleRef{{Name: "minecraft-java"}},
	}

	t.Run("type oci requires spec.oci", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-oci-no-config")},
			Spec:       gameplanev1alpha1.ModuleSourceSpec{Type: gameplanev1alpha1.ModuleSourceTypeOCI},
		}
		if err := k8sClient.Create(context.Background(), src); !apierrors.IsInvalid(err) {
			t.Fatalf("expected Invalid, got %v", err)
		}
	})

	t.Run("type defaults to oci and still requires spec.oci", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-default-no-config")},
		}
		if err := k8sClient.Create(context.Background(), src); !apierrors.IsInvalid(err) {
			t.Fatalf("expected Invalid, got %v", err)
		}
	})

	t.Run("mismatched nested config rejected", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-git-with-oci")},
			Spec: gameplanev1alpha1.ModuleSourceSpec{
				Type: gameplanev1alpha1.ModuleSourceTypeGit,
				Git:  &gameplanev1alpha1.GitSourceSpec{URL: "https://example.com/mods.git"},
				OCI:  ociSpec,
			},
		}
		if err := k8sClient.Create(context.Background(), src); !apierrors.IsInvalid(err) {
			t.Fatalf("expected Invalid, got %v", err)
		}
	})

	t.Run("git subPath escape rejected", func(t *testing.T) {
		src := &gameplanev1alpha1.ModuleSource{
			ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-git-escape")},
			Spec: gameplanev1alpha1.ModuleSourceSpec{
				Type: gameplanev1alpha1.ModuleSourceTypeGit,
				Git: &gameplanev1alpha1.GitSourceSpec{
					URL:     "https://example.com/mods.git",
					SubPath: "../outside",
				},
			},
		}
		if err := k8sClient.Create(context.Background(), src); !apierrors.IsInvalid(err) {
			t.Fatalf("expected Invalid, got %v", err)
		}
	})

	t.Run("valid typed sources accepted", func(t *testing.T) {
		for _, src := range []*gameplanev1alpha1.ModuleSource{
			{
				ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-ok-oci")},
				Spec: gameplanev1alpha1.ModuleSourceSpec{
					Type: gameplanev1alpha1.ModuleSourceTypeOCI,
					OCI:  ociSpec,
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-ok-git")},
				Spec: gameplanev1alpha1.ModuleSourceSpec{
					Type: gameplanev1alpha1.ModuleSourceTypeGit,
					Git: &gameplanev1alpha1.GitSourceSpec{
						URL:     "https://example.com/mods.git",
						SubPath: "modules",
					},
					Allow: []string{"minecraft-*"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-ok-local")},
				Spec: gameplanev1alpha1.ModuleSourceSpec{
					Type:  gameplanev1alpha1.ModuleSourceTypeLocal,
					Local: &gameplanev1alpha1.LocalSourceSpec{Path: "bundles"},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{Name: uniqueName("v-ok-upload")},
				Spec: gameplanev1alpha1.ModuleSourceSpec{
					Type: gameplanev1alpha1.ModuleSourceTypeUpload,
				},
			},
		} {
			if err := k8sClient.Create(context.Background(), src); err != nil {
				t.Fatalf("valid %s source rejected: %v", src.Spec.Type, err)
			}
			deleteCleanup(t, src)
		}
	})
}

func TestCRDValidation_ConsoleModeRconRequiresRcon(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "v-rcon-mode-no-rcon"},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			DisplayName: "rcon mode without rcon",
			Game:        "busybox",
			Version:     "1",
			Image:       "busybox:1.36",
			ConsoleMode: "rcon",
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); !apierrors.IsInvalid(err) {
		t.Fatalf("expected Invalid for consoleMode=rcon without rcon, got %v", err)
	}

	// consoleMode pty needs no rcon block.
	tmpl = &gameplanev1alpha1.GameTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: "v-pty-mode-no-rcon"},
		Spec: gameplanev1alpha1.GameTemplateSpec{
			DisplayName: "pty mode without rcon",
			Game:        "busybox",
			Version:     "1",
			Image:       "busybox:1.36",
			ConsoleMode: "pty",
		},
	}
	if err := k8sClient.Create(context.Background(), tmpl); err != nil {
		t.Fatalf("consoleMode=pty without rcon rejected: %v", err)
	}
	t.Cleanup(func() {
		_ = k8sClient.Delete(context.Background(), tmpl)
	})
}
