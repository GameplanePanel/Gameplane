// Package auth handles user authentication (local + OIDC) and session
// lifecycle for the Gameplane API.
//
// Passwords are hashed with argon2id using parameters aligned with
// OWASP 2023 guidance for responsive dashboards: 64 MiB, t=3, p=2.
// (The OWASP table suggests p=4 for high-core servers; we pick p=2 to
// stay responsive on the single/dual-core nodes that small k3s installs
// run on — see argonThreads comment.) Encoded format follows the PHC
// string scheme so hashes remain verifiable if we later bump params.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/argon2"
	"golang.org/x/sync/semaphore"
)

// argonTime / argonMemory are variables (not consts) solely so tests can
// dial the hashing cost down via SetFastHashParams — production code
// never mutates them. VerifyPassword decodes its parameters from the PHC
// hash string, so verification is unaffected by the values here.
var (
	argonTime   uint32 = 3
	argonMemory uint32 = 64 * 1024

	// argonSem bounds concurrent argon2 operations to argonConcurrency
	// simultaneous hashes/verifies. Each operation allocates a 64 MiB block
	// that turns into garbage as soon as it returns; the semaphore bounds the
	// LIVE blocks, and api/cmd's soft memory limit (derived from the cgroup
	// limit) makes the GC reclaim the garbage before the 256 MiB container
	// limit is reached. One slot keeps live argon memory at 64 MiB: a burst of
	// concurrent failed logins previously OOM-killed the API with two slots.
	// The semaphore is context-aware so client disconnects abort pending
	// operations.
	argonSem = semaphore.NewWeighted(argonConcurrency)
)

// maxArgonWaiters bounds how many callers may queue for argonSem. Beyond it a
// caller fails fast with ErrPasswordBusy instead of waiting out the request
// timeout behind a flood.
const maxArgonWaiters = 64

// ErrPasswordBusy reports that too many password operations are already
// queued; callers answer with 503 and a Retry-After header.
var ErrPasswordBusy = errors.New("password hashing busy")

var argonWaiters atomic.Int64

// acquireArgon takes the argon slot. A cancelled context returns its error
// first; a full wait queue returns ErrPasswordBusy.
func acquireArgon(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if argonSem.TryAcquire(1) {
		return nil
	}
	if argonWaiters.Add(1) > maxArgonWaiters {
		argonWaiters.Add(-1)
		return ErrPasswordBusy
	}
	defer argonWaiters.Add(-1)
	return argonSem.Acquire(ctx, 1)
}

// argonConcurrency is the number of argon2 operations allowed to run at once.
const argonConcurrency = 1

const (
	// 2 threads matches OWASP 2023 guidance and stays responsive on
	// single- and dual-core nodes (common in small k3s deployments).
	argonThreads = 2
	argonKeyLen  = 32
	argonSaltLen = 16
)

// MinPasswordLen is the floor enforced anywhere a user picks or resets a
// password — HTTP user CRUD, password reset, and the bootstrap-admin CLI.
// Short enough to not block legitimate use, long enough that argon2id
// makes brute force impractical. Matches the design.fig reset-modal copy.
const MinPasswordLen = 12

// dummyHash is a valid argon2id hash of a random password nobody knows.
// It exists so the login handler can run a full VerifyPassword even on
// not-found / OIDC-only accounts, keeping the response-time envelope
// constant. Without it, a "user does not exist" reply returns in µs
// while a real wrong-password reply takes ~argon2 cost, and attackers
// enumerate valid usernames by timing.
//
// Computed lazily (first use, not package init) so every binary that
// merely links this package — most notably test binaries that lower the
// cost via SetFastHashParams — doesn't pay a full-cost 64 MiB hash at
// startup.
var dummyHash = sync.OnceValue(computeDummyHash)

// computeDummyHash builds the hash behind dummyHash.
func computeDummyHash() string {
	r := make([]byte, 32)
	if _, err := rand.Read(r); err != nil {
		// rand.Read only fails if the OS CSPRNG is broken; in that case
		// we can't safely serve auth anyway.
		panic("crypto/rand: " + err.Error())
	}
	// Bypass the waiter cap: this runs lazily on the first unknown-user
	// login, which may be during a flood, and a cached panic would break the
	// deny path for good. Waiting on the slot here is bounded by one hash.
	if err := argonSem.Acquire(context.Background(), 1); err != nil {
		panic("init dummy hash: " + err.Error())
	}
	defer argonSem.Release(1)
	h, err := hashLocked(string(r))
	if err != nil {
		panic("init dummy hash: " + err.Error())
	}
	return h
}

// VerifyDummy runs an argon2id compare against a hash nobody can match.
// Use it on login paths where the outcome is already "deny" but you
// want the time envelope to match a real verify. pw is discarded.
// It returns ErrPasswordBusy (or the context error) when no slot was obtained.
func VerifyDummy(ctx context.Context, pw string) error {
	_, err := VerifyPassword(ctx, pw, dummyHash())
	return err
}

// HashPassword hashes a password using argon2id.
func HashPassword(ctx context.Context, pw string) (string, error) {
	if err := acquireArgon(ctx); err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	defer argonSem.Release(1)
	return hashLocked(pw)
}

// hashLocked hashes pw; the caller must hold the argon2 slot.
func hashLocked(pw string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key),
	), nil
}

// VerifyPassword verifies a password against an argon2id hash.
func VerifyPassword(ctx context.Context, pw, encoded string) (bool, error) {
	// Acquire semaphore before parsing to ensure timing parity on all paths.
	if err := acquireArgon(ctx); err != nil {
		return false, fmt.Errorf("verify password: %w", err)
	}
	defer argonSem.Release(1)

	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, errors.New("unsupported hash format")
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return false, err
	}
	var mem, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &mem, &time, &threads); err != nil {
		return false, err
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false, err
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false, err
	}
	if len(want) != argonKeyLen {
		return false, fmt.Errorf("invalid hash length")
	}
	got := argon2.IDKey([]byte(pw), salt, time, mem, threads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}
