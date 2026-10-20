package i18n

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/pack"
)

func TestStampRecordsSourceHash(t *testing.T) {
	t.Parallel()
	dir := writeModule(t, testModule, testTemplate)
	if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o750); err != nil {
		t.Fatal(err)
	}
	fr := filepath.Join(dir, "locales", "fr.json")
	en := filepath.Join(dir, "locales", "en.json")
	if err := os.WriteFile(fr, []byte(`{"template.displayName":{"defaultMessage":"Jeu"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(en, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}

	stamped, err := Stamp(dir)
	if err != nil || len(stamped) != 1 {
		t.Fatalf("Stamp = %v, %v", stamped, err)
	}
	data, err := os.ReadFile(fr)
	if err != nil {
		t.Fatal(err)
	}
	got, err := pack.ParseEntries(data)
	if err != nil {
		t.Fatal(err)
	}
	if got["template.displayName"].SourceHash != pack.SourceHash("Demo Game") {
		t.Errorf("entry = %+v", got["template.displayName"])
	}
	if b, _ := os.ReadFile(en); string(b) != `{}` {
		t.Errorf("en.json was rewritten: %q", b)
	}

	again, err := Stamp(dir)
	if err != nil || len(again) != 0 {
		t.Fatalf("second Stamp = %v, %v; want a no-op", again, err)
	}
}

func TestStampWithoutLocalesIsNoOp(t *testing.T) {
	t.Parallel()
	stamped, err := Stamp(writeModule(t, testModule, testTemplate))
	if err != nil || len(stamped) != 0 {
		t.Fatalf("Stamp = %v, %v", stamped, err)
	}
}

func TestStampRejectsBadFiles(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{"xx_YY.json": `{}`, "fr.json": `[`} {
		dir := writeModule(t, testModule, testTemplate)
		if err := os.MkdirAll(filepath.Join(dir, "locales"), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "locales", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Stamp(dir); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}
