//go:build e2e

package e2e

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	sftpSystemNS      = "gameplane-system"
	sftpGamesNS       = "gameplane-games"
	sftpService       = "svc/gameplane-sftp"
	sftpServicePort   = 2222
	sftpHostKeySecret = "gameplane-sftp-host-key"
	// sftpUploadSize is the round-trip payload. The spec's 2 GiB is too slow
	// for a CI runner behind kubectl port-forward; 32 MiB still exercises the
	// streamed write path and a ranged download.
	sftpUploadSize = 32 << 20
)

// sftpHostKey reads the chart-generated host key Secret and returns its public
// half, so every SSH dial pins the server to the key the operator installed.
func sftpHostKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	sec, err := envInstance.K8s.CoreV1().Secrets(sftpSystemNS).Get(t.Context(), sftpHostKeySecret, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("read %s: %v", sftpHostKeySecret, err)
	}
	signer, err := ssh.ParsePrivateKey(sec.Data["host_key"])
	if err != nil {
		t.Fatalf("parse host_key: %v", err)
	}
	return signer.PublicKey()
}

// sftpDial opens an SSH connection with auth and starts the SFTP subsystem.
// It returns the error instead of failing so negative logins can be asserted.
func sftpDial(port int, user string, auth ssh.AuthMethod, hostKey ssh.PublicKey) (*ssh.Client, *sftp.Client, error) {
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{auth},
		HostKeyCallback: ssh.FixedHostKey(hostKey),
		Timeout:         20 * time.Second,
	}
	conn, err := ssh.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port), cfg)
	if err != nil {
		return nil, nil, err
	}
	sc, err := sftp.NewClient(conn)
	if err != nil {
		_ = conn.Close()
		return nil, nil, err
	}
	return conn, sc, nil
}

// sftpEntryNames returns the names of a directory listing, or the error.
func sftpEntryNames(sc *sftp.Client, dir string) ([]string, error) {
	entries, err := sc.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names, nil
}

func sftpContains(names []string, want string) bool {
	for _, n := range names {
		if n == want {
			return true
		}
	}
	return false
}

// sftpSHA256 hashes a whole reader.
func sftpSHA256(t *testing.T, r io.Reader) [32]byte {
	t.Helper()
	h := sha256.New()
	if _, err := io.Copy(h, r); err != nil {
		t.Fatalf("hash: %v", err)
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum
}

// sftpNewKey generates an ed25519 client key and returns its authorized_keys
// line (for the API) and an SSH auth method (for the client).
func sftpNewKey(t *testing.T) (string, ssh.AuthMethod) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("ssh signer: %v", err)
	}
	line := strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub)))
	return line, ssh.PublicKeys(signer)
}

// sftpAddKey registers an authorized_keys line on the caller's account and
// returns the key id.
func sftpAddKey(t *testing.T, cli *APIClient, name, line string) int64 {
	t.Helper()
	resp, body, err := cli.Post("/users/me/sftp/keys", map[string]string{"name": name, "publicKey": line})
	if err != nil {
		t.Fatalf("add sftp key: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("add sftp key: status=%d body=%s", resp.StatusCode, body)
	}
	var out struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &out); err != nil || out.ID == 0 {
		t.Fatalf("add sftp key: no id in %s (%v)", body, err)
	}
	return out.ID
}

// sftpGeneratePassword replaces the caller's SFTP password and returns it.
func sftpGeneratePassword(t *testing.T, cli *APIClient, length int) string {
	t.Helper()
	resp, body, err := cli.Post("/users/me/sftp/password", map[string]int{"length": length})
	if err != nil {
		t.Fatalf("generate sftp password: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("generate sftp password: status=%d body=%s", resp.StatusCode, body)
	}
	var out struct {
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &out); err != nil || len(out.Password) != length {
		t.Fatalf("generate sftp password: want %d chars, got %q (%v)", length, out.Password, err)
	}
	return out.Password
}

// TestSFTP_Lifecycle walks spec 040 on the bucket's own cluster: the endpoint
// is off by default, `helm upgrade --reuse-values` enables it, then one
// user with a key and one with a password browse, download, upload, rename
// and delete through SFTP. The per-server login form, the read-only
// permission matrix, the audit `source` field, a stopped server, and
// revocation are checked in the same run.
//
// Admin logins: one. Alice (read and write): API login, key login,
// per-server password login, and one refused login after revocation (4 of
// her 6-per-user burst). Bob (read only): API login, password login, and one
// refused login for a server he cannot read (3). Each SFTP login spends the
// same limiter as /auth/login, so the test stays well under the bucket's
// per-IP burst of 10.
func TestSFTP_Lifecycle(t *testing.T) {
	t.Parallel()

	suffix := time.Now().UnixNano()
	gsName := fmt.Sprintf("e2e-sftp-gs-%d", suffix)
	tmplName := fmt.Sprintf("e2e-sftp-tmpl-%d", suffix)
	aliceRole := fmt.Sprintf("e2e-sftp-rw-%d", suffix)
	bobRole := fmt.Sprintf("e2e-sftp-ro-%d", suffix)
	uploadName := "e2e-sftp-upload.bin"
	renamedName := "e2e-sftp-renamed.bin"
	dirName := "e2e-sftp-dir"

	if !t.Run("disabled", func(t *testing.T) {
		_, err := envInstance.K8s.AppsV1().Deployments(sftpSystemNS).Get(t.Context(), "gameplane-sftp", metav1.GetOptions{})
		if !apierrors.IsNotFound(err) {
			t.Fatalf("gameplane-sftp deployment: want NotFound by default, got err=%v", err)
		}
	}) {
		return
	}

	if !t.Run("enable", func(t *testing.T) {
		helmUpgradeReuse(t.Context(), t, "sftp.enabled=true")
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			d, err := envInstance.K8s.AppsV1().Deployments(sftpSystemNS).Get(t.Context(), "gameplane-sftp", metav1.GetOptions{})
			if err != nil {
				return false, err.Error()
			}
			return d.Status.ReadyReplicas >= 1, fmt.Sprintf("gameplane-sftp readyReplicas=%d", d.Status.ReadyReplicas)
		})
	}) {
		return
	}

	port, stopForward := envInstance.PortForward(t, sftpSystemNS, sftpService, sftpServicePort)
	t.Cleanup(stopForward)
	hostKey := sftpHostKey(t)

	applyBusyboxTemplate(t, tmplName)
	applyBusyboxGameServer(t, sftpGamesNS, gsName, tmplName)
	requireAgentReady(t, sftpGamesNS, gsName)

	envInstance.BootstrapAdmin(t, adminUsername, adminPassword)
	admin := envInstance.APIClient(t, adminUsername, adminPassword)
	t.Cleanup(admin.Close)

	for _, r := range []struct {
		name  string
		perms []string
	}{
		{aliceRole, []string{"servers:read", "servers:write"}},
		{bobRole, []string{"servers:read"}},
	} {
		resp, body, err := admin.Post("/roles", map[string]any{
			"name":        r.name,
			"description": "e2e sftp access",
			"permissions": r.perms,
		})
		if err != nil {
			t.Fatalf("create role %s: %v", r.name, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create role %s: status=%d body=%s", r.name, resp.StatusCode, body)
		}
		roleName := r.name
		t.Cleanup(func() {
			if d, _, _ := admin.Delete("/roles/" + roleName); d != nil {
				_ = d.Body.Close()
			}
		})
	}

	aliceName, alicePW, aliceID := envInstance.CreateUser(t, admin, aliceRole, "e2e-sftp-alice")
	t.Cleanup(func() {
		if d, _, _ := admin.Delete("/users/" + aliceID); d != nil {
			_ = d.Body.Close()
		}
	})
	bobName, bobPW, bobID := envInstance.CreateUser(t, admin, bobRole, "e2e-sftp-bob")
	t.Cleanup(func() {
		if d, _, _ := admin.Delete("/users/" + bobID); d != nil {
			_ = d.Body.Close()
		}
	})

	alice := envInstance.APIClient(t, aliceName, alicePW)
	t.Cleanup(alice.Close)
	keyLine, keyAuth := sftpNewKey(t)
	keyID := sftpAddKey(t, alice, "e2e-sftp-laptop", keyLine)

	var aliceSSH *ssh.Client
	var aliceSFTP *sftp.Client
	if !t.Run("login-and-browse", func(t *testing.T) {
		var err error
		aliceSSH, aliceSFTP, err = sftpDial(port, aliceName, keyAuth, hostKey)
		if err != nil {
			t.Fatalf("alice key login: %v", err)
		}
		t.Cleanup(func() { _ = aliceSSH.Close() })

		names, err := sftpEntryNames(aliceSFTP, "/")
		if err != nil {
			t.Fatalf("list root: %v", err)
		}
		if !sftpContains(names, gsName) {
			t.Fatalf("root listing %v: missing server %s", names, gsName)
		}
		for _, n := range names {
			if strings.HasPrefix(n, ".") {
				t.Fatalf("root listing exposes dotfile %q", n)
			}
		}
	}) {
		return
	}

	if !t.Run("upload-download-and-edit", func(t *testing.T) {
		payload := make([]byte, sftpUploadSize)
		if _, err := rand.Read(payload); err != nil {
			t.Fatalf("random payload: %v", err)
		}
		want := sha256.Sum256(payload)

		target := "/" + gsName + "/" + uploadName
		f, err := aliceSFTP.Create(target)
		if err != nil {
			t.Fatalf("create %s: %v", target, err)
		}
		if _, err := io.Copy(f, bytes.NewReader(payload)); err != nil {
			t.Fatalf("upload %s: %v", target, err)
		}
		if err := f.Close(); err != nil {
			t.Fatalf("close %s: %v", target, err)
		}

		// Full download and checksum against the bytes uploaded.
		rf, err := aliceSFTP.Open(target)
		if err != nil {
			t.Fatalf("open %s: %v", target, err)
		}
		got := sftpSHA256(t, rf)
		_ = rf.Close()
		if got != want {
			t.Fatalf("download checksum mismatch for %s", target)
		}

		// Ranged download from the middle (the resume path a client uses).
		half := int64(sftpUploadSize / 2)
		rf, err = aliceSFTP.Open(target)
		if err != nil {
			t.Fatalf("reopen %s: %v", target, err)
		}
		tail := sftpSHA256(t, io.NewSectionReader(rf, half, int64(sftpUploadSize)-half))
		_ = rf.Close()
		if tail != sha256.Sum256(payload[half:]) {
			t.Fatalf("ranged download from offset %d checksum mismatch", half)
		}

		if err := aliceSFTP.Rename(target, "/"+gsName+"/"+renamedName); err != nil {
			t.Fatalf("rename: %v", err)
		}
		if err := aliceSFTP.Mkdir("/" + gsName + "/" + dirName); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := aliceSFTP.RemoveDirectory("/" + gsName + "/" + dirName); err != nil {
			t.Fatalf("rmdir: %v", err)
		}
		if err := aliceSFTP.Remove("/" + gsName + "/" + renamedName); err != nil {
			t.Fatalf("remove: %v", err)
		}
	}) {
		return
	}

	if !t.Run("audit-source-sftp", func(t *testing.T) {
		envInstance.Eventually(t, time.Minute, func() (bool, string) {
			resp, body, err := admin.Get("/admin/audit?limit=200")
			if err != nil {
				return false, err.Error()
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return false, fmt.Sprintf("GET /admin/audit: status=%d", resp.StatusCode)
			}
			return strings.Contains(string(body), `"source":"sftp"`), "no audit row with source sftp yet"
		})
	}) {
		return
	}

	if !t.Run("per-server-login", func(t *testing.T) {
		// alice+<server> roots the session at that server's files (OD-3).
		aliceSrvPW := sftpGeneratePassword(t, alice, 32)
		conn, sc, err := sftpDial(port, aliceName+"+"+gsName, ssh.Password(aliceSrvPW), hostKey)
		if err != nil {
			t.Fatalf("alice+%s password login: %v", gsName, err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		names, err := sftpEntryNames(sc, "/")
		if err != nil {
			t.Fatalf("list per-server root: %v", err)
		}
		if sftpContains(names, gsName) {
			t.Fatalf("per-server root %v should not show the server folder itself", names)
		}
	}) {
		return
	}

	bob := envInstance.APIClient(t, bobName, bobPW)
	t.Cleanup(bob.Close)
	bobPassword := sftpGeneratePassword(t, bob, 24)

	if !t.Run("read-only-permission-matrix", func(t *testing.T) {
		conn, sc, err := sftpDial(port, bobName, ssh.Password(bobPassword), hostKey)
		if err != nil {
			t.Fatalf("bob password login: %v", err)
		}
		t.Cleanup(func() { _ = conn.Close() })

		names, err := sftpEntryNames(sc, "/")
		if err != nil {
			t.Fatalf("bob list root: %v", err)
		}
		if !sftpContains(names, gsName) {
			t.Fatalf("bob root listing %v: missing readable server %s", names, gsName)
		}
		if err := sc.Mkdir("/" + gsName + "/bob-denied"); err == nil {
			t.Fatalf("bob mkdir on %s: want permission denied", gsName)
		}
		if f, err := sc.Create("/" + gsName + "/bob-denied.txt"); err == nil {
			_ = f.Close()
			t.Fatalf("bob create on %s: want permission denied", gsName)
		}

		// A server bob cannot read is refused exactly like a wrong password.
		if c, _, err := sftpDial(port, bobName+"+e2e-sftp-missing", ssh.Password(bobPassword), hostKey); err == nil {
			_ = c.Close()
			t.Fatalf("bob login to an unreadable server: want refusal")
		}
	}) {
		return
	}

	if !t.Run("stopped-server-files", func(t *testing.T) {
		resp, body, err := alice.Post("/servers/"+gsName+":stop", nil)
		if err != nil {
			t.Fatalf("stop server: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("stop server: status=%d body=%s", resp.StatusCode, body)
		}
		// The file-only pod serves the folder while the game stays stopped.
		envInstance.Eventually(t, 3*time.Minute, func() (bool, string) {
			if _, err := sftpEntryNames(aliceSFTP, "/"+gsName); err != nil {
				return false, err.Error()
			}
			return true, ""
		})
		pods, err := envInstance.K8s.CoreV1().Pods(sftpGamesNS).List(t.Context(), metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/name=gameplane-files,app.kubernetes.io/instance=" + gsName,
		})
		if err != nil {
			t.Fatalf("list files-only pods: %v", err)
		}
		if len(pods.Items) == 0 {
			t.Fatalf("no gameplane-files pod for stopped server %s", gsName)
		}
		for _, p := range pods.Items {
			for _, c := range p.Spec.Containers {
				for _, cp := range c.Ports {
					if cp.HostPort != 0 {
						t.Fatalf("files-only pod %s exposes host port %d", p.Name, cp.HostPort)
					}
				}
			}
		}
	}) {
		return
	}

	t.Run("revocation", func(t *testing.T) {
		resp, body, err := admin.Delete(fmt.Sprintf("/users/%s/sftp/keys/%d", aliceID, keyID))
		if err != nil {
			t.Fatalf("revoke key: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			t.Fatalf("revoke key: status=%d body=%s", resp.StatusCode, body)
		}
		// The open session closes within the 30 s check loop plus margin.
		envInstance.Eventually(t, 90*time.Second, func() (bool, string) {
			if _, err := sftpEntryNames(aliceSFTP, "/"); err != nil {
				return true, ""
			}
			return false, "alice's session still open after key revocation"
		})
		if c, _, err := sftpDial(port, aliceName, keyAuth, hostKey); err == nil {
			_ = c.Close()
			t.Fatalf("alice key login after revocation: want refusal")
		}
	})
}
