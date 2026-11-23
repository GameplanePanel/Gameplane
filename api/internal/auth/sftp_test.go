package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

// sftpAuthorizedKey returns an authorized_keys line for a freshly generated key of the given kind.
func sftpAuthorizedKey(t *testing.T, kind string) (string, ssh.PublicKey) {
	t.Helper()
	var pub any
	var err error
	switch kind {
	case "ed25519":
		pub, _, err = ed25519.GenerateKey(rand.Reader)
	case "p256":
		var k *ecdsa.PrivateKey
		k, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if err == nil {
			pub = &k.PublicKey
		}
	case "p384":
		var k *ecdsa.PrivateKey
		k, err = ecdsa.GenerateKey(elliptic.P384(), rand.Reader)
		if err == nil {
			pub = &k.PublicKey
		}
	case "p521":
		var k *ecdsa.PrivateKey
		k, err = ecdsa.GenerateKey(elliptic.P521(), rand.Reader)
		if err == nil {
			pub = &k.PublicKey
		}
	case "rsa3072", "rsa2048":
		bits := 3072
		if kind == "rsa2048" {
			bits = 2048
		}
		var k *rsa.PrivateKey
		k, err = rsa.GenerateKey(rand.Reader, bits)
		if err == nil {
			pub = &k.PublicKey
		}
	default:
		t.Fatalf("unknown kind %q", kind)
	}
	if err != nil {
		t.Fatalf("generate %s: %v", kind, err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(sshPub))), sshPub
}

func TestParseSFTPPublicKey_Accepts(t *testing.T) {
	for _, kind := range []string{"ed25519", "p256", "p384", "p521", "rsa3072"} {
		t.Run(kind, func(t *testing.T) {
			line, sshPub := sftpAuthorizedKey(t, kind)
			parsed, err := ParseSFTPPublicKey(line + " laptop comment")
			if err != nil {
				t.Fatalf("ParseSFTPPublicKey: %v", err)
			}
			if parsed.Normalized != line {
				t.Fatalf("normalized = %q, want comment-free %q", parsed.Normalized, line)
			}
			if parsed.Type != sshPub.Type() {
				t.Fatalf("type = %q, want %q", parsed.Type, sshPub.Type())
			}
			if !strings.HasPrefix(parsed.Fingerprint, "SHA256:") || parsed.Fingerprint != ssh.FingerprintSHA256(sshPub) {
				t.Fatalf("fingerprint = %q", parsed.Fingerprint)
			}
		})
	}
}

func TestParseSFTPPublicKey_Rejects(t *testing.T) {
	ed, _ := sftpAuthorizedKey(t, "ed25519")
	weak, _ := sftpAuthorizedKey(t, "rsa2048")
	cases := map[string]string{
		"rsa 2048":  weak,
		"dsa":       "ssh-dss AAAAB3NzaC1kc3MAAACBAOfH junk",
		"sk key":    "sk-ssh-ed25519@openssh.com AAAAGnNrLXNzaC1lZDI1NTE5QG9wZW5zc2guY29tAAAAIM junk",
		"options":   `command="/bin/sh" ` + ed,
		"two lines": ed + "\n" + ed,
		"garbage":   "not a key at all",
		"empty":     "   ",
	}
	for name, line := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseSFTPPublicKey(line); err == nil {
				t.Fatalf("ParseSFTPPublicKey accepted %q", name)
			}
		})
	}
	if _, err := ParseSFTPPublicKey(weak); !errors.Is(err, ErrSFTPKeyWeak) {
		t.Fatalf("rsa 2048 error = %v, want ErrSFTPKeyWeak", err)
	}
}

func TestSFTPFingerprintForLogin(t *testing.T) {
	line, sshPub := sftpAuthorizedKey(t, "ed25519")
	fp, err := SFTPFingerprintForLogin(line)
	if err != nil {
		t.Fatalf("SFTPFingerprintForLogin: %v", err)
	}
	if fp != ssh.FingerprintSHA256(sshPub) {
		t.Fatalf("fingerprint = %q", fp)
	}
}

func TestGenerateSFTPPassword(t *testing.T) {
	for _, n := range []int{SFTPPasswordMinLen, SFTPPasswordMaxLen} {
		pw, err := GenerateSFTPPassword(n)
		if err != nil || len(pw) != n {
			t.Fatalf("GenerateSFTPPassword(%d) = %q, %v", n, pw, err)
		}
	}
	for _, n := range []int{SFTPPasswordMinLen - 1, SFTPPasswordMaxLen + 1} {
		if _, err := GenerateSFTPPassword(n); !errors.Is(err, ErrSFTPPasswordLen) {
			t.Fatalf("GenerateSFTPPassword(%d) error = %v, want ErrSFTPPasswordLen", n, err)
		}
	}
	seen := map[byte]bool{}
	for i := 0; i < 500; i++ {
		pw, err := GenerateSFTPPassword(SFTPPasswordMaxLen)
		if err != nil {
			t.Fatalf("GenerateSFTPPassword: %v", err)
		}
		for j := 0; j < len(pw); j++ {
			if pw[j] < '!' || pw[j] > '~' {
				t.Fatalf("password has non-printable or space byte %q", pw[j])
			}
			seen[pw[j]] = true
		}
	}
	if len(seen) != 94 {
		t.Fatalf("saw %d distinct characters, want 94", len(seen))
	}
}

// sftpUserID returns the id of a seeded user.
func sftpUserID(t *testing.T, s *SessionStore, username string) int64 {
	t.Helper()
	u, _, err := s.SFTPUserByName(context.Background(), username)
	if err != nil {
		t.Fatalf("SFTPUserByName(%s): %v", username, err)
	}
	return u.ID
}

func TestSFTPKeys_AddListDelete(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	seedUser(t, st, "bob", "hunter3", "admin")
	s := NewSessionStore(st)
	alice, bob := sftpUserID(t, s, "alice"), sftpUserID(t, s, "bob")

	line, _ := sftpAuthorizedKey(t, "ed25519")
	key, err := s.AddSFTPKey(ctx, alice, "  laptop  ", line)
	if err != nil {
		t.Fatalf("AddSFTPKey: %v", err)
	}
	if key.Name != "laptop" || key.PublicKey != line {
		t.Fatalf("stored key = %+v", key)
	}
	if _, err := s.AddSFTPKey(ctx, bob, "other", line); !errors.Is(err, ErrSFTPKeyDuplicate) {
		t.Fatalf("same key for other user error = %v, want ErrSFTPKeyDuplicate", err)
	}
	if _, err := s.AddSFTPKey(ctx, alice, "laptop", mustKey(t)); !errors.Is(err, ErrSFTPKeyNameInUse) {
		t.Fatalf("reused name error = %v, want ErrSFTPKeyNameInUse", err)
	}
	if _, err := s.AddSFTPKey(ctx, alice, "   ", mustKey(t)); !errors.Is(err, ErrSFTPKeyName) {
		t.Fatalf("empty name error = %v, want ErrSFTPKeyName", err)
	}
	if _, err := s.AddSFTPKey(ctx, alice, strings.Repeat("x", maxSFTPKeyNameLen+1), mustKey(t)); !errors.Is(err, ErrSFTPKeyName) {
		t.Fatalf("long name error = %v, want ErrSFTPKeyName", err)
	}

	keys, err := s.ListSFTPKeys(ctx, alice)
	if err != nil || len(keys) != 1 {
		t.Fatalf("ListSFTPKeys = %d keys, %v", len(keys), err)
	}

	// Deleting a key revokes sessions that used it.
	token, _, err := s.IssueSFTPSession(ctx, alice, "local", "key:"+fmt.Sprint(key.ID), 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	if err := s.DeleteSFTPKey(ctx, bob, key.ID); !errors.Is(err, ErrSFTPNotFound) {
		t.Fatalf("delete other user's key error = %v, want ErrSFTPNotFound", err)
	}
	if err := s.DeleteSFTPKey(ctx, alice, key.ID); err != nil {
		t.Fatalf("DeleteSFTPKey: %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, token); err == nil {
		t.Fatal("session of deleted key still valid")
	}
}

func TestSFTPKeys_Limit(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")
	for i := 0; i < SFTPMaxKeysPerUser; i++ {
		if _, err := s.AddSFTPKey(ctx, alice, "key"+fmt.Sprint(int64(i)), mustKey(t)); err != nil {
			t.Fatalf("AddSFTPKey %d: %v", i, err)
		}
	}
	if _, err := s.AddSFTPKey(ctx, alice, "one-more", mustKey(t)); !errors.Is(err, ErrSFTPKeyLimit) {
		t.Fatalf("21st key error = %v, want ErrSFTPKeyLimit", err)
	}
}

func TestSFTPPassword_GenerateVerifyRegenerateRevoke(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	SetFastHashParams(t)
	alice := sftpUserID(t, s, "alice")

	pw, err := s.GenerateAndStoreSFTPPassword(ctx, alice, SFTPPasswordMinLen)
	if err != nil {
		t.Fatalf("GenerateAndStoreSFTPPassword: %v", err)
	}
	hash, found, err := s.GetSFTPPasswordHash(ctx, alice)
	if err != nil || !found || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("stored hash found=%v err=%v", found, err)
	}
	if strings.Contains(hash, pw) {
		t.Fatal("hash contains the clear password")
	}
	if _, _, ok, err := s.VerifySFTPPassword(ctx, "alice", pw); err != nil || !ok {
		t.Fatalf("VerifySFTPPassword(current) ok=%v err=%v", ok, err)
	}
	if _, _, ok, _ := s.VerifySFTPPassword(ctx, "alice", pw+"x"); ok {
		t.Fatal("wrong password verified")
	}
	if _, _, ok, _ := s.VerifySFTPPassword(ctx, "nobody", pw); ok {
		t.Fatal("unknown user verified")
	}

	// Regenerating replaces the row: the old password stops working and only one row remains.
	pw2, err := s.GenerateAndStoreSFTPPassword(ctx, alice, SFTPPasswordMinLen)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if _, _, ok, _ := s.VerifySFTPPassword(ctx, "alice", pw); ok {
		t.Fatal("old password still verifies after regenerate")
	}
	if _, _, ok, err := s.VerifySFTPPassword(ctx, "alice", pw2); err != nil || !ok {
		t.Fatalf("new password ok=%v err=%v", ok, err)
	}
	var rows int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_passwords WHERE user_id = ?`, alice).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("password rows = %d, %v", rows, err)
	}

	if err := s.DeleteSFTPPassword(ctx, alice); err != nil {
		t.Fatalf("DeleteSFTPPassword: %v", err)
	}
	if _, _, ok, _ := s.VerifySFTPPassword(ctx, "alice", pw2); ok {
		t.Fatal("password verifies after revoke")
	}
	if err := s.DeleteSFTPPassword(ctx, alice); !errors.Is(err, ErrSFTPNotFound) {
		t.Fatalf("second delete error = %v, want ErrSFTPNotFound", err)
	}
}

func TestSFTPServiceTokens_FormatStorageLimitRevoke(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	s := NewSessionStore(st)

	tok, raw, err := s.CreateSFTPServiceToken(ctx, "local")
	if err != nil {
		t.Fatalf("CreateSFTPServiceToken: %v", err)
	}
	if !strings.HasPrefix(raw, SFTPServiceTokenPrefix) || len(raw) != len(SFTPServiceTokenPrefix)+sftpTokenBodyLen {
		t.Fatalf("token format = %q", raw)
	}
	var stored int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_service_tokens WHERE digest = ?`, raw).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("raw token stored as digest: rows=%d err=%v", stored, err)
	}
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_service_tokens WHERE digest = ?`, sessionDigest(raw)).Scan(&stored); err != nil || stored != 1 {
		t.Fatalf("digest not stored: rows=%d err=%v", stored, err)
	}
	id, cluster, err := s.AuthenticateSFTPService(ctx, raw)
	if err != nil || id != tok.ID || cluster != "local" {
		t.Fatalf("AuthenticateSFTPService = %d %q %v", id, cluster, err)
	}

	if _, _, err := s.CreateSFTPServiceToken(ctx, "local"); err != nil {
		t.Fatalf("second token: %v", err)
	}
	if _, _, err := s.CreateSFTPServiceToken(ctx, "local"); !errors.Is(err, ErrSFTPServiceTokenLimit) {
		t.Fatalf("third token error = %v, want ErrSFTPServiceTokenLimit", err)
	}

	if err := s.RevokeSFTPServiceToken(ctx, "local", tok.ID); err != nil {
		t.Fatalf("RevokeSFTPServiceToken: %v", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, raw); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("revoked token error = %v, want sql.ErrNoRows", err)
	}
	if _, _, err := s.AuthenticateSFTPService(ctx, "gpsftps_short"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("malformed token error = %v, want sql.ErrNoRows", err)
	}
}

func TestSFTPSessions_EpochRevokeAndCheck(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	token, expires, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	if !strings.HasPrefix(token, SFTPSessionTokenPrefix) || len(token) != len(SFTPSessionTokenPrefix)+sftpTokenBodyLen {
		t.Fatalf("session token format = %q", token)
	}
	if d := time.Until(expires); d < 14*time.Minute || d > 15*time.Minute+time.Second {
		t.Fatalf("expiry in %v, want about 15m", d)
	}
	u, sess, err := s.LookupSFTPSessionToken(ctx, token)
	if err != nil || u.Username != "alice" || sess.Cluster != "local" {
		t.Fatalf("LookupSFTPSessionToken = %v %+v", err, sess)
	}

	missing, err := s.CheckSFTPSessions(ctx, "local", []string{sessionDigest(token), "nope"})
	if err != nil || len(missing) != 1 || missing[0] != "nope" {
		t.Fatalf("CheckSFTPSessions(valid) = %v, %v", missing, err)
	}

	// A password reset bumps users.session_epoch: the session is no longer valid.
	if _, err := st.DB.ExecContext(ctx, `UPDATE users SET session_epoch = session_epoch + 1 WHERE id = ?`, alice); err != nil {
		t.Fatalf("bump epoch: %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, token); err == nil {
		t.Fatal("session valid after epoch bump")
	}
	revoked, err := s.CheckSFTPSessions(ctx, "local", []string{sessionDigest(token)})
	if err != nil || len(revoked) != 1 {
		t.Fatalf("CheckSFTPSessions(after bump) = %v, %v", revoked, err)
	}
}

func TestSFTPSessions_PasswordSessionsSurviveKeyDelete(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	key, err := s.AddSFTPKey(ctx, alice, "laptop", mustKey(t))
	if err != nil {
		t.Fatalf("AddSFTPKey: %v", err)
	}
	keyToken, _, err := s.IssueSFTPSession(ctx, alice, "local", "key:"+fmt.Sprint(key.ID), 0)
	if err != nil {
		t.Fatalf("issue key session: %v", err)
	}
	pwToken, _, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("issue password session: %v", err)
	}
	if err := s.DeleteSFTPKey(ctx, alice, key.ID); err != nil {
		t.Fatalf("DeleteSFTPKey: %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, keyToken); err == nil {
		t.Fatal("key session survived key delete")
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, pwToken); err != nil {
		t.Fatalf("password session revoked by key delete: %v", err)
	}

	// DeleteForUser (privilege change, user delete path) revokes every SFTP session.
	if err := s.DeleteForUser(ctx, alice); err != nil {
		t.Fatalf("DeleteForUser: %v", err)
	}
	if _, _, err := s.LookupSFTPSessionToken(ctx, pwToken); err == nil {
		t.Fatal("password session survived DeleteForUser")
	}
}

func TestSFTPSessions_SlideThrottledAndGC(t *testing.T) {
	ctx := context.Background()
	st := newAuthDB(t)
	seedUser(t, st, "alice", "hunter2", "admin")
	s := NewSessionStore(st)
	alice := sftpUserID(t, s, "alice")

	token, _, err := s.IssueSFTPSession(ctx, alice, "local", "password", 0)
	if err != nil {
		t.Fatalf("IssueSFTPSession: %v", err)
	}
	_, sess, err := s.LookupSFTPSessionToken(ctx, token)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	sentinel := "2099-01-01T00:00:00Z"
	setExpiry := func() {
		if _, err := st.DB.ExecContext(ctx, `UPDATE sftp_sessions SET expires_at = ? WHERE id = ?`, sentinel, sess.ID); err != nil {
			t.Fatalf("set expiry: %v", err)
		}
	}
	readExpiry := func() string {
		var v string
		if err := st.DB.QueryRowContext(ctx, `SELECT expires_at FROM sftp_sessions WHERE id = ?`, sess.ID).Scan(&v); err != nil {
			t.Fatalf("read expiry: %v", err)
		}
		return v
	}

	// First slide writes; a second slide inside the interval does not.
	setExpiry()
	sftpLastSlide.Delete(sess.ID)
	s.slideSFTPSession(ctx, sess.ID)
	if got := readExpiry(); got == sentinel {
		t.Fatal("first slide did not write expires_at")
	}
	setExpiry()
	s.slideSFTPSession(ctx, sess.ID)
	if got := readExpiry(); got != sentinel {
		t.Fatalf("slide within interval wrote expires_at = %q", got)
	}

	// Expired and revoked rows are swept by the hourly GC.
	if _, err := st.DB.ExecContext(ctx, `UPDATE sftp_sessions SET expires_at = ? WHERE id = ?`, "2000-01-01T00:00:00Z", sess.ID); err != nil {
		t.Fatalf("expire: %v", err)
	}
	s.gcOnce(ctx)
	var n int
	if err := st.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM sftp_sessions WHERE id = ?`, sess.ID).Scan(&n); err != nil || n != 0 {
		t.Fatalf("expired session rows after gc = %d, %v", n, err)
	}
}

// mustKey returns a fresh ed25519 authorized_keys line.
func mustKey(t *testing.T) string {
	t.Helper()
	line, _ := sftpAuthorizedKey(t, "ed25519")
	return line
}
