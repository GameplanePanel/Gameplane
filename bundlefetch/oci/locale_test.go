package oci

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestLocaleTag(t *testing.T) {
	cases := []struct {
		title string
		tag   string
		ok    bool
	}{
		{"locales/fr.json", "fr", true},
		{"locales/pt-BR.json", "pt-BR", true},
		{"locales/zh-Hans-CN.json", "zh-Hans-CN", true},
		{"locales/.json", "", false},
		{"locales/fr.txt", "", false},
		{"locales/a/b.json", "", false},
		{"fr.json", "", false},
		{"module.yaml", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		tag, ok := LocaleTag(c.title)
		if tag != c.tag || ok != c.ok {
			t.Errorf("LocaleTag(%q) = %q, %v; want %q, %v", c.title, tag, ok, c.tag, c.ok)
		}
	}
}

func TestLocaleLayers(t *testing.T) {
	got := LocaleLayers(map[string][]byte{
		"module.yaml":        []byte("m"),
		"locales/en.json":    []byte("en"),
		"locales/fr.json":    []byte("fr"),
		"locales/pt-BR.json": []byte("pt"),
		"locales/x/y.json":   []byte("nested"),
		"locales/notes.txt":  []byte("txt"),
	})
	if len(got) != 2 || string(got["fr"]) != "fr" || string(got["pt-BR"]) != "pt" {
		t.Fatalf("LocaleLayers = %v", got)
	}
	if got := LocaleLayers(nil); len(got) != 0 {
		t.Fatalf("LocaleLayers(nil) = %v", got)
	}
}

type typedLayer struct {
	mediaType string
	data      []byte
}

// pushTyped pushes a bundle whose layers carry explicit media types, which
// pushBundle does not support for locale layers.
func (r *fakeRegistry) pushTyped(repo, tag string, layers map[string]typedLayer) string {
	r.t.Helper()
	empty := []byte(`{}`)
	r.putBlob(empty)
	manifest := ocispec.Manifest{
		MediaType:    ocispec.MediaTypeImageManifest,
		ArtifactType: ArtifactType,
		Config: ocispec.Descriptor{
			MediaType: MediaTypeConfig,
			Digest:    digestFor(empty),
			Size:      int64(len(empty)),
		},
	}
	for name, l := range layers {
		d := r.putBlob(l.data)
		manifest.Layers = append(manifest.Layers, ocispec.Descriptor{
			MediaType:   l.mediaType,
			Digest:      digestFromString(d),
			Size:        int64(len(l.data)),
			Annotations: map[string]string{AnnotationTitle: name},
		})
	}
	mb, err := json.Marshal(manifest)
	if err != nil {
		r.t.Fatalf("marshal manifest: %v", err)
	}
	return r.pushManifest(repo, tag, mb)
}

func TestClient_Pull_LocaleLayers(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/locales"
	big := []byte(strings.Repeat("x", MaxLocaleLayerSize+1))
	reg.pushTyped(repo, "1.0.0", map[string]typedLayer{
		LayerNameMetadata:      {MediaTypeMetadata, []byte(fixtureModuleYAML)},
		LayerNameTemplate:      {MediaTypeTemplate, []byte(fixtureTemplateYAML)},
		"locales/fr.json":      {MediaTypeLocale, []byte(`{"a":{"defaultMessage":"x"}}`)},
		"locales/de.json":      {"application/octet-stream", []byte(`{}`)},
		"locales/it.json":      {MediaTypeLocale, big},
		"locales/pt-BR.json":   {MediaTypeLocale, []byte(`{}`)},
		"locales/not-a-layer/": {MediaTypeLocale, []byte(`{}`)},
	})

	c := New(nil, true)
	_, files, err := c.Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if _, ok := files["locales/fr.json"]; !ok {
		t.Error("typed locale layer was not returned")
	}
	if _, ok := files["locales/pt-BR.json"]; !ok {
		t.Error("pt-BR locale layer was not returned")
	}
	if _, ok := files["locales/de.json"]; ok {
		t.Error("mistyped locale layer was returned")
	}
	if !IsOversizedLocale(files["locales/it.json"]) {
		t.Errorf("over-size locale layer = %q, want the oversized marker", files["locales/it.json"])
	}
	locales, oversized := SplitLocaleLayers(files)
	if len(locales) != 2 {
		t.Errorf("SplitLocaleLayers locales = %d entries, want 2", len(locales))
	}
	if len(oversized) != 1 || oversized[0] != "it" {
		t.Errorf("oversized = %v, want [it]", oversized)
	}
}

func TestSplitLocaleLayers(t *testing.T) {
	cases := []struct {
		name      string
		files     map[string][]byte
		locales   []string
		oversized []string
	}{
		{"nil", nil, nil, nil},
		{"plain", map[string][]byte{"locales/fr.json": []byte("x")}, []string{"fr"}, nil},
		{"oversized sorted", map[string][]byte{
			"locales/fr.json": []byte("x"),
			"locales/de.json": OversizedLocale,
			"locales/ca.json": OversizedLocale,
		}, []string{"fr"}, []string{"ca", "de"}},
		{"en skipped", map[string][]byte{"locales/en.json": OversizedLocale}, nil, nil},
		{"non-locale ignored", map[string][]byte{"module.yaml": OversizedLocale}, nil, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			locales, oversized := SplitLocaleLayers(c.files)
			if len(locales) != len(c.locales) {
				t.Fatalf("locales = %v, want %v", locales, c.locales)
			}
			for _, tag := range c.locales {
				if _, ok := locales[tag]; !ok {
					t.Errorf("missing %s", tag)
				}
			}
			if len(oversized) != len(c.oversized) {
				t.Fatalf("oversized = %v, want %v", oversized, c.oversized)
			}
			for i := range oversized {
				if oversized[i] != c.oversized[i] {
					t.Errorf("oversized = %v, want %v", oversized, c.oversized)
				}
			}
		})
	}
}
