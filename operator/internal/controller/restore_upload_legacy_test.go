package controller

import "testing"

func TestLegacyArchiveLayout(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		archive []string
		target  []string
		want    bool
	}{
		{name: "capture format 1 archive", kind: "gameplane", archive: []string{"data"}, target: []string{"primary", "extra-mods"}, want: true},
		{name: "per-volume archive", kind: "gameplane", archive: []string{"primary"}, target: []string{"primary"}, want: false},
		{name: "data plus another volume", kind: "gameplane", archive: []string{"data", "primary"}, target: []string{"primary"}, want: false},
		{name: "server has its own data volume", kind: "gameplane", archive: []string{"data"}, target: []string{"primary", "data"}, want: false},
		{name: "plain archive", kind: "plain", archive: []string{"data"}, target: []string{"primary"}, want: false},
		{name: "empty archive", kind: "gameplane", archive: nil, target: []string{"primary"}, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := legacyArchiveLayout(tc.kind, tc.archive, tc.target); got != tc.want {
				t.Errorf("legacyArchiveLayout(%q, %v, %v) = %v, want %v", tc.kind, tc.archive, tc.target, got, tc.want)
			}
		})
	}
}

func TestCheckUploadArchiveAcceptsCaptureFormatOne(t *testing.T) {
	target := uploadArchiveTarget{TemplateName: "minecraft", GameVersion: "1.21.0", VolumeNames: []string{"primary"}}
	s := uploadSummary{Kind: "gameplane", Volumes: []string{"data"}, Manifest: &uploadManifest{
		GameVersion: "1.21.0",
		Template:    uploadManifestTmpl{Name: "minecraft"},
	}}
	if warn, reason := checkUploadArchive(s, "", target); reason != "" || len(warn) != 0 {
		t.Fatalf("capture format 1 archive refused: %q, warnings %v", reason, warn)
	}
	s.Manifest.Template.Name = "terraria"
	if _, reason := checkUploadArchive(s, "", target); reason != "archive is for terraria, this server runs minecraft" {
		t.Fatalf("template mismatch reason = %q", reason)
	}
}
