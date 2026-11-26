package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GameplanePanel/gameplane/api/internal/gatewayprotocol"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// backupExportFixture stores a Backup owned by backupServer and a
// BackupExport of it in the given phase, then returns the route that binds
// both to the GameServer "same-name" with UID "original-uid".
func backupExportFixture(t *testing.T, h *handler, phase, serviceName, backupServer string) gatewayprotocol.BackupExportTarget {
	t.Helper()
	bk := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Backup",
		"metadata":   map[string]any{"name": "bk-one", "namespace": "games", "uid": "backup-uid"},
		"spec":       map[string]any{"serverRef": map[string]any{"name": backupServer}, "strategy": "restic"},
	}}
	if _, err := h.client.Dynamic.Resource(kube.GVRBackup).Namespace("games").Create(t.Context(), bk, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	exp := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "BackupExport",
		"metadata":   map[string]any{"name": "exp-one", "namespace": "games", "uid": "export-uid"},
		"spec":       map[string]any{"backupRef": map[string]any{"name": "bk-one"}, "backupUID": "backup-uid", "format": "zip"},
		"status":     map[string]any{"phase": phase, "serviceName": serviceName},
	}}
	if _, err := h.client.Dynamic.Resource(kube.GVRBackupExport).Namespace("games").Create(t.Context(), exp, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return gatewayprotocol.BackupExportTarget{
		Target:    gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"},
		Export:    "exp-one",
		ExportUID: "export-uid",
	}
}

func backupExportPathFor(t *testing.T, target gatewayprotocol.BackupExportTarget) string {
	t.Helper()
	path, err := gatewayprotocol.BackupExportPath(target)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func TestBackupExportWrongIdentitiesAreNotFoundAndNeverReachWorker(t *testing.T) {
	for _, tc := range []struct {
		name          string
		backupServer  string
		mutate        func(*gatewayprotocol.BackupExportTarget)
		wantNotFound  bool
		wantWorkerHit bool
	}{
		{name: "valid", backupServer: "same-name", mutate: func(*gatewayprotocol.BackupExportTarget) {}, wantWorkerHit: true},
		{name: "wrong server", backupServer: "same-name", mutate: func(b *gatewayprotocol.BackupExportTarget) { b.UID = "replacement-server" }, wantNotFound: true},
		{name: "wrong export uid", backupServer: "same-name", mutate: func(b *gatewayprotocol.BackupExportTarget) { b.ExportUID = "replacement" }, wantNotFound: true},
		{name: "missing export", backupServer: "same-name", mutate: func(b *gatewayprotocol.BackupExportTarget) { b.Export = "missing" }, wantNotFound: true},
		{name: "backup of another server", backupServer: "other-server", mutate: func(*gatewayprotocol.BackupExportTarget) {}, wantNotFound: true},
		{name: "foreign namespace", backupServer: "same-name", mutate: func(b *gatewayprotocol.BackupExportTarget) { b.Namespace = "other" }, wantNotFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cert := newFixture(t)
			target := backupExportFixture(t, h, "Ready", "exp-svc", tc.backupServer)
			tc.mutate(&target)
			var hits atomic.Int64
			h.transport = func(TLSFiles) (*http.Transport, error) {
				hits.Add(1)
				return nil, io.ErrUnexpectedEOF
			}
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, request(t, cert, http.MethodGet, backupExportPathFor(t, target)))
			if tc.wantNotFound && rr.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			if tc.wantWorkerHit == (hits.Load() == 0) {
				t.Fatalf("worker contacted=%v want=%v", hits.Load() > 0, tc.wantWorkerHit)
			}
		})
	}
}

func TestBackupExportPendingIsConflictUntilReady(t *testing.T) {
	for _, tc := range []struct {
		phase  string
		status int
	}{
		{"", http.StatusConflict},
		{"Pending", http.StatusConflict},
		{"Failed", http.StatusNotFound},
		{"Expired", http.StatusNotFound},
		{"Completed", http.StatusNotFound},
	} {
		t.Run("phase "+tc.phase, func(t *testing.T) {
			h, cert := newFixture(t)
			target := backupExportFixture(t, h, tc.phase, "exp-svc", "same-name")
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, request(t, cert, http.MethodGet, backupExportPathFor(t, target)))
			if rr.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body)
			}
		})
	}
}

func TestBackupExportRejectsInvalidServiceNameAndNonGetRequests(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "Bad_Name", "same-name")
	path := backupExportPathFor(t, target)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, request(t, cert, http.MethodGet, path))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("invalid service name status=%d", rr.Code)
	}

	h, cert = newFixture(t)
	target = backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	path = backupExportPathFor(t, target)
	for name, mutate := range map[string]func(*http.Request){
		"post":     func(r *http.Request) { r.Method = http.MethodPost },
		"delete":   func(r *http.Request) { r.Method = http.MethodDelete },
		"query":    func(r *http.Request) { r.URL.RawQuery = "url=https://elsewhere" },
		"upgrade":  func(r *http.Request) { r.Header.Set("Upgrade", "websocket") },
		"body":     func(r *http.Request) { r.ContentLength = 4 },
		"raw path": func(r *http.Request) { r.URL.RawPath = path },
	} {
		t.Run(name, func(t *testing.T) {
			req := request(t, cert, http.MethodGet, path)
			mutate(req)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != http.StatusNotFound && rr.Code != http.StatusBadRequest {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			if rr.Code == http.StatusOK {
				t.Fatal("request reached the export worker")
			}
		})
	}
}

func TestBackupExportStreamsReadyArchiveFromBoundWorker(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/export/export-uid" || r.Host != "exp-svc.games.svc.cluster.local:9443" {
			t.Errorf("unexpected worker request %s %s %s", r.Method, r.Host, r.URL.Path)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("client credentials leaked to the worker")
		}
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Disposition", `attachment; filename="backup.zip"`)
		w.Header().Set("Set-Cookie", "secret=bad")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("archive-"))
		_, _ = w.Write([]byte("bytes"))
	}))
	defer upstream.Close()
	h.transport = func(TLSFiles) (*http.Transport, error) {
		tr := upstream.Client().Transport.(*http.Transport).Clone()
		tr.TLSClientConfig.ServerName = "127.0.0.1"
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return tr, nil
	}
	req := request(t, cert, http.MethodGet, backupExportPathFor(t, target))
	req.Header.Set("Cookie", "secret")
	req.Header.Set("Authorization", "secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	if got := rr.Body.String(); got != "archive-bytes" {
		t.Fatalf("body=%q", got)
	}
	if rr.Header().Get("Content-Type") != "application/zip" || !strings.Contains(rr.Header().Get("Content-Disposition"), "backup.zip") {
		t.Fatalf("headers=%v", rr.Header())
	}
	if rr.Header().Get("Set-Cookie") != "" {
		t.Fatal("worker Set-Cookie forwarded")
	}
}

// A worker that dies mid-archive must not look like a clean end: the handler
// has already written the status line, so it aborts the connection instead.
func TestBackupExportUpstreamFailureAbortsResponse(t *testing.T) {
	h, cert := newFixture(t)
	target := backupExportFixture(t, h, "Ready", "exp-svc", "same-name")
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Header().Set("Content-Length", "100000")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("partial-archive"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		_ = conn.Close()
	}))
	defer upstream.Close()
	h.transport = func(TLSFiles) (*http.Transport, error) {
		tr := upstream.Client().Transport.(*http.Transport).Clone()
		tr.TLSClientConfig.ServerName = "127.0.0.1"
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return tr, nil
	}
	rr := httptest.NewRecorder()
	recovered := func() (v any) {
		defer func() { v = recover() }()
		h.ServeHTTP(rr, request(t, cert, http.MethodGet, backupExportPathFor(t, target)))
		return nil
	}()
	if err, ok := recovered.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("recovered=%v want http.ErrAbortHandler (status=%d body=%q)", recovered, rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "partial-archive" {
		t.Fatalf("body=%q", got)
	}
}
