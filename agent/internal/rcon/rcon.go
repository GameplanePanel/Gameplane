// Package rcon implements the Valve/Source RCON protocol used by
// Minecraft, Source engine games, CS servers, and many others.
//
// Protocol reference: https://developer.valvesoftware.com/wiki/Source_RCON_Protocol
//
// Packet layout (little-endian):
//
//	int32 size    // bytes that follow (excluding itself)
//	int32 id      // request id, echoed in response
//	int32 type    // 3=AUTH, 2=EXEC_CMD or AUTH_RESPONSE, 0=RESPONSE_VALUE
//	string body   // null-terminated ASCII
//	byte  pad     // extra null
//
// An authentication failure responds with id=-1. Large command
// responses are split across multiple RESPONSE_VALUE packets; we
// assemble them with a short grace-window read loop instead of the
// Valve-documented "empty-cmd sentinel" trick, which Minecraft's RCON
// doesn't tolerate (see the comment on Exec's read loop).
//
// Every client in this package dials the address configured by
// --rcon-host/--rcon-port, which the game module declares and which is
// expected to stay pod-local (typically 127.0.0.1, the sidecar's own
// pod). The WebSocket and Satisfactory clients additionally enforce
// this: WebSocket routes its dial through netguard.IsAllowed, and
// Satisfactory only relaxes TLS verification when the configured host
// actually resolves to loopback.
package rcon

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	typeAuth         = 3
	typeExecCmd      = 2
	typeAuthResponse = 2
	typeRespValue    = 0

	// minPacketSize / maxPacketSize bound the wire size field of an
	// incoming reply packet (id + type + body + 2 trailing nulls).
	// Minecraft chunks a long reply at up to 4096 *characters* (size up
	// to 4106); multi-byte UTF-8 text (accents, CJK, emoji in player or
	// ban-reason strings) can inflate a chunk to up to 4 bytes/character,
	// so the bound allows a full 4096-character chunk at its worst-case
	// byte length (F-104).
	minPacketSize = 10
	maxPacketSize = 4*4096 + 10 // 16394

	// sourceMaxResponseBytes permits large administrative listings while
	// bounding the aggregate allocation across otherwise valid fragments.
	// Status collection applies its separate, smaller 64-KiB acceptance cap.
	sourceMaxResponseBytes = 8 * 1024 * 1024
)

// PassFn resolves the RCON password on demand (allowing rotation without restart).
type PassFn func() (string, error)

// PasswordFromFile returns a password resolver that reads from disk
// on each call (so rotated Secrets are picked up without a restart).
func PasswordFromFile(path string) PassFn {
	// Normalize the path at creation time to prevent directory traversal.
	cleanPath := filepath.Clean(path)
	return func() (string, error) {
		if cleanPath == "" || cleanPath == "." {
			return "", errors.New("no rcon password file configured")
		}
		b, err := os.ReadFile(cleanPath)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(b)), nil
	}
}

// Client is a lazy, auto-reconnecting RCON client. It's safe for use
// from multiple goroutines; all ops are serialized on a single conn.
type Client struct {
	addr     string
	password PassFn

	// execDeadline bounds a single command exchange (see Exec).
	execDeadline time.Duration
	// authDeadline separately bounds authentication, including all preamble packets.
	authDeadline time.Duration

	// authFailureCooldown bounds how long ensureLocked refuses to re-dial
	// after an explicit AUTH_RESPONSE rejection (id=-1). A struct field
	// (not a package const) so tests can shrink it — mirrors
	// websocket.go's/battleye.go's identically-named field. Without this,
	// pollers that call Exec on a fixed timer (heartbeat, players) would
	// re-dial and resend a known-bad password every tick, which on
	// Source-engine games risks tripping sv_rcon_maxfailures and getting
	// the agent temporarily banned from RCON.
	authFailureCooldown time.Duration

	mu     sync.Mutex
	conn   net.Conn
	nextID uint32

	// lastAuthFailure records when ensureLocked last saw an explicit auth
	// rejection, so a later call can back off instead of re-dialing with a
	// password it already knows is wrong.
	lastAuthFailure time.Time
}

// defaultExecDeadline bounds a command's request/response exchange.
// ensureLocked clears the auth deadline on success, so without a fresh
// per-call deadline a wedged server would block readPacket forever while
// holding c.mu — stalling every caller that shares the client
// (heartbeat, players, quiesce, console).
const defaultExecDeadline = 30 * time.Second

const defaultSourceAuthDeadline = 10 * time.Second

// defaultAuthFailureCooldown bounds how long ensureLocked backs off after
// an explicit auth rejection before it will dial again. Mirrors
// websocket.go's defaultWebSocketAuthFailureCooldown — same value, same
// rationale (see ensureLocked).
const defaultAuthFailureCooldown = 15 * time.Second

// responseGrace bounds how long Exec waits for additional reply fragments
// after the first one arrives. Servers may split large replies across packets
// and keep the connection open after answering, so without this bound Exec
// would block on the full execDeadline after every command. It only takes
// effect once a reply packet has been read.
const responseGrace = 400 * time.Millisecond

// New returns a new lazy RCON client for the given host and port.
func New(host string, port int, pw PassFn) *Client {
	return &Client{
		addr:                net.JoinHostPort(host, fmt.Sprint(port)),
		password:            pw,
		execDeadline:        defaultExecDeadline,
		authDeadline:        defaultSourceAuthDeadline,
		authFailureCooldown: defaultAuthFailureCooldown,
	}
}

// Exec runs one RCON command and returns the concatenated response.
func (c *Client) Exec(cmd string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.ensureLocked(); err != nil {
		return "", err
	}

	// Bound the whole exchange. Capture the conn locally: dropLocked may
	// nil out c.conn on error, but resetting the deadline on the (now
	// closed) connection is harmless.
	conn := c.conn
	d := c.execDeadline
	if d <= 0 {
		d = defaultExecDeadline
	}
	exchangeDeadline := time.Now().Add(d)
	_ = conn.SetDeadline(exchangeDeadline)
	defer func() { _ = conn.SetDeadline(time.Time{}) }()

	reqID := c.allocID()
	if err := c.writePacket(reqID, typeExecCmd, cmd); err != nil {
		c.dropLocked()
		return "", err
	}

	// We deliberately do NOT use the Valve "empty-cmd sentinel" trick (send an
	// extra empty RESPONSE_VALUE and treat its echo as the end-of-reply
	// marker). Minecraft's RCON chokes on that extra packet and tears the
	// connection down ("Thread RCON Client shutting down") before — or as — it
	// answers, so the real reply is lost to the close/RST and every command
	// surfaced as "EOF". Instead we read the reply packets (which all echo
	// reqID) and bound the wait for any trailing fragments with a short grace
	// window: a single-packet reply returns as soon as the grace
	// elapses or the server closes; a fragmented Source reply keeps extending
	// the window as each fragment arrives, never beyond exchangeDeadline.
	var out bytes.Buffer
	gotResponse := false
	for {
		if !time.Now().Before(exchangeDeadline) {
			c.dropLocked()
			return "", fmt.Errorf("rcon: command exchange deadline exceeded: %w", os.ErrDeadlineExceeded)
		}
		id, _, body, err := c.readPacket()
		if err != nil {
			if !time.Now().Before(exchangeDeadline) {
				// Reaching the hard deadline is not evidence of a complete
				// response. Discard its partial text and any queued fragments.
				c.dropLocked()
				return "", fmt.Errorf("rcon: command exchange deadline exceeded: %w", os.ErrDeadlineExceeded)
			}
			if gotResponse {
				// We already hold the reply. A timeout means the grace window
				// elapsed on a still-healthy socket — keep it for reuse. Any
				// other error (EOF/RST) means the server closed after
				// answering — drop it so the
				// next call re-dials. Either way the reply is complete, so
				// return it instead of discarding it.
				if !isTimeout(err) {
					c.dropLocked()
				}
				return out.String(), nil
			}
			c.dropLocked()
			return "", fmt.Errorf("rcon exec %q: %w", cmd, err)
		}
		// Source/Minecraft echo the request id in every reply fragment; ignore
		// anything else.
		if id == reqID {
			if len(body) > sourceMaxResponseBytes-out.Len() {
				c.dropLocked()
				return "", fmt.Errorf("rcon: response exceeded limit of %d bytes", sourceMaxResponseBytes)
			}
			out.WriteString(body)
			gotResponse = true
			readDeadline := time.Now().Add(responseGrace)
			if readDeadline.After(exchangeDeadline) {
				readDeadline = exchangeDeadline
			}
			_ = conn.SetReadDeadline(readDeadline)
		}
	}
}

// isTimeout reports whether err is a network timeout (the grace/exec deadline
// firing) as opposed to a hard connection error like EOF or a reset.
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// Close shuts down the underlying connection.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn != nil {
		err := c.conn.Close()
		c.conn = nil
		return err
	}
	return nil
}

func (c *Client) ensureLocked() error {
	if c.conn != nil {
		return nil
	}

	// Back off after a recent explicit auth rejection instead of
	// re-dialing (and resending the same known-bad password) on every
	// call. Callers like the heartbeat and players pollers call Exec on a
	// fixed timer — mirrors websocket.go's/battleye.go's identical guard
	// for the identical reason. The cooldown expires on its own so a
	// corrected password is picked back up without a process restart.
	if cooldown := c.authFailureCooldown; !c.lastAuthFailure.IsZero() && cooldown > 0 && time.Since(c.lastAuthFailure) < cooldown {
		return fmt.Errorf("rcon: %w (cached, retrying dial after cooldown)", ErrAuth)
	}

	pw, err := c.password()
	if err != nil {
		return err
	}
	dialer := &net.Dialer{Timeout: 5 * time.Second}
	conn, err := dialer.DialContext(context.Background(), "tcp", c.addr)
	if err != nil {
		return err
	}
	c.conn = conn
	authBudget := c.authDeadline
	if authBudget <= 0 {
		authBudget = defaultSourceAuthDeadline
	}
	authDeadline := time.Now().Add(authBudget)
	_ = conn.SetDeadline(authDeadline)

	authID := c.allocID()
	if err := c.writePacket(authID, typeAuth, pw); err != nil {
		c.dropLocked()
		return err
	}
	// Server MAY send an empty RESPONSE_VALUE before AUTH_RESPONSE.
	for {
		if !time.Now().Before(authDeadline) {
			c.dropLocked()
			return fmt.Errorf("rcon: authentication deadline exceeded: %w", os.ErrDeadlineExceeded)
		}
		id, kind, _, err := c.readPacket()
		if err != nil {
			c.dropLocked()
			return err
		}
		if kind == typeAuthResponse {
			if id != authID {
				c.dropLocked()
				c.lastAuthFailure = time.Now()
				return fmt.Errorf("rcon: %w", ErrAuth)
			}
			_ = conn.SetDeadline(time.Time{})
			c.lastAuthFailure = time.Time{}
			return nil
		}
	}
}

func (c *Client) dropLocked() {
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

func (c *Client) allocID() uint32 {
	c.nextID++
	if c.nextID == 0 {
		c.nextID = 1
	}
	return c.nextID
}

func (c *Client) writePacket(id, kind uint32, body string) error {
	payload := make([]byte, 0, 14+len(body))
	payload = binary.LittleEndian.AppendUint32(payload, id)
	payload = binary.LittleEndian.AppendUint32(payload, kind)
	payload = append(payload, body...)
	payload = append(payload, 0, 0)

	n := len(payload)
	if n > 4096 {
		return fmt.Errorf("rcon: payload too large (%d bytes)", n)
	}
	// Vanilla Minecraft 1.21.4 reads a complete RCON frame in one socket
	// read and closes on a header-only read. Submit the unchanged header and
	// payload together instead of forcing an avoidable split across writes.
	packet := make([]byte, 4+n)
	binary.LittleEndian.PutUint32(packet, uint32(n))
	copy(packet[4:], payload)
	written, err := c.conn.Write(packet)
	if err == nil && written != len(packet) {
		// A partial command may already have reached the server. Fail without
		// resending any bytes; the caller drops this ambiguous connection.
		err = io.ErrShortWrite
	}
	return err
}

func (c *Client) readPacket() (id, kind uint32, body string, err error) {
	var hdr [4]byte
	if _, err = io.ReadFull(c.conn, hdr[:]); err != nil {
		return
	}
	size := binary.LittleEndian.Uint32(hdr[:])
	if size < minPacketSize || size > maxPacketSize {
		err = fmt.Errorf("rcon: malformed packet size %d", size)
		return
	}
	buf := make([]byte, size)
	if _, err = io.ReadFull(c.conn, buf); err != nil {
		return
	}
	id = binary.LittleEndian.Uint32(buf[0:4])
	kind = binary.LittleEndian.Uint32(buf[4:8])
	// body is null-terminated; strip the two trailing nulls
	end := len(buf) - 2
	if end < 8 {
		end = 8
	}
	body = string(buf[8:end])
	return
}

// ErrDisabled marks RCON as deliberately not configured for this game
// (GameTemplate declares no RCON protocol). Callers degrade gracefully
// — players reports an unknown count, moderation reports unsupported —
// instead of treating it as an upstream outage.
var ErrDisabled = errors.New("rcon disabled for this game")

// Disabled returns an Exec-er whose every call fails with ErrDisabled.
type Disabled struct{}

// Exec always returns ErrDisabled.
func (Disabled) Exec(string) (string, error) { return "", ErrDisabled }
