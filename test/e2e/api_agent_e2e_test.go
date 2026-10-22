//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// requireAgentReady waits for the agent sidecar in `<gs>-0` to reach
// Ready=True with no restarts, and fails the test if it never does.
// The chart provisions the agent CA unconditionally (templates/mtls.yaml)
// and the operator wires --tls-cert/--tls-key/--tls-client-ca from the
// per-GameServer Secret it signs, so an unready agent here is a real
// regression, not missing test wiring.
func requireAgentReady(t *testing.T, ns, gsName string) {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(ctx, gsName+"-0", metav1.GetOptions{})
		if err == nil {
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.Name != "agent" {
					continue
				}
				// Require restartCount=0 in addition to Ready=true: a
				// crashlooping agent has a tiny window where
				// ContainerStatus.Ready flips to true before the kubelet
				// records the crash — without the restart-count guard,
				// the test passes the wait and then 500s on the first
				// agent call.
				if cs.Ready && cs.RestartCount == 0 {
					return
				}
			}
		}
		time.Sleep(2 * time.Second)
	}
	logs, _ := envInstance.Kubectl(ctx, "logs", "-n", ns, gsName+"-0", "-c", "agent", "--tail=10")
	t.Fatalf("agent sidecar never reached stable Ready in 90s. agent logs:\n%s", logs)
}

// waitAgentReachable polls a cheap agent endpoint through the API until
// it answers 200. "Agent container Ready" does not mean routable yet:
// the per-GameServer agent Service, its EndpointSlice, and kube-proxy's
// dataplane programming are all asynchronous, so the first proxied
// request can race that chain and see a 502.
func waitAgentReachable(t *testing.T, cli *APIClient, gs string) {
	t.Helper()
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		resp, body, err := cli.Get("/servers/" + gs + "/players")
		if err != nil {
			return false, "GET /players: " + err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, "status=" + http.StatusText(resp.StatusCode) + " body=" + string(body)
		}
		return true, ""
	})
}

// TestAPI_AgentFilesRoundTrip exercises the API → agent proxy for the
// files endpoints. The dashboard's Files tab calls this same path:
// /servers/{name}/files/{write,list,read,delete}. The test:
//
//  1. Creates a busybox GameServer (auto-injects the agent sidecar).
//  2. Waits for the pod (including agent) to reach Ready — without
//     this, every files request 502s through the proxy.
//  3. Writes a file via /files/write.
//  4. Lists the data root and asserts the file is present.
//  5. Reads it back and asserts the bytes match.
//  6. Deletes it and asserts a subsequent list does NOT show it.
//
// This is the slowest non-restic test in the suite (the agent's mTLS
// cert plumbing + container startup is a few minutes), so we set
// generous timeouts on the Ready wait but keep per-request timeouts
// tight to surface plumbing regressions cleanly.
func TestAPI_AgentFilesRoundTrip(t *testing.T) {
	t.Parallel()

	ns := "gameplane-games"
	tmpl := "e2e-agent-files-tmpl"
	gs := "e2e-agent-files-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	requireAgentReady(t, ns, gs)
	waitAgentReachable(t, cli, gs)

	// Agent file paths are relative to the agent's data root ("/" is the
	// game's data volume), matching the paths the list endpoint returns —
	// NOT absolute pod paths.
	const fileName = "hello-from-e2e.txt"
	const filePath = "/" + fileName
	const payload = "hi from gameplane api e2e"

	// Write a file. The endpoint takes path as query param and body as
	// raw octet-stream — APIClient.Do marshals JSON, so we go direct.
	writeURL := cli.BaseURL + "/servers/" + gs + "/files/write?path=" + url.QueryEscape(filePath)
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, writeURL, bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("build write req: %v", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	resp, err := cli.HTTP.Do(req)
	if err != nil {
		t.Fatalf("POST /files/write: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("/files/write expected 2xx, got %d body=%q", resp.StatusCode, string(body))
	}

	// List the data root — file must appear.
	envInstance.Eventually(t, 30*time.Second, func() (bool, string) {
		resp, body, err := cli.Get("/servers/" + gs + "/files/list?path=" + url.QueryEscape("/"))
		if err != nil {
			return false, "list: " + err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, "list status=" + http.StatusText(resp.StatusCode) + " body=" + string(body)
		}
		var entries []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &entries); err != nil {
			return false, "decode list: " + err.Error() + " body=" + string(body)
		}
		for _, e := range entries {
			if e.Name == fileName {
				return true, ""
			}
		}
		return false, "file not in listing yet"
	})

	// Read back — content must match.
	readResp, readBody, err := cli.Get("/servers/" + gs + "/files/read?path=" + url.QueryEscape(filePath))
	if err != nil {
		t.Fatalf("GET /files/read: %v", err)
	}
	defer func() { _ = readResp.Body.Close() }()
	if readResp.StatusCode != http.StatusOK {
		t.Fatalf("/files/read expected 200, got %d body=%q", readResp.StatusCode, string(readBody))
	}
	if got := string(readBody); got != payload {
		t.Errorf("/files/read body=%q want %q", got, payload)
	}

	// F-074 regression: a download that takes longer than the API's 60s
	// app-wide request-timeout must not be cut off. Seed a 200 MiB file
	// directly on the PVC (the same method as the finding's own repro —
	// evidence/review-api/verification.md#c-api-01 step 4 — since this
	// needs to exceed kernel TCP buffer sizes to force real backpressure,
	// well past what /files/write's 64 MiB cap would allow), then
	// download it while pacing our reads well below the file's size, so
	// the transfer is still in flight past 60s. Before the fix, chi's
	// middleware.Timeout cancels req.Context() at 60s, the agent-proxy
	// io.Copy in ws/dialer.go aborts, and the paced read below sees an
	// error long before slowReadFloor elapses.
	const bigFileName = "large-from-e2e.bin"
	const bigFileSizeMB = 200
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0",
		"dd", "if=/dev/zero", "of=/data/"+bigFileName,
		"bs=1M", fmt.Sprintf("count=%d", bigFileSizeMB)); err != nil {
		t.Fatalf("seed large file via dd: %v output=%s", err, out)
	}

	dlReq, err := http.NewRequestWithContext(t.Context(), http.MethodGet,
		cli.BaseURL+"/servers/"+gs+"/files/download?path="+url.QueryEscape("/"+bigFileName), nil)
	if err != nil {
		t.Fatalf("build large download req: %v", err)
	}
	dlReq.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	// cli.HTTP carries a 90s Timeout that also covers reading the body, so
	// the deliberately-slow transfers below go through a client with a
	// longer ceiling (same cookie jar and transport).
	slowCli := longTransferClient(cli)
	dlResp, err := slowCli.Do(dlReq)
	if err != nil {
		t.Fatalf("GET /files/download (large): %v", err)
	}
	defer func() { _ = dlResp.Body.Close() }()
	if dlResp.StatusCode != http.StatusOK {
		t.Fatalf("/files/download (large) expected 200, got %d", dlResp.StatusCode)
	}

	// Pace by elapsed time rather than per Read call: Read may return far
	// fewer bytes than len(buf), so a fixed per-call pause would make the
	// total duration depend on how the transport fragments the stream.
	// After each read, sleep until total/size of slowReadTarget has passed.
	const slowReadFloor = 65 * time.Second
	const slowReadTarget = 72 * time.Second
	const chunkSize = 256 * 1024
	const bigFileSize = int64(bigFileSizeMB) << 20
	start := time.Now()
	buf := make([]byte, chunkSize)
	var total int64
	for {
		n, rerr := dlResp.Body.Read(buf)
		total += int64(n)
		if rerr != nil {
			if rerr == io.EOF {
				break
			}
			t.Fatalf("large download read at %s elapsed, %d/%d bytes (cut short by requestTimeout?): %v",
				time.Since(start), total, bigFileSize, rerr)
		}
		time.Sleep(time.Until(start.Add(time.Duration(float64(slowReadTarget) * float64(total) / float64(bigFileSize)))))
	}
	elapsed := time.Since(start)
	if total != bigFileSize {
		t.Fatalf("large download got %d bytes, want %d", total, bigFileSize)
	}
	if elapsed < slowReadFloor {
		t.Fatalf("large download finished in %s, want it paced past %s to exercise the 60s request timeout", elapsed, slowReadFloor)
	}

	// F-074 regression, upload side: POST /files/write streams its body to
	// the agent on req.Context() just like the download above, so a write
	// whose body trickles in past the 60s request-timeout must still land.
	// Pace a 2 MiB body (also over the old 1 MiB bodyLimit, F-075) so the
	// upload is still in flight past slowWriteFloor.
	const slowFileName = "slow-write-from-e2e.bin"
	const slowWriteSize = 2 << 20
	const slowWriteFloor = 65 * time.Second
	slowBody := bytes.Repeat([]byte("s"), slowWriteSize)
	slowWriteURL := cli.BaseURL + "/servers/" + gs + "/files/write?path=" + url.QueryEscape("/"+slowFileName)
	slowReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost, slowWriteURL,
		newPacedReader(slowBody, 64*1024, 72*time.Second))
	if err != nil {
		t.Fatalf("build slow write req: %v", err)
	}
	slowReq.ContentLength = int64(len(slowBody))
	slowReq.Header.Set("Content-Type", "application/octet-stream")
	slowReq.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	slowStart := time.Now()
	slowResp, err := slowCli.Do(slowReq)
	if err != nil {
		t.Fatalf("POST /files/write (slow) after %s: %v", time.Since(slowStart), err)
	}
	slowRespBody, _ := io.ReadAll(slowResp.Body)
	_ = slowResp.Body.Close()
	slowElapsed := time.Since(slowStart)
	if slowResp.StatusCode/100 != 2 {
		t.Fatalf("/files/write (slow) expected 2xx after %s (cut short by requestTimeout?), got %d body=%q",
			slowElapsed, slowResp.StatusCode, string(slowRespBody))
	}
	if slowElapsed < slowWriteFloor {
		t.Fatalf("slow /files/write finished in %s, want it paced past %s to exercise the 60s request timeout", slowElapsed, slowWriteFloor)
	}
	listResp, listBody, err := cli.Get("/servers/" + gs + "/files/list?path=" + url.QueryEscape("/"))
	if err != nil {
		t.Fatalf("list after slow write: %v", err)
	}
	_ = listResp.Body.Close()
	var slowEntries []struct {
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(listBody, &slowEntries); err != nil {
		t.Fatalf("decode list after slow write: %v body=%q", err, string(listBody))
	}
	foundSlow := false
	for _, e := range slowEntries {
		if e.Name == slowFileName {
			foundSlow = true
			if e.Size != slowWriteSize {
				t.Fatalf("slow-written file size=%d want %d", e.Size, slowWriteSize)
			}
		}
	}
	if !foundSlow {
		t.Fatalf("slow-written file %q not in listing: %s", slowFileName, string(listBody))
	}

	// Delete and verify the file is gone from a subsequent list.
	delResp, delBody, err := cli.Delete("/servers/" + gs + "/files/delete?path=" + url.QueryEscape(filePath))
	if err != nil {
		t.Fatalf("DELETE /files/delete: %v", err)
	}
	defer func() { _ = delResp.Body.Close() }()
	if delResp.StatusCode/100 != 2 {
		t.Fatalf("/files/delete expected 2xx, got %d body=%q", delResp.StatusCode, string(delBody))
	}

	envInstance.Eventually(t, 15*time.Second, func() (bool, string) {
		resp, body, err := cli.Get("/servers/" + gs + "/files/list?path=" + url.QueryEscape("/"))
		if err != nil {
			return false, "list-after-delete: " + err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			return false, "list-after-delete status=" + http.StatusText(resp.StatusCode)
		}
		var entries []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(body, &entries); err != nil {
			return false, "decode: " + err.Error()
		}
		for _, e := range entries {
			if e.Name == fileName {
				return false, "file still listed"
			}
		}
		return true, ""
	})

	// Upload confinement: an upload whose destination name is an existing
	// symlink pointing outside the data root must be refused with 400 Bad Request (errPathOutOfRoot),
	// leaving the symlink in place.
	const escLinkName = "esc-link"
	const escLinkPath = "/data/" + escLinkName
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0",
		"ln", "-s", "/etc", escLinkPath); err != nil {
		t.Fatalf("create out-of-root symlink: %v output=%s", err, out)
	}
	t.Cleanup(func() {
		// t.Context() is already canceled when cleanups run.
		_, _ = envInstance.Kubectl(context.Background(), "exec", "-n", ns, "pod/"+gs+"-0", "--", "rm", "-f", escLinkPath)
	})

	escBuf := &bytes.Buffer{}
	escMW := multipart.NewWriter(escBuf)
	escFW, err := escMW.CreateFormFile("files", escLinkName)
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := escFW.Write([]byte("should-be-rejected")); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := escMW.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	escReq, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		cli.BaseURL+"/servers/"+gs+"/files/upload?path="+url.QueryEscape("/"), escBuf)
	if err != nil {
		t.Fatalf("build symlink upload req: %v", err)
	}
	escReq.Header.Set("Content-Type", escMW.FormDataContentType())
	escReq.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	escResp, err := cli.HTTP.Do(escReq)
	if err != nil {
		t.Fatalf("POST /files/upload onto symlink: %v", err)
	}
	escBody, _ := io.ReadAll(escResp.Body)
	_ = escResp.Body.Close()
	if escResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("upload onto out-of-root symlink expected %d, got %d body=%q", http.StatusBadRequest, escResp.StatusCode, string(escBody))
	}
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0",
		"test", "-L", escLinkPath); err != nil {
		t.Fatalf("out-of-root symlink was replaced by the rejected upload: %v output=%s", err, out)
	}
}

// TestAPI_AgentPlayers covers /servers/{name}/players. Busybox doesn't
// speak any game protocol, so the agent reports 0 online and an empty
// banned list — but the JSON shape must still be the contract the
// dashboard expects (PlayersResp in web/src/types.ts: online, max,
// players, asOf, capabilities).
//
// We don't assert specific numbers; we assert the response is valid
// JSON with the expected keys, which proves the API correctly proxied
// to the agent and decoded its response.
func TestAPI_AgentPlayers(t *testing.T) {
	t.Parallel()

	ns := "gameplane-games"
	tmpl := "e2e-agent-players-tmpl"
	gs := "e2e-agent-players-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	requireAgentReady(t, ns, gs)

	waitAgentReachable(t, cli, gs)

	resp, body, err := cli.Get("/servers/" + gs + "/players")
	if err != nil {
		t.Fatalf("GET /players: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/players expected 200, got %d body=%q", resp.StatusCode, string(body))
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode /players: %v body=%q", err, string(body))
	}
	if _, ok := got["online"]; !ok {
		t.Errorf("/players response missing 'online' field: %s", string(body))
	}
}

// TestAPI_AgentUnreachable: when the GameServer pod is suspended (and
// the agent therefore unreachable), the API must surface a structured
// error rather than hang or 500. The dashboard distinguishes 502/503/504
// from a real auth failure or other 4xx; we assert the response code is
// in that "agent down" range.
func TestAPI_AgentUnreachable(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ns := "gameplane-games"
	tmpl := "e2e-agent-unreach-tmpl"
	gs := "e2e-agent-unreach-gs"

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	cli := envInstance.APIClient(t, adminUsername, adminPassword)
	defer cli.Close()

	applyBusyboxTemplate(t, tmpl)
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitStatefulSetReplicas(t, ns, gs, 1, 90*time.Second)
	requireAgentReady(t, ns, gs)

	// Suspend → operator scales the StatefulSet to 0 → pod gone → agent
	// unreachable. We don't wait for full pod deletion because the
	// proxy's connect attempt fails as soon as the Service has no ready
	// endpoints, which happens shortly after the suspend patch.
	suspendPatch := []byte(`{"spec":{"suspend":true}}`)
	if _, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).
		Patch(ctx, gs, types.MergePatchType, suspendPatch, metav1.PatchOptions{}); err != nil {
		t.Fatalf("patch suspend=true: %v", err)
	}
	waitStatefulSetReplicas(t, ns, gs, 0, 60*time.Second)

	// Allow the kube Service's endpoints to drop. With 0 ready pods
	// behind the headless Service the proxy will fail fast.
	envInstance.Eventually(t, 60*time.Second, func() (bool, string) {
		resp, body, err := cli.Get("/servers/" + gs + "/players")
		if err != nil {
			// network errors here would be from our own port-forward
			// dropping, not from the proxy → agent leg. Surface and
			// retry.
			return false, "GET /players: " + err.Error()
		}
		defer func() { _ = resp.Body.Close() }()
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true, ""
		case http.StatusOK:
			return false, "agent still answering despite suspend — endpoints not yet drained"
		default:
			// Something other than 5xx-from-proxy and not 200. Print and
			// fail fast so the test surface is unambiguous: a 401/403
			// here would mean session loss; a 404 would mean the proxy
			// route changed.
			s := strings.TrimSpace(string(body))
			if len(s) > 200 {
				s = s[:200]
			}
			return false, "unexpected status " + http.StatusText(resp.StatusCode) + " body=" + s
		}
	})
}

// longTransferClient returns an http.Client sharing cli's cookie jar and
// transport but with a 5-minute Timeout instead of APIClient's 90s. The
// F-074 regressions deliberately stretch a transfer past the API's 60s
// request-timeout, and http.Client.Timeout covers the whole exchange
// (sending the request body and reading the response body), so 90s would
// race those ~72s transfers plus cluster latency.
func longTransferClient(cli *APIClient) *http.Client {
	return &http.Client{
		Jar:       cli.HTTP.Jar,
		Transport: cli.HTTP.Transport,
		Timeout:   5 * time.Minute,
	}
}

// pacedReader yields data in chunks of at most chunk bytes, pacing by
// elapsed time: before handing out the bytes at offset off it sleeps until
// off/len(data) of total has passed since the first Read. The body thus
// trickles in over roughly total regardless of the caller's buffer size
// (net/http reads request bodies 32 KiB at a time). Used to prove
// large-transfer routes survive the API's 60s request-timeout (F-074).
type pacedReader struct {
	data  []byte
	chunk int
	total time.Duration
	off   int
	start time.Time
}

func newPacedReader(data []byte, chunk int, total time.Duration) *pacedReader {
	return &pacedReader{data: data, chunk: chunk, total: total}
}

func (p *pacedReader) Read(b []byte) (int, error) {
	if p.off >= len(p.data) {
		return 0, io.EOF
	}
	if p.start.IsZero() {
		p.start = time.Now()
	} else {
		due := p.start.Add(time.Duration(float64(p.total) * float64(p.off) / float64(len(p.data))))
		time.Sleep(time.Until(due))
	}
	n := min(len(b), p.chunk, len(p.data)-p.off)
	copy(b, p.data[p.off:p.off+n])
	p.off += n
	return n, nil
}
// spec 023 validation scenario 4
