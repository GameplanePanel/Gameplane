package oci

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// pushDeclared pushes a manifest of artifactType whose layers declare the
// given titles and sizes. Blobs are stored only for layers whose data is
// non-nil, so a size check that runs before any fetch can be exercised with
// declared sizes far larger than the test stores.
func pushDeclared(t *testing.T, reg *fakeRegistry, repo, artifactType string, layers []ocispec.Descriptor, data map[string][]byte) {
	t.Helper()
	emptyConfig := []byte(`{}`)
	reg.putBlob(emptyConfig)
	manifest := ocispec.Manifest{
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: artifactType,
		Config: ocispec.Descriptor{
			MediaType: MediaTypeConfig,
			Digest:    digestFor(emptyConfig),
			Size:      int64(len(emptyConfig)),
		},
	}
	for _, l := range layers {
		title := l.Annotations[AnnotationTitle]
		if b, ok := data[title]; ok {
			l.Digest = digestFromString(reg.putBlob(b))
			l.Size = int64(len(b))
		} else if l.Digest == "" {
			l.Digest = digestFor([]byte(title))
		}
		manifest.Layers = append(manifest.Layers, l)
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	reg.pushManifest(repo, "1.0.0", mb)
}

func titled(title, mediaType string, size int64) ocispec.Descriptor {
	return ocispec.Descriptor{MediaType: mediaType, Size: size, Annotations: map[string]string{AnnotationTitle: title}}
}

func TestPull_RejectsNegativeLayerSize(t *testing.T) {
	reg := newFakeRegistry(t)
	pushDeclared(t, reg, "gameplane/negative", "application/vnd.example.theme",
		[]ocispec.Descriptor{titled("theme.yaml", "text/yaml", -1)}, nil)
	_, _, err := New(nil, true).WithArtifactType("application/vnd.example.theme").
		Pull(context.Background(), reg.host()+"/gameplane/negative", "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "negative size") {
		t.Fatalf("Pull err = %v, want a negative size error", err)
	}
}

func TestPull_RejectsModuleBundleOverTotal(t *testing.T) {
	// Repeated template.yaml layers each stay within their own cap but
	// together exceed MaxBundleSize.
	reg := newFakeRegistry(t)
	var layers []ocispec.Descriptor
	for range MaxBundleSize/MaxTemplateLayerSize + 1 {
		layers = append(layers, titled(LayerNameTemplate, MediaTypeTemplate, MaxTemplateLayerSize))
	}
	pushDeclared(t, reg, "gameplane/overtotal", ArtifactType, layers, map[string][]byte{LayerNameTemplate: bytes.Repeat([]byte("t"), MaxTemplateLayerSize)})
	_, _, err := New(nil, true).Pull(context.Background(), reg.host()+"/gameplane/overtotal", "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "bundle layers exceed") {
		t.Fatalf("Pull err = %v, want a bundle total error", err)
	}
}

func TestPull_OtherArtifactTypeReadsItsOwnFiles(t *testing.T) {
	// The module per-title caps must not skip theme package files.
	reg := newFakeRegistry(t)
	data := map[string][]byte{"theme.yaml": []byte("name: x"), "theme.css": []byte(":root{}")}
	pushDeclared(t, reg, "gameplane/theme", "application/vnd.example.theme", []ocispec.Descriptor{
		titled("theme.yaml", "text/yaml", 0),
		titled("theme.css", "text/css", 0),
	}, data)
	_, files, err := New(nil, true).WithArtifactType("application/vnd.example.theme").
		WithMaxLayerBytes(1024).WithMaxTotalBytes(1024).
		Pull(context.Background(), reg.host()+"/gameplane/theme", "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if string(files["theme.yaml"]) != "name: x" || string(files["theme.css"]) != ":root{}" {
		t.Fatalf("theme files = %q / %q, want both read", files["theme.yaml"], files["theme.css"])
	}
}
