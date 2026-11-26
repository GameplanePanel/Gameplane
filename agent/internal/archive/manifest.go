package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// ManifestName is the file name of the manifest at the root of an archive.
const ManifestName = "gameplane-backup.json"

// FormatVersion is the newest manifest format this worker writes and reads.
const FormatVersion = 1

// maxManifestBytes caps how much manifest data DecodeManifest reads.
const maxManifestBytes = 1 << 20

// ErrNewerFormat reports a manifest written by a newer Gameplane than this worker.
var ErrNewerFormat = errors.New("archive was made by a newer Gameplane")

// volumeNamePattern matches a DNS-label style logical volume name.
var volumeNamePattern = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$`)

// Manifest describes an archive: who made it, what it holds and where the
// volume data lives inside the archive.
type Manifest struct {
	FormatVersion        int              `json:"formatVersion"`
	Writer               string           `json:"writer,omitempty"`
	Cluster              string           `json:"cluster,omitempty"`
	Namespace            string           `json:"namespace,omitempty"`
	Server               ManifestServer   `json:"server,omitempty"`
	Template             ManifestTemplate `json:"template,omitempty"`
	GameVersion          string           `json:"gameVersion,omitempty"`
	CapturedAt           time.Time        `json:"capturedAt,omitempty"`
	ResticSnapshotID     string           `json:"resticSnapshotID,omitempty"`
	CaptureFormatVersion int              `json:"captureFormatVersion,omitempty"`
	Volumes              []ManifestVolume `json:"volumes,omitempty"`
}

// ManifestServer identifies the GameServer the archive was taken from.
type ManifestServer struct {
	Name string `json:"name,omitempty"`
	UID  string `json:"uid,omitempty"`
}

// ManifestTemplate identifies the GameTemplate revision the server ran.
type ManifestTemplate struct {
	Name     string `json:"name,omitempty"`
	Revision string `json:"revision,omitempty"`
}

// ManifestVolume records one archived volume and its folder in the archive.
type ManifestVolume struct {
	LogicalName    string `json:"logicalName,omitempty"`
	Folder         string `json:"folder,omitempty"`
	Classification string `json:"classification,omitempty"`
}

// DecodeManifest reads and validates a manifest. It never reads more than
// maxManifestBytes of input.
func DecodeManifest(r io.Reader) (Manifest, error) {
	raw, err := io.ReadAll(io.LimitReader(r, maxManifestBytes+1))
	if err != nil {
		return Manifest{}, fmt.Errorf("read manifest: %w", err)
	}
	if len(raw) > maxManifestBytes {
		return Manifest{}, errors.New("manifest too large")
	}
	var m Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return Manifest{}, fmt.Errorf("decode manifest: %w", err)
	}
	if m.FormatVersion < 1 {
		return Manifest{}, errors.New("manifest formatVersion missing")
	}
	if m.FormatVersion > FormatVersion {
		return Manifest{}, fmt.Errorf("%w (formatVersion %d)", ErrNewerFormat, m.FormatVersion)
	}
	for _, v := range m.Volumes {
		if !ValidFolder(v.Folder) {
			return Manifest{}, fmt.Errorf("manifest volume %q has invalid folder %q", v.LogicalName, v.Folder)
		}
	}
	return m, nil
}

// Encode writes the manifest as indented JSON followed by a newline.
func (m Manifest) Encode(w io.Writer) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return fmt.Errorf("encode manifest: %w", err)
	}
	return nil
}

// VolumeFolder returns the archive folder for a volume. Capture format 2 and
// later keep one folder per volume; older archives hold a single "data" folder.
func VolumeFolder(logicalName string, captureFormatVersion int) string {
	if captureFormatVersion >= 2 {
		return "volumes/" + logicalName
	}
	return "data"
}

// ValidFolder reports whether f is a folder the archive format allows: "data"
// or "volumes/<name>" with a DNS-label name.
func ValidFolder(f string) bool {
	if f == "data" {
		return true
	}
	name, ok := strings.CutPrefix(f, "volumes/")
	return ok && volumeNamePattern.MatchString(name)
}
