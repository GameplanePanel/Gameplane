package archive

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

func sampleManifest() Manifest {
	return Manifest{
		FormatVersion:        FormatVersion,
		Writer:               "api/1.2.3",
		Cluster:              "prod",
		Namespace:            "games",
		Server:               ManifestServer{Name: "mc-1", UID: "uid-123"},
		Template:             ManifestTemplate{Name: "minecraft", Revision: "7"},
		GameVersion:          "1.21.4",
		CapturedAt:           time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC),
		ResticSnapshotID:     "abc123",
		CaptureFormatVersion: 2,
		Volumes: []ManifestVolume{
			{LogicalName: "world", Folder: "volumes/world", Classification: "save"},
			{LogicalName: "mods", Folder: "volumes/mods", Classification: "config"},
		},
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

func TestManifestRoundTrip(t *testing.T) {
	want := sampleManifest()
	var buf bytes.Buffer
	if err := want.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if !strings.HasSuffix(buf.String(), "}\n") {
		t.Errorf("Encode output should end with a closing brace and newline, got %q", buf.String())
	}
	if !strings.Contains(buf.String(), `"resticSnapshotID": "abc123"`) {
		t.Errorf("encoded manifest missing resticSnapshotID key: %s", buf.String())
	}
	got, err := DecodeManifest(&buf)
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if !got.CapturedAt.Equal(want.CapturedAt) {
		t.Errorf("CapturedAt = %v, want %v", got.CapturedAt, want.CapturedAt)
	}
	got.CapturedAt = want.CapturedAt
	if !reflect.DeepEqual(got, want) {
		t.Errorf("round trip mismatch:\n got %+v\nwant %+v", got, want)
	}
}

func TestManifestEncodeWriteError(t *testing.T) {
	if err := sampleManifest().Encode(failWriter{}); err == nil {
		t.Fatal("Encode to failing writer: want error")
	}
}

func TestDecodeManifestNewerFormat(t *testing.T) {
	m := sampleManifest()
	m.FormatVersion = FormatVersion + 1
	var buf bytes.Buffer
	if err := m.Encode(&buf); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	_, err := DecodeManifest(&buf)
	if !errors.Is(err, ErrNewerFormat) {
		t.Fatalf("DecodeManifest error = %v, want ErrNewerFormat", err)
	}
}

func TestDecodeManifestAllowsUnknownFields(t *testing.T) {
	in := `{"formatVersion":1,"futureField":{"x":1},"writer":"w"}`
	got, err := DecodeManifest(strings.NewReader(in))
	if err != nil {
		t.Fatalf("DecodeManifest: %v", err)
	}
	if got.Writer != "w" {
		t.Errorf("Writer = %q, want %q", got.Writer, "w")
	}
}

func TestDecodeManifestRejects(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantSub string
	}{
		{"missing version", `{"writer":"x"}`, "formatVersion missing"},
		{"zero version", `{"formatVersion":0}`, "formatVersion missing"},
		{"malformed json", `{`, "decode manifest"},
		{"relative escape folder", `{"formatVersion":1,"volumes":[{"logicalName":"w","folder":"../etc"}]}`, "invalid folder"},
		{"absolute folder", `{"formatVersion":1,"volumes":[{"logicalName":"w","folder":"/data"}]}`, "invalid folder"},
		{"too large", strings.Repeat(" ", maxManifestBytes+1), "manifest too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := DecodeManifest(strings.NewReader(tt.input))
			if err == nil || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("DecodeManifest error = %v, want containing %q", err, tt.wantSub)
			}
		})
	}
}

func TestDecodeManifestAcceptsMaxSize(t *testing.T) {
	// Valid JSON padded with whitespace to exactly maxManifestBytes.
	body := `{"formatVersion":1}`
	in := body + strings.Repeat(" ", maxManifestBytes-len(body))
	if _, err := DecodeManifest(strings.NewReader(in)); err != nil {
		t.Fatalf("DecodeManifest at max size: %v", err)
	}
}

func TestDecodeManifestReadError(t *testing.T) {
	if _, err := DecodeManifest(iotest.ErrReader(errors.New("boom"))); err == nil {
		t.Fatal("DecodeManifest with failing reader: want error")
	}
}

func TestVolumeFolder(t *testing.T) {
	tests := []struct {
		name    string
		volume  string
		capture int
		want    string
	}{
		{"format 2 uses volume folder", "world", 2, "volumes/world"},
		{"format 3 uses volume folder", "mods", 3, "volumes/mods"},
		{"format 1 uses data", "world", 1, "data"},
		{"unset uses data", "world", 0, "data"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := VolumeFolder(tt.volume, tt.capture); got != tt.want {
				t.Errorf("VolumeFolder(%q, %d) = %q, want %q", tt.volume, tt.capture, got, tt.want)
			}
		})
	}
}

func TestValidFolder(t *testing.T) {
	tests := []struct {
		folder string
		want   bool
	}{
		{"data", true},
		{"volumes/world", true},
		{"volumes/a", true},
		{"volumes/a-b", true},
		{"volumes/" + strings.Repeat("a", 63), true},
		{"volumes/" + strings.Repeat("a", 64), false},
		{"volumes/", false},
		{"volumes/..", false},
		{"volumes/../x", false},
		{"volumes/a/b", false},
		{"volumes/-a", false},
		{"volumes/a-", false},
		{"volumes/Abc", false},
		{"volumes/a_b", false},
		{"/data", false},
		{"/volumes/world", false},
		{"data/x", false},
		{"data ", false},
		{"", false},
		{"other", false},
	}
	for _, tt := range tests {
		if got := ValidFolder(tt.folder); got != tt.want {
			t.Errorf("ValidFolder(%q) = %v, want %v", tt.folder, got, tt.want)
		}
	}
}
