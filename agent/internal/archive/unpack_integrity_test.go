package archive

import (
	"archive/tar"
	"strings"
	"testing"
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
