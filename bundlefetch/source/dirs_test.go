package source

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/GameplanePanel/gameplane/bundlefetch/oci"
)

// errDirsTestInjected is the failure the dirsTestFS fake injects.
var errDirsTestInjected = errors.New("injected failure")

// dirsTestFS wraps an in-memory filesystem and injects failures per path, so
// the error branches of the directory helpers can be reached.
type dirsTestFS struct {
	base       fstest.MapFS
	openErr    map[string]bool  // Open fails for these paths
	readErr    map[string]bool  // Open succeeds, Read fails
	readDirErr map[string]bool  // ReadDir fails for these directories
	infoErr    map[string]bool  // DirEntry.Info fails for these paths
	sizeOf     map[string]int64 // DirEntry.Info reports this size
}

func (t *dirsTestFS) Open(name string) (fs.File, error) {
	if t.openErr[name] {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errDirsTestInjected}
	}
	f, err := t.base.Open(name)
	if err != nil {
		return nil, err
	}
	if t.readErr[name] {
		return dirsTestReadErrFile{f}, nil
	}
	return f, nil
}

func (t *dirsTestFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if t.readDirErr[name] {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: errDirsTestInjected}
	}
	entries, err := fs.ReadDir(t.base, name)
	if err != nil {
		return nil, err
	}
	out := make([]fs.DirEntry, 0, len(entries))
	for _, e := range entries {
		out = append(out, dirsTestEntry{DirEntry: e, full: path.Join(name, e.Name()), fsys: t})
	}
	return out, nil
}

// dirsTestEntry is a directory entry whose Info can fail or report a size.
type dirsTestEntry struct {
	fs.DirEntry
	full string
	fsys *dirsTestFS
}

func (e dirsTestEntry) Info() (fs.FileInfo, error) {
	if e.fsys.infoErr[e.full] {
		return nil, &fs.PathError{Op: "stat", Path: e.full, Err: errDirsTestInjected}
	}
	info, err := e.DirEntry.Info()
	if err != nil {
		return nil, err
	}
	if size, ok := e.fsys.sizeOf[e.full]; ok {
		return dirsTestSizedInfo{FileInfo: info, size: size}, nil
	}
	return info, nil
}

// dirsTestSizedInfo reports a size that differs from the file's content.
type dirsTestSizedInfo struct {
	fs.FileInfo
	size int64
}

func (i dirsTestSizedInfo) Size() int64 { return i.size }

// dirsTestReadErrFile opens fine but fails on every Read.
type dirsTestReadErrFile struct{ fs.File }

func (dirsTestReadErrFile) Read([]byte) (int, error) { return 0, errDirsTestInjected }

func dirsTestFile(data string) *fstest.MapFile {
	return &fstest.MapFile{Data: []byte(data)}
}

func TestDirsFindDirs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		fsys   fs.FS
		marker string
		want   []string
	}{
		{
			name: "nested and root markers in walk order",
			fsys: fstest.MapFS{
				"a/module.yaml":   dirsTestFile("x"),
				"b/c/module.yaml": dirsTestFile("x"),
				"b/other.yaml":    dirsTestFile("x"),
				"module.yaml":     dirsTestFile("x"),
			},
			marker: "module.yaml",
			want:   []string{"a", "b/c", "."},
		},
		{
			name:   "no marker present",
			fsys:   fstest.MapFS{"a/x.yaml": dirsTestFile("x")},
			marker: "module.yaml",
			want:   nil,
		},
		{
			name:   "directory named like the marker is not a bundle",
			fsys:   fstest.MapFS{"module.yaml/readme.txt": dirsTestFile("x")},
			marker: "module.yaml",
			want:   nil,
		},
		{
			name:   "marker matches on base name only",
			fsys:   fstest.MapFS{"a/xmodule.yaml": dirsTestFile("x")},
			marker: "module.yaml",
			want:   nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := FindDirs(tc.fsys, tc.marker)
			if err != nil {
				t.Fatalf("FindDirs: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("FindDirs = %q, want %q", got, tc.want)
			}
		})
	}

	t.Run("root open failure is returned", func(t *testing.T) {
		t.Parallel()
		fsys := &dirsTestFS{
			base:    fstest.MapFS{"module.yaml": dirsTestFile("x")},
			openErr: map[string]bool{".": true},
		}
		if _, err := FindDirs(fsys, "module.yaml"); err == nil {
			t.Fatal("FindDirs over an unopenable root: want error")
		}
	})

	t.Run("nested read failure is returned", func(t *testing.T) {
		t.Parallel()
		fsys := &dirsTestFS{
			base:       fstest.MapFS{"a/module.yaml": dirsTestFile("x")},
			readDirErr: map[string]bool{"a": true},
		}
		if _, err := FindDirs(fsys, "module.yaml"); err == nil {
			t.Fatal("FindDirs over an unlistable directory: want error")
		}
	})
}

func TestDirsReadDir(t *testing.T) {
	t.Parallel()

	t.Run("given digest is returned and nothing is hashed", func(t *testing.T) {
		t.Parallel()
		// The directory does not exist: with a digest given it is never walked.
		files, digest, err := ReadDir(fstest.MapFS{}, "absent", "sha256:given", []string{"module.yaml"})
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if digest != "sha256:given" {
			t.Fatalf("digest = %q, want sha256:given", digest)
		}
		if len(files) != 0 {
			t.Fatalf("files = %v, want none", files)
		}
	})

	t.Run("missing and unreadable names are left out", func(t *testing.T) {
		t.Parallel()
		fsys := &dirsTestFS{
			base: fstest.MapFS{
				"mod/module.yaml": dirsTestFile("name: a"),
				"mod/broken.yaml": dirsTestFile("x"),
			},
			openErr: map[string]bool{"mod/broken.yaml": true},
		}
		files, digest, err := ReadDir(fsys, "mod", "sha256:d", []string{"module.yaml", "other.yaml", "broken.yaml"})
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if digest != "sha256:d" {
			t.Fatalf("digest = %q, want sha256:d", digest)
		}
		if len(files) != 1 || string(files["module.yaml"]) != "name: a" {
			t.Fatalf("files = %v, want only module.yaml", files)
		}
	})

	t.Run("locale files are read and skip names honoured", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{
			"mod/module.yaml":         dirsTestFile("name: a"),
			"mod/locales/en.json":     dirsTestFile("{}"),
			"mod/locales/review.json": dirsTestFile("{}"),
		}
		files, _, err := ReadDir(fsys, "mod", "sha256:d", []string{"module.yaml"}, "review.json")
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if _, ok := files["locales/en.json"]; !ok {
			t.Fatalf("locales/en.json missing: %v", files)
		}
		if _, ok := files["locales/review.json"]; ok {
			t.Fatalf("skipped locales/review.json was read")
		}
	})

	t.Run("empty digest computes the content digest", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{
			"mod/module.yaml": dirsTestFile("name: a"),
			"mod/sub/x.json":  dirsTestFile("{}"),
		}
		_, digest, err := ReadDir(fsys, "mod", "", []string{"module.yaml"})
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		want, err := ContentDigest(fsys, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		if digest != want {
			t.Fatalf("digest = %q, want %q", digest, want)
		}
	})

	t.Run("empty digest over a missing directory fails", func(t *testing.T) {
		t.Parallel()
		files, digest, err := ReadDir(fstest.MapFS{}, "absent", "", []string{"module.yaml"})
		if err == nil || !strings.Contains(err.Error(), "hash module dir") {
			t.Fatalf("ReadDir error = %v, want hash module dir failure", err)
		}
		if files != nil || digest != "" {
			t.Fatalf("on error got files=%v digest=%q, want nil and empty", files, digest)
		}
	})

	t.Run("unreadable file fails the computed digest", func(t *testing.T) {
		t.Parallel()
		fsys := &dirsTestFS{
			base:    fstest.MapFS{"mod/module.yaml": dirsTestFile("name: a")},
			openErr: map[string]bool{"mod/module.yaml": true},
		}
		if _, _, err := ReadDir(fsys, "mod", "", []string{"module.yaml"}); err == nil {
			t.Fatal("ReadDir: want error when a file cannot be hashed")
		}
	})
}

func TestDirsReadLocaleFiles(t *testing.T) {
	t.Parallel()

	budget := fstest.MapFS{}
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		budget["mod/locales/"+name+".json"] = dirsTestFile(strings.Repeat("x", oci.MaxLocaleLayerSize))
	}
	budget["mod/locales/f.json"] = dirsTestFile(strings.Repeat("x", 10))

	tests := []struct {
		name string
		fsys fs.FS
		skip []string
		// want maps each expected key to its stored length; -1 expects the
		// oversized marker instead.
		want map[string]int
	}{
		{
			name: "no locales directory",
			fsys: fstest.MapFS{"mod/module.yaml": dirsTestFile("x")},
			want: map[string]int{},
		},
		{
			name: "locales is a file",
			fsys: fstest.MapFS{"mod/locales": dirsTestFile("x")},
			want: map[string]int{},
		},
		{
			name: "only regular json files are locales",
			fsys: fstest.MapFS{
				"mod/locales/en.json":          dirsTestFile("0123456789"),
				"mod/locales/fr.json":          dirsTestFile("01234567890"),
				"mod/locales/readme.txt":       dirsTestFile("abc"),
				"mod/locales/review.json":      dirsTestFile("abcd"),
				"mod/locales/sub.json/x.json":  dirsTestFile("ab"),
				"mod/locales/nested/deep.json": dirsTestFile("ab"),
			},
			skip: []string{"review.json"},
			want: map[string]int{"locales/en.json": 10, "locales/fr.json": 11},
		},
		{
			name: "one layer over the layer cap is marked oversized",
			fsys: fstest.MapFS{
				"mod/locales/big.json":   dirsTestFile(strings.Repeat("x", oci.MaxLocaleLayerSize+1)),
				"mod/locales/small.json": dirsTestFile("12345"),
			},
			want: map[string]int{"locales/big.json": -1, "locales/small.json": 5},
		},
		{
			name: "total budget marks the language that does not fit, smaller later ones still fit",
			fsys: budget,
			want: map[string]int{
				"locales/a.json": oci.MaxLocaleLayerSize,
				"locales/b.json": oci.MaxLocaleLayerSize,
				"locales/c.json": oci.MaxLocaleLayerSize,
				"locales/d.json": oci.MaxLocaleLayerSize,
				"locales/e.json": -1,
				"locales/f.json": 10,
			},
		},
		{
			name: "entry whose info fails is skipped",
			fsys: &dirsTestFS{
				base: fstest.MapFS{
					"mod/locales/broken.json": dirsTestFile("123"),
					"mod/locales/ok.json":     dirsTestFile("1234"),
				},
				infoErr: map[string]bool{"mod/locales/broken.json": true},
			},
			want: map[string]int{"locales/ok.json": 4},
		},
		{
			name: "file that grew past the cap after its size check is marked oversized",
			fsys: &dirsTestFS{
				base: fstest.MapFS{
					"mod/locales/grow.json": dirsTestFile(strings.Repeat("x", oci.MaxLocaleLayerSize+5)),
				},
				sizeOf: map[string]int64{"mod/locales/grow.json": 10},
			},
			want: map[string]int{"locales/grow.json": -1},
		},
		{
			name: "file that cannot be opened is skipped",
			fsys: &dirsTestFS{
				base: fstest.MapFS{
					"mod/locales/nope.json": dirsTestFile("123"),
					"mod/locales/ok.json":   dirsTestFile("1234"),
				},
				openErr: map[string]bool{"mod/locales/nope.json": true},
			},
			want: map[string]int{"locales/ok.json": 4},
		},
		{
			name: "file that fails to read is skipped",
			fsys: &dirsTestFS{
				base: fstest.MapFS{
					"mod/locales/rd.json": dirsTestFile("123"),
					"mod/locales/ok.json": dirsTestFile("1234"),
				},
				readErr: map[string]bool{"mod/locales/rd.json": true},
			},
			want: map[string]int{"locales/ok.json": 4},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			files := map[string][]byte{"module.yaml": []byte("m")}
			ReadLocaleFiles(tc.fsys, "mod", files, tc.skip...)

			if string(files["module.yaml"]) != "m" {
				t.Fatalf("existing entry changed: %q", files["module.yaml"])
			}
			if len(files) != len(tc.want)+1 {
				t.Fatalf("got %d locale entries, want %d", len(files)-1, len(tc.want))
			}
			for key, size := range tc.want {
				data, ok := files[key]
				if !ok {
					t.Errorf("%s missing", key)
					continue
				}
				if size < 0 {
					if !oci.IsOversizedLocale(data) {
						t.Errorf("%s: want oversized marker, got %d bytes", key, len(data))
					}
					continue
				}
				if oci.IsOversizedLocale(data) || len(data) != size {
					t.Errorf("%s: got %d bytes (oversized=%v), want %d", key, len(data), oci.IsOversizedLocale(data), size)
				}
			}
		})
	}
}

func TestDirsReadFileCapped(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		fsys    fs.FS
		file    string
		limit   int64
		wantLen int
		wantErr string
	}{
		{
			name:    "under the limit",
			fsys:    fstest.MapFS{"f": dirsTestFile("abc")},
			file:    "f",
			limit:   10,
			wantLen: 3,
		},
		{
			name:    "exactly the limit",
			fsys:    fstest.MapFS{"f": dirsTestFile("abc")},
			file:    "f",
			limit:   3,
			wantLen: 3,
		},
		{
			name:    "over the limit returns limit plus one bytes",
			fsys:    fstest.MapFS{"f": dirsTestFile("abcdef")},
			file:    "f",
			limit:   2,
			wantLen: 3,
		},
		{
			name:    "missing file reports open failure",
			fsys:    fstest.MapFS{},
			file:    "f",
			limit:   10,
			wantErr: "open f",
		},
		{
			name:    "read failure is reported",
			fsys:    &dirsTestFS{base: fstest.MapFS{"f": dirsTestFile("abc")}, readErr: map[string]bool{"f": true}},
			file:    "f",
			limit:   10,
			wantErr: "read f",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := ReadFileCapped(tc.fsys, tc.file, tc.limit)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("ReadFileCapped error = %v, want containing %q", err, tc.wantErr)
				}
				if data != nil {
					t.Fatalf("data on error = %q, want nil", data)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReadFileCapped: %v", err)
			}
			if len(data) != tc.wantLen {
				t.Fatalf("len = %d, want %d", len(data), tc.wantLen)
			}
		})
	}
}

func TestDirsContentDigest(t *testing.T) {
	t.Parallel()
	const prefix = "sha256:"
	base := fstest.MapFS{
		"mod/module.yaml":   dirsTestFile("name: a"),
		"mod/sub/data.json": dirsTestFile("{}"),
	}

	t.Run("format and stability", func(t *testing.T) {
		t.Parallel()
		d1, err := ContentDigest(base, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		d2, err := ContentDigest(base, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		if d1 != d2 {
			t.Fatalf("digest not stable: %q vs %q", d1, d2)
		}
		if !strings.HasPrefix(d1, prefix) || len(d1) != len(prefix)+64 {
			t.Fatalf("digest %q is not sha256:<64 hex>", d1)
		}
	})

	t.Run("content and name changes change the digest", func(t *testing.T) {
		t.Parallel()
		want, err := ContentDigest(base, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		tests := []struct {
			name string
			fsys fstest.MapFS
		}{
			{
				name: "top-level content",
				fsys: fstest.MapFS{
					"mod/module.yaml":   dirsTestFile("name: b"),
					"mod/sub/data.json": dirsTestFile("{}"),
				},
			},
			{
				name: "nested content",
				fsys: fstest.MapFS{
					"mod/module.yaml":   dirsTestFile("name: a"),
					"mod/sub/data.json": dirsTestFile("[]"),
				},
			},
			{
				name: "renamed file",
				fsys: fstest.MapFS{
					"mod/other.yaml":    dirsTestFile("name: a"),
					"mod/sub/data.json": dirsTestFile("{}"),
				},
			},
		}
		for _, tc := range tests {
			got, err := ContentDigest(tc.fsys, "mod")
			if err != nil {
				t.Fatalf("%s: ContentDigest: %v", tc.name, err)
			}
			if got == want {
				t.Errorf("%s: digest unchanged (%q)", tc.name, got)
			}
		}
	})

	t.Run("same tree under another directory hashes identically", func(t *testing.T) {
		t.Parallel()
		want, err := ContentDigest(base, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		moved := fstest.MapFS{
			"elsewhere/module.yaml":   dirsTestFile("name: a"),
			"elsewhere/sub/data.json": dirsTestFile("{}"),
		}
		got, err := ContentDigest(moved, "elsewhere")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		if got != want {
			t.Fatalf("moved digest = %q, want %q", got, want)
		}
	})

	t.Run("empty directory hashes like an empty map", func(t *testing.T) {
		t.Parallel()
		fsys := fstest.MapFS{"mod": {Mode: fs.ModeDir}}
		got, err := ContentDigest(fsys, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		if want := DigestFiles(map[string][]byte{}); got != want {
			t.Fatalf("empty dir digest = %q, want %q", got, want)
		}
	})

	t.Run("walk failure is returned", func(t *testing.T) {
		t.Parallel()
		if _, err := ContentDigest(base, "absent"); err == nil {
			t.Fatal("ContentDigest over a missing directory: want error")
		}
	})

	t.Run("unreadable file is returned", func(t *testing.T) {
		t.Parallel()
		fsys := &dirsTestFS{base: base, openErr: map[string]bool{"mod/sub/data.json": true}}
		if _, err := ContentDigest(fsys, "mod"); err == nil {
			t.Fatal("ContentDigest with an unreadable file: want error")
		}
	})
}

func TestDirsDigestFiles(t *testing.T) {
	t.Parallel()
	const prefix = "sha256:"

	t.Run("format and determinism", func(t *testing.T) {
		t.Parallel()
		files := map[string][]byte{"module.yaml": []byte("a"), "sub/x.json": []byte("b"), "z": []byte("c")}
		d1 := DigestFiles(files)
		for range 5 {
			if d := DigestFiles(files); d != d1 {
				t.Fatalf("digest not deterministic: %q vs %q", d, d1)
			}
		}
		if !strings.HasPrefix(d1, prefix) || len(d1) != len(prefix)+64 {
			t.Fatalf("digest %q is not sha256:<64 hex>", d1)
		}
	})

	t.Run("nil and empty maps match", func(t *testing.T) {
		t.Parallel()
		if DigestFiles(nil) != DigestFiles(map[string][]byte{}) {
			t.Fatal("nil map and empty map digest differently")
		}
	})

	t.Run("matches ContentDigest over the same tree", func(t *testing.T) {
		t.Parallel()
		tree := fstest.MapFS{
			"mod/module.yaml":   dirsTestFile("name: a"),
			"mod/sub/data.json": dirsTestFile("{}"),
		}
		want, err := ContentDigest(tree, "mod")
		if err != nil {
			t.Fatalf("ContentDigest: %v", err)
		}
		files := map[string][]byte{"module.yaml": []byte("name: a"), "sub/data.json": []byte("{}")}
		if got := DigestFiles(files); got != want {
			t.Fatalf("DigestFiles = %q, ContentDigest = %q; want equal", got, want)
		}
	})

	t.Run("content and name changes change the digest", func(t *testing.T) {
		t.Parallel()
		base := DigestFiles(map[string][]byte{"a": []byte("x")})
		if DigestFiles(map[string][]byte{"a": []byte("y")}) == base {
			t.Error("content change did not change digest")
		}
		if DigestFiles(map[string][]byte{"b": []byte("x")}) == base {
			t.Error("name change did not change digest")
		}
	})
}

func TestDirsAllowed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		allow []string
		want  bool
	}{
		{name: "nil list allows everything", input: "a.yaml", allow: nil, want: true},
		{name: "empty list allows everything", input: "a.yaml", allow: []string{}, want: true},
		{name: "exact name", input: "module.yaml", allow: []string{"module.yaml"}, want: true},
		{name: "glob matches", input: "a.yaml", allow: []string{"*.yaml"}, want: true},
		{name: "glob does not cross a slash", input: "dir/a.yaml", allow: []string{"*.yaml"}, want: false},
		{name: "no entry matches", input: "a.json", allow: []string{"*.yaml", "b"}, want: false},
		{name: "malformed pattern does not match", input: "a", allow: []string{"["}, want: false},
		{name: "malformed pattern before a match", input: "a", allow: []string{"[", "a"}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Allowed(tc.input, tc.allow); got != tc.want {
				t.Fatalf("Allowed(%q, %q) = %v, want %v", tc.input, tc.allow, got, tc.want)
			}
		})
	}
}

func TestDirsContains(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		list []string
		s    string
		want bool
	}{
		{name: "nil list", list: nil, s: "a", want: false},
		{name: "present", list: []string{"a", "b"}, s: "b", want: true},
		{name: "absent", list: []string{"a", "b"}, s: "c", want: false},
		{name: "exact not prefix", list: []string{"review"}, s: "review.json", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := contains(tc.list, tc.s); got != tc.want {
				t.Fatalf("contains(%q, %q) = %v, want %v", tc.list, tc.s, got, tc.want)
			}
		})
	}
}
