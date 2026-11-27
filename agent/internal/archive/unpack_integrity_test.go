package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestUnpackHardLinkToReplacedSymlinkRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "d/f", body: "x"},
		{name: "d/f", typ: tar.TypeSymlink, link: "../x"},
		{name: "top", typ: tar.TypeLink, link: "d/f"},
	}
	_, _, err := unpackTar(t, ents, UnpackOptions{})
	if err == nil {
		t.Fatal("Unpack accepted a hard link to a name replaced by a symlink")
	}
	// symlink() forgets the replaced regular file, so the hard link is
	// refused by the x.files lookup; the Fstatat check is a second guard.
	if msg := err.Error(); !strings.Contains(msg, "not yet extracted") && !strings.Contains(msg, "not a regular file") {
		t.Fatalf("error %q did not come from the hard link check", err)
	}
}

func TestUnpackDirOverRegularFileRefused(t *testing.T) {
	ents := []tarEnt{
		{name: "f", body: "x"},
		{name: "f", typ: tar.TypeDir},
	}
	if _, _, err := unpackTar(t, ents, UnpackOptions{}); err == nil {
		t.Fatal("Unpack accepted a directory entry over a regular file")
	}
}

func TestUnpackTarGzBadCRCRefused(t *testing.T) {
	data := makeTarGzBytes(t, []tarEnt{{name: "f", body: "hello"}})
	data[len(data)-8] ^= 0xff
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("Unpack accepted an archive with a bad gzip CRC")
	}
}

func TestUnpackTarGzMissingTrailerRefused(t *testing.T) {
	data := makeTarGzBytes(t, []tarEnt{{name: "f", body: "hello"}})
	data = data[:len(data)-8]
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil {
		t.Fatal("Unpack accepted an archive with a missing gzip trailer")
	}
}

// tarGzWithTrailer builds a tar.gz whose gzip stream carries extra bytes
// after the tar end marker.
func tarGzWithTrailer(t *testing.T, trailer []byte) []byte {
	t.Helper()
	var tarBuf bytes.Buffer
	tw := tar.NewWriter(&tarBuf)
	if err := tw.WriteHeader(&tar.Header{Name: "f", Typeflag: tar.TypeReg, Mode: 0o644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	if _, err := gz.Write(append(tarBuf.Bytes(), trailer...)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestUnpackTarGzTrailingDataRefused(t *testing.T) {
	data := tarGzWithTrailer(t, []byte("junk after the end marker"))
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "unexpected data after tar end marker") {
		t.Fatalf("err = %v, want trailing data refusal", err)
	}
}

func TestUnpackTarGzZeroPaddingAccepted(t *testing.T) {
	data := tarGzWithTrailer(t, make([]byte, 4096))
	if _, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20}); err != nil {
		t.Fatalf("Unpack refused zero padding after the end marker: %v", err)
	}
}

// The x.files bookkeeping normally keeps a hard link off anything but a
// regular file; this checks the on-disk guard behind it on its own.
func TestHardlinkRefusesNonRegularTarget(t *testing.T) {
	root := t.TempDir()
	vol := filepath.Join(root, "volumes", plainVolume)
	if err := os.MkdirAll(vol, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/passwd", filepath.Join(vol, "s")); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	x := &extractor{rootFD: fd, max: 1 << 20, files: map[string]int64{path.Join("volumes", plainVolume, "s"): 0}, dirs: map[string]fs.FileMode{}}
	err = x.hardlink(path.Join("volumes", plainVolume, "top"), "s", layout{plain: true})
	if err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("err = %v, want not a regular file", err)
	}
}

func TestUnpackTarGzOversizedPaddingRefused(t *testing.T) {
	data := tarGzWithTrailer(t, make([]byte, maxTarTrailerBytes+1024))
	_, err := Unpack(writeArchive(t, data), t.TempDir(), UnpackOptions{Format: "tar.gz", MaxBytes: 1 << 20})
	if err == nil || !strings.Contains(err.Error(), "after tar end marker") {
		t.Fatalf("err = %v, want oversized padding refusal", err)
	}
}
