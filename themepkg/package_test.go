package themepkg

import (
	"encoding/binary"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tokenValues are the 14 token names with valid lowercase colors, in contract order.
var tokenValues = []struct{ name, value string }{
	{"background", "#0b1220"},
	{"foreground", "#e6edf7"},
	{"surface", "#111a2e"},
	{"surface-secondary", "#16213a"},
	{"surface-tertiary", "#1c2946"},
	{"overlay", "#0f172a"},
	{"accent", "#38bdf8"},
	{"accent-foreground", "#04121d"},
	{"accent-soft", "#0c2a3d"},
	{"accent-soft-foreground", "#a5e3ff"},
	{"muted", "#8a9bb8"},
	{"border", "#24324f"},
	{"focus", "#38bdf8"},
	{"link", "#7dd3fc"},
}

// themeYAML builds a valid theme.yaml with the given token modes.
func themeYAML(modes ...string) string {
	var b strings.Builder
	b.WriteString("apiVersion: gameplane.local/theme/v1\n" +
		"name: deep-ocean\n" +
		"version: 1.2.0\n" +
		"displayName: Deep Ocean\n" +
		"author: Jane Doe\n" +
		"license: MIT\n" +
		"tokens:\n")
	for _, mode := range modes {
		b.WriteString("  " + mode + ":\n")
		for _, tv := range tokenValues {
			b.WriteString("    " + tv.name + ": \"" + tv.value + "\"\n")
		}
	}
	return b.String()
}

// pngHeader returns the first 24 bytes of a PNG file with an IHDR of the given
// size. PreviewInfo reads only the header, so no pixel data is needed.
func pngHeader(w, h uint32) []byte {
	b := []byte("\x89PNG\r\n\x1a\n")
	b = binary.BigEndian.AppendUint32(b, 13)
	b = append(b, "IHDR"...)
	b = binary.BigEndian.AppendUint32(b, w)
	b = binary.BigEndian.AppendUint32(b, h)
	return append(b, 8, 6, 0, 0, 0)
}

// webpChunk wraps payload in a RIFF/WEBP file whose first chunk is chunk.
func webpChunk(chunk string, payload []byte) []byte {
	b := []byte("RIFF")
	b = binary.LittleEndian.AppendUint32(b, uint32(4+8+len(payload)))
	b = append(b, "WEBP"...)
	b = append(b, chunk...)
	b = binary.LittleEndian.AppendUint32(b, uint32(len(payload)))
	return append(b, payload...)
}

// vp8Payload is a lossy VP8 frame header with the given dimensions.
func vp8Payload(w, h uint16) []byte {
	b := []byte{0, 0, 0, 0x9d, 0x01, 0x2a}
	b = binary.LittleEndian.AppendUint16(b, w)
	return binary.LittleEndian.AppendUint16(b, h)
}

// vp8lPayload is a lossless VP8L header with the given dimensions.
func vp8lPayload(w, h int) []byte {
	bits := uint32(w-1) | uint32(h-1)<<14
	return binary.LittleEndian.AppendUint32([]byte{0x2f}, bits)
}

// vp8xPayload is an extended header with the given canvas dimensions.
func vp8xPayload(w, h int) []byte {
	b := make([]byte, 4, 10)
	b = append(b, byte(w-1), byte((w-1)>>8), byte((w-1)>>16))
	return append(b, byte(h-1), byte((h-1)>>8), byte((h-1)>>16))
}

// oversizedPNG is a PNG header followed by padding past MaxPreviewBytes.
func oversizedPNG() []byte {
	b := append(pngHeader(2, 2), make([]byte, MaxPreviewBytes)...)
	return b
}

// loadPackageDir reads every file under dir into a map keyed by the slash path
// relative to dir.
func loadPackageDir(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	files := map[string][]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = data
		return nil
	})
	if err != nil {
		t.Fatalf("load %s: %v", dir, err)
	}
	return files
}

func hasRule(probs []Problem, rule string) bool {
	for _, p := range probs {
		if p.Rule == rule {
			return true
		}
	}
	return false
}

func TestPreviewInfo(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		data    []byte
		wantCT  string
		wantW   int
		wantH   int
		wantErr bool
	}{
		{name: "png", data: pngHeader(1600, 1000), wantCT: "image/png", wantW: 1600, wantH: 1000},
		{name: "png signature only", data: []byte("\x89PNG\r\n\x1a\n"), wantErr: true},
		{name: "png truncated in header", data: append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 12)...), wantErr: true},
		{name: "png first chunk not IHDR", data: func() []byte {
			b := pngHeader(2, 2)
			copy(b[12:16], "IDAT")
			return b
		}(), wantErr: true},
		{name: "png IHDR length not 13", data: func() []byte {
			b := pngHeader(2, 2)
			binary.BigEndian.PutUint32(b[8:12], 12)
			return b
		}(), wantErr: true},
		{name: "webp lossy", data: webpChunk("VP8 ", vp8Payload(320, 200)), wantCT: "image/webp", wantW: 320, wantH: 200},
		{name: "webp lossy width masked to 14 bits", data: webpChunk("VP8 ", vp8Payload(0x4000|5, 7)), wantCT: "image/webp", wantW: 5, wantH: 7},
		{name: "webp lossy truncated", data: webpChunk("VP8 ", vp8Payload(320, 200))[:25], wantErr: true},
		{name: "webp lossy missing start code", data: func() []byte {
			b := webpChunk("VP8 ", vp8Payload(320, 200))
			b[23] = 0
			return b
		}(), wantErr: true},
		{name: "webp lossless", data: webpChunk("VP8L", vp8lPayload(1000, 500)), wantCT: "image/webp", wantW: 1000, wantH: 500},
		{name: "webp lossless truncated", data: webpChunk("VP8L", []byte{0x2f, 1})[:22], wantErr: true},
		{name: "webp lossless missing signature", data: func() []byte {
			b := webpChunk("VP8L", vp8lPayload(1000, 500))
			b[20] = 0x2e
			return b
		}(), wantErr: true},
		{name: "webp extended", data: webpChunk("VP8X", vp8xPayload(1600, 1000)), wantCT: "image/webp", wantW: 1600, wantH: 1000},
		{name: "webp extended truncated", data: webpChunk("VP8X", make([]byte, 4)), wantErr: true},
		{name: "webp unknown first chunk", data: webpChunk("ALPH", make([]byte, 10)), wantErr: true},
		{name: "webp chunk header truncated", data: append([]byte("RIFF\x00\x00\x00\x00WEBP"), "VP8"...), wantErr: true},
		{name: "riff but not webp", data: []byte("RIFF\x00\x00\x00\x00WAVEfmt  "), wantErr: true},
		{name: "garbage", data: []byte("not an image at all"), wantErr: true},
		{name: "empty", data: nil, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ct, w, h, err := PreviewInfo(tt.data)
			if (err != nil) != tt.wantErr {
				t.Fatalf("PreviewInfo error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if ct != tt.wantCT || w != tt.wantW || h != tt.wantH {
				t.Fatalf("PreviewInfo = (%q, %d, %d), want (%q, %d, %d)", ct, w, h, tt.wantCT, tt.wantW, tt.wantH)
			}
		})
	}
}

func TestPreviewInfoGeneratedPNG(t *testing.T) {
	t.Parallel()
	data, err := os.ReadFile(filepath.Join("testdata", "good", "previews", "dark.png"))
	if err != nil {
		t.Fatal(err)
	}
	ct, w, h, err := PreviewInfo(data)
	if err != nil {
		t.Fatalf("PreviewInfo: %v", err)
	}
	if ct != "image/png" || w != 2 || h != 2 {
		t.Fatalf("PreviewInfo = (%q, %d, %d), want (image/png, 2, 2)", ct, w, h)
	}
}

func TestValidateGoodPackage(t *testing.T) {
	t.Parallel()
	files := loadPackageDir(t, filepath.Join("testdata", "good"))
	res, probs := Validate(files)
	if len(probs) != 0 {
		t.Fatalf("good package problems: %v", probs)
	}
	prev, ok := res.Previews["dark"]
	if !ok {
		t.Fatal("Previews[dark] not set")
	}
	if prev.ContentType != "image/png" || prev.Width != 2 || prev.Height != 2 || prev.Path != "previews/dark.png" || len(prev.Data) == 0 {
		t.Fatalf("Previews[dark] = %+v", prev)
	}
	if res.Manifest.Name != "deep-ocean" {
		t.Fatalf("Manifest.Name = %q", res.Manifest.Name)
	}
	if res.CSS != "" || res.LowContrast {
		t.Fatalf("CSS = %q, LowContrast = %v", res.CSS, res.LowContrast)
	}
}

func TestValidateBadPackages(t *testing.T) {
	t.Parallel()
	tests := []struct {
		dir    string
		rule   string
		extend func(map[string][]byte)
	}{
		{dir: "schema", rule: RuleSchema},
		{dir: "tokens-names", rule: RuleTokensNames},
		{dir: "tokens-format", rule: RuleTokensFormat},
		{dir: "css-size", rule: RuleCSSSize, extend: func(f map[string][]byte) {
			f["theme.css"] = []byte(strings.Repeat("a{}\n", 8200))
		}},
		{dir: "css-import", rule: RuleCSSImport},
		{dir: "css-url", rule: RuleCSSURL},
		{dir: "css-remote", rule: RuleCSSRemote},
		{dir: "css-syntax", rule: RuleCSSSyntax},
		{dir: "preview-missing", rule: RulePreviewMissing},
		{dir: "preview-format", rule: RulePreviewFormat},
		{dir: "files-extra", rule: RuleFilesExtra},
		{dir: "size-total", rule: RuleSizeTotal, extend: func(f map[string][]byte) {
			f["padding.bin"] = make([]byte, MaxPackageBytes)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.dir, func(t *testing.T) {
			t.Parallel()
			files := loadPackageDir(t, filepath.Join("testdata", "bad", tt.dir))
			if tt.extend != nil {
				tt.extend(files)
			}
			_, probs := Validate(files)
			if !hasRule(probs, tt.rule) {
				t.Fatalf("want rule %s, got %v", tt.rule, probs)
			}
		})
	}
}

func TestValidateCases(t *testing.T) {
	t.Parallel()
	// base returns a fresh valid dark-only package for each case.
	base := func() map[string][]byte {
		return map[string][]byte{
			"theme.yaml":        []byte(themeYAML("dark")),
			"previews/dark.png": pngHeader(2, 2),
		}
	}
	tests := []struct {
		name   string
		mutate func(map[string][]byte)
		want   string // expected rule; empty means the package must be valid
	}{
		{name: "valid", mutate: func(map[string][]byte) {}},
		{name: "valid webp preview", mutate: func(f map[string][]byte) {
			delete(f, "previews/dark.png")
			f["previews/dark.webp"] = webpChunk("VP8X", vp8xPayload(2, 2))
		}},
		{name: "valid light and dark", mutate: func(f map[string][]byte) {
			f["theme.yaml"] = []byte(themeYAML("light", "dark"))
			f["previews/light.png"] = pngHeader(2, 2)
		}},
		{name: "theme.yaml missing", mutate: func(f map[string][]byte) {
			delete(f, "theme.yaml")
		}, want: RuleSchema},
		{name: "unexpected file", mutate: func(f map[string][]byte) {
			f["notes.md"] = []byte("x")
		}, want: RuleFilesExtra},
		{name: "unexpected preview name", mutate: func(f map[string][]byte) {
			f["previews/dark.jpg"] = []byte("x")
		}, want: RuleFilesExtra},
		{name: "png and webp for one mode", mutate: func(f map[string][]byte) {
			f["previews/dark.webp"] = webpChunk("VP8X", vp8xPayload(2, 2))
		}, want: RuleFilesExtra},
		{name: "preview for mode without tokens", mutate: func(f map[string][]byte) {
			f["previews/light.png"] = pngHeader(2, 2)
		}, want: RuleFilesExtra},
		{name: "package too large", mutate: func(f map[string][]byte) {
			f["padding.bin"] = make([]byte, MaxPackageBytes)
		}, want: RuleSizeTotal},
		{name: "preview missing", mutate: func(f map[string][]byte) {
			delete(f, "previews/dark.png")
		}, want: RulePreviewMissing},
		{name: "preview too large", mutate: func(f map[string][]byte) {
			f["previews/dark.png"] = oversizedPNG()
		}, want: RulePreviewFormat},
		{name: "preview not an image", mutate: func(f map[string][]byte) {
			f["previews/dark.png"] = []byte("junk")
		}, want: RulePreviewFormat},
		{name: "preview extension does not match content", mutate: func(f map[string][]byte) {
			delete(f, "previews/dark.png")
			f["previews/dark.webp"] = pngHeader(2, 2)
		}, want: RulePreviewFormat},
		{name: "preview too wide", mutate: func(f map[string][]byte) {
			f["previews/dark.png"] = pngHeader(1601, 1)
		}, want: RulePreviewFormat},
		{name: "preview too tall", mutate: func(f map[string][]byte) {
			f["previews/dark.png"] = pngHeader(1, 1001)
		}, want: RulePreviewFormat},
		{name: "preview zero height", mutate: func(f map[string][]byte) {
			f["previews/dark.png"] = pngHeader(1, 0)
		}, want: RulePreviewFormat},
		{name: "css valid", mutate: func(f map[string][]byte) {
			f["theme.css"] = []byte("a{color:red}\n")
		}},
		{name: "css too large", mutate: func(f map[string][]byte) {
			f["theme.css"] = []byte(strings.Repeat("a{}\n", 8200))
		}, want: RuleCSSSize},
		{name: "css import", mutate: func(f map[string][]byte) {
			f["theme.css"] = []byte("@import \"x.css\";\n")
		}, want: RuleCSSImport},
		{name: "css unterminated comment", mutate: func(f map[string][]byte) {
			f["theme.css"] = []byte("a{}\n/* open\n")
		}, want: RuleCSSSyntax},
		{name: "css sanitizer and comment failures both reported", mutate: func(f map[string][]byte) {
			f["theme.css"] = []byte("@import \"x.css\";\n/* open\n")
		}, want: RuleCSSImport},
		{name: "tokens name missing", mutate: func(f map[string][]byte) {
			f["theme.yaml"] = []byte(strings.Replace(themeYAML("dark"), "    link: \"#7dd3fc\"\n", "", 1))
		}, want: RuleTokensNames},
		{name: "tokens format", mutate: func(f map[string][]byte) {
			f["theme.yaml"] = []byte(strings.Replace(themeYAML("dark"), "\"#0b1220\"", "\"#0B1220\"", 1))
		}, want: RuleTokensFormat},
		{name: "manifest without tokens", mutate: func(f map[string][]byte) {
			f["theme.yaml"] = []byte(strings.Split(themeYAML("dark"), "tokens:")[0] + "tokens: {}\n")
		}, want: RuleSchema},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			files := base()
			tt.mutate(files)
			_, probs := Validate(files)
			if tt.want == "" {
				if len(probs) != 0 {
					t.Fatalf("want no problems, got %v", probs)
				}
				return
			}
			if !hasRule(probs, tt.want) {
				t.Fatalf("want rule %s, got %v", tt.want, probs)
			}
		})
	}
}

func TestValidateResultFields(t *testing.T) {
	t.Parallel()
	t.Run("css kept when valid", func(t *testing.T) {
		t.Parallel()
		files := map[string][]byte{
			"theme.yaml":        []byte(themeYAML("dark")),
			"previews/dark.png": pngHeader(2, 2),
			"theme.css":         []byte("a{color:red}\n"),
		}
		res, probs := Validate(files)
		if len(probs) != 0 || res.CSS != "a{color:red}\n" {
			t.Fatalf("CSS = %q, problems %v", res.CSS, probs)
		}
	})
	t.Run("css dropped when rejected", func(t *testing.T) {
		t.Parallel()
		files := map[string][]byte{
			"theme.yaml":        []byte(themeYAML("dark")),
			"previews/dark.png": pngHeader(2, 2),
			"theme.css":         []byte("a{}\n/* open\n"),
		}
		res, probs := Validate(files)
		if res.CSS != "" || !hasRule(probs, RuleCSSSyntax) {
			t.Fatalf("CSS = %q, problems %v", res.CSS, probs)
		}
	})
	t.Run("low contrast flagged", func(t *testing.T) {
		t.Parallel()
		// foreground equal to background is far below 4.5:1.
		files := map[string][]byte{
			"theme.yaml":        []byte(strings.Replace(themeYAML("dark"), "foreground: \"#e6edf7\"", "foreground: \"#0b1220\"", 1)),
			"previews/dark.png": pngHeader(2, 2),
		}
		res, probs := Validate(files)
		if len(probs) != 0 || !res.LowContrast {
			t.Fatalf("LowContrast = %v, problems %v", res.LowContrast, probs)
		}
	})
	t.Run("no low contrast flag on invalid tokens", func(t *testing.T) {
		t.Parallel()
		files := map[string][]byte{
			"theme.yaml":        []byte(strings.Replace(themeYAML("dark"), "    link: \"#7dd3fc\"\n", "", 1)),
			"previews/dark.png": pngHeader(2, 2),
		}
		res, probs := Validate(files)
		if res.LowContrast || !hasRule(probs, RuleTokensNames) {
			t.Fatalf("LowContrast = %v, problems %v", res.LowContrast, probs)
		}
	})
	t.Run("nil package", func(t *testing.T) {
		t.Parallel()
		res, probs := Validate(nil)
		if res.Manifest.Name != "" || len(res.Previews) != 0 || !hasRule(probs, RuleSchema) {
			t.Fatalf("result %+v, problems %v", res, probs)
		}
	})
	t.Run("manifest kept on schema failure", func(t *testing.T) {
		t.Parallel()
		files := map[string][]byte{
			"theme.yaml": []byte(strings.Replace(themeYAML("dark"), "name: deep-ocean", "name: Deep_Ocean", 1)),
		}
		res, probs := Validate(files)
		if res.Manifest.DisplayName != "Deep Ocean" || !hasRule(probs, RuleSchema) {
			t.Fatalf("manifest %+v, problems %v", res.Manifest, probs)
		}
	})
}
