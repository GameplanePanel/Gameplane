package gatewayprotocol

import (
	"strings"
	"testing"
)

func TestRestoreUploadPathBindsIdentities(t *testing.T) {
	target := RestoreUploadTarget{Target: Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "server-uid"}, Restore: "alpha-restore-abc12", RestoreUID: "restore-uid"}
	path, err := RestoreUploadPath(target)
	if err != nil {
		t.Fatal(err)
	}
	want := "/v1/clusters/remote/namespaces/games/servers/same-name/uids/server-uid/restores/alpha-restore-abc12/uids/restore-uid/upload"
	if path != want {
		t.Fatalf("path=%s want=%s", path, want)
	}
	parsed, err := ParseRestoreUploadPath(path)
	if err != nil || parsed != target {
		t.Fatalf("round trip: %+v %v", parsed, err)
	}
}

func TestRestoreUploadPathRejectsInvalidTargets(t *testing.T) {
	base := RestoreUploadTarget{Target: Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "server-uid"}, Restore: "rs", RestoreUID: "restore-uid"}
	for name, mutate := range map[string]func(*RestoreUploadTarget){
		"restore with slash":   func(b *RestoreUploadTarget) { b.Restore = "a/b" },
		"restore uppercase":    func(b *RestoreUploadTarget) { b.Restore = "Rs" },
		"restore too long":     func(b *RestoreUploadTarget) { b.Restore = strings.Repeat("a", 254) },
		"restore uid empty":    func(b *RestoreUploadTarget) { b.RestoreUID = "" },
		"restore uid dotdot":   func(b *RestoreUploadTarget) { b.RestoreUID = ".." },
		"restore uid too long": func(b *RestoreUploadTarget) { b.RestoreUID = strings.Repeat("u", 129) },
		"bad server name":      func(b *RestoreUploadTarget) { b.Name = "Bad_Name" },
		"empty server uid":     func(b *RestoreUploadTarget) { b.UID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			target := base
			mutate(&target)
			if _, err := RestoreUploadPath(target); err == nil {
				t.Fatal("built a path for an invalid target")
			}
		})
	}
}

func TestParseRestoreUploadPathRejectsMalformedRoutes(t *testing.T) {
	target := RestoreUploadTarget{Target: Target{Cluster: "remote", Namespace: "games", Name: "alpha", UID: "server-uid"}, Restore: "rs", RestoreUID: "restore-uid"}
	path, err := RestoreUploadPath(target)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		path + "/extra",
		path + "?url=http://elsewhere",
		strings.TrimSuffix(path, "/upload") + "/archive",
		strings.Replace(path, "restores", "backup-exports", 1),
		strings.Replace(path, "/uids/restore-uid", "", 1),
		strings.Replace(path, "/rs/", "/../", 1),
		strings.Replace(path, "/rs/", "/%2fetc/", 1),
		strings.Replace(path, "/v1/", "/v2/", 1),
		strings.Replace(path, "servers/alpha", "servers/Alpha", 1),
		strings.Replace(path, "/upload", "/status", 1),
		"",
		"/",
	} {
		if _, err := ParseRestoreUploadPath(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}
