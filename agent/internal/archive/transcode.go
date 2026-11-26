package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"
)

// Stats summarises one Transcode run.
type Stats struct {
	// Entries counts the entries written to the archive, the manifest included.
	Entries int
	// Skipped counts source entries that were left out of the archive.
	Skipped int
	// Bytes counts the regular file content copied into the archive.
	Bytes int64
}

// volumesDir is the parent folder of every capture format 2 volume folder. Its
// directory entry is kept so the archive layout stays browsable.
const volumesDir = "volumes"

// Transcode reads the tar stream produced by "restic dump <snapshot> /
// --archive tar" from src and writes a downloadable archive to dst in the
// given format ("tar.gz" or "zip"). The manifest is written first. Only the
// volume folders named in m (and the "volumes" parent) are kept; every other
// entry is skipped and its body discarded.
func Transcode(dst io.Writer, src io.Reader, format string, m Manifest) (Stats, error) {
	switch format {
	case "tar.gz":
		return transcodeTarGz(dst, src, m)
	case "zip":
		return transcodeZip(dst, src, m)
	default:
		return Stats{}, fmt.Errorf("unsupported format %q", format)
	}
}

func transcodeTarGz(dst io.Writer, src io.Reader, m Manifest) (Stats, error) {
	var stats Stats
	gz := gzip.NewWriter(dst)
	tw := tar.NewWriter(gz)

	if err := writeManifestTar(tw, m); err != nil {
		return stats, err
	}
	stats.Entries++

	err := walk(src, volumeFolders(m), true, &stats, func(hdr *tar.Header, name string, body io.Reader) error {
		out := &tar.Header{
			Typeflag: hdr.Typeflag,
			Name:     name,
			Linkname: hdr.Linkname,
			Mode:     hdr.Mode,
			Uid:      hdr.Uid,
			Gid:      hdr.Gid,
			ModTime:  hdr.ModTime,
		}
		regular := false
		switch hdr.Typeflag {
		case tar.TypeDir, tar.TypeSymlink, tar.TypeLink:
		default:
			// Any other data-bearing entry is written as a plain regular file.
			out.Typeflag = tar.TypeReg
			out.Size = hdr.Size
			regular = true
		}
		if err := tw.WriteHeader(out); err != nil {
			return fmt.Errorf("write tar header %q: %w", name, err)
		}
		if regular {
			n, err := io.Copy(tw, body)
			stats.Bytes += n
			if err != nil {
				return fmt.Errorf("copy %q: %w", name, err)
			}
		}
		stats.Entries++
		return nil
	})
	if err != nil {
		return stats, err
	}
	if err := tw.Close(); err != nil {
		return stats, fmt.Errorf("close tar: %w", err)
	}
	if err := gz.Close(); err != nil {
		return stats, fmt.Errorf("close gzip: %w", err)
	}
	return stats, nil
}

func transcodeZip(dst io.Writer, src io.Reader, m Manifest) (Stats, error) {
	var stats Stats
	zw := zip.NewWriter(dst)

	if err := writeManifestZip(zw, m); err != nil {
		return stats, err
	}
	stats.Entries++

	// Hard links cannot be expressed in a zip stream, so they are skipped.
	err := walk(src, volumeFolders(m), false, &stats, func(hdr *tar.Header, name string, body io.Reader) error {
		switch hdr.Typeflag {
		case tar.TypeDir:
			fh := &zip.FileHeader{Name: name + "/", Modified: hdr.ModTime}
			fh.SetMode(fs.ModeDir | hdr.FileInfo().Mode().Perm())
			if _, err := zw.CreateHeader(fh); err != nil {
				return fmt.Errorf("write zip directory %q: %w", name, err)
			}
		case tar.TypeSymlink:
			fh := &zip.FileHeader{Name: name, Modified: hdr.ModTime, Method: zip.Store}
			fh.SetMode(fs.ModeSymlink | 0o777)
			w, err := zw.CreateHeader(fh)
			if err != nil {
				return fmt.Errorf("write zip symlink %q: %w", name, err)
			}
			if _, err := io.WriteString(w, hdr.Linkname); err != nil {
				return fmt.Errorf("write zip symlink %q: %w", name, err)
			}
		default:
			fh := &zip.FileHeader{Name: name, Method: zip.Deflate, Modified: hdr.ModTime}
			fh.SetMode(hdr.FileInfo().Mode())
			w, err := zw.CreateHeader(fh)
			if err != nil {
				return fmt.Errorf("write zip entry %q: %w", name, err)
			}
			n, err := io.Copy(w, body)
			stats.Bytes += n
			if err != nil {
				return fmt.Errorf("copy %q: %w", name, err)
			}
		}
		stats.Entries++
		return nil
	})
	if err != nil {
		return stats, err
	}
	if err := zw.Close(); err != nil {
		return stats, fmt.Errorf("close zip: %w", err)
	}
	return stats, nil
}

// walk reads every entry of the tar stream in src. Entries outside the kept
// set, device and FIFO nodes, and hard links when keepLinks is false are
// counted as skipped. fn receives each kept entry with its archive name.
func walk(src io.Reader, folders []string, keepLinks bool, stats *Stats,
	fn func(hdr *tar.Header, name string, body io.Reader) error) error {
	tr := tar.NewReader(src)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("read archive: %w", err)
		}
		name, keep, err := keptName(hdr, folders)
		if err != nil {
			return err
		}
		if !keep || (hdr.Typeflag == tar.TypeLink && !keepLinks) {
			stats.Skipped++
			continue
		}
		if err := fn(hdr, name, tr); err != nil {
			return err
		}
	}
}

// keptName returns the archive name of a source entry and whether the entry
// belongs in the archive. Entries whose raw name contains a ".." element are
// rejected with an error.
func keptName(hdr *tar.Header, folders []string) (string, bool, error) {
	for _, el := range strings.Split(hdr.Name, "/") {
		if el == ".." {
			return "", false, fmt.Errorf("unsafe archive entry %q", hdr.Name)
		}
	}
	switch hdr.Typeflag {
	case tar.TypeChar, tar.TypeBlock, tar.TypeFifo:
		return "", false, nil
	}
	name := strings.TrimPrefix(path.Clean("/"+hdr.Name), "/")
	if name == volumesDir || underFolder(name, folders) {
		return name, true, nil
	}
	return "", false, nil
}

// underFolder reports whether name is one of folders or lies below one.
func underFolder(name string, folders []string) bool {
	for _, f := range folders {
		if name == f || strings.HasPrefix(name, f+"/") {
			return true
		}
	}
	return false
}

// volumeFolders lists the archive folders of the manifest volumes.
func volumeFolders(m Manifest) []string {
	out := make([]string, 0, len(m.Volumes))
	for _, v := range m.Volumes {
		if v.Folder != "" {
			out = append(out, v.Folder)
		}
	}
	return out
}

// manifestBytes encodes the manifest and returns its bytes and the time the
// manifest entry is stamped with. A zero capture time falls back to the Unix
// epoch so tar headers stay encodable.
func manifestBytes(m Manifest) ([]byte, time.Time, error) {
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		return nil, time.Time{}, err
	}
	modTime := m.CapturedAt
	if modTime.IsZero() {
		modTime = time.Unix(0, 0).UTC()
	}
	return buf.Bytes(), modTime, nil
}

func writeManifestTar(tw *tar.Writer, m Manifest) error {
	data, modTime, err := manifestBytes(m)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Typeflag: tar.TypeReg,
		Name:     ManifestName,
		Mode:     0o644,
		Size:     int64(len(data)),
		ModTime:  modTime,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

func writeManifestZip(zw *zip.Writer, m Manifest) error {
	data, modTime, err := manifestBytes(m)
	if err != nil {
		return err
	}
	fh := &zip.FileHeader{Name: ManifestName, Method: zip.Deflate, Modified: modTime}
	fh.SetMode(0o644)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return fmt.Errorf("write manifest header: %w", err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}
