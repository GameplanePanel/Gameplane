package archive

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/sys/unix"
)

const (
	// kindGameplane marks an archive written by Gameplane (manifest at the root).
	kindGameplane = "gameplane"
	// kindPlain marks any other archive, unpacked as the "data" volume.
	kindPlain = "plain"
	// legacyFolder is the single data folder of capture format 1 archives.
	legacyFolder = "data"
	// plainVolume is the volume a plain archive is placed in.
	plainVolume = "data"
	// maxSymlinkTargetBytes caps the stored target of a zip symlink.
	maxSymlinkTargetBytes = 4096
	// zipFileMode and zipDirMode replace the stored permissions of zip entries.
	zipFileMode fs.FileMode = 0o644
	zipDirMode  fs.FileMode = 0o755
)

// errSizeLimit reports that the unpacked content exceeds the configured cap.
var errSizeLimit = errors.New("unpacked size limit exceeded")

// UnpackOptions configures one Unpack run.
type UnpackOptions struct {
	// Format is the archive format: "tar.gz" or "zip".
	Format string
	// Plain forces plain handling even when a Gameplane manifest is present.
	Plain bool
	// MaxBytes caps the bytes written to the unpacked tree.
	MaxBytes int64
}

// UnpackResult describes an unpacked archive.
type UnpackResult struct {
	// Kind is kindGameplane or kindPlain.
	Kind string
	// Manifest is the decoded manifest of a Gameplane archive, nil otherwise.
	Manifest *Manifest
	// UnpackedBytes counts the file content written, hard link copies included.
	UnpackedBytes int64
	// Volumes lists the volume folders under volumes/ that hold data.
	Volumes []string
	// ZipPermissionsReset is true when zip entry permissions were replaced.
	ZipPermissionsReset bool
}

// entryKind classifies one archive entry.
type entryKind int

const (
	entryFile entryKind = iota
	entryDir
	entrySymlink
	entryHardlink
	entrySpecial
)

// rawEntry is one archive entry as the format reader reports it.
type rawEntry struct {
	name     string
	kind     entryKind
	mode     fs.FileMode
	linkname string
}

// entryStream reads the entries of one archive in order.
type entryStream interface {
	// next advances to the next entry and returns io.EOF after the last one.
	next() (rawEntry, error)
	// body returns the content of the entry last returned by next.
	body() (io.Reader, error)
	close() error
}

// Unpack extracts the archive at archivePath into root, which must exist and
// be empty. Refused entries (absolute or ".." names, escaping or absolute
// symlinks, hard links outside the archive, devices and FIFOs, or content above
// opts.MaxBytes) abort the run with an error that names the entry.
//
// A Gameplane archive (its first entry is the manifest) is laid out as
// volumes/<name>/ per its manifest. Any other archive is placed under
// volumes/data/, with one wrapping folder stripped when every entry shares it.
func Unpack(archivePath, root string, opts UnpackOptions) (UnpackResult, error) {
	if opts.MaxBytes <= 0 {
		return UnpackResult{}, errors.New("unpack size limit must be positive")
	}
	if opts.Format != "tar.gz" && opts.Format != "zip" {
		return UnpackResult{}, fmt.Errorf("unsupported archive format %q", opts.Format)
	}
	sc, err := scanArchive(archivePath, opts)
	if err != nil {
		return UnpackResult{}, err
	}
	lay := newLayout(sc)

	rootFD, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return UnpackResult{}, fmt.Errorf("open unpack root: %w", err)
	}
	defer func() { _ = unix.Close(rootFD) }()

	x := &extractor{
		rootFD: rootFD,
		max:    opts.MaxBytes,
		zip:    opts.Format == "zip",
		files:  map[string]int64{},
		dirs:   map[string]fs.FileMode{},
	}
	if lay.plain {
		// A plain archive always has its volume, even when it is empty.
		fd, err := openDirAt(rootFD, path.Join("volumes", plainVolume), true)
		if err != nil {
			return UnpackResult{}, err
		}
		_ = unix.Close(fd)
	}
	if err := x.extractAll(archivePath, opts.Format, lay); err != nil {
		return UnpackResult{}, err
	}
	if err := x.applyDirModes(); err != nil {
		return UnpackResult{}, err
	}

	res := UnpackResult{
		Kind:                sc.kind,
		Manifest:            sc.manifest,
		UnpackedBytes:       x.written,
		Volumes:             volumeDirs(root, sc, lay),
		ZipPermissionsReset: opts.Format == "zip",
	}
	return res, nil
}

// scanResult is what the first pass learns about an archive.
type scanResult struct {
	kind     string
	manifest *Manifest
	// wrapper is the single top-level folder every entry sits under, or "".
	wrapper string
}

// scanArchive reads every entry header of the archive (bodies are only read
// for the manifest) to detect the archive kind and any wrapping folder.
func scanArchive(p string, opts UnpackOptions) (scanResult, error) {
	s, err := openStream(p, opts.Format)
	if err != nil {
		return scanResult{}, err
	}
	defer func() { _ = s.close() }()

	sc := scanResult{kind: kindPlain}
	var (
		first   = true
		seen    bool
		wrapOK  = true
		wrapper string
	)
	for {
		e, err := s.next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return scanResult{}, err
		}
		name, err := cleanEntryName(e.name)
		if err != nil {
			return scanResult{}, err
		}
		if first && name == ManifestName && e.kind == entryFile && !opts.Plain {
			body, err := s.body()
			if err != nil {
				return scanResult{}, err
			}
			m, err := DecodeManifest(body)
			if err != nil {
				return scanResult{}, fmt.Errorf("archive manifest: %w", err)
			}
			sc.kind = kindGameplane
			sc.manifest = &m
		}
		first = false
		if name == "" {
			continue
		}
		elems := strings.Split(name, "/")
		deeper := len(elems) > 1 || e.kind == entryDir
		if !seen {
			wrapper = elems[0]
			seen = true
		}
		if elems[0] != wrapper || !deeper {
			wrapOK = false
		}
	}
	if sc.kind == kindPlain && seen && wrapOK {
		sc.wrapper = wrapper
	}
	return sc, nil
}

// layout maps archive names to their place under the unpacked root.
type layout struct {
	plain     bool
	gameplane bool
	legacy    bool
	// folders lists the capture format 2 volume folders ("volumes/<name>").
	folders []string
	// strip is the wrapping folder removed from plain archive names.
	strip string
}

func newLayout(sc scanResult) layout {
	l := layout{plain: sc.kind == kindPlain, strip: sc.wrapper}
	if sc.kind != kindGameplane {
		return l
	}
	l.gameplane = true
	for _, v := range sc.manifest.Volumes {
		if v.Folder == legacyFolder {
			l.legacy = true
			continue
		}
		l.folders = append(l.folders, v.Folder)
	}
	return l
}

// dest returns the path under the unpacked root for an archive name, and
// false when the entry is dropped. Entries outside the layout are an error.
func (l layout) dest(name string, dir bool) (string, bool, error) {
	var dst string
	if l.gameplane {
		if name == ManifestName {
			return "", false, nil
		}
		d, ok := l.gameplaneDest(name)
		if !ok {
			return "", false, fmt.Errorf("entry %q is outside the volumes in the archive manifest", name)
		}
		dst = d
	} else {
		switch {
		case l.strip != "" && name == l.strip:
			return "", false, nil
		case l.strip != "" && strings.HasPrefix(name, l.strip+"/"):
			dst = path.Join("volumes", plainVolume, strings.TrimPrefix(name, l.strip+"/"))
		default:
			dst = path.Join("volumes", plainVolume, name)
		}
	}
	if !dir && (dst == "volumes" || path.Dir(dst) == "volumes") {
		return "", false, fmt.Errorf("entry %q must be a directory", name)
	}
	return dst, true, nil
}

func (l layout) gameplaneDest(name string) (string, bool) {
	if name == "volumes" {
		return name, true
	}
	if l.legacy {
		if name == legacyFolder {
			return path.Join("volumes", legacyFolder), true
		}
		if strings.HasPrefix(name, legacyFolder+"/") {
			return path.Join("volumes", name), true
		}
	}
	for _, f := range l.folders {
		if name == f || strings.HasPrefix(name, f+"/") {
			return name, true
		}
	}
	return "", false
}

// extractor writes entries below a root directory descriptor. It never
// follows a symlink: every path is walked with openat(O_NOFOLLOW) from the
// root descriptor, so an entry cannot write outside the root through a link
// that an earlier entry created.
type extractor struct {
	rootFD  int
	max     int64
	zip     bool
	written int64
	// files maps the destination of every regular file written to its size,
	// which is what a later hard link may refer to.
	files map[string]int64
	// dirs maps directory destinations to the mode they get at the end.
	dirs map[string]fs.FileMode
}

// extractAll streams the archive and writes every kept entry.
func (x *extractor) extractAll(archivePath, format string, lay layout) error {
	s, err := openStream(archivePath, format)
	if err != nil {
		return err
	}
	defer func() { _ = s.close() }()

	for {
		e, err := s.next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name, err := cleanEntryName(e.name)
		if err != nil {
			return err
		}
		if name == "" {
			continue
		}
		if e.kind == entrySpecial {
			return fmt.Errorf("entry %q: devices, FIFOs and sockets are not allowed", e.name)
		}
		dst, keep, err := lay.dest(name, e.kind == entryDir)
		if err != nil {
			return err
		}
		if !keep {
			continue
		}
		if err := x.entry(s, e, dst, lay); err != nil {
			return fmt.Errorf("entry %q: %w", e.name, err)
		}
	}
}

func (x *extractor) entry(s entryStream, e rawEntry, dst string, lay layout) error {
	switch e.kind {
	case entryDir:
		// The owner always keeps rwx so the copy worker and the game server,
		// which run as the volume owner, can still walk the tree.
		mode := e.mode&0o777 | 0o700
		if x.zip {
			mode = zipDirMode
		}
		return x.mkdir(dst, mode)
	case entryFile:
		// The owner always keeps read and write so the copy worker can read it.
		mode := e.mode&0o777 | 0o600
		if x.zip {
			mode = zipFileMode
		}
		body, err := s.body()
		if err != nil {
			return err
		}
		return x.file(dst, mode, body)
	case entrySymlink:
		return x.symlink(dst, e.linkname)
	case entryHardlink:
		return x.hardlink(dst, e.linkname, lay)
	}
	return errors.New("unsupported entry type")
}

// cleanEntryName validates a raw archive name and returns its clean form. The
// empty result names the archive root, which carries nothing to extract.
func cleanEntryName(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("entry with an empty name")
	}
	if strings.ContainsRune(raw, 0) {
		return "", fmt.Errorf("entry %q: name contains a NUL byte", raw)
	}
	if strings.HasPrefix(raw, "/") {
		return "", fmt.Errorf("entry %q: absolute path", raw)
	}
	for _, el := range strings.Split(raw, "/") {
		if el == ".." {
			return "", fmt.Errorf("entry %q: parent directory reference", raw)
		}
	}
	name := path.Clean(raw)
	if name == "." {
		return "", nil
	}
	return name, nil
}

// openDirAt returns a new descriptor for dir, a slash-separated path below
// rootFD. When create is set, missing directories are made. The caller closes
// the result.
func openDirAt(rootFD int, dir string, create bool) (int, error) {
	fd, err := unix.Dup(rootFD)
	if err != nil {
		return -1, fmt.Errorf("duplicate root descriptor: %w", err)
	}
	if dir == "." || dir == "" {
		return fd, nil
	}
	for _, el := range strings.Split(dir, "/") {
		next, err := openNoFollowDir(fd, el, create)
		_ = unix.Close(fd)
		if err != nil {
			return -1, err
		}
		fd = next
	}
	return fd, nil
}

// openNoFollowDir opens one directory component below dirFD without following
// a symlink, creating it when create is set and it is missing.
func openNoFollowDir(dirFD int, name string, create bool) (int, error) {
	flags := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(dirFD, name, flags, 0)
	if errors.Is(err, unix.ENOENT) && create {
		if err := unix.Mkdirat(dirFD, name, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create directory %q: %w", name, err)
		}
		fd, err = unix.Openat(dirFD, name, flags, 0)
	}
	if err != nil {
		return -1, fmt.Errorf("directory %q: %w", name, err)
	}
	return fd, nil
}

// parentFD returns a descriptor for the directory that holds name, creating
// missing parents, and the base name inside it.
func (x *extractor) parentFD(name string) (int, string, error) {
	fd, err := openDirAt(x.rootFD, path.Dir(name), true)
	if err != nil {
		return -1, "", err
	}
	return fd, path.Base(name), nil
}

func (x *extractor) mkdir(dst string, mode fs.FileMode) error {
	pfd, base, err := x.parentFD(dst)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	if err := unix.Mkdirat(pfd, base, 0o755); err != nil && !errors.Is(err, unix.EEXIST) {
		return fmt.Errorf("create directory: %w", err)
	}
	fd, err := openNoFollowDir(pfd, base, false)
	if err != nil {
		return err
	}
	_ = unix.Close(fd)
	x.dirs[dst] = mode
	return nil
}

// createFile creates base in pfd exclusively. An existing entry of that name
// is replaced (unlinked, never followed); a directory is refused by unlink.
func createFile(pfd int, base string) (int, error) {
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(pfd, base, flags, 0o600)
	if errors.Is(err, unix.EEXIST) {
		if err := unix.Unlinkat(pfd, base, 0); err != nil {
			return -1, fmt.Errorf("replace existing entry: %w", err)
		}
		fd, err = unix.Openat(pfd, base, flags, 0o600)
	}
	return fd, err
}

func (x *extractor) file(dst string, mode fs.FileMode, body io.Reader) error {
	pfd, base, err := x.parentFD(dst)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	fd, err := createFile(pfd, base)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	f := os.NewFile(uintptr(fd), base)

	limit := x.max - x.written
	n, err := io.Copy(f, io.LimitReader(body, limit+1))
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("write file: %w", err)
	}
	if n > limit {
		_ = f.Close()
		return fmt.Errorf("%w (%d bytes)", errSizeLimit, x.max)
	}
	if err := f.Chmod(mode); err != nil {
		_ = f.Close()
		return fmt.Errorf("set mode: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close file: %w", err)
	}
	x.written += n
	x.files[dst] = n
	return nil
}

// checkLinkTarget refuses a symlink target that could let the link reach
// outside the unpacked root. The target is walked from the link's directory
// over the tree extracted so far. Each step is checked against the real tree:
// ".." steps up one directory, and a component that is an existing symlink is
// refused, so no chain of links is followed while it is checked. A ".." that
// follows a component which is missing or not a directory is refused too,
// because a later entry may still create or change that component. Directories
// cannot be replaced by a later entry, so a validated ".." chain stays inside
// the root.
func (x *extractor) checkLinkTarget(dst, target string) error {
	if target == "" {
		return errors.New("symlink with an empty target")
	}
	if strings.ContainsRune(target, 0) {
		return errors.New("symlink target contains a NUL byte")
	}
	if strings.HasPrefix(target, "/") {
		return fmt.Errorf("absolute symlink target %q", target)
	}
	// pos holds the directories from the root to the current position. The
	// link's own directory exists (parentFD created it) and is a real directory.
	var pos []string
	if d := path.Dir(dst); d != "." {
		pos = strings.Split(d, "/")
	}
	// floor keeps a link inside its own volume: under volumes/<name>/ a ".."
	// may not climb above that folder, because each volume is copied to its
	// own target claim and a link into a sibling volume would point outside it.
	floor := 0
	if len(pos) >= 2 && pos[0] == "volumes" {
		floor = 2
	}
	// stable is true while every component of pos is an existing directory.
	stable := true
	for _, el := range strings.Split(target, "/") {
		switch el {
		case "", ".":
			continue
		case "..":
			if len(pos) <= floor {
				return fmt.Errorf("symlink target %q escapes its volume folder", target)
			}
			if !stable {
				return fmt.Errorf("symlink target %q steps up through an entry not yet extracted", target)
			}
			pos = pos[:len(pos)-1]
			continue
		}
		pos = append(pos, el)
		if !stable {
			continue
		}
		// Every earlier component is a real directory, so the kernel does not
		// follow a link while resolving this path; only the last one is inspected.
		rel := strings.Join(pos, "/")
		var st unix.Stat_t
		err := unix.Fstatat(x.rootFD, rel, &st, unix.AT_SYMLINK_NOFOLLOW)
		switch {
		case errors.Is(err, unix.ENOENT), errors.Is(err, unix.ENOTDIR):
			stable = false
		case err != nil:
			return fmt.Errorf("inspect symlink target %q: %w", target, err)
		case st.Mode&unix.S_IFMT == unix.S_IFLNK:
			return fmt.Errorf("symlink target %q passes through existing symlink %q", target, rel)
		case st.Mode&unix.S_IFMT != unix.S_IFDIR:
			stable = false
		}
	}
	return nil
}

func (x *extractor) symlink(dst, target string) error {
	pfd, base, err := x.parentFD(dst)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	if err := x.checkLinkTarget(dst, target); err != nil {
		return err
	}
	err = unix.Symlinkat(target, pfd, base)
	if errors.Is(err, unix.EEXIST) {
		if err := unix.Unlinkat(pfd, base, 0); err != nil {
			return fmt.Errorf("replace existing entry: %w", err)
		}
		err = unix.Symlinkat(target, pfd, base)
	}
	if err != nil {
		return fmt.Errorf("create symlink: %w", err)
	}
	return nil
}

// hardlink links dst to a regular file extracted earlier in this run. The
// linked content counts against the size cap again, because a copy of it is
// made when the volume is restored.
func (x *extractor) hardlink(dst, rawTarget string, lay layout) error {
	t, err := cleanEntryName(rawTarget)
	if err != nil {
		return fmt.Errorf("hard link target: %w", err)
	}
	if t == "" {
		return errors.New("hard link target is the archive root")
	}
	td, keep, err := lay.dest(t, false)
	if err != nil {
		return fmt.Errorf("hard link target: %w", err)
	}
	if !keep {
		return fmt.Errorf("hard link target %q is not part of the archive layout", rawTarget)
	}
	size, ok := x.files[td]
	if !ok {
		return fmt.Errorf("hard link target %q is outside the archive or not yet extracted", rawTarget)
	}
	if limit := x.max - x.written; size > limit {
		return fmt.Errorf("%w (%d bytes)", errSizeLimit, x.max)
	}

	tfd, err := openDirAt(x.rootFD, path.Dir(td), false)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(tfd) }()
	pfd, base, err := x.parentFD(dst)
	if err != nil {
		return err
	}
	defer func() { _ = unix.Close(pfd) }()
	tbase := path.Base(td)
	err = unix.Linkat(tfd, tbase, pfd, base, 0)
	if errors.Is(err, unix.EEXIST) {
		if err := unix.Unlinkat(pfd, base, 0); err != nil {
			return fmt.Errorf("replace existing entry: %w", err)
		}
		err = unix.Linkat(tfd, tbase, pfd, base, 0)
	}
	if err != nil {
		return fmt.Errorf("create hard link: %w", err)
	}
	x.written += size
	x.files[dst] = size
	return nil
}

// applyDirModes sets the final mode of every directory from the archive. The
// deepest directories go first so a restrictive parent does not block them.
func (x *extractor) applyDirModes() error {
	names := make([]string, 0, len(x.dirs))
	for n := range x.dirs {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		return strings.Count(names[i], "/") > strings.Count(names[j], "/")
	})
	for _, n := range names {
		fd, err := openDirAt(x.rootFD, n, false)
		if err != nil {
			return fmt.Errorf("entry %q: %w", n, err)
		}
		err = unix.Fchmod(fd, uint32(x.dirs[n].Perm()))
		_ = unix.Close(fd)
		if err != nil {
			return fmt.Errorf("entry %q: set mode: %w", n, err)
		}
	}
	return nil
}

// volumeDirs lists the volume folders that exist after unpacking, in manifest
// order. Gameplane archives use the folder name; a legacy data/ folder is "data".
func volumeDirs(root string, sc scanResult, lay layout) []string {
	out := []string{}
	names := []string{plainVolume}
	if lay.gameplane {
		names = names[:0]
		for _, v := range sc.manifest.Volumes {
			names = append(names, strings.TrimPrefix(v.Folder, "volumes/"))
		}
	}
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		fi, err := os.Lstat(filepath.Join(root, "volumes", name))
		if err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, name)
	}
	return out
}

// openStream opens an archive for sequential reading in the given format.
func openStream(p, format string) (entryStream, error) {
	switch format {
	case "tar.gz":
		return openTarStream(p)
	case "zip":
		return openZipStream(p)
	}
	return nil, fmt.Errorf("unsupported archive format %q", format)
}

// tarStream reads a gzip-compressed tar archive.
type tarStream struct {
	f  *os.File
	gz *gzip.Reader
	tr *tar.Reader
}

// openArchiveFile opens the archive at p through a root scoped to its directory.
// The returned file stays valid after the root is closed.
func openArchiveFile(p string) (*os.File, error) {
	dir, err := os.OpenRoot(filepath.Dir(p))
	if err != nil {
		return nil, err
	}
	defer func() { _ = dir.Close() }()
	return dir.Open(filepath.Base(p))
}

func openTarStream(p string) (*tarStream, error) {
	f, err := openArchiveFile(p)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	gz, err := gzip.NewReader(f)
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("read archive: %w", err)
	}
	return &tarStream{f: f, gz: gz, tr: tar.NewReader(gz)}, nil
}

func (s *tarStream) next() (rawEntry, error) {
	var hdr *tar.Header
	for {
		h, err := s.tr.Next()
		if errors.Is(err, io.EOF) {
			return rawEntry{}, io.EOF
		}
		if err != nil {
			return rawEntry{}, fmt.Errorf("read archive: %w", err)
		}
		if h.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		hdr = h
		break
	}
	// Masking in int64 first bounds the value to 0o777, so the conversion cannot overflow.
	perm := hdr.Mode & 0o777
	e := rawEntry{name: hdr.Name, mode: fs.FileMode(perm), linkname: hdr.Linkname}
	switch hdr.Typeflag {
	case tar.TypeReg:
		e.kind = entryFile
	case tar.TypeDir:
		e.kind = entryDir
	case tar.TypeSymlink:
		e.kind = entrySymlink
	case tar.TypeLink:
		e.kind = entryHardlink
	default:
		e.kind = entrySpecial
	}
	return e, nil
}

func (s *tarStream) body() (io.Reader, error) {
	return s.tr, nil
}

func (s *tarStream) close() error {
	gzErr := s.gz.Close()
	fErr := s.f.Close()
	if gzErr != nil {
		return gzErr
	}
	return fErr
}

// zipStream reads a zip archive in central directory order.
type zipStream struct {
	zr  *zip.ReadCloser
	idx int
	cur io.ReadCloser
}

func openZipStream(p string) (*zipStream, error) {
	zr, err := zip.OpenReader(p)
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	return &zipStream{zr: zr}, nil
}

func (s *zipStream) next() (rawEntry, error) {
	if s.cur != nil {
		_ = s.cur.Close()
		s.cur = nil
	}
	if s.idx >= len(s.zr.File) {
		return rawEntry{}, io.EOF
	}
	f := s.zr.File[s.idx]
	s.idx++
	m := f.Mode()
	e := rawEntry{name: f.Name, mode: m.Perm()}
	switch {
	case m.IsDir() || strings.HasSuffix(f.Name, "/"):
		e.kind = entryDir
	case m&fs.ModeSymlink != 0:
		e.kind = entrySymlink
		target, err := s.readSymlink(f)
		if err != nil {
			return rawEntry{}, err
		}
		e.linkname = target
	case m.Type()&(fs.ModeDevice|fs.ModeCharDevice|fs.ModeNamedPipe|fs.ModeSocket|fs.ModeIrregular) != 0:
		e.kind = entrySpecial
	default:
		e.kind = entryFile
	}
	return e, nil
}

// readSymlink reads the stored target of a zip symlink entry.
func (s *zipStream) readSymlink(f *zip.File) (string, error) {
	rc, err := f.Open()
	if err != nil {
		return "", fmt.Errorf("open %q: %w", f.Name, err)
	}
	defer func() { _ = rc.Close() }()
	raw, err := io.ReadAll(io.LimitReader(rc, maxSymlinkTargetBytes+1))
	if err != nil {
		return "", fmt.Errorf("read symlink %q: %w", f.Name, err)
	}
	if len(raw) > maxSymlinkTargetBytes {
		return "", fmt.Errorf("symlink %q: target too long", f.Name)
	}
	return string(raw), nil
}

func (s *zipStream) body() (io.Reader, error) {
	f := s.zr.File[s.idx-1]
	rc, err := f.Open()
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", f.Name, err)
	}
	s.cur = rc
	return rc, nil
}

func (s *zipStream) close() error {
	if s.cur != nil {
		_ = s.cur.Close()
		s.cur = nil
	}
	return s.zr.Close()
}
