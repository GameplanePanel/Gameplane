package modsrc

import (
	"context"
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

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
