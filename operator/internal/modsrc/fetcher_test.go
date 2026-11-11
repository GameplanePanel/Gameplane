package modsrc

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestForSourceRequiresCredentialURLBinding(t *testing.T) {
	key, knownHosts := testSSHKey(t)
	ref := &corev1.LocalObjectReference{Name: "creds"}
	for _, tc := range []struct {
		name, annotation string
		spec             gameplanev1alpha1.ModuleSourceSpec
	}{
		{"http", "gameplane.local/http-source-url", gameplanev1alpha1.ModuleSourceSpec{Type: "http", HTTP: &gameplanev1alpha1.HTTPSourceSpec{URL: "https://example.org/other", SecretRef: ref}}},
		{"git https", "gameplane.local/git-source-url", gameplanev1alpha1.ModuleSourceSpec{Type: "git", Git: &gameplanev1alpha1.GitSourceSpec{URL: "https://example.org/other", SecretRef: ref}}},
		{"git ssh", "gameplane.local/git-source-url", gameplanev1alpha1.ModuleSourceSpec{Type: "git", Git: &gameplanev1alpha1.GitSourceSpec{URL: "git@github.com:other/repo", SecretRef: ref}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			secret := gitSecret(ref.Name, "", map[string][]byte{
				"token": []byte("credential"), "ssh-privatekey": key, "known_hosts": knownHosts,
			})
			for _, binding := range []string{"", "https://example.org/approved", "git@github.com:approved/repo"} {
				secret.Annotations = map[string]string{tc.annotation: binding}
				c := fake.NewClientBuilder().WithObjects(secret).Build()
				_, err := ForSource(t.Context(), c, secret.Namespace, &gameplanev1alpha1.ModuleSource{Spec: tc.spec}, Options{})
				if err == nil || !strings.Contains(err.Error(), tc.annotation) {
					t.Errorf("binding %q: expected rejection naming %s, got %v", binding, tc.annotation, err)
				}
			}
		})
	}
}

func TestForSourceRejectsServiceAccountTokenSecret(t *testing.T) {
	key, knownHosts := testSSHKey(t)
	secret := gitSecret("operator-token", "", map[string][]byte{
		corev1.ServiceAccountTokenKey: []byte("service-account-token"),
		corev1.DockerConfigJsonKey:    []byte(`{"auths":{"example.org":{"username":"user","password":"secret"}}}`),
		"ssh-privatekey":              key,
		"known_hosts":                 knownHosts,
	})
	secret.Type = corev1.SecretTypeServiceAccountToken
	decoy := secret.DeepCopy()
	decoy.Namespace = "module-data"
	decoy.Type = corev1.SecretTypeOpaque
	c := fake.NewClientBuilder().WithObjects(secret, decoy).Build()
	ref := &corev1.LocalObjectReference{Name: secret.Name}
	for name, spec := range map[string]gameplanev1alpha1.ModuleSourceSpec{
		"http":      {Type: "http", HTTP: &gameplanev1alpha1.HTTPSourceSpec{URL: "https://example.org/modules.zip", SecretRef: ref}},
		"git https": {Type: "git", Git: &gameplanev1alpha1.GitSourceSpec{URL: "https://example.org/modules.git", SecretRef: ref}},
		"git ssh":   {Type: "git", Git: &gameplanev1alpha1.GitSourceSpec{URL: "git@github.com:org/modules.git", SecretRef: ref}},
		"oci":       {Type: "oci", OCI: &gameplanev1alpha1.OCISourceSpec{URL: "example.org/modules", PullSecretRef: ref}},
	} {
		t.Run(name, func(t *testing.T) {
			source := &gameplanev1alpha1.ModuleSource{Spec: spec}
			if _, err := ForSource(t.Context(), c, secret.Namespace, source, Options{ConfigMapNamespace: "module-data"}); err == nil || !strings.Contains(err.Error(), "ServiceAccount token") {
				t.Fatalf("expected rejection of ServiceAccount token Secret, got %v", err)
			}
		})
	}
}

func TestForSource_RequiresTypedSpec(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	for _, typ := range []string{
		gameplanev1alpha1.ModuleSourceTypeOCI,
		gameplanev1alpha1.ModuleSourceTypeGit,
		gameplanev1alpha1.ModuleSourceTypeLocal,
		gameplanev1alpha1.ModuleSourceTypeHTTP,
	} {
		t.Run(typ, func(t *testing.T) {
			src := &gameplanev1alpha1.ModuleSource{Spec: gameplanev1alpha1.ModuleSourceSpec{Type: typ}}
			if _, err := ForSource(context.Background(), c, "ns", src, Options{}); err == nil {
				t.Fatalf("expected error for %s type with no matching spec", typ)
			}
		})
	}
}

func TestForSource_UnknownType(t *testing.T) {
	c := fake.NewClientBuilder().Build()
	src := &gameplanev1alpha1.ModuleSource{Spec: gameplanev1alpha1.ModuleSourceSpec{Type: "bogus"}}
	if _, err := ForSource(context.Background(), c, "ns", src, Options{}); err == nil {
		t.Fatal("expected error for an unknown source type")
	}
}

func TestForSource_OCIAndUpload(t *testing.T) {
	c := fake.NewClientBuilder().Build()

	// OCI with an allow-list still builds; the list filters module names.
	ociSrc := &gameplanev1alpha1.ModuleSource{Spec: gameplanev1alpha1.ModuleSourceSpec{
		Type: gameplanev1alpha1.ModuleSourceTypeOCI,
		OCI: &gameplanev1alpha1.OCISourceSpec{
			URL:     "ghcr.io/test/modules",
			Modules: []gameplanev1alpha1.ModuleRef{{Name: "minecraft"}, {Name: "valheim"}},
		},
		Allow: []string{"minecraft"},
	}}
	if _, err := ForSource(context.Background(), c, "ns", ociSrc, Options{}); err != nil {
		t.Fatalf("oci ForSource: %v", err)
	}

	// Upload sources need no typed spec.
	upSrc := &gameplanev1alpha1.ModuleSource{Spec: gameplanev1alpha1.ModuleSourceSpec{
		Type: gameplanev1alpha1.ModuleSourceTypeUpload,
	}}
	if _, err := ForSource(context.Background(), c, "ns", upSrc, Options{}); err != nil {
		t.Fatalf("upload ForSource: %v", err)
	}
}

func TestAllowed(t *testing.T) {
	cases := []struct {
		name  string
		allow []string
		in    string
		want  bool
	}{
		{"empty list allows all", nil, "anything", true},
		{"exact match", []string{"minecraft"}, "minecraft", true},
		{"glob match", []string{"mine*"}, "minecraft", true},
		{"no match", []string{"valheim"}, "minecraft", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := allowed(tc.in, tc.allow); got != tc.want {
				t.Errorf("allowed(%q, %v) = %v, want %v", tc.in, tc.allow, got, tc.want)
			}
		})
	}
}
