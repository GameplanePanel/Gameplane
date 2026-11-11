package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GameplanePanel/gameplane/api/internal/db"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

type runtimeKey struct{}

type sharedAuth struct {
	store     *db.Store
	deviceKey []byte
}

func issueDeviceCookie(ctx context.Context, username string) string {
	state, _ := ctx.Value(runtimeKey{}).(*sharedAuth)
	if state == nil {
		return loginDeviceToken(username, loginDeviceKey)
	}
	expires := strconv.FormatInt(time.Now().Add(loginDeviceTTL*time.Second).Unix(), 10)
	return expires + "." + loginDeviceToken(username+"\x00"+expires, state.deviceKey)
}

func validDeviceCookie(ctx context.Context, username, value string) bool {
	state, _ := ctx.Value(runtimeKey{}).(*sharedAuth)
	if state == nil {
		return hmac.Equal([]byte(value), []byte(loginDeviceToken(username, loginDeviceKey)))
	}
	expires, signature, ok := strings.Cut(value, ".")
	deadline, err := strconv.ParseInt(expires, 10, 64)
	return ok && err == nil && deadline > time.Now().Unix() && hmac.Equal([]byte(signature), []byte(loginDeviceToken(username+"\x00"+expires, state.deviceKey)))
}

// PostgresRuntime shares budgets and known-device signatures across all API replicas.
func PostgresRuntime(ctx context.Context, store *db.Store) (func(http.Handler) http.Handler, error) {
	candidate := make([]byte, 32)
	if _, err := rand.Read(candidate); err != nil {
		return nil, err
	}
	_, err := store.DB.ExecContext(ctx, `INSERT INTO api_runtime_keys (purpose,key_hex) VALUES ('login_device',?) ON CONFLICT (purpose) DO NOTHING`, hex.EncodeToString(candidate))
	if err != nil {
		return nil, fmt.Errorf("initialize login device key: %w", err)
	}
	var encoded string
	if err := store.DB.QueryRowContext(ctx, `SELECT key_hex FROM api_runtime_keys WHERE purpose='login_device'`).Scan(&encoded); err != nil {
		return nil, fmt.Errorf("read login device key: %w", err)
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("invalid login device key in database")
	}
	state := &sharedAuth{store: store, deviceKey: key}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), runtimeKey{}, state)))
		})
	}, nil
}

// check consumes a token using PostgreSQL's clock and row-level conflict handling.
// next_us is the database time at which all previously spent tokens refill.
// An exhausted bucket leaves its timestamp unchanged. Losing a database never
// creates a new local budget.
func (t *TokenBucket) check(r *http.Request, key string) (bool, error) {
	state, _ := r.Context().Value(runtimeKey{}).(*sharedAuth)
	if state == nil {
		return t.Allow(key), nil
	}
	sum := sha256.Sum256([]byte(key))
	step := int64(math.Round(float64(time.Second/time.Microsecond) / t.rate))
	var next int64
	err := state.store.DB.QueryRowContext(r.Context(), `
		INSERT INTO api_rate_buckets (budget, subject_hash, next_us)
		VALUES (?, ?, floor(extract(epoch FROM statement_timestamp())*1000000)::bigint + CAST(? AS bigint))
		ON CONFLICT (budget,subject_hash) DO UPDATE
		SET next_us=GREATEST(api_rate_buckets.next_us+CAST(? AS bigint), EXCLUDED.next_us)
		WHERE api_rate_buckets.next_us <= EXCLUDED.next_us+CAST(? AS bigint)
		RETURNING next_us`, t.name, hex.EncodeToString(sum[:]), step, step, (int64(t.burst)-2)*step).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (t *TokenBucket) requestBudget(w http.ResponseWriter, r *http.Request, key string) bool {
	ok, err := t.check(r, key)
	if err != nil {
		httperr.WriteMessage(w, r, messages.New("server.generic.503", nil))
		return false
	}
	if !ok {
		w.Header().Set("Retry-After", "30")
		httperr.WriteCode(w, r, http.StatusTooManyRequests, errors.New("too many requests"))
	}
	return ok
}
