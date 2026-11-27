package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawZipEntry is one zip entry for buildRawZip, written with the header it carries.
type rawZipEntry struct {
	hdr  *zip.FileHeader
	body string
}

// storedHeader returns a header for an entry stored without compression, so a
// test can change its bytes and keep the checksum that the central directory has.
func storedHeader(name string) *zip.FileHeader {
	return &zip.FileHeader{Name: name, Method: zip.Store}
}

// buildRawZip writes ents in order, each with its own header.
func buildRawZip(t *testing.T, ents []rawZipEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range ents {
		w, err := zw.CreateHeader(e.hdr)
		if err != nil {
			t.Fatalf("zip header %q: %v", e.hdr.Name, err)
		}
		if _, err := io.WriteString(w, e.body); err != nil {
			t.Fatalf("zip body %q: %v", e.hdr.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}
	return buf.Bytes()
}

// gapTarGz builds a gzip-compressed tar archive with write, which may add any
// header the tar writer accepts.
func gapTarGz(t *testing.T, write func(tw *tar.Writer)) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write(tw)
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// gapManifestJSON is a Gameplane manifest with one volume at folder.
func gapManifestJSON(folder string) string {
	return `{"formatVersion":1,"volumes":[{"logicalName":"world","folder":"` + folder + `"}]}`
}

func TestUnpackRejectsBadOptions(t *testing.T) {
	archive := writeArchive(t, makeTarGzBytes(t, []tarEnt{{name: "a", body: "x"}}))
	if _, err := Unpack(archive, t.TempDir(), UnpackOptions{Format: "tar.gz"}); err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Errorf("zero MaxBytes: error = %v, want size limit refusal", err)
	}
	if _, err := Unpack(archive, t.TempDir(), UnpackOptions{Format: "rar", MaxBytes: 1 << 20}); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unknown format: error = %v, want unsupported format refusal", err)
	}
}

func TestUnpackMissingRootOrArchiveRefused(t *testing.T) {
	archive := writeArchive(t, makeTarGzBytes(t, []tarEnt{{name: "a", body: "x"}}))
	_, err := Unpack(archive, filepath.Join(t.TempDir(), "absent"), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "open unpack root") {
		t.Errorf("missing root: error = %v, want open unpack root", err)
	}
	_, err = Unpack(filepath.Join(t.TempDir(), "absent"), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil {
		t.Error("missing archive: Unpack succeeded")
	}
}

func TestUnpackPlainVolumeBlockedByFileRefused(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "volumes"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Unpack(writeArchive(t, makeTarGzBytes(t, []tarEnt{{name: "a", body: "x"}})), root,
		UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("Unpack succeeded with a file where the volumes folder belongs")
	}
}

func TestUnpackNonArchiveInputRefused(t *testing.T) {
	_, err := Unpack(writeArchive(t, []byte("plain text, not gzip")), t.TempDir(),
		UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "read archive") {
		t.Errorf("gzip garbage: error = %v, want read archive", err)
	}
	_, err = Unpack(writeArchive(t, []byte("plain text, not zip")), t.TempDir(),
		UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "read archive") {
		t.Errorf("zip garbage: error = %v, want read archive", err)
	}
}

func TestUnpackTruncatedTarStreamRefused(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write(bytes.Repeat([]byte("x"), 100)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := Unpack(writeArchive(t, buf.Bytes()), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("Unpack succeeded on a stream that is not a tar archive")
	}
}

func TestUnpackPAXGlobalHeaderIgnored(t *testing.T) {
	data := gapTarGz(t, func(tw *tar.Writer) {
		if err := tw.WriteHeader(&tar.Header{
			Typeflag:   tar.TypeXGlobalHeader,
			Name:       "pax_global_header",
			PAXRecords: map[string]string{"comment": "global"},
			Format:     tar.FormatPAX,
		}); err != nil {
			t.Fatalf("global header: %v", err)
		}
		if err := tw.WriteHeader(&tar.Header{Typeflag: tar.TypeReg, Name: "a.txt", Mode: 0o644, Size: 1}); err != nil {
			t.Fatalf("file header: %v", err)
		}
		if _, err := tw.Write([]byte("a")); err != nil {
			t.Fatalf("file body: %v", err)
		}
	})
	root := t.TempDir()
	if _, err := Unpack(writeArchive(t, data), root, UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20}); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", "a.txt")); err != nil {
		t.Errorf("file after the global header not extracted: %v", err)
	}
}

func TestUnpackDotEntriesIgnored(t *testing.T) {
	ents := []tarEnt{
		{name: "./", typ: tar.TypeDir},
		{name: ".", typ: tar.TypeDir},
		{name: "a.txt", body: "a"},
	}
	root, _, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "volumes", "data", "a.txt")); err != nil {
		t.Errorf("file not extracted: %v", err)
	}
}

func TestUnpackEmptyEntryNameRefused(t *testing.T) {
	_, _, err := unpackTar(t, []tarEnt{{name: "", body: "x"}}, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Errorf("error = %v, want empty name refusal", err)
	}
}

func TestUnpackZipNULInNameRefused(t *testing.T) {
	_, err := Unpack(writeArchive(t, makeZipBytes(t, []zipEnt{{name: "a\x00b", body: "x"}})), t.TempDir(),
		UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Errorf("error = %v, want NUL byte refusal", err)
	}
}

func TestUnpackGameplaneVolumeFolderNotDirectoryRefused(t *testing.T) {
	for _, name := range []string{"volumes", "volumes/world"} {
		t.Run(name, func(t *testing.T) {
			ents := []tarEnt{
				{name: ManifestName, body: gapManifestJSON("volumes/world")},
				{name: name, body: "x"},
			}
			_, _, err := unpackTar(t, ents, UnpackOptions{})
			if err == nil || !strings.Contains(err.Error(), "must be a directory") {
				t.Errorf("error = %v, want must be a directory refusal", err)
			}
		})
	}
}

func TestUnpackFileWhereParentDirectoryNeededRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "d", body: "x"},
		{name: "d/sub/", typ: tar.TypeDir},
	}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err == nil {
		t.Error("Unpack succeeded with a directory under a regular file")
	}
}

func TestUnpackOverlongDirectoryNameRefused(t *testing.T) {
	// A second top-level entry keeps the long folder from being stripped as a
	// wrapping folder, so the unpacker really tries to create it.
	ents := []tarEnt{{name: "top.txt", body: "x"}, {name: strings.Repeat("d", 300), typ: tar.TypeDir}}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "create directory") {
		t.Errorf("error = %v, want create directory failure", err)
	}
}

func TestUnpackDirectoryOverSymlinkRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "lnk", typ: tar.TypeSymlink, link: "sub"},
		{name: "lnk", typ: tar.TypeDir},
	}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err == nil {
		t.Error("Unpack followed an existing symlink when creating a directory")
	}
}

func TestUnpackDuplicateFileReplacesEarlierCopy(t *testing.T) {
	ents := []tarEnt{
		{name: "a", body: "1"},
		{name: "a", body: "2"},
	}
	root, _, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "volumes", "data", "a"))
	if err != nil || string(b) != "2" {
		t.Errorf("a = %q, %v, want the later entry", b, err)
	}
}

func TestUnpackFileOverDirectoryRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "x", typ: tar.TypeDir},
		{name: "x", body: "y"},
	}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "replace existing entry") {
		t.Errorf("error = %v, want replace refusal", err)
	}
}

func TestUnpackOverlongFileNameRefused(t *testing.T) {
	ents := []tarEnt{{name: strings.Repeat("f", 300), body: "x"}}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "create file") {
		t.Errorf("error = %v, want create file failure", err)
	}
}

func TestUnpackZipStoredBodyChecksumRefused(t *testing.T) {
	data := buildRawZip(t, []rawZipEntry{{hdr: storedHeader("f"), body: "hello world"}})
	i := bytes.Index(data, []byte("hello world"))
	if i < 0 {
		t.Fatal("stored body not found in archive bytes")
	}
	data[i] ^= 0x20
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if !errors.Is(err, zip.ErrChecksum) {
		t.Errorf("error = %v, want zip checksum refusal", err)
	}
}

func TestUnpackZipManifestLocalHeaderCorruptRefused(t *testing.T) {
	data := buildRawZip(t, []rawZipEntry{{hdr: storedHeader(ManifestName), body: `{"formatVersion":1}`}})
	data[0] = 'X'
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if !errors.Is(err, zip.ErrFormat) {
		t.Errorf("error = %v, want zip format refusal for the manifest", err)
	}
}

func TestUnpackZipFileLocalHeaderCorruptRefused(t *testing.T) {
	data := makeZipBytes(t, []zipEnt{
		{name: "a", body: "x"},
		{name: "zzlink", symlink: true, body: "a"},
	})
	i := bytes.Index(data, []byte("zzlink"))
	if i < 30 {
		t.Fatal("symlink local header not found")
	}
	data[i-30] = 'X'
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if !errors.Is(err, zip.ErrFormat) {
		t.Errorf("error = %v, want zip format refusal for the symlink entry", err)
	}
}

func TestUnpackZipSymlinkTargetChecksumRefused(t *testing.T) {
	data := makeZipBytes(t, []zipEnt{{name: "lnk", symlink: true, body: "zztarget"}})
	i := bytes.Index(data, []byte("zztarget"))
	if i < 0 {
		t.Fatal("symlink target not found in archive bytes")
	}
	data[i] = 'y'
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if !errors.Is(err, zip.ErrChecksum) {
		t.Errorf("error = %v, want zip checksum refusal for the symlink target", err)
	}
}

func TestUnpackZipSymlinkTargetTooLongRefused(t *testing.T) {
	data := makeZipBytes(t, []zipEnt{{name: "lnk", symlink: true, body: strings.Repeat("a", maxSymlinkTargetBytes+1)}})
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Errorf("error = %v, want target too long refusal", err)
	}
}

func TestUnpackZipNamedPipeRefused(t *testing.T) {
	hdr := storedHeader("fifo0")
	hdr.SetMode(fs.ModeNamedPipe | 0o644)
	data := buildRawZip(t, []rawZipEntry{{hdr: hdr}})
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %v, want special file refusal", err)
	}
}

func TestUnpackGameplaneVolumeDirsListedOnce(t *testing.T) {
	ents := []tarEnt{
		{name: ManifestName, body: `{"formatVersion":1,"volumes":[` +
			`{"logicalName":"world","folder":"volumes/world"},` +
			`{"logicalName":"again","folder":"volumes/world"},` +
			`{"logicalName":"ghost","folder":"volumes/ghost"}]}`},
		{name: "volumes/world/a.txt", body: "a"},
	}
	_, res, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	if len(res.Volumes) != 1 || res.Volumes[0] != "world" {
		t.Errorf("Volumes = %v, want [world]", res.Volumes)
	}
}

func TestUnpackSymlinkEmptyTargetRefused(t *testing.T) {
	_, _, err := unpackTar(t, []tarEnt{{name: "l", typ: tar.TypeSymlink, link: ""}}, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "empty target") {
		t.Errorf("error = %v, want empty target refusal", err)
	}
}

func TestUnpackSymlinkNULTargetRefused(t *testing.T) {
	data := makeZipBytes(t, []zipEnt{{name: "lnk", symlink: true, body: "a\x00b"}})
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "zip", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Errorf("error = %v, want NUL target refusal", err)
	}
}

func TestUnpackSymlinkNameTooLongRefused(t *testing.T) {
	ents := []tarEnt{{name: strings.Repeat("l", 300), typ: tar.TypeSymlink, link: "x"}}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "create symlink") {
		t.Errorf("error = %v, want create symlink failure", err)
	}
}

func TestUnpackSymlinkTargetComponentTooLongRefused(t *testing.T) {
	ents := []tarEnt{{name: "l", typ: tar.TypeSymlink, link: strings.Repeat("a", 300)}}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "inspect symlink target") {
		t.Errorf("error = %v, want inspect symlink target failure", err)
	}
}

func TestUnpackSymlinkThroughRegularFileAccepted(t *testing.T) {
	ents := []tarEnt{
		{name: "f", body: "x"},
		{name: "l", typ: tar.TypeSymlink, link: "f/x"},
	}
	root, _, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	fi, err := os.Lstat(filepath.Join(root, "volumes", "data", "l"))
	if err != nil || fi.Mode()&fs.ModeSymlink == 0 {
		t.Errorf("symlink not created: %v", err)
	}
}

func TestUnpackSymlinkDotTargetAccepted(t *testing.T) {
	ents := []tarEnt{{name: "l", typ: tar.TypeSymlink, link: "./x"}}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err != nil {
		t.Fatalf("Unpack: %v", err)
	}
}

func TestUnpackSymlinkUnderFileRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "d", body: "x"},
		{name: "d/l", typ: tar.TypeSymlink, link: "x"},
	}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err == nil {
		t.Error("Unpack created a symlink below a regular file")
	}
}

func TestUnpackDuplicateSymlinkReplacesEarlierLink(t *testing.T) {
	ents := []tarEnt{
		{name: "l", typ: tar.TypeSymlink, link: "a"},
		{name: "l", typ: tar.TypeSymlink, link: "b"},
	}
	root, _, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	target, err := os.Readlink(filepath.Join(root, "volumes", "data", "l"))
	if err != nil || target != "b" {
		t.Errorf("link target = %q, %v, want b", target, err)
	}
}

func TestUnpackSymlinkOverDirectoryRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "x", typ: tar.TypeDir},
		{name: "x", typ: tar.TypeSymlink, link: "y"},
	}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "replace existing entry") {
		t.Errorf("error = %v, want replace refusal", err)
	}
}

func TestUnpackHardLinkToArchiveRootRefused(t *testing.T) {
	_, _, err := unpackTar(t, []tarEnt{{name: "h", typ: tar.TypeLink, link: "."}}, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "archive root") {
		t.Errorf("error = %v, want archive root refusal", err)
	}
}

func TestUnpackHardLinkToManifestRefused(t *testing.T) {
	ents := []tarEnt{
		{name: ManifestName, body: gameplaneManifestDataLegacy},
		{name: "data/h", typ: tar.TypeLink, link: ManifestName},
	}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "not part of the archive layout") {
		t.Errorf("error = %v, want layout refusal", err)
	}
}

func TestUnpackHardLinkBelowFileRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "x", body: "1"},
		{name: "y", body: "2"},
		{name: "x/h", typ: tar.TypeLink, link: "y"},
	}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err == nil {
		t.Error("Unpack created a hard link below a regular file")
	}
}

func TestUnpackHardLinkOverlongNameRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "a", body: "xy"},
		{name: strings.Repeat("h", 300), typ: tar.TypeLink, link: "a"},
	}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "create hard link") {
		t.Errorf("error = %v, want create hard link failure", err)
	}
}

func TestUnpackHardLinkOverDirectoryRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "a", body: "xy"},
		{name: "b", typ: tar.TypeDir},
		{name: "b", typ: tar.TypeLink, link: "a"},
	}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil || !strings.Contains(err.Error(), "replace existing entry") {
		t.Errorf("error = %v, want replace refusal", err)
	}
}

func TestUnpackDuplicateHardLinkReplacesEarlierLink(t *testing.T) {
	ents := []tarEnt{
		{name: "a", body: "xy"},
		{name: "b", typ: tar.TypeLink, link: "a"},
		{name: "b", typ: tar.TypeLink, link: "a"},
	}
	root, _, err := unpackTar(t, ents, UnpackOptions{})
	if err != nil {
		t.Fatalf("Unpack: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(root, "volumes", "data", "b"))
	if err != nil || string(b) != "xy" {
		t.Errorf("b = %q, %v, want xy", b, err)
	}
}

func TestOpenStreamUnknownFormatRefused(t *testing.T) {
	if _, err := openStream(writeArchive(t, []byte("x")), "rar"); err == nil {
		t.Error("openStream accepted an unknown format")
	}
}

func TestOpenTarStreamMissingDirectoryRefused(t *testing.T) {
	if _, err := openTarStream(filepath.Join(t.TempDir(), "missing", "archive")); err == nil {
		t.Error("openTarStream opened a file in a missing directory")
	}
}

func TestOpenTarStreamNotGzipRefused(t *testing.T) {
	if _, err := openTarStream(writeArchive(t, []byte("not gzip"))); err == nil {
		t.Error("openTarStream accepted a non-gzip file")
	}
}
