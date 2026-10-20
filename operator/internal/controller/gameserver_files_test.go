package controller

import (
	"strings"
	"testing"

	gameplanev1alpha1 "github.com/GameplanePanel/gameplane/operator/api/v1alpha1"
)

func TestBuildConfigInitContainer_Defaults(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	c := buildConfigInitContainer("", tmpl)

	if c.Name != "config-init" {
		t.Errorf("name = %q, want config-init", c.Name)
	}
	if c.Image != DefaultConfigInitImage {
		t.Errorf("image = %q, want default pin %q", c.Image, DefaultConfigInitImage)
	}
	if len(c.Args) != 1 || !strings.Contains(c.Args[0], configFilesStagingPath+"/*") {
		t.Errorf("args should copy from the staging glob, got %v", c.Args)
	}
	if !strings.Contains(c.Args[0], "'/data/'") {
		t.Errorf("args should copy into the default mount path, got %v", c.Args)
	}
	if len(c.VolumeMounts) != 2 {
		t.Fatalf("got %d volume mounts, want 2: %v", len(c.VolumeMounts), c.VolumeMounts)
	}
	staging, data := c.VolumeMounts[0], c.VolumeMounts[1]
	if staging.Name != "config-files" || staging.MountPath != configFilesStagingPath || !staging.ReadOnly {
		t.Errorf("staging mount = %+v, want read-only config-files at %s", staging, configFilesStagingPath)
	}
	if data.Name != "data" || data.MountPath != "/data" || data.ReadOnly {
		t.Errorf("data mount = %+v, want writable data at /data", data)
	}
}

func TestBuildConfigInitContainer_HonorsMountPath(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Storage: gameplanev1alpha1.GameStorageSpec{MountPath: "/world"},
		},
	}
	c := buildConfigInitContainer("", tmpl)

	if !strings.Contains(c.Args[0], "'/world/'") {
		t.Errorf("args should copy into /world, got %v", c.Args)
	}
	if c.VolumeMounts[1].MountPath != "/world" {
		t.Errorf("data mount path = %q, want /world", c.VolumeMounts[1].MountPath)
	}
}

func TestBuildConfigInitContainer_HonorsImageOverride(t *testing.T) {
	tmpl := &gameplanev1alpha1.GameTemplate{}
	const override = "registry.internal.example/busybox:1.37.0"
	c := buildConfigInitContainer(override, tmpl)

	if c.Image != override {
		t.Errorf("image = %q, want override %q", c.Image, override)
	}
}

func TestBuildConfigInitContainer_GroupWritableWithFSGroup(t *testing.T) {
	fsGroup := int64(1000)
	tmpl := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Security: &gameplanev1alpha1.GameSecuritySpec{
				FSGroup: &fsGroup,
			},
		},
	}
	c := buildConfigInitContainer("", tmpl)

	if len(c.Args) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(c.Args))
	}
	arg := c.Args[0]

	// Should still contain the original cp command
	if !strings.Contains(arg, "cp -RL "+configFilesStagingPath+"/*") {
		t.Errorf("args should contain cp from staging, got %q", arg)
	}
	if !strings.Contains(arg, "'/data/'") {
		t.Errorf("args should contain copy into default mount path, got %q", arg)
	}

	// Should contain the chmod step
	if !strings.Contains(arg, "chmod g+w") {
		t.Errorf("args should contain chmod g+w when fsGroup is set, got %q", arg)
	}

	// Should not contain recursive chmod
	if strings.Contains(arg, "chmod -R") {
		t.Errorf("args should not contain chmod -R, got %q", arg)
	}

	// The whole command must be valid shell: the mount path is single-quoted
	// and closed before the loop variable, so $p still expands.
	want := "cp -RL " + configFilesStagingPath + "/* '/data/' && cd " + configFilesStagingPath +
		" && find ./* -follow \\( -type f -o -type d \\) | while IFS= read -r p; do chmod g+w '/data/'\"$p\"; done"
	if arg != want {
		t.Errorf("config-init command =\n  %q\nwant\n  %q", arg, want)
	}

	// Verify the command contains "find ./*"
	if !strings.Contains(arg, "find ./*") {
		t.Errorf("args should contain 'find ./*' to handle entries starting with '-', got %q", arg)
	}
}

func TestBuildConfigInitContainer_NoChmodWithoutFSGroup(t *testing.T) {
	// Test 1: no Security block at all
	tmpl1 := &gameplanev1alpha1.GameTemplate{}
	c1 := buildConfigInitContainer("", tmpl1)
	if strings.Contains(c1.Args[0], "chmod") {
		t.Errorf("args should not contain chmod when no Security block, got %q", c1.Args[0])
	}

	// Test 2: Security block exists but FSGroup is nil
	tmpl2 := &gameplanev1alpha1.GameTemplate{
		Spec: gameplanev1alpha1.GameTemplateSpec{
			Security: &gameplanev1alpha1.GameSecuritySpec{
				// RunAsUser/RunAsGroup may be set, but not FSGroup
			},
		},
	}
	c2 := buildConfigInitContainer("", tmpl2)
	if strings.Contains(c2.Args[0], "chmod") {
		t.Errorf("args should not contain chmod when FSGroup is nil, got %q", c2.Args[0])
	}
}
