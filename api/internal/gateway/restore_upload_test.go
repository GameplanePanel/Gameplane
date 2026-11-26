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

const restoreUploadBody = "archive"

// restoreUploadFixture stores an upload Restore named rs-one. serverRef is the
// GameServer it targets, receiver the receiver Service in status (empty for
// none), phase its status phase, and withUpload whether spec.upload is set.
func restoreUploadFixture(t *testing.T, h *handler, phase, receiver, serverRef string, withUpload bool) gatewayprotocol.RestoreUploadTarget {
	t.Helper()
	spec := map[string]any{"serverRef": map[string]any{"name": serverRef}}
	if withUpload {
		spec["upload"] = map[string]any{"fileName": "b.zip", "format": "zip", "declaredBytes": int64(len(restoreUploadBody))}
	} else {
		spec["backupRef"] = map[string]any{"name": "bk"}
	}
	status := map[string]any{"phase": phase}
	if receiver != "" {
		status["upload"] = map[string]any{"receiverService": receiver}
	}
	rs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "Restore",
		"metadata":   map[string]any{"name": "rs-one", "namespace": "games", "uid": "restore-uid"},
		"spec":       spec,
		"status":     status,
	}}
	if _, err := h.client.Dynamic.Resource(kube.GVRRestore).Namespace("games").Create(t.Context(), rs, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	return gatewayprotocol.RestoreUploadTarget{
		Target:     gatewayprotocol.Target{Cluster: "remote", Namespace: "games", Name: "same-name", UID: "original-uid"},
		Restore:    "rs-one",
		RestoreUID: "restore-uid",
	}
}

func restoreUploadPathFor(t *testing.T, target gatewayprotocol.RestoreUploadTarget) string {
	t.Helper()
	path, err := gatewayprotocol.RestoreUploadPath(target)
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// workerTransport routes the gateway's upstream dial to a local TLS worker
// double while keeping the production request shape.
func workerTransport(t *testing.T, upstream *httptest.Server) func(TLSFiles) (*http.Transport, error) {
	return func(TLSFiles) (*http.Transport, error) {
		tr := upstream.Client().Transport.(*http.Transport).Clone()
		tr.TLSClientConfig.ServerName = "127.0.0.1"
		tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
		}
		return tr, nil
	}
}

func TestRestoreUploadWrongIdentitiesAreNotFoundAndNeverReachWorker(t *testing.T) {
	for _, tc := range []struct {
		name          string
		serverRef     string
		withUpload    bool
		mutate        func(*gatewayprotocol.RestoreUploadTarget)
		wantNotFound  bool
		wantWorkerHit bool
	}{
		{name: "valid", serverRef: "same-name", withUpload: true, mutate: func(*gatewayprotocol.RestoreUploadTarget) {}, wantWorkerHit: true},
		{name: "wrong server uid", serverRef: "same-name", withUpload: true, mutate: func(b *gatewayprotocol.RestoreUploadTarget) { b.UID = "replacement-server" }, wantNotFound: true},
		{name: "wrong restore uid", serverRef: "same-name", withUpload: true, mutate: func(b *gatewayprotocol.RestoreUploadTarget) { b.RestoreUID = "replacement" }, wantNotFound: true},
		{name: "missing restore", serverRef: "same-name", withUpload: true, mutate: func(b *gatewayprotocol.RestoreUploadTarget) { b.Restore = "missing" }, wantNotFound: true},
		{name: "restore of another server", serverRef: "other-server", withUpload: true, mutate: func(*gatewayprotocol.RestoreUploadTarget) {}, wantNotFound: true},
		{name: "restore without upload", serverRef: "same-name", withUpload: false, mutate: func(*gatewayprotocol.RestoreUploadTarget) {}, wantNotFound: true},
		{name: "foreign namespace", serverRef: "same-name", withUpload: true, mutate: func(b *gatewayprotocol.RestoreUploadTarget) { b.Namespace = "other" }, wantNotFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cert := newFixture(t)
			target := restoreUploadFixture(t, h, "Receiving", "rs-svc", tc.serverRef, tc.withUpload)
			tc.mutate(&target)
			var hits atomic.Int64
			h.transport = func(TLSFiles) (*http.Transport, error) {
				hits.Add(1)
				return nil, io.ErrUnexpectedEOF
			}
			req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
			req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
			req.ContentLength = int64(len(restoreUploadBody))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if tc.wantNotFound && rr.Code != http.StatusNotFound {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
			if tc.wantWorkerHit == (hits.Load() == 0) {
				t.Fatalf("worker contacted=%v want=%v", hits.Load() > 0, tc.wantWorkerHit)
			}
		})
	}
}

func TestRestoreUploadNotReceivingIsConflict(t *testing.T) {
	for _, tc := range []struct {
		name, phase, receiver string
		status                int
	}{
		{"pending", "Pending", "rs-svc", http.StatusConflict},
		{"awaiting confirmation", "AwaitingConfirmation", "rs-svc", http.StatusConflict},
		{"empty phase", "", "rs-svc", http.StatusConflict},
		{"receiving without receiver", "Receiving", "", http.StatusConflict},
		{"invalid receiver name", "Receiving", "Bad_Name", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, cert := newFixture(t)
			target := restoreUploadFixture(t, h, tc.phase, tc.receiver, "same-name", true)
			var hits atomic.Int64
			h.transport = func(TLSFiles) (*http.Transport, error) {
				hits.Add(1)
				return nil, io.ErrUnexpectedEOF
			}
			req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
			req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
			req.ContentLength = int64(len(restoreUploadBody))
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", rr.Code, tc.status, rr.Body)
			}
			if hits.Load() != 0 {
				t.Fatal("request reached the receiver before it was Receiving")
			}
		})
	}
}

func TestRestoreUploadRejectsMissingLengthMethodAndQuery(t *testing.T) {
	h, cert := newFixture(t)
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	path := restoreUploadPathFor(t, target)
	for name, mutate := range map[string]func(*http.Request){
		"no length": func(r *http.Request) { r.ContentLength = -1 },
		"zero":      func(r *http.Request) { r.ContentLength = 0 },
		"post":      func(r *http.Request) { r.Method = http.MethodPost },
		"query":     func(r *http.Request) { r.URL.RawQuery = "url=https://elsewhere" },
		"upgrade":   func(r *http.Request) { r.Header.Set("Upgrade", "websocket") },
	} {
		t.Run(name, func(t *testing.T) {
			req := request(t, cert, http.MethodPut, path)
			req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
			req.ContentLength = int64(len(restoreUploadBody))
			mutate(req)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)
			if rr.Code == http.StatusOK || rr.Code == http.StatusNoContent {
				t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
			}
		})
	}
}

func TestRestoreUploadStreamsBodyToBoundReceiver(t *testing.T) {
	h, cert := newFixture(t)
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Path != "/upload/restore-uid" || r.Host != "rs-svc.games.svc:9443" {
			t.Errorf("unexpected worker request %s %s %s", r.Method, r.Host, r.URL.Path)
		}
		if r.ContentLength != int64(len(restoreUploadBody)) {
			t.Errorf("worker content length=%d", r.ContentLength)
		}
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("client credentials leaked to the receiver")
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != restoreUploadBody {
			t.Errorf("worker body=%q err=%v", body, err)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer upstream.Close()
	h.transport = workerTransport(t, upstream)
	req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
	req.Body = io.NopCloser(strings.NewReader(restoreUploadBody))
	req.ContentLength = int64(len(restoreUploadBody))
	req.Header.Set("Cookie", "secret")
	req.Header.Set("Authorization", "secret")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body=%q", rr.Body.String())
	}
}

// A body that fails before the declared length arrives must not be reported as
// a successful upload: the gateway answers 502 and the receiver never sees a
// complete body.
func TestRestoreUploadBodyFailureIsBadGatewayNotSuccess(t *testing.T) {
	h, cert := newFixture(t)
	target := restoreUploadFixture(t, h, "Receiving", "rs-svc", "same-name", true)
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()
	h.transport = workerTransport(t, upstream)
	req := request(t, cert, http.MethodPut, restoreUploadPathFor(t, target))
	req.Body = io.NopCloser(io.MultiReader(strings.NewReader("arc"), &failingReader{err: errors.New("client went away")}))
	req.ContentLength = int64(len(restoreUploadBody))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body)
	}
}

type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }
