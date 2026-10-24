package controller

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	if !strings.Contains(arg, "stat -c %g") || !strings.Contains(arg, "stat -c %a") {
		t.Errorf("already-correct ownership and modes must be checked before mutation: %q", arg)
	}

	// Verify the command contains "find ./*"
	if !strings.Contains(arg, "find ./*") {
		t.Errorf("args should contain 'find ./*' to handle entries starting with '-', got %q", arg)
	}
}

// The second start models files that are writable through fsGroup but cannot
// be chmodded by the init UID, as occurs on a root-to-Restricted transition.
func TestBuildConfigInitContainer_ReusesSharedFilesWithoutOwnership(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("config-init runs in Linux containers")
	}
	dir := t.TempDir()
	staging, data := filepath.Join(dir, "staging"), filepath.Join(dir, "data")
	for _, path := range []string{filepath.Join(staging, "cfg"), data} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	source := filepath.Join(staging, "cfg", "profile.txt")
	if err := os.WriteFile(source, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(data, "world.dat")
	if err := os.WriteFile(unrelated, []byte("world"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmpl := &gameplanev1alpha1.GameTemplate{}
	tmpl.Spec.Security = &gameplanev1alpha1.GameSecuritySpec{FSGroup: ptrTo(int64(os.Getgid()))}
	tmpl.Spec.Storage.MountPath = data
	c := buildConfigInitContainer("", tmpl)
	if c.SecurityContext == nil || c.SecurityContext.RunAsGroup == nil || *c.SecurityContext.RunAsGroup != *tmpl.Spec.Security.FSGroup {
		t.Fatal("config bootstrap must create files in the shared volume group")
	}
	script := strings.ReplaceAll(c.Args[0], configFilesStagingPath, "'"+staging+"'")
	if out, err := exec.CommandContext(t.Context(), "/bin/sh", "-c", script).CombinedOutput(); err != nil {
		t.Fatalf("bootstrap: %v %s", err, out)
	}
	// A non-owner cannot change modes or groups, even when their requested
	// values are unchanged. Reject both operations on the second invocation.
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"chmod", "chgrp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho unexpected ownership mutation >&2\nexit 1\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("updated"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "/bin/sh", "-c", script)
	command.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"))
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("repeat copy required ownership of writable files: %v %s", err, out)
	}
	if got, err := os.ReadFile(filepath.Join(data, "cfg", "profile.txt")); err != nil || string(got) != "updated" {
		t.Fatalf("config was not replaced: %q %v", got, err)
	}
	if info, err := os.Stat(unrelated); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("unrelated data permissions changed: %v %v", info, err)
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
