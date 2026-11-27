package oci

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"oras.land/oras-go/v2/content"
)

const fixtureModuleYAML = `apiVersion: gameplane.local/module/v1
name: minecraft-java
displayName: Minecraft (Java Edition)
version: 1.0.0
game: minecraft-java
summary: Vanilla / Paper / Forge / Fabric
`

const fixtureTemplateYAML = `apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
spec:
  displayName: Minecraft (Java Edition)
  game: minecraft-java
  version: 1.0.0
  image: itzg/minecraft-server:2025.1.0
`

func TestClient_Pull(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/minecraft-java"
	digest := reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameMetadata: []byte(fixtureModuleYAML),
		LayerNameTemplate: []byte(fixtureTemplateYAML),
		LayerNameReadme:   []byte("# Minecraft\n"),
	})

	c := New(nil, true)
	gotDigest, files, err := c.Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if gotDigest != digest {
		t.Errorf("digest = %q, want %q", gotDigest, digest)
	}
	if !strings.Contains(string(files[LayerNameMetadata]), "name: minecraft-java") {
		t.Errorf("metadata layer missing name; got %q", files[LayerNameMetadata])
	}
	if !strings.Contains(string(files[LayerNameTemplate]), "itzg/minecraft-server") {
		t.Errorf("template missing image line; got %q", files[LayerNameTemplate])
	}
	if !strings.Contains(string(files[LayerNameReadme]), "Minecraft") {
		t.Errorf("readme missing; got %q", files[LayerNameReadme])
	}
}

// TestClient_Pull_RequiresManifestToMatchDigest checks that Pull accepts
// a manifest only when its bytes hash to the digest Pull reports.
func TestClient_Pull_RequiresManifestToMatchDigest(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/manifest-digest"
	reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameMetadata: []byte(fixtureModuleYAML),
		LayerNameTemplate: []byte(fixtureTemplateYAML),
	})
	reg.pushBundle(repo, "2.0.0", map[string][]byte{
		LayerNameMetadata: []byte(fixtureModuleYAML),
		LayerNameTemplate: []byte(strings.ReplaceAll(fixtureTemplateYAML, "2025.1.0", "2025.2.0")),
	})
	reg.serveBody("1.0.0", reg.storedManifest(repo, "2.0.0"))

	_, files, err := New(nil, true).Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err == nil {
		t.Fatalf("Pull returned %d files for a manifest that does not match its digest; want an error", len(files))
	}
	if !strings.Contains(err.Error(), "read manifest") {
		t.Fatalf("want a manifest read error, got %v", err)
	}
}

// TestClient_Pull_RequiresLayersToMatchDigest checks that Pull accepts a
// layer only when its bytes hash to the digest the manifest lists.
func TestClient_Pull_RequiresLayersToMatchDigest(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/layer-digest"
	template := []byte(fixtureTemplateYAML)
	reg.pushBundle(repo, "1.0.0", map[string][]byte{
		LayerNameMetadata: []byte(fixtureModuleYAML),
		LayerNameTemplate: template,
	})
	// Equal length, so the digest comparison is the check under test.
	reg.serveBody(digestFor(template).String(), bytes.ToUpper(template))

	_, _, err := New(nil, true).Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if !errors.Is(err, content.ErrMismatchedDigest) {
		t.Fatalf("want content.ErrMismatchedDigest for a layer that does not match its digest, got %v", err)
	}
}

func TestClient_Pull_RejectsForeignArtifact(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/bogus"
	// Push a manifest with a different artifactType to simulate a
	// non-Gameplane bundle pushed to the same registry.
	manifest := []byte(`{"mediaType":"application/vnd.oci.image.manifest.v1+json",` +
		`"artifactType":"application/vnd.something.else","config":{"mediaType":"application/json","digest":"sha256:` +
		emptyDigestHex + `","size":2},"layers":[]}`)
	reg.pushManifest(repo, "1.0.0", manifest)
	_ = reg.putBlob([]byte(`{}`))

	_, _, err := New(nil, true).Pull(context.Background(), reg.host()+"/"+repo, "1.0.0")
	if err == nil || !strings.Contains(err.Error(), "artifactType") {
		t.Fatalf("want artifactType rejection, got %v", err)
	}
}

func TestClient_ListTags_DropsNonSemverAndSorts(t *testing.T) {
	reg := newFakeRegistry(t)
	repo := "gameplane/multi"
	for _, tag := range []string{"1.0.0", "1.2.0", "0.9.0", "latest", "main"} {
		reg.pushBundle(repo, tag, map[string][]byte{
			LayerNameMetadata: []byte(fixtureModuleYAML),
			LayerNameTemplate: []byte(fixtureTemplateYAML),
		})
	}
	tags, err := New(nil, true).ListTags(context.Background(), reg.host()+"/"+repo)
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	wantOrdered := []string{"1.2.0", "1.0.0", "0.9.0"}
	if len(tags) != len(wantOrdered) {
		t.Fatalf("ListTags = %v, want %v", tags, wantOrdered)
	}
	for i, w := range wantOrdered {
		if tags[i] != w {
			t.Errorf("tags[%d] = %q, want %q (full=%v)", i, tags[i], w, tags)
		}
	}
}

const emptyDigestHex = "44136fa355b3678a1146ad16f7e8649e94fb4fc21fe77e8310c060f61caaff8a"
