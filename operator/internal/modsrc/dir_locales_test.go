package modsrc

import (
	"context"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

func localesFS(frBody string) fstest.MapFS {
	fsys := moduleDirFS(map[string]map[string]string{"mc": validModuleFiles("mc", "1.0.0")})
	fsys["mc/locales/fr.json"] = &fstest.MapFile{Data: []byte(frBody)}
	fsys["mc/locales/en.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	fsys["mc/locales/sub/x.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	fsys["mc/locales/readme.txt"] = &fstest.MapFile{Data: []byte(`hi`)}
	fsys["mc/locales/de.json"] = &fstest.MapFile{Data: []byte(strings.Repeat("x", oci.MaxLocaleLayerSize+1))}
	return fsys
}

func TestBundleFromDirReadsLocaleFiles(t *testing.T) {
	t.Parallel()
	b, err := bundleFromDir(localesFS(`{"a":1}`), "mc", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(b.Locales) != 1 || string(b.Locales["fr"]) != `{"a":1}` {
		t.Fatalf("Locales = %v, want fr only", b.Locales)
	}
	if len(b.OversizedLocales) != 1 || b.OversizedLocales[0] != "de" {
		t.Fatalf("OversizedLocales = %v, want [de]", b.OversizedLocales)
	}
}

func TestBundleFromDirDigestTracksLocaleChanges(t *testing.T) {
	t.Parallel()
	a, err := bundleFromDir(localesFS(`{"a":1}`), "mc", "")
	if err != nil {
		t.Fatal(err)
	}
	b, err := bundleFromDir(localesFS(`{"a":2}`), "mc", "")
	if err != nil {
		t.Fatal(err)
	}
	if a.Digest == b.Digest {
		t.Fatal("digest did not change when fr.json changed")
	}
}

func TestFSFetcherPullCarriesLocales(t *testing.T) {
	t.Parallel()
	f := newFSFetcher(staticFS(localesFS(`{"a":1}`), ""), "local:x", nil)
	b, err := f.Pull(context.Background(), "mc", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if string(b.Locales["fr"]) != `{"a":1}` || len(b.OversizedLocales) != 1 {
		t.Fatalf("bundle locales = %v oversized = %v", b.Locales, b.OversizedLocales)
	}
}
