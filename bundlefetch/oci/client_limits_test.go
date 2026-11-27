package oci

import (
	"context"
	"strings"
	"testing"
)

// limitBundle pushes a bundle with two layers of 10 and 20 bytes.
func limitBundle(t *testing.T) (*fakeRegistry, string) {
	t.Helper()
	reg := newFakeRegistry(t)
	repo := "gameplane/limits"
	reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameMetadata: []byte(strings.Repeat("m", 10)),
		LayerNameTemplate: []byte(strings.Repeat("t", 20)),
	})
	return reg, reg.host() + "/" + repo
}

func TestClient_PullMaxLayerBytes(t *testing.T) {
	_, ref := limitBundle(t)

	if _, _, err := New(nil, true).WithMaxLayerBytes(20).Pull(context.Background(), ref, "1.0.0"); err != nil {
		t.Fatalf("Pull at the layer cap: %v", err)
	}
	_, _, err := New(nil, true).WithMaxLayerBytes(19).Pull(context.Background(), ref, "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "limit is 19") {
		t.Fatalf("Pull over the layer cap: err = %v, want a layer limit error", err)
	}
}

func TestClient_PullMaxTotalBytes(t *testing.T) {
	_, ref := limitBundle(t)

	_, files, err := New(nil, true).WithMaxTotalBytes(30).Pull(context.Background(), ref, "1.0.0")
	if err != nil {
		t.Fatalf("Pull at the total cap: %v", err)
	}
	if len(files[LayerNameMetadata]) != 10 || len(files[LayerNameTemplate]) != 20 {
		t.Fatalf("files = %d/%d bytes, want 10/20", len(files[LayerNameMetadata]), len(files[LayerNameTemplate]))
	}
	_, _, err = New(nil, true).WithMaxTotalBytes(29).Pull(context.Background(), ref, "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "29 byte total limit") {
		t.Fatalf("Pull over the total cap: err = %v, want a total limit error", err)
	}
}

func TestClient_PullArtifactType(t *testing.T) {
	_, ref := limitBundle(t)

	_, _, err := New(nil, true).WithArtifactType("application/vnd.example.other").Pull(context.Background(), ref, "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "unexpected artifactType") {
		t.Fatalf("Pull with another artifact type: err = %v, want an artifactType error", err)
	}
	if _, _, err := New(nil, true).WithArtifactType(ArtifactType).Pull(context.Background(), ref, "1.0.0"); err != nil {
		t.Fatalf("Pull with the bundle's artifact type: %v", err)
	}
}
