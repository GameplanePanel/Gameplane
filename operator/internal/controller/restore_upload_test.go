package controller

import (
	"errors"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestChooseStagingStorageClass(t *testing.T) {
	defaultClass := func(name string) storagev1.StorageClass {
		return storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{
			Name:        name,
			Annotations: map[string]string{uploadStorageClassDefaultAnnotation: "true"},
		}}
	}
	plain := storagev1.StorageClass{ObjectMeta: metav1.ObjectMeta{Name: "slow"}}

	if got, err := chooseStagingStorageClass("fast", []storagev1.StorageClass{defaultClass("std")}); err != nil || got != "fast" {
		t.Fatalf("configured class: got %q, %v; want fast", got, err)
	}
	if got, err := chooseStagingStorageClass("", []storagev1.StorageClass{plain, defaultClass("std")}); err != nil || got != "std" {
		t.Fatalf("default class: got %q, %v; want std", got, err)
	}
	if got, err := chooseStagingStorageClass("", []storagev1.StorageClass{defaultClass("zeta"), defaultClass("alpha")}); err != nil || got != "alpha" {
		t.Fatalf("multiple defaults: got %q, %v; want alpha", got, err)
	}
	if _, err := chooseStagingStorageClass("", []storagev1.StorageClass{plain}); !errors.Is(err, errNoStagingStorageClass) {
		t.Fatalf("no class: err = %v, want errNoStagingStorageClass", err)
	}
	if !strings.HasPrefix(errNoStagingStorageClass.Error(), "NoStagingStorageClass: ") {
		t.Fatalf("missing-class message lacks reason prefix: %q", errNoStagingStorageClass.Error())
	}
}

func TestUploadStagingSize(t *testing.T) {
	const gi = int64(1) << 30
	cases := []struct {
		name           string
		declared, targ int64
		wantBytes      int64
	}{
		{"tiny archive floors at 1Gi", 1, 0, gi},
		{"exact GiB stays", gi/2 + gi/2, 0, gi},
		{"one byte over rounds up", gi, 1, 2 * gi},
		{"archive plus target", 3 * gi, 2 * gi, 5 * gi},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := uploadStagingSize(tc.declared, tc.targ)
			if got.Value() != tc.wantBytes {
				t.Fatalf("size = %s (%d bytes), want %d bytes", got.String(), got.Value(), tc.wantBytes)
			}
			if got.Format != resource.BinarySI {
				t.Fatalf("format = %v, want BinarySI", got.Format)
			}
		})
	}
}

func TestUploadConfirmationExpired(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	if uploadConfirmationExpired(now.Add(-29*time.Minute), now) {
		t.Fatal("29 minutes after receive must still be inside the window")
	}
	if !uploadConfirmationExpired(now.Add(-30*time.Minute), now) {
		t.Fatal("exactly 30 minutes after receive must have expired")
	}
}

func TestParseUploadSummary(t *testing.T) {
	ok := `{"kind":"gameplane","manifest":{"templateName":"x"},"unpackedBytes":7,"volumes":["primary"],"zipPermissionsReset":true,"error":""}`
	s, err := parseUploadSummary(ok)
	if err != nil || s.Kind != "gameplane" || s.UnpackedBytes != 7 || len(s.Volumes) != 1 || !s.ZipPermissionsReset {
		t.Fatalf("valid summary: %+v, %v", s, err)
	}
	if s, err := parseUploadSummary(`{"kind":"","error":"archive is corrupt"}`); err != nil || s.Error != "archive is corrupt" {
		t.Fatalf("error-only summary: %+v, %v", s, err)
	}
	for _, bad := range []string{"", "   ", "not json", `{"kind":"tarball"}`} {
		if _, err := parseUploadSummary(bad); err == nil {
			t.Fatalf("parseUploadSummary(%q) accepted a bad message", bad)
		}
	}
}

func TestCheckUploadArchive(t *testing.T) {
	target := uploadArchiveTarget{TemplateName: "minecraft", GameVersion: "1.21.0", VolumeNames: []string{"primary"}}
	gameplane := func(template, version string, volumes ...string) uploadSummary {
		return uploadSummary{Kind: "gameplane", Volumes: volumes, Manifest: &uploadManifest{
			GameVersion: version,
			Template:    uploadManifestTmpl{Name: template},
		}}
	}

	if warn, reason := checkUploadArchive(gameplane("minecraft", "1.21.0", "primary"), "", target); reason != "" || len(warn) != 0 {
		t.Fatalf("matching archive refused: %q, warnings %v", reason, warn)
	}
	if _, reason := checkUploadArchive(gameplane("terraria", "1.21.0", "primary"), "", target); reason != "archive is for terraria, this server runs minecraft" {
		t.Fatalf("template mismatch reason = %q", reason)
	}
	if _, reason := checkUploadArchive(gameplane("minecraft", "1.21.0", "extra-mods"), "", target); reason != `archive has no volume "primary", which this server uses` {
		t.Fatalf("missing volume reason = %q", reason)
	}
	if _, reason := checkUploadArchive(uploadSummary{Kind: "gameplane"}, "", target); reason != "archive manifest is missing" {
		t.Fatalf("missing manifest reason = %q", reason)
	}
	warn, reason := checkUploadArchive(gameplane("minecraft", "1.20.4", "primary"), "", target)
	if reason != "" || len(warn) != 1 || !strings.Contains(warn[0], "older") {
		t.Fatalf("older game version: warnings %v, reason %q", warn, reason)
	}
	zip := gameplane("minecraft", "1.21.0", "primary")
	zip.ZipPermissionsReset = true
	if warn, reason := checkUploadArchive(zip, "", target); reason != "" || len(warn) != 1 {
		t.Fatalf("zip permission reset: warnings %v, reason %q", warn, reason)
	}

	plain := uploadSummary{Kind: "plain", Volumes: []string{"data"}}
	if _, reason := checkUploadArchive(plain, "terraria", target); reason != "plain archive is for terraria, this server runs minecraft" {
		t.Fatalf("plain mismatch reason = %q", reason)
	}
	if warn, reason := checkUploadArchive(plain, "minecraft", target); reason != "" || len(warn) != 0 {
		t.Fatalf("plain match: warnings %v, reason %q", warn, reason)
	}
	if _, reason := checkUploadArchive(uploadSummary{Kind: "tarball"}, "", target); reason == "" {
		t.Fatal("unknown kind was accepted")
	}
}

func TestArchiveGameVersionOlder(t *testing.T) {
	cases := []struct {
		archive, target string
		want            bool
	}{
		{"1.20.4", "1.21.0", true},
		{"v1.21.0", "1.21.0", false},
		{"1.22.0", "1.21.0", false},
		{"", "1.21.0", false},
		{"nightly", "1.21.0", false},
	}
	for _, tc := range cases {
		if got := archiveGameVersionOlder(tc.archive, tc.target); got != tc.want {
			t.Errorf("archiveGameVersionOlder(%q, %q) = %v, want %v", tc.archive, tc.target, got, tc.want)
		}
	}
}

func TestParseCopyFailure(t *testing.T) {
	if got := parseCopyFailure(`{"error":"disk full","copiedVolumes":["primary"]}`); got != "disk full" {
		t.Fatalf("structured error = %q", got)
	}
	if got := parseCopyFailure("  raw text  \n"); got != "raw text" {
		t.Fatalf("raw message = %q", got)
	}
	if got := parseCopyFailure(""); got != "" {
		t.Fatalf("empty message = %q", got)
	}
}

func TestUploadCopyVolumesPlainUsesDataClaim(t *testing.T) {
	vols := []uploadVolume{
		{LogicalName: "extra-mods", ClaimName: "smp-extra-mods"},
		{LogicalName: "primary", ClaimName: "smp-data", CapacityBytes: 10},
	}
	if got := uploadCopyVolumes("gameplane", vols); len(got) != 2 {
		t.Fatalf("gameplane copy volumes = %d, want all 2", len(got))
	}
	got := uploadCopyVolumes("plain", vols)
	if len(got) != 1 || got[0].LogicalName != "data" || got[0].ClaimName != "smp-data" {
		t.Fatalf("plain copy volumes = %+v, want the data claim as data", got)
	}
}

func TestClaimCapacityBytesPrefersProvisionedSize(t *testing.T) {
	pvc := &corev1.PersistentVolumeClaim{Spec: corev1.PersistentVolumeClaimSpec{
		Resources: corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("2Gi")}},
	}}
	if got := claimCapacityBytes(pvc); got != 2<<30 {
		t.Fatalf("requested size = %d", got)
	}
	pvc.Status.Capacity = corev1.ResourceList{corev1.ResourceStorage: resource.MustParse("3Gi")}
	if got := claimCapacityBytes(pvc); got != 3<<30 {
		t.Fatalf("provisioned size = %d", got)
	}
}
