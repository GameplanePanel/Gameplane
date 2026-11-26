package ws

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
)

// A canceled context makes the gateway request fail inside the HTTP client,
// before any connection is attempted, so these tests never dial out.
func canceledCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	return ctx
}

func TestGapBackupExportDownloadRejectsMalformedTargetBeforeDialing(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test:8443")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.BackupExportTarget{
		Target:    gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"},
		Export:    "Bad/Name",
		ExportUID: "export-uid",
	}
	resp, err := client.BackupExportDownload(t.Context(), target, http.Header{})
	if resp != nil {
		_ = resp.Body.Close()
		t.Fatal("malformed backup export target produced a response")
	}
	if err == nil || !strings.Contains(err.Error(), "build gateway backup export path") {
		t.Fatalf("err=%v", err)
	}
}

func TestGapBackupExportDownloadRefusesReplacedServerIdentity(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test:8443")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.BackupExportTarget{
		Target:    gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "replaced-uid"},
		Export:    "exp-one",
		ExportUID: "export-uid",
	}
	resp, err := client.BackupExportDownload(t.Context(), target, http.Header{})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "backup export server identity changed") {
		t.Fatalf("err=%v", err)
	}
}

func TestGapBackupExportDownloadFailsWhenRequestCannotBeSent(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test:8443")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.BackupExportTarget{
		Target:    gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"},
		Export:    "exp-one",
		ExportUID: "export-uid",
	}
	resp, err := client.BackupExportDownload(canceledCtx(t), target, http.Header{"Cookie": {"secret"}})
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "send gateway backup export request") || !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestGapRestoreUploadStreamReportsUnresolvableServer(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test:8443")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.RestoreUploadTarget{
		Target:     gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "missing", UID: "remote-uid"},
		Restore:    "rs-one",
		RestoreUID: "restore-uid",
	}
	resp, err := client.RestoreUploadStream(t.Context(), "remote", target, strings.NewReader("archive"), 7)
	if resp != nil {
		_ = resp.Body.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "resolve gateway restore upload target") {
		t.Fatalf("err=%v", err)
	}
}

func TestGapRestoreUploadStreamFailsWhenRequestCannotBeSent(t *testing.T) {
	resolver, _, _ := gatewayFixture(t, "https://gateway.test:8443")
	client := &CaptureGatewayClient{resolver: resolver}
	target := gatewayprotocol.RestoreUploadTarget{
		Target:     gatewayprotocol.Target{Cluster: "remote", Namespace: "gameplane-games", Name: "alpha", UID: "remote-uid"},
		Restore:    "rs-one",
		RestoreUID: "restore-uid",
	}
	resp, err := client.RestoreUploadStream(canceledCtx(t), "remote", target, strings.NewReader("archive"), 7)
	if resp != nil {
		_ = resp.Body.Close()
	}
	// The failure must surface as an error, never as a successful upload.
	if err == nil || !strings.Contains(err.Error(), "send gateway restore upload request") || !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}
