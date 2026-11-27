package gatewayprotocol

import (
	"strings"
	"testing"
)

func TestBackupExportPathBindsIdentities(t *testing.T) {
	target := BackupExportTarget{Target: Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "server-uid"}, Export: "bk-export-abc12", ExportUID: "export-uid"}
	path, err := BackupExportPath(target)
	if err != nil {
		t.Fatal(err)
	}
	want := "/v1/clusters/remote/namespaces/games/servers/same-name/uids/server-uid/backup-exports/bk-export-abc12/uids/export-uid/archive"
	if path != want {
		t.Fatalf("path=%s want=%s", path, want)
	}
	parsed, err := ParseBackupExportPath(path)
	if err != nil || parsed != target {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
}

func TestBackupExportPathRejectsInvalidTargets(t *testing.T) {
	base := BackupExportTarget{Target: Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "server-uid"}, Export: "exp", ExportUID: "export-uid"}
	for name, mutate := range map[string]func(*BackupExportTarget){
		"export with slash":   func(b *BackupExportTarget) { b.Export = "a/b" },
		"export uppercase":    func(b *BackupExportTarget) { b.Export = "Exp" },
		"export too long":     func(b *BackupExportTarget) { b.Export = strings.Repeat("a", 254) },
		"export uid empty":    func(b *BackupExportTarget) { b.ExportUID = "" },
		"export uid dotdot":   func(b *BackupExportTarget) { b.ExportUID = ".." },
		"export uid too long": func(b *BackupExportTarget) { b.ExportUID = strings.Repeat("u", 129) },
		"bad server name":     func(b *BackupExportTarget) { b.Name = "Bad_Name" },
		"empty server uid":    func(b *BackupExportTarget) { b.UID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			target := base
			mutate(&target)
			if _, err := BackupExportPath(target); err == nil {
				t.Fatal("built a path for an invalid target")
			}
		})
	}
}

func TestParseBackupExportPathRejectsMalformedRoutes(t *testing.T) {
	target := BackupExportTarget{Target: Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "server-uid"}, Export: "exp", ExportUID: "export-uid"}
	path, err := BackupExportPath(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		path + "/extra",
		path + "?url=http://elsewhere",
		strings.TrimSuffix(path, "/archive") + "/file",
		strings.Replace(path, "backup-exports", "captures", 1),
		strings.Replace(path, "/uids/export-uid", "", 1),
		strings.Replace(path, "/exp/", "/../", 1),
		strings.Replace(path, "/exp/", "/%2fetc/", 1),
		strings.Replace(path, "/export-uid/", "/export-uid%2F/", 1),
		strings.Replace(path, "/v1/", "/v2/", 1),
		strings.Replace(path, "servers/alpha", "servers/Alpha", 1),
		strings.Replace(path, "/archive", "/status", 1),
		"",
		"/",
	} {
		if _, err := ParseBackupExportPath(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
