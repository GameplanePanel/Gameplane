package modsrc

import (
	"bytes"
	"testing"
	"testing/fstest"

	"github.com/GameplanePanel/gameplane/operator/internal/oci"
)

func TestReadLocaleFilesTotalBudget(t *testing.T) {
	body := bytes.Repeat([]byte("x"), 800*1024)
	fsys := fstest.MapFS{}
	for _, tag := range []string{"aa", "bb", "cc", "dd", "ee", "ff"} {
		fsys["m/locales/"+tag+".json"] = &fstest.MapFile{Data: body}
	}
	files := map[string][]byte{}
	readLocaleFiles(fsys, "m", files)

	locales, oversized := oci.SplitLocaleLayers(files)
	if len(locales) != 5 {
		t.Errorf("read %d locale files, want 5", len(locales))
	}
	if len(oversized) != 1 || oversized[0] != "ff" {
		t.Errorf("oversized = %v, want [ff]", oversized)
	}
}

func TestReadLocaleFilesSmallerLanguageStillFits(t *testing.T) {
	big := bytes.Repeat([]byte("x"), 800*1024)
	fsys := fstest.MapFS{}
	for _, tag := range []string{"aa", "bb", "cc", "dd", "ee"} {
		fsys["m/locales/"+tag+".json"] = &fstest.MapFile{Data: big}
	}
	fsys["m/locales/ff.json"] = &fstest.MapFile{Data: big}
	fsys["m/locales/gg.json"] = &fstest.MapFile{Data: []byte(`{}`)}
	files := map[string][]byte{}
	readLocaleFiles(fsys, "m", files)

	if !oci.IsOversizedLocale(files["locales/ff.json"]) {
		t.Error("ff should be oversized")
	}
	if string(files["locales/gg.json"]) != `{}` {
		t.Errorf("gg = %q, want the small file read", files["locales/gg.json"])
	}
}
