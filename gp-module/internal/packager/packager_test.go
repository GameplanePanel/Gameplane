package packager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/gp-module/internal/scaffold"
)

func TestPackageArchive_ValidModule(t *testing.T) {
	tempDir := t.TempDir()
	modDir := filepath.Join(tempDir, "test-pkg")

	opts := scaffold.Options{
		Name:        "test-pkg",
		DisplayName: "Test Package",
		Archetype:   "steamcmd",
		OutputDir:   modDir,
		Overwrite:   true,
	}

	if _, err := scaffold.Scaffold(opts); err != nil {
		t.Fatalf("scaffold failed: %v", err)
	}

	tarGzBytes, warnings, err := CreateArchiveFromDir(modDir, DefaultPackageLimits)
	if err != nil {
		t.Fatalf("CreateArchiveFromDir failed: %v", err)
	}

	if len(warnings) > 0 {
		t.Errorf("expected 0 warnings, got %v", warnings)
	}

	// Verify tar.gz contents
	gzr, err := gzip.NewReader(bytes.NewReader(tarGzBytes))
	if err != nil {
		t.Fatalf("gzip reader failed: %v", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	filesFound := make(map[string]bool)

	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read error: %v", err)
		}
		filesFound[header.Name] = true
	}

	expected := []string{"module.yaml", "template.yaml", "README.md", "icon.png"}
	for _, exp := range expected {
		if !filesFound[exp] {
			t.Errorf("expected entry %q in archive, found entries: %+v", exp, filesFound)
		}
	}
}

func TestPackageArchive_SizeLimits(t *testing.T) {
	files := map[string][]byte{
		"module.yaml":   []byte("apiVersion: gameplane.local/module/v1\nname: big\nversion: 1.0.0\n"),
		"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n"),
		"README.md":     []byte("# Big\n"),
		"icon.png":      make([]byte, 600*1024), // 600 KiB > 512 KiB
	}

	limits := PackageLimits{
		MaxIconSize:   512 * 1024,
		MaxBundleSize: 1024 * 1024,
	}

	_, warnings, err := CreateArchiveFromFiles(files, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundIconWarn := false
	for _, w := range warnings {
		if w.File == "icon.png" {
			foundIconWarn = true
		}
	}
	if !foundIconWarn {
		t.Errorf("expected warning for icon exceeding 512 KiB limit")
	}
}

func TestPackageArchive_MissingRequiredFiles(t *testing.T) {
	files := map[string][]byte{
		"README.md": []byte("# Missing other files\n"),
	}

	_, _, err := CreateArchiveFromFiles(files, DefaultPackageLimits)
	if err == nil {
		t.Errorf("expected error for missing required files")
	}
}

func TestPackageToFile(t *testing.T) {
	tempDir := t.TempDir()
	modDir := filepath.Join(tempDir, "file-pkg")
	outFile := filepath.Join(tempDir, "bundle.tar.gz")

	opts := scaffold.Options{
		Name:        "file-pkg",
		DisplayName: "File Package",
		Archetype:   "generic",
		OutputDir:   modDir,
		Overwrite:   true,
	}

	if _, err := scaffold.Scaffold(opts); err != nil {
		t.Fatalf("scaffold failed: %v", err)
	}

	warnings, err := ExportArchiveToFile(modDir, outFile, DefaultPackageLimits)
	if err != nil {
		t.Fatalf("ExportArchiveToFile failed: %v", err)
	}
	if len(warnings) > 0 {
		t.Errorf("expected 0 warnings, got %v", warnings)
	}

	fi, err := os.Stat(outFile)
	if err != nil {
		t.Fatalf("output file not created: %v", err)
	}
	if fi.Size() == 0 {
		t.Errorf("output file is empty")
	}
}

func TestPackageArchive_BundleSizeLimit(t *testing.T) {
	files := map[string][]byte{
		"module.yaml":   []byte("apiVersion: gameplane.local/module/v1\nname: big-bundle\nversion: 1.0.0\n"),
		"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n"),
		"README.md":     make([]byte, 1100*1024), // 1.1 MiB > 1.0 MiB
	}

	limits := PackageLimits{
		MaxIconSize:   512 * 1024,
		MaxBundleSize: 1024 * 1024,
	}

	_, warnings, err := CreateArchiveFromFiles(files, limits)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	foundBundleWarn := false
	for _, w := range warnings {
		if w.File == "bundle" {
			foundBundleWarn = true
		}
	}
	if !foundBundleWarn {
		t.Errorf("expected warning for bundle exceeding limit")
	}
}

func TestCreateArchiveFromDir_Errors(t *testing.T) {
	tempDir := t.TempDir()
	nonExistent := filepath.Join(tempDir, "does-not-exist")
	if _, _, err := CreateArchiveFromDir(nonExistent, DefaultPackageLimits); err == nil {
		t.Errorf("expected error for non-existent directory")
	}

	filePath := filepath.Join(tempDir, "regular-file")
	_ = os.WriteFile(filePath, []byte("hello"), 0o644)
	if _, _, err := CreateArchiveFromDir(filePath, DefaultPackageLimits); err == nil {
		t.Errorf("expected error when path is a file")
	}
}

func TestPushOCI_ValidationErrors(t *testing.T) {
	tempDir := t.TempDir()

	// Missing module.yaml
	opts := PackageOptions{
		ModuleDir: tempDir,
		Registry:  "localhost:5000",
	}
	if _, err := PushOCI(opts); err == nil {
		t.Errorf("expected error for missing required files")
	}

	// Missing name in module.yaml
	_ = os.WriteFile(filepath.Join(tempDir, "module.yaml"), []byte("apiVersion: gameplane.local/module/v1\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tempDir, "template.yaml"), []byte("kind: GameTemplate\n"), 0o644)
	_ = os.WriteFile(filepath.Join(tempDir, "README.md"), []byte("# Title\n"), 0o644)

	if _, err := PushOCI(opts); err == nil {
		t.Errorf("expected error for missing name in module.yaml")
	}

	// Missing version in module.yaml and no tag
	_ = os.WriteFile(filepath.Join(tempDir, "module.yaml"), []byte("apiVersion: gameplane.local/module/v1\nname: test\n"), 0o644)
	if _, err := PushOCI(opts); err == nil {
		t.Errorf("expected error for missing version")
	}
}

func TestReadModuleFiles_SymlinkRejected(t *testing.T) {
	tempDir := t.TempDir()
	extFile := filepath.Join(tempDir, "outside.txt")
	_ = os.WriteFile(extFile, []byte("sensitive"), 0o600)

	modDir := filepath.Join(tempDir, "mod")
	_ = os.Mkdir(modDir, 0o750)
	symlink := filepath.Join(modDir, "link.txt")
	if err := os.Symlink(extFile, symlink); err != nil {
		t.Skipf("symlink not supported on this platform: %v", err)
	}

	_, err := ReadModuleFiles(modDir)
	if err == nil {
		t.Errorf("expected error when directory contains symbolic link")
	}
}

func TestValidateEntryName_TraversalPath(t *testing.T) {
	files := map[string][]byte{
		"module.yaml":   []byte("apiVersion: gameplane.local/module/v1\nname: test\nversion: 1.0.0\n"),
		"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n"),
		"README.md":     []byte("# Test\n"),
		"../etc/passwd": []byte("malicious"),
	}

	_, _, err := CreateArchiveFromFiles(files, DefaultPackageLimits)
	if err == nil {
		t.Errorf("expected error for traversal path ../etc/passwd")
	}
	if !strings.Contains(err.Error(), "..") {
		t.Errorf("expected error message to mention '..' traversal, got: %v", err)
	}
}

func TestValidateEntryName_AbsolutePath(t *testing.T) {
	files := map[string][]byte{
		"module.yaml":   []byte("apiVersion: gameplane.local/module/v1\nname: test\nversion: 1.0.0\n"),
		"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n"),
		"README.md":     []byte("# Test\n"),
		"/etc/passwd":   []byte("malicious"),
	}

	_, _, err := CreateArchiveFromFiles(files, DefaultPackageLimits)
	if err == nil {
		t.Errorf("expected error for absolute path /etc/passwd")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Errorf("expected error message to mention absolute path, got: %v", err)
	}
}

func TestValidateEntryName_ValidPath(t *testing.T) {
	files := map[string][]byte{
		"module.yaml":   []byte("apiVersion: gameplane.local/module/v1\nname: test\nversion: 1.0.0\n"),
		"template.yaml": []byte("apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n"),
		"README.md":     []byte("# Test\n"),
		"icon.png":      []byte("PNG"),
	}

	_, _, err := CreateArchiveFromFiles(files, DefaultPackageLimits)
	if err != nil {
		t.Errorf("expected valid entry names to succeed, got error: %v", err)
	}
}
