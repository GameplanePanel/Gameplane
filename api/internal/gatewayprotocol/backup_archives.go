package gatewayprotocol

import (
	"errors"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
)

// BackupExportTarget binds a backup archive download to the immutable
// GameServer identity and to the BackupExport's name and UID (spec 041).
type BackupExportTarget struct {
	Target
	Export    string
	ExportUID string
}

// BackupExportPath is the sole private backup-archive route; it never accepts a URL.
func BackupExportPath(target BackupExportTarget) (string, error) {
	base, err := Path(target.Target, "/status")
	if err != nil || len(target.Export) > 253 || len(validation.IsDNS1123Subdomain(target.Export)) != 0 || !uidPattern.MatchString(target.ExportUID) {
		return "", errors.New("invalid backup export target")
	}
	return strings.TrimSuffix(base, "/status") + "/backup-exports/" + target.Export + "/uids/" + target.ExportUID + "/archive", nil
}

// ParseBackupExportPath accepts only the canonical immutable backup-archive route.
func ParseBackupExportPath(path string) (BackupExportTarget, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 15 || parts[0] != "" || parts[1] != "v1" || parts[2] != "clusters" || parts[4] != "namespaces" || parts[6] != "servers" || parts[8] != "uids" || parts[10] != "backup-exports" || parts[12] != "uids" || parts[14] != "archive" {
		return BackupExportTarget{}, errors.New("invalid backup export route")
	}
	target := BackupExportTarget{Target: Target{Cluster: parts[3], Namespace: parts[5], Name: parts[7], UID: parts[9]}, Export: parts[11], ExportUID: parts[13]}
	canonical, err := BackupExportPath(target)
	if err != nil || canonical != path {
		return BackupExportTarget{}, errors.New("invalid backup export route")
	}
	return target, nil
}

// RestoreUploadTarget binds an uploaded archive to the immutable GameServer
// identity and to the upload Restore's name and UID (spec 041).
type RestoreUploadTarget struct {
	Target
	Restore    string
	RestoreUID string
}

// RestoreUploadPath is the sole private upload route for a restore archive; it
// never accepts a URL.
func RestoreUploadPath(target RestoreUploadTarget) (string, error) {
	base, err := Path(target.Target, "/status")
	if err != nil || len(target.Restore) > 253 || len(validation.IsDNS1123Subdomain(target.Restore)) != 0 || !uidPattern.MatchString(target.RestoreUID) {
		return "", errors.New("invalid restore upload target")
	}
	return strings.TrimSuffix(base, "/status") + "/restores/" + target.Restore + "/uids/" + target.RestoreUID + "/upload", nil
}

// ParseRestoreUploadPath accepts only the canonical immutable restore-upload route.
func ParseRestoreUploadPath(path string) (RestoreUploadTarget, error) {
	parts := strings.Split(path, "/")
	if len(parts) != 15 || parts[0] != "" || parts[1] != "v1" || parts[2] != "clusters" || parts[4] != "namespaces" || parts[6] != "servers" || parts[8] != "uids" || parts[10] != "restores" || parts[12] != "uids" || parts[14] != "upload" {
		return RestoreUploadTarget{}, errors.New("invalid restore upload route")
	}
	target := RestoreUploadTarget{Target: Target{Cluster: parts[3], Namespace: parts[5], Name: parts[7], UID: parts[9]}, Restore: parts[11], RestoreUID: parts[13]}
	canonical, err := RestoreUploadPath(target)
	if err != nil || canonical != path {
		return RestoreUploadTarget{}, errors.New("invalid restore upload route")
	}
	return target, nil
}
