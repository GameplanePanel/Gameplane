//go:build e2e

package e2e

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
)

// Backup archive downloads and upload restores (spec 041).
//
// Login budget: TestBackupArchives_Download uses 1 e2e-admin login plus 1
// login as its own viewer-role user; TestBackupArchives_UploadRestore uses 1
// e2e-admin login. The bucket total is 3, within the ~7 admin-login budget.
//
// The remote-cluster download (quickstart step 12) is not covered here: it
// needs a registered remote cluster, which only the multicluster fixture
// provides, and that bucket runs on its own two-cluster job.

const archiveManifestName = "gameplane-backup.json"

var archiveSHARe = regexp.MustCompile(`[0-9a-f]{64}`)

// archiveManifest is the part of gameplane-backup.json the tests check.
type archiveManifest struct {
	FormatVersion int `json:"formatVersion"`
	Server        struct {
		Name string `json:"name"`
	} `json:"server"`
	Template struct {
		Name string `json:"name"`
	} `json:"template"`
}

// archiveFiles maps entry names to contents; directories and links are skipped.
type archiveFiles map[string][]byte

// find returns the content of the first regular entry whose base name is base.
func (a archiveFiles) find(base string) ([]byte, bool) {
	for name, data := range a {
		if path.Base(name) == base && name != archiveManifestName {
			return data, true
		}
	}
	return nil, false
}

func readTarGz(t *testing.T, data []byte) archiveFiles {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("open gzip: %v", err)
	}
	tr := tar.NewReader(gz)
	out := archiveFiles{}
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return out
		}
		if err != nil {
			t.Fatalf("read tar: %v", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatalf("read tar entry %s: %v", hdr.Name, err)
		}
		out[strings.TrimPrefix(hdr.Name, "./")] = b
	}
}

func readZip(t *testing.T, data []byte) archiveFiles {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	out := archiveFiles{}
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || !f.Mode().IsRegular() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", f.Name, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatalf("read zip entry %s: %v", f.Name, err)
		}
		out[f.Name] = b
	}
	return out
}

// buildTarGz writes a tar.gz of the given entries in order, without any
// path checking, so a test can build an unsafe archive.
func buildTarGz(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, e := range entries {
		hdr := &tar.Header{Name: e[0], Mode: 0o644, Size: int64(len(e[1])), Typeflag: tar.TypeReg, ModTime: time.Now()}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write tar header: %v", err)
		}
		if _, err := tw.Write([]byte(e[1])); err != nil {
			t.Fatalf("write tar entry: %v", err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func buildZip(t *testing.T, entries [][2]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		w, err := zw.Create(e[0])
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := w.Write([]byte(e[1])); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func archiveSHA(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// archiveWaitGameReady waits for the server's game container to be Ready and
// returns the pod UID.
func archiveWaitGameReady(t *testing.T, ns, gs string) types.UID {
	t.Helper()
	var uid types.UID
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), gs+"-0", metav1.GetOptions{})
		if err != nil {
			return false, "get pod: " + err.Error()
		}
		for _, cs := range pod.Status.ContainerStatuses {
			if cs.Name == "game" && cs.Ready {
				uid = pod.UID
				return true, ""
			}
		}
		return false, "game container not ready yet"
	})
	return uid
}

// archiveNewServer creates a busybox GameServer on tmpl and waits for it.
func archiveNewServer(t *testing.T, ns, gs, tmpl string) {
	t.Helper()
	applyBusyboxGameServer(t, ns, gs, tmpl)
	waitPVCBound(t, ns, gs+"-data", 90*time.Second)
	archiveWaitGameReady(t, ns, gs)
}

// archiveBackup takes a restic backup of gs and returns its name.
func archiveBackup(t *testing.T, ns, gs, name string) {
	t.Helper()
	createBackup(t, ns, name, gs, "e2e-restic-creds", "repo")
	if id := waitBackupSucceeded(t, ns, name, 5*time.Minute); id == "" {
		t.Fatalf("backup %s succeeded without a snapshot id", name)
	}
}

// archiveDownload GETs a backup archive as cli.
// archiveResult is the status and headers of a finished download; the body
// is already read and closed.
type archiveResult struct {
	StatusCode int
	Header     http.Header
}

func archiveDownload(t *testing.T, cli *APIClient, ns, backup, format string) (archiveResult, []byte) {
	t.Helper()
	resp, body, err := cli.Get("/backups/" + backup + ":download?namespace=" + url.QueryEscape(ns) + "&format=" + url.QueryEscape(format))
	if err != nil {
		t.Fatalf("download %s as %s: %v", backup, format, err)
	}
	_ = resp.Body.Close()
	return archiveResult{StatusCode: resp.StatusCode, Header: resp.Header}, body
}

// archiveUpload POSTs a raw archive body to /restores:upload. size is the
// declared Content-Length.
func archiveUpload(cli *APIClient, ns, server, format, fileName, template string, body io.Reader, size int64) (int, []byte, error) {
	q := url.Values{"namespace": {ns}, "server": {server}, "format": {format}, "fileName": {fileName}}
	if template != "" {
		q.Set("template", template)
	}
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, cli.BaseURL+"/restores:upload?"+q.Encode(), body)
	if err != nil {
		return 0, nil, err
	}
	req.ContentLength = size
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-Gameplane-CSRF", cli.CSRF)
	resp, err := cli.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	rb, err := io.ReadAll(resp.Body)
	return resp.StatusCode, rb, err
}

// archiveUploadBytes uploads a whole archive and returns the Restore name
// from a 202, or "" with the status for a refusal.
func archiveUploadBytes(t *testing.T, cli *APIClient, ns, server, format, template string, data []byte) (string, int, string) {
	t.Helper()
	status, body, err := archiveUpload(cli, ns, server, format, "e2e-upload."+format, template, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("upload to %s: %v", server, err)
	}
	if status != http.StatusAccepted {
		return "", status, string(body)
	}
	var out struct {
		Restore string `json:"restore"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.Restore == "" {
		t.Fatalf("upload response %q: %v", body, err)
	}
	name := out.Restore
	t.Cleanup(func() {
		_ = envInstance.Dyn.Resource(restoreGVR).Namespace(ns).Delete(context.Background(), name, metav1.DeleteOptions{})
	})
	return name, status, string(body)
}

// archiveWaitRestorePhase waits for the Restore to reach phase want. A
// different terminal phase fails the test.
func archiveWaitRestorePhase(t *testing.T, ns, name, want string, timeout time.Duration) *unstructured.Unstructured {
	t.Helper()
	var got *unstructured.Unstructured
	envInstance.Eventually(t, timeout, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			return false, "get restore: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if phase == want {
			got = obj
			return true, ""
		}
		if phase == "Failed" || phase == "Succeeded" {
			msg, _, _ := unstructured.NestedString(obj.Object, "status", "message")
			t.Fatalf("restore %s ended %s (want %s): %s", name, phase, want, msg)
		}
		return false, "phase=" + phase
	})
	return got
}

// archiveConfirm confirms an AwaitingConfirmation upload restore.
func archiveConfirm(t *testing.T, cli *APIClient, ns string, rs *unstructured.Unstructured) {
	t.Helper()
	resp, body, err := cli.Post("/restores/"+rs.GetName()+":confirm?namespace="+url.QueryEscape(ns), map[string]int64{"generation": rs.GetGeneration()})
	if err != nil {
		t.Fatalf("confirm %s: %v", rs.GetName(), err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("confirm %s: %d %s", rs.GetName(), resp.StatusCode, body)
	}
}

// archiveRefused expects an upload that never reaches a confirmable state:
// either the API refuses it, or the Restore ends Failed.
func archiveRefused(t *testing.T, ns, name string, status int, body string) {
	t.Helper()
	if name == "" {
		if status < 400 {
			t.Fatalf("upload neither accepted nor refused: %d %s", status, body)
		}
		return
	}
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		obj, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			// The restore may already be cleaned up after failing.
			return apierrors.IsNotFound(err), "get restore: " + err.Error()
		}
		phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
		switch phase {
		case "Failed":
			return true, ""
		case "AwaitingConfirmation", "Succeeded":
			t.Fatalf("restore %s reached %s, want it refused", name, phase)
		}
		return false, "phase=" + phase
	})
}

// archiveServerUntouched asserts the server kept its pod and was never suspended.
func archiveServerUntouched(t *testing.T, ns, gs string, podUID types.UID) {
	t.Helper()
	obj, err := envInstance.Dyn.Resource(gameServerGVR).Namespace(ns).Get(context.Background(), gs, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if suspend, _, _ := unstructured.NestedBool(obj.Object, "spec", "suspend"); suspend {
		t.Errorf("server %s was suspended by a refused upload", gs)
	}
	pod, err := envInstance.K8s.CoreV1().Pods(ns).Get(context.Background(), gs+"-0", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("server %s pod: %v", gs, err)
	}
	if pod.UID != podUID {
		t.Errorf("server %s pod was replaced (%s -> %s)", gs, podUID, pod.UID)
	}
}

// archiveNoStagingClaims waits until no PVC in ns is owned by a Restore
// that targets gs. A refused or interrupted upload Restore is often deleted
// by the API, so a claim whose owner no longer exists is matched by the
// owner name too: the API names upload Restores "<server>-restore-<random>",
// with the server name cut to 40 characters.
func archiveNoStagingClaims(t *testing.T, ns, gs string) {
	t.Helper()
	prefix := gs
	if len(prefix) > 40 {
		prefix = prefix[:40]
	}
	prefix += "-restore-"
	envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
		restores, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return false, err.Error()
		}
		mine := map[string]bool{}
		for _, r := range restores.Items {
			if name, _, _ := unstructured.NestedString(r.Object, "spec", "serverRef", "name"); name == gs {
				mine[r.GetName()] = true
			}
		}
		pvcs, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			return false, err.Error()
		}
		for _, pvc := range pvcs.Items {
			if pvc.Name == gs+"-data" {
				continue
			}
			for _, o := range pvc.OwnerReferences {
				if o.Kind == "Restore" && (mine[o.Name] || strings.HasPrefix(o.Name, prefix)) {
					return false, "staging claim " + pvc.Name + " still present"
				}
			}
		}
		return true, ""
	})
}

func archiveReadFile(t *testing.T, ns, gs, file string) (string, error) {
	t.Helper()
	return envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "cat", file)
}

// TestBackupArchives_Download downloads a restic backup as tar.gz and zip and
// checks the manifest and the data against what was written before the backup.
// It also covers the viewer and CSI cases.
func TestBackupArchives_Download(t *testing.T) {
	t.Parallel()
	const ns = "gameplane-games"
	suffix := time.Now().UnixNano()
	tmpl := fmt.Sprintf("e2e-archive-dl-tmpl-%d", suffix)
	gs := fmt.Sprintf("e2e-archive-dl-gs-%d", suffix)
	bk := fmt.Sprintf("e2e-archive-dl-bk-%d", suffix)

	ensureResticRepo(t)
	applyBusyboxTemplate(t, tmpl)
	archiveNewServer(t, ns, gs, tmpl)

	const marker = "gameplane-archive-download-marker"
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "sh", "-c",
		"printf %s "+marker+" > /data/marker.txt && head -c 1048576 /dev/urandom > /data/blob.bin && sync"); err != nil {
		t.Fatalf("write data: %v\n%s", err, out)
	}
	sumOut, err := envInstance.KubectlExec(t, ns, "pod/"+gs+"-0", "sha256sum", "/data/blob.bin")
	if err != nil {
		t.Fatalf("checksum blob: %v\n%s", err, sumOut)
	}
	blobSum := archiveSHARe.FindString(sumOut)
	if blobSum == "" {
		t.Fatalf("no checksum in %q", sumOut)
	}
	archiveBackup(t, ns, gs, bk)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	check := func(t *testing.T, name string, files archiveFiles) {
		t.Helper()
		raw, ok := files[archiveManifestName]
		if !ok {
			t.Fatalf("%s: no %s in archive", name, archiveManifestName)
		}
		var m archiveManifest
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("%s: manifest: %v", name, err)
		}
		if m.FormatVersion != 1 || m.Server.Name != gs || m.Template.Name != tmpl {
			t.Errorf("%s: manifest = %s, want formatVersion 1, server %s, template %s", name, raw, gs, tmpl)
		}
		if got, ok := files.find("marker.txt"); !ok || string(got) != marker {
			t.Errorf("%s: marker.txt = %q (found %v), want %q", name, got, ok, marker)
		}
		if got, ok := files.find("blob.bin"); !ok || archiveSHA(got) != blobSum {
			t.Errorf("%s: blob.bin checksum mismatch (found %v): got %s, want %s", name, ok, archiveSHA(got), blobSum)
		}
	}

	t.Run("tar.gz", func(t *testing.T) {
		resp, body := archiveDownload(t, admin, ns, bk, "tar.gz")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/gzip" {
			t.Errorf("Content-Type = %q", ct)
		}
		if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") || !strings.Contains(cd, gs) || !strings.HasSuffix(strings.TrimSuffix(cd, `"`), ".tar.gz") {
			t.Errorf("Content-Disposition = %q", cd)
		}
		check(t, "tar.gz", readTarGz(t, body))
	})

	t.Run("zip", func(t *testing.T) {
		resp, body := archiveDownload(t, admin, ns, bk, "zip")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d: %s", resp.StatusCode, body)
		}
		if ct := resp.Header.Get("Content-Type"); ct != "application/zip" {
			t.Errorf("Content-Type = %q", ct)
		}
		if cd := resp.Header.Get("Content-Disposition"); !strings.HasSuffix(strings.TrimSuffix(cd, `"`), ".zip") {
			t.Errorf("Content-Disposition = %q", cd)
		}
		check(t, "zip", readZip(t, body))
	})

	t.Run("bad format", func(t *testing.T) {
		resp, body := archiveDownload(t, admin, ns, bk, "rar")
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("status %d, want 400: %s", resp.StatusCode, body)
		}
	})

	t.Run("viewer", func(t *testing.T) {
		// The viewer role holds backups:read but not backups:restore.
		name, pw, id := envInstance.CreateUser(t, admin, "viewer", "e2e-archive-viewer")
		t.Cleanup(func() {
			if r, _, _ := admin.Delete("/users/" + id); r != nil {
				_ = r.Body.Close()
			}
		})
		viewer := envInstance.APIClient(t, name, pw)
		defer viewer.Close()
		resp, body := archiveDownload(t, viewer, ns, bk, "tar.gz")
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("viewer download status %d: %s", resp.StatusCode, body)
		}
		check(t, "viewer tar.gz", readTarGz(t, body))

		payload := []byte("not an archive")
		status, rb, err := archiveUpload(viewer, ns, gs, "tar.gz", "x.tar.gz", "", bytes.NewReader(payload), int64(len(payload)))
		if err != nil {
			t.Fatal(err)
		}
		if status != http.StatusForbidden {
			t.Errorf("viewer upload status %d, want 403: %s", status, rb)
		}
	})

	t.Run("volume snapshot refused", func(t *testing.T) {
		csi := fmt.Sprintf("e2e-archive-dl-csi-%d", suffix)
		obj := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": "gameplane.local/v1alpha1", "kind": "Backup",
			"metadata": map[string]any{"name": csi, "namespace": ns},
			"spec": map[string]any{
				"serverRef": map[string]any{"name": gs},
				"repoRef":   map[string]any{"name": "e2e-restic-creds", "key": "repo"},
				"strategy":  "volume-snapshot",
			},
		}}
		if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Create(context.Background(), obj, metav1.CreateOptions{}); err != nil {
			t.Fatalf("create volume-snapshot backup: %v", err)
		}
		t.Cleanup(func() {
			_ = envInstance.Dyn.Resource(backupGVR).Namespace(ns).Delete(context.Background(), csi, metav1.DeleteOptions{})
		})
		// The cluster has no CSI snapshot support, so the operator fails this
		// backup. Mark it Succeeded the way a real snapshot would end, then ask
		// for the download; re-mark if the operator wins the race.
		patch := []byte(`{"status":{"phase":"Succeeded","snapshotID":"e2e-csi-snapshot"}}`)
		envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
			if _, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).Patch(context.Background(), csi, types.MergePatchType, patch, metav1.PatchOptions{}, "status"); err != nil {
				return false, "patch status: " + err.Error()
			}
			resp, body := archiveDownload(t, admin, ns, csi, "tar.gz")
			if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "Snapshot backups stay inside the storage system") {
				return true, ""
			}
			return false, fmt.Sprintf("status %d: %s", resp.StatusCode, body)
		})
	})
}

// TestBackupArchives_UploadRestore covers the upload restore path: a Gameplane
// archive round trip, a wrong-game refusal, plain archives, an unsafe archive
// and an interrupted upload.
func TestBackupArchives_UploadRestore(t *testing.T) {
	t.Parallel()
	const ns = "gameplane-games"
	suffix := time.Now().UnixNano()
	tmpl := fmt.Sprintf("e2e-archive-up-tmpl-%d", suffix)
	otherTmpl := fmt.Sprintf("e2e-archive-up-other-%d", suffix)
	src := fmt.Sprintf("e2e-archive-up-src-%d", suffix)
	dst := fmt.Sprintf("e2e-archive-up-dst-%d", suffix)
	wrong := fmt.Sprintf("e2e-archive-up-wrong-%d", suffix)
	bk := fmt.Sprintf("e2e-archive-up-bk-%d", suffix)

	ensureResticRepo(t)
	applyBusyboxTemplate(t, tmpl)
	applyBusyboxTemplate(t, otherTmpl)
	archiveNewServer(t, ns, src, tmpl)
	archiveNewServer(t, ns, dst, tmpl)
	archiveNewServer(t, ns, wrong, otherTmpl)

	const marker = "gameplane-archive-upload-marker"
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+src+"-0", "sh", "-c",
		"printf %s "+marker+" > /data/marker.txt && head -c 524288 /dev/urandom > /data/blob.bin && sync"); err != nil {
		t.Fatalf("write source data: %v\n%s", err, out)
	}
	sumOut, err := envInstance.KubectlExec(t, ns, "pod/"+src+"-0", "sha256sum", "/data/blob.bin")
	if err != nil {
		t.Fatalf("checksum blob: %v\n%s", err, sumOut)
	}
	blobSum := archiveSHARe.FindString(sumOut)
	if out, err := envInstance.KubectlExec(t, ns, "pod/"+dst+"-0", "sh", "-c", "printf stale > /data/marker.txt && sync"); err != nil {
		t.Fatalf("write target data: %v\n%s", err, out)
	}
	archiveBackup(t, ns, src, bk)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	defer admin.Close()

	resp, archive := archiveDownload(t, admin, ns, bk, "tar.gz")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("download source archive: %d %s", resp.StatusCode, archive)
	}
	dstPod := archiveWaitGameReady(t, ns, dst)

	// Refusals run first, so the target keeps its original data until the
	// round trip. Each leaves the server running and its pod untouched.
	t.Run("wrong game refused", func(t *testing.T) {
		wrongPod := archiveWaitGameReady(t, ns, wrong)
		name, status, body := archiveUploadBytes(t, admin, ns, wrong, "tar.gz", "", archive)
		archiveRefused(t, ns, name, status, body)
		archiveServerUntouched(t, ns, wrong, wrongPod)
		archiveNoStagingClaims(t, ns, wrong)
	})

	t.Run("plain archive without game refused", func(t *testing.T) {
		plain := buildTarGz(t, [][2]string{{"plain.txt", "plain"}})
		name, status, body := archiveUploadBytes(t, admin, ns, dst, "tar.gz", "", plain)
		archiveRefused(t, ns, name, status, body)
		archiveServerUntouched(t, ns, dst, dstPod)
	})

	t.Run("plain archive for another game refused", func(t *testing.T) {
		plain := buildTarGz(t, [][2]string{{"plain.txt", "plain"}})
		name, status, body := archiveUploadBytes(t, admin, ns, dst, "tar.gz", otherTmpl, plain)
		archiveRefused(t, ns, name, status, body)
		archiveServerUntouched(t, ns, dst, dstPod)
	})

	t.Run("unsafe archive refused", func(t *testing.T) {
		unsafe := buildTarGz(t, [][2]string{{"fine.txt", "fine"}, {"../evil.txt", "escape"}})
		name, status, body := archiveUploadBytes(t, admin, ns, dst, "tar.gz", tmpl, unsafe)
		archiveRefused(t, ns, name, status, body)
		archiveServerUntouched(t, ns, dst, dstPod)
		archiveNoStagingClaims(t, ns, dst)
	})

	t.Run("interrupted upload", func(t *testing.T) {
		const declared = 128 << 20
		pr, pw := io.Pipe()
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, _ = archiveUpload(admin, ns, dst, "tar.gz", "interrupted.tar.gz", tmpl, pr, declared)
		}()
		// Feed some bytes so the request is in flight, then wait until the
		// operator has the receiver up and the staging claim named.
		chunk := make([]byte, 64<<10)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = pw.Write(chunk)
		}()
		var restore, claim string
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			list, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).List(context.Background(), metav1.ListOptions{})
			if err != nil {
				return false, err.Error()
			}
			for _, r := range list.Items {
				if name, _, _ := unstructured.NestedString(r.Object, "spec", "serverRef", "name"); name != dst {
					continue
				}
				if phase, _, _ := unstructured.NestedString(r.Object, "status", "phase"); phase != "Receiving" {
					continue
				}
				restore = r.GetName()
				claim, _, _ = unstructured.NestedString(r.Object, "status", "upload", "stagingClaimName")
				return true, ""
			}
			return false, "no Receiving upload restore for " + dst
		})
		pw.CloseWithError(io.ErrUnexpectedEOF)
		wg.Wait()

		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			obj, err := envInstance.Dyn.Resource(restoreGVR).Namespace(ns).Get(context.Background(), restore, metav1.GetOptions{})
			if apierrors.IsNotFound(err) {
				return true, ""
			}
			if err != nil {
				return false, err.Error()
			}
			phase, _, _ := unstructured.NestedString(obj.Object, "status", "phase")
			return phase == "Failed", "phase=" + phase
		})
		archiveServerUntouched(t, ns, dst, dstPod)
		archiveNoStagingClaims(t, ns, dst)
		if claim != "" {
			envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
				_, err := envInstance.K8s.CoreV1().PersistentVolumeClaims(ns).Get(context.Background(), claim, metav1.GetOptions{})
				return apierrors.IsNotFound(err), fmt.Sprintf("staging claim %s: %v", claim, err)
			})
		}
	})

	t.Run("gameplane archive round trip", func(t *testing.T) {
		name, status, body := archiveUploadBytes(t, admin, ns, dst, "tar.gz", "", archive)
		if name == "" {
			t.Fatalf("upload refused: %d %s", status, body)
		}
		rs := archiveWaitRestorePhase(t, ns, name, "AwaitingConfirmation", 5*time.Minute)
		if kind, _, _ := unstructured.NestedString(rs.Object, "status", "upload", "kind"); kind != "gameplane" {
			t.Errorf("upload kind = %q, want gameplane", kind)
		}
		if got, _, _ := unstructured.NestedString(rs.Object, "status", "upload", "manifest", "serverName"); got != src {
			t.Errorf("manifest serverName = %q, want %q", got, src)
		}
		// Nothing changes on the server before the user confirms.
		archiveServerUntouched(t, ns, dst, dstPod)
		archiveConfirm(t, admin, ns, rs)
		waitRestoreSucceeded(t, ns, name, 10*time.Minute)
		archiveWaitGameReady(t, ns, dst)
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			out, err := archiveReadFile(t, ns, dst, "/data/marker.txt")
			if err != nil || !strings.Contains(out, marker) {
				return false, fmt.Sprintf("marker = %q (%v)", out, err)
			}
			sum, err := envInstance.KubectlExec(t, ns, "pod/"+dst+"-0", "sha256sum", "/data/blob.bin")
			if err != nil || archiveSHARe.FindString(sum) != blobSum {
				return false, fmt.Sprintf("blob checksum = %q (%v)", sum, err)
			}
			return true, ""
		})
		archiveNoStagingClaims(t, ns, dst)
		// An upload restore is not a backup: the target gets no new Backup.
		backups, err := envInstance.Dyn.Resource(backupGVR).Namespace(ns).List(context.Background(), metav1.ListOptions{})
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range backups.Items {
			if n, _, _ := unstructured.NestedString(b.Object, "spec", "serverRef", "name"); n == dst {
				t.Errorf("unexpected backup %s for %s after an upload restore", b.GetName(), dst)
			}
		}
	})

	t.Run("plain zip archive accepted", func(t *testing.T) {
		const plainMarker = "plain-archive-marker"
		plain := buildZip(t, [][2]string{{"plain-marker.txt", plainMarker}})
		name, status, body := archiveUploadBytes(t, admin, ns, dst, "zip", tmpl, plain)
		if name == "" {
			t.Fatalf("plain upload refused: %d %s", status, body)
		}
		rs := archiveWaitRestorePhase(t, ns, name, "AwaitingConfirmation", 5*time.Minute)
		if kind, _, _ := unstructured.NestedString(rs.Object, "status", "upload", "kind"); kind != "plain" {
			t.Errorf("upload kind = %q, want plain", kind)
		}
		archiveConfirm(t, admin, ns, rs)
		waitRestoreSucceeded(t, ns, name, 10*time.Minute)
		archiveWaitGameReady(t, ns, dst)
		envInstance.Eventually(t, 2*time.Minute, func() (bool, string) {
			out, err := archiveReadFile(t, ns, dst, "/data/plain-marker.txt")
			return err == nil && strings.Contains(out, plainMarker), fmt.Sprintf("plain marker = %q (%v)", out, err)
		})
		archiveNoStagingClaims(t, ns, dst)
	})
}
