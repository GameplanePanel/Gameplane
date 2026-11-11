package modsrc

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestForSourceRejectsServiceAccountTokenSecret(t *testing.T) {
	key, knownHosts := testSSHKey(t)
	secret := gitSecret("operator-token", map[string][]byte{
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
			if _, err := ForSource(t.Context(), c, secret.Namespace, source, Options{ConfigMapNamespace: "module-data"}); err == nil {
				t.Fatal("accepted a ServiceAccount token Secret for a module source")
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
