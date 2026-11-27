package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// tarEnt describes one tar entry built by makeTarGzBytes.
type tarEnt struct {
	name string
	// typ is the tar type flag; zero means a regular file.
	typ  byte
	body string
	link string
	mode int64
}

// zipEnt describes one zip entry built by makeZipBytes.
type zipEnt struct {
	name    string
	body    string
	mode    fs.FileMode
	dir     bool
	symlink bool
}

// makeTarGzBytes builds a gzip-compressed tar archive from ents.
func makeTarGzBytes(t *testing.T, ents []tarEnt) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range ents {
		typ := e.typ
		if typ == 0 {
			typ = tar.TypeReg
		}
		mode := e.mode
		if mode == 0 {
			mode = 0o644
		}
		hdr := &tar.Header{Name: e.name, Typeflag: typ, Linkname: e.link, Mode: mode}
		if typ == tar.TypeReg {
			hdr.Size = int64(len(e.body))
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("tar header %q: %v", e.name, err)
		}
		if typ == tar.TypeReg {
			if _, err := tw.Write([]byte(e.body)); err != nil {
				t.Fatalf("tar body %q: %v", e.name, err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// makeZipBytes builds a zip archive from ents.
func makeZipBytes(t *testing.T, ents []zipEnt) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		fh := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch {
		case e.dir:
			fh.Name += "/"
			fh.SetMode(fs.ModeDir | 0o755)
		case e.symlink:
			fh.Method = zip.Store
			fh.SetMode(fs.ModeSymlink | 0o777)
		default:
			mode := e.mode
			if mode == 0 {
				mode = 0o644
			}
			fh.SetMode(mode)
		}
		w, err := zw.CreateHeader(fh)
		if err != nil {
			t.Fatalf("zip header %q: %v", e.name, err)
		}
		if !e.dir {
			if _, err := w.Write([]byte(e.body)); err != nil {
				t.Fatalf("zip body %q: %v", e.name, err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// writeArchive writes data to a fresh file and returns its path.
func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "upload")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatalf("write archive: %v", err)
	}
	return p
}

// unpackTar unpacks ents as a tar.gz into a fresh root.
func unpackTar(t *testing.T, ents []tarEnt, opts UnpackOptions) (string, UnpackResult, error) {
	t.Helper()
	root := t.TempDir()
	if opts.Format == "" {
		opts.Format = "tar.gz"
	}
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 1 << 20
	}
	res, err := Unpack(writeArchive(t, makeTarGzBytes(t, ents)), root, opts)
	return root, res, err
}

const gameplaneManifestDataLegacy = `{"formatVersion":1,"volumes":[{"logicalName":"data","folder":"data"}]}`

func TestUnpackRefusesUnsafeEntries(t *testing.T) {
	tests := []struct {
		name string
		ents []tarEnt
	}{
		{"absolute path", []tarEnt{{name: "/etc/passwd", body: "x"}}},
		{"parent reference", []tarEnt{{name: "a/../../escape", body: "x"}}},
		{"absolute symlink", []tarEnt{{name: "l", typ: tar.TypeSymlink, link: "/etc/passwd"}}},
		{"escaping symlink", []tarEnt{{name: "d/l", typ: tar.TypeSymlink, link: "../../../../x"}}},
		{"hard link outside", []tarEnt{{name: "h", typ: tar.TypeLink, link: "/etc/passwd"}}},
		{"hard link not extracted", []tarEnt{{name: "h", typ: tar.TypeLink, link: "nothere"}}},
		{"character device", []tarEnt{{name: "dev0", typ: tar.TypeChar}}},
		{"fifo", []tarEnt{{name: "fifo0", typ: tar.TypeFifo}}},
		{"write through existing symlink", []tarEnt{
			{name: "lnk", typ: tar.TypeSymlink, link: "sub"},
			{name: "lnk/x", body: "x"},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := unpackTar(t, tt.ents, UnpackOptions{})
			if err == nil {
				t.Fatal("Unpack succeeded, want refusal")
			}
		})
	}
}

func TestUnpackErrorNamesTheEntry(t *testing.T) {
	_, _, err := unpackTar(t, []tarEnt{{name: "/etc/passwd", body: "x"}}, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "/etc/passwd") {
		t.Fatalf("error = %v, want it to name the entry", err)
	}
}

func TestUnpackEnforcesSizeCapOnActualBytes(t *testing.T) {
	_, _, err := unpackTar(t, []tarEnt{{name: "big", body: strings.Repeat("a", 100)}}, UnpackOptions{MaxBytes: 50})
	if !errors.Is(err, errSizeLimit) {
		t.Fatalf("error = %v, want errSizeLimit", err)
	}
}

func TestUnpackHardLinkCountsAgainstCap(t *testing.T) {
	ents := []tarEnt{
		{name: "a", body: "xy"},
		{name: "b", typ: tar.TypeLink, link: "a"},
	}
	root, res, err := unpackTar(t, ents, UnpackOptions{MaxBytes: 4})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if res.UnpackedBytes != 4 {
		t.Errorf("UnpackedBytes = %d, want 4", res.UnpackedBytes)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", "b")); err != nil {
		t.Errorf("hard link not created: %v", err)
	}
	_, _, err = unpackTar(t, ents, UnpackOptions{MaxBytes: 3})
	if !errors.Is(err, errSizeLimit) {
		t.Fatalf("error = %v, want errSizeLimit", err)
	}
}

func TestUnpackTarMasksModeBits(t *testing.T) {
	root, _, err := unpackTar(t, []tarEnt{{name: "f", body: "x", mode: 0o4755}}, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	fi, err := os.Stat(filepath.Join(root, "volumes", "data", "f"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != 0o755 {
		t.Errorf("mode = %v, want 0755 with no setuid", fi.Mode())
	}
}

func TestUnpackZipResetsPermissions(t *testing.T) {
	ents := []zipEnt{
		{name: "world", dir: true},
		{name: "world/run.sh", body: "x", mode: 0o755},
	}
	root := t.TempDir()
	res, err := Unpack(writeArchive(t, makeZipBytes(t, ents)), root, UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if !res.ZipPermissionsReset {
		t.Error("ZipPermissionsReset = false, want true")
	}
	fi, err := os.Stat(filepath.Join(root, "volumes", "data", "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode() != 0o644 {
		t.Errorf("mode = %v, want 0644", fi.Mode())
	}
}

func TestUnpackPlainStripsWrappingFolder(t *testing.T) {
	ents := []tarEnt{
		{name: "world/", typ: tar.TypeDir},
		{name: "world/a.txt", body: "a"},
	}
	root, res, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if res.Kind != kindPlain {
		t.Errorf("Kind = %q, want plain", res.Kind)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", "a.txt")); err != nil {
		t.Errorf("stripped file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", "world")); !os.IsNotExist(err) {
		t.Errorf("wrapping folder kept: %v", err)
	}
	if len(res.Volumes) != 1 || res.Volumes[0] != "data" {
		t.Errorf("Volumes = %v, want [data]", res.Volumes)
	}
}

func TestUnpackPlainKeepsMixedTopLevel(t *testing.T) {
	ents := []tarEnt{{name: "x/a", body: "a"}, {name: "y", body: "y"}}
	root, _, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	for _, p := range []string{"x/a", "y"} {
		if _, err := os.Stat(filepath.Join(root, "volumes", "data", p)); err != nil {
			t.Errorf("%s missing: %v", p, err)
		}
	}
}

func TestUnpackGameplaneLegacyDataFolder(t *testing.T) {
	ents := []tarEnt{
		{name: ManifestName, body: gameplaneManifestDataLegacy},
		{name: "data/", typ: tar.TypeDir},
		{name: "data/level.dat", body: "lvl"},
	}
	root, res, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if res.Kind != kindGameplane || res.Manifest == nil {
		t.Fatalf("Kind = %q, Manifest = %v, want gameplane with manifest", res.Kind, res.Manifest)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", "level.dat")); err != nil {
		t.Errorf("legacy data file not mapped to volumes/data: %v", err)
	}
	if len(res.Volumes) != 1 || res.Volumes[0] != "data" {
		t.Errorf("Volumes = %v, want [data]", res.Volumes)
	}
}

func TestUnpackGameplaneVolumeFolders(t *testing.T) {
	manifest := `{"formatVersion":1,"volumes":[{"logicalName":"world","folder":"volumes/world"}]}`
	ents := []tarEnt{
		{name: ManifestName, body: manifest},
		{name: "volumes/", typ: tar.TypeDir},
		{name: "volumes/world/", typ: tar.TypeDir},
		{name: "volumes/world/level.dat", body: "lvl"},
	}
	root, res, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "world", "level.dat")); err != nil {
		t.Errorf("volume file missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ManifestName)); !os.IsNotExist(err) {
		t.Errorf("manifest should not be unpacked: %v", err)
	}
	if len(res.Volumes) != 1 || res.Volumes[0] != "world" {
		t.Errorf("Volumes = %v, want [world]", res.Volumes)
	}
}

func TestUnpackGameplaneRefusesEntryOutsideManifest(t *testing.T) {
	ents := []tarEnt{
		{name: ManifestName, body: gameplaneManifestDataLegacy},
		{name: "etc/passwd", body: "x"},
	}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err == nil {
		t.Fatal("Unpack succeeded, want refusal of an entry outside the manifest volumes")
	}
}

func TestUnpackRefusesNewerFormatVersion(t *testing.T) {
	ents := []tarEnt{{name: ManifestName, body: `{"formatVersion":99}`}}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "newer Gameplane") {
		t.Fatalf("error = %v, want a newer Gameplane refusal", err)
	}
}

func TestUnpackPlainForcedIgnoresManifest(t *testing.T) {
	ents := []tarEnt{{name: ManifestName, body: gameplaneManifestDataLegacy}}
	root, res, err := unpackTar(t, ents, UnpackOptions{Plain: true})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if res.Kind != kindPlain || res.Manifest != nil {
		t.Errorf("Kind = %q, Manifest = %v, want plain without manifest", res.Kind, res.Manifest)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", ManifestName)); err != nil {
		t.Errorf("manifest should be kept as plain data: %v", err)
	}
}

func TestUnpackZipRefusesEscapingSymlink(t *testing.T) {
	ents := []zipEnt{{name: "l", body: "../../../../x", symlink: true}}
	root := t.TempDir()
	_, err := Unpack(writeArchive(t, makeZipBytes(t, ents)), root, UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("Unpack succeeded, want refusal of an escaping symlink")
	}
}

// TestUnpackRefusesSymlinkChainEscape covers links that are each harmless on
// their own but escape the root once followed together. "d/a" resolves to the
// root, so "d/l" through "a" would climb above it.
func TestUnpackRefusesSymlinkChainEscape(t *testing.T) {
	tests := []struct {
		name string
		ents []tarEnt
	}{
		{"two-link chain", []tarEnt{
			{name: "top.txt", body: "x"},
			{name: "d/a", typ: tar.TypeSymlink, link: ".."},
			{name: "d/l", typ: tar.TypeSymlink, link: "a/../.."},
		}},
		{"up through a missing entry", []tarEnt{
			{name: "top.txt", body: "x"},
			{name: "d/l", typ: tar.TypeSymlink, link: "m/../.."},
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := unpackTar(t, tt.ents, UnpackOptions{})
			if err == nil {
				t.Fatal("Unpack succeeded, want refusal of a link chain that escapes the root")
			}
			if !strings.Contains(err.Error(), "d/l") {
				t.Errorf("error = %v, want it to name the escaping entry", err)
			}
		})
	}
}

// TestUnpackRefusesSymlinkIntoSiblingVolume checks that a link cannot point
// from one volume folder into another, since each volume is copied to its own
// claim.
func TestUnpackRefusesSymlinkIntoSiblingVolume(t *testing.T) {
	_, _, err := unpackTar(t, []tarEnt{
		{name: "top.txt", body: "x"},
		{name: "d/l", typ: tar.TypeSymlink, link: "../../other"},
	}, UnpackOptions{})
	if err == nil {
		t.Fatal("Unpack succeeded, want refusal of a link that leaves its volume folder")
	}
	if !strings.Contains(err.Error(), "d/l") {
		t.Errorf("error = %v, want it to name the escaping entry", err)
	}
}

// TestUnpackAllowsSymlinkUpWithinRoot checks that a ".." step that stays inside
// the root is still accepted.
func TestUnpackAllowsSymlinkUpWithinRoot(t *testing.T) {
	root, _, err := unpackTar(t, []tarEnt{
		{name: "top.txt", body: "x"},
		{name: "d/a.txt", body: "x"},
		{name: "d/up", typ: tar.TypeSymlink, link: "../volumes"},
	}, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "volumes", "data", "d", "up")); err != nil {
		t.Errorf("symlink not created: %v", err)
	}
}
