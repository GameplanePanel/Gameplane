package preview

import (
	"strings"
	"testing"
)

// secPreviewTemplate returns a minimal template.yaml with the given spec.security block.
func secPreviewTemplate(security string) []byte {
	return []byte("apiVersion: gameplane.local/v1alpha1\n" +
		"kind: GameTemplate\n" +
		"metadata:\n  name: sec-demo\n" +
		"spec:\n" +
		"  game: sec-demo\n" +
		"  displayName: Security Demo\n" +
		"  image: example/sec-demo:1\n" +
		"  storage:\n    size: 1Gi\n    mountPath: /data\n" +
		security)
}

func TestPreview_SecurityResolvedDefaults(t *testing.T) {
	res, err := GeneratePreview(Options{TemplateYAML: secPreviewTemplate(
		"  security:\n" +
			"    runAsUser: 2000\n" +
			"    runAsGroup: 2000\n" +
			"    fsGroup: 1000\n")})
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	if res.Security == nil {
		t.Fatal("Security = nil, want resolved block")
	}
	s := res.Security
	if s.RunAsUser == nil || *s.RunAsUser != 2000 {
		t.Errorf("RunAsUser = %v, want 2000", s.RunAsUser)
	}
	if s.RunAsGroup == nil || *s.RunAsGroup != 2000 {
		t.Errorf("RunAsGroup = %v, want 2000", s.RunAsGroup)
	}
	if s.FSGroup == nil || *s.FSGroup != 1000 {
		t.Errorf("FSGroup = %v, want 1000", s.FSGroup)
	}
	if s.VolumeOwnershipCheck != "OnlyWhenWrong" || s.OwnershipCheckSource != "built-in default" {
		t.Errorf("ownership = %q (%q), want OnlyWhenWrong (built-in default)", s.VolumeOwnershipCheck, s.OwnershipCheckSource)
	}
	if s.OwnershipCheckNoEffect {
		t.Error("OwnershipCheckNoEffect = true with fsGroup set")
	}
	if len(s.Risky) != 0 {
		t.Errorf("Risky = %+v, want none", s.Risky)
	}
}

func TestPreview_SecurityExplicitOwnershipCheck(t *testing.T) {
	res, err := GeneratePreview(Options{TemplateYAML: secPreviewTemplate(
		"  security:\n" +
			"    fsGroup: 1000\n" +
			"    volumeOwnershipCheck: EveryStart\n")})
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	s := res.Security
	if s == nil {
		t.Fatal("Security = nil")
	}
	if s.VolumeOwnershipCheck != "EveryStart" || s.OwnershipCheckSource != "template" {
		t.Errorf("ownership = %q (%q), want EveryStart (template)", s.VolumeOwnershipCheck, s.OwnershipCheckSource)
	}
	if s.RunAsUser != nil {
		t.Errorf("RunAsUser = %v, want unset (image default)", s.RunAsUser)
	}
}

func TestPreview_SecurityOwnershipWithoutFSGroupHasNoEffect(t *testing.T) {
	res, err := GeneratePreview(Options{TemplateYAML: secPreviewTemplate(
		"  security:\n    volumeOwnershipCheck: EveryStart\n")})
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	s := res.Security
	if s == nil {
		t.Fatal("Security = nil")
	}
	if !s.OwnershipCheckNoEffect {
		t.Error("OwnershipCheckNoEffect = false, want true without fsGroup")
	}
	out := res.FormatHuman()
	if !strings.Contains(out, "EveryStart (no effect: fsGroup is not set)") {
		t.Errorf("FormatHuman missing no-effect marker:\n%s", out)
	}
}

func TestPreview_SecurityNoFSGroupNoPolicyIsNotApplicable(t *testing.T) {
	res, err := GeneratePreview(Options{TemplateYAML: secPreviewTemplate(
		"  security:\n    runAsUser: 1000\n")})
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	out := res.FormatHuman()
	if !strings.Contains(out, "volumeOwnershipCheck: not applicable (no fsGroup)") {
		t.Errorf("FormatHuman missing not-applicable line:\n%s", out)
	}
}

func TestPreview_SecurityRiskyWithReasons(t *testing.T) {
	res, err := GeneratePreview(Options{TemplateYAML: secPreviewTemplate(
		"  security:\n" +
			"    runAsUser: 2000\n" +
			"    fsGroup: 1000\n" +
			"    risky:\n" +
			"      fsGroup:\n" +
			"        reason: The data directory is owned by gid 1000.\n" +
			"      runAsUser:\n" +
			"        reason: The image only starts as uid 2000.\n" +
			"      volumeOwnershipCheck: {}\n")})
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	risky := res.Security.Risky
	if len(risky) != 3 {
		t.Fatalf("Risky = %+v, want 3 entries", risky)
	}
	// Fixed order: runAsUser, runAsGroup, fsGroup, volumeOwnershipCheck.
	wantOrder := []string{"runAsUser", "fsGroup", "volumeOwnershipCheck"}
	for i, want := range wantOrder {
		if risky[i].Setting != want {
			t.Errorf("Risky[%d].Setting = %q, want %q", i, risky[i].Setting, want)
		}
	}
	if risky[0].Reason != "The image only starts as uid 2000." {
		t.Errorf("runAsUser reason = %q", risky[0].Reason)
	}
	if risky[2].Reason != "" {
		t.Errorf("volumeOwnershipCheck reason = %q, want empty", risky[2].Reason)
	}

	out := res.FormatHuman()
	for _, want := range []string{
		"runAsUser:",
		"2000 (module default)",
		"risky settings:",
		"- runAsUser: The image only starts as uid 2000.",
		"- fsGroup: The data directory is owned by gid 1000.",
		"- volumeOwnershipCheck: no reason given",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("FormatHuman missing %q:\n%s", want, out)
		}
	}
}

func TestPreview_SecurityAbsentPrintsImageDefault(t *testing.T) {
	res, err := GeneratePreview(Options{TemplateYAML: secPreviewTemplate("")})
	if err != nil {
		t.Fatalf("GeneratePreview: %v", err)
	}
	if res.Security != nil {
		t.Errorf("Security = %+v, want nil without a security block", res.Security)
	}
	out := res.FormatHuman()
	if !strings.Contains(out, "image default (template sets no security block)") {
		t.Errorf("FormatHuman missing image-default line:\n%s", out)
	}

	js, err := res.ToJSON()
	if err != nil {
		t.Fatalf("ToJSON: %v", err)
	}
	if strings.Contains(string(js), `"security"`) {
		t.Errorf("JSON gained a security key for a template without one:\n%s", js)
	}
}
