package oci

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestPull_RejectsOversizedKnownLayer(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/bigtemplate"
	reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameTemplate: bytes.Repeat([]byte("a"), MaxTemplateLayerSize+1),
	})
	_, _, err := New(nil, true).Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "over the") {
		t.Fatalf("Pull err = %v, want a layer size error", err)
	}
}

func TestPull_RejectsOversizedMetadataLayer(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/bigmeta"
	reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameMetadata: bytes.Repeat([]byte("a"), MaxMetadataLayerSize+1),
	})
	_, _, err := New(nil, true).Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err == nil {
		t.Fatal("Pull accepted an oversized module.yaml layer")
	}
}

func TestPull_SkipsUnknownLayerTitles(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/unknown"
	reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameTemplate: []byte("apiVersion: x"),
		"surprise.bin":    bytes.Repeat([]byte("z"), 8<<20),
	})
	_, files, err := New(nil, true).Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if _, ok := files["surprise.bin"]; ok || len(files) != 1 {
		t.Fatalf("unknown layer was read: %d files", len(files))
	}
}

func TestLayerSizeLimit(t *testing.T) {
	for title, want := range map[string]int64{
		LayerNameMetadata: MaxMetadataLayerSize,
		LayerNameTemplate: MaxTemplateLayerSize,
		LayerNameReadme:   MaxReadmeLayerSize,
		LayerNameIcon:     MaxIconLayerSize,
	} {
		if got, ok := layerSizeLimit(title); !ok || got != want {
			t.Errorf("layerSizeLimit(%q) = %d, %v; want %d", title, got, ok, want)
		}
	}
	if _, ok := layerSizeLimit("other"); ok {
		t.Error("unknown title reported as known")
	}
	if MaxBundleSize < MaxLocaleTotalSize+MaxTemplateLayerSize {
		t.Error("MaxBundleSize below the locale budget plus the template cap")
	}
}
