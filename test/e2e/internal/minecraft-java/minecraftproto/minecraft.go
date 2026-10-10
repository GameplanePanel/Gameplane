// Package minecraftproto implements the Minecraft: Java Edition wire protocol client
// for the e2e suite to prove that a Gameplane-managed Minecraft server is not
// merely "Running" in Kubernetes but genuinely playable: it answers a Server
// List Ping and accepts a login.
//
// It speaks just enough of the post-Netty protocol (Minecraft 1.7+):
//
//   - Ping  — Server List Ping (status): version name, protocol number, and
//     player counts, as JSON. Works regardless of online-mode.
//   - Login — completes the (offline-mode) login handshake and reports whether
//     the server answered with Login Success, demanded authentication
//     (Encryption Request => online-mode), or disconnected us.
//   - Hold  — sustained mode: logs in, then stays in the Configuration and Play
//     states for a fixed window, echoing keep-alives and measuring ping/pong
//     round trips. Packet ids are for protocol 769 (Minecraft 1.21.4) only.
//
// Ping and Login use only the Handshaking/Status/Login states, which is enough
// to confirm a bot can join and keeps the code stable across Minecraft
// versions — login-state packet IDs have not changed in years.
package minecraftproto

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"time"
)

// Status is the subset of the Server List Ping JSON we care about.
type Status struct {
	Version struct {
		Name     string `json:"name"`
		Protocol int    `json:"protocol"`
	} `json:"version"`
	Players struct {
		Max    int `json:"max"`
		Online int `json:"online"`
	} `json:"players"`
}

// Outcome is the result of a login attempt.
type Outcome int

const (
	// Success is the outcome when the server sent Login Success — a bot can join.
	Success Outcome = iota
	// NeedsAuth is the outcome when the server sent an Encryption Request,
	// i.e. it runs in online-mode and requires a real (Mojang-authenticated)
	// account.
	NeedsAuth
	// Disconnected is the outcome when the server refused the login
	// (Disconnect packet).
	Disconnected
)

// LoginResult carries the outcome plus a human-readable detail (the accepted
// username on success, or the server's reason otherwise).
type LoginResult struct {
	Outcome Outcome
	Detail  string
}

// Ping performs a Server List Ping against addr ("host:port") and returns the
// server's reported version/protocol/player counts.
func Ping(ctx context.Context, addr string) (*Status, error) {
	host, port, err := splitHostPort(addr)
	if err != nil {
		return nil, err
	}
	conn, err := dial(ctx, addr, 15*time.Second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	// Handshake with next state = 1 (status). Protocol version is ignored by
	// servers for status, so -1 is conventional.
	if _, err := conn.Write(handshake(-1, host, port, 1)); err != nil {
		return nil, fmt.Errorf("minecraft: write handshake: %w", err)
	}
	if _, err := conn.Write(buildPacket(0x00, nil)); err != nil { // Status Request
		return nil, fmt.Errorf("minecraft: write status request: %w", err)
	}

	rd := &reader{br: bufio.NewReader(conn)}
	id, payload, err := rd.readPacket()
	if err != nil {
		return nil, fmt.Errorf("minecraft: read status response: %w", err)
	}
	if id != 0x00 {
		return nil, fmt.Errorf("minecraft: unexpected status packet id 0x%02x", id)
	}
	raw, err := readString(bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("minecraft: read status json: %w", err)
	}
	var s Status
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return nil, fmt.Errorf("minecraft: parse status json: %w", err)
	}
	return &s, nil
}

// Login completes a login handshake against addr using the given protocol
// number (get it from Ping) and username, in offline mode (no encryption).
func Login(ctx context.Context, addr string, protocol int, username string) (*LoginResult, error) {
	if err := validUsername(username); err != nil {
		return nil, err
	}

	host, port, err := splitHostPort(addr)
	if err != nil {
		return nil, err
	}
	conn, err := dial(ctx, addr, 25*time.Second)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close() }()

	login, err := loginPackets(host, port, protocol, username)
	if err != nil {
		return nil, err
	}
	// The handshake and Login Start go out in a SINGLE write (see loginPackets).
	if _, err := conn.Write(login); err != nil {
		return nil, fmt.Errorf("minecraft: write login: %w", err)
	}

	res, _, err := awaitLoginResult(&reader{br: bufio.NewReader(conn)}, username)
	return res, err
}

// Connect pings addr and then attempts a login using the protocol the ping
// reported, returning both. It's the convenience the e2e test uses.
func Connect(ctx context.Context, addr, username string) (*Status, *LoginResult, error) {
	st, err := Ping(ctx, addr)
	if err != nil {
		return nil, nil, err
	}
	res, err := Login(ctx, addr, st.Version.Protocol, username)
	if err != nil {
		return st, nil, err
	}
	return st, res, nil
}

// validUsername rejects names Minecraft cannot read. Minecraft reads the login
// name with readUtf(16); a longer name makes the server throw DecoderException
// and drop the connection with the opaque "Failed to decode packet
// 'serverbound/minecraft:hello'". Fail loudly here instead.
func validUsername(username string) error {
	if len(username) > 16 {
		return fmt.Errorf("minecraft: username %q is %d characters; Minecraft allows at most 16",
			username, len(username))
	}
	return nil
}

// loginPackets builds the handshake (next state = login) followed by Login
// Start (name + player UUID; the UUID has been mandatory since 1.20.2). A real
// client sends them together, avoiding any partial-pipeline risk across the
// handshake->login state transition, so callers write the result in one go.
func loginPackets(host string, port uint16, protocol int, username string) ([]byte, error) {
	var ls bytes.Buffer
	writeString(&ls, username)
	uuid := make([]byte, 16)
	if _, err := rand.Read(uuid); err != nil {
		return nil, fmt.Errorf("minecraft: uuid: %w", err)
	}
	ls.Write(uuid)

	var login bytes.Buffer
	login.Write(handshake(int32(protocol), host, port, 2))
	login.Write(buildPacket(0x00, ls.Bytes()))
	return login.Bytes(), nil
}

// awaitLoginResult reads login-state packets until a terminal one arrives
// (Login Success, Encryption Request, or Disconnect), applying Set Compression
// to rd on the way. It returns how many packets it read, so callers that keep
// the connection open can account for them.
func awaitLoginResult(rd *reader, username string) (*LoginResult, int, error) {
	read := 0
	for {
		id, payload, err := rd.readPacket()
		if err != nil {
			return nil, read, fmt.Errorf("minecraft: read login response: %w", err)
		}
		read++
		switch id {
		case 0x03: // Set Compression — subsequent frames are compressed.
			threshold, _ := readVarInt(bytes.NewReader(payload))
			rd.compressed = threshold >= 0
			rd.threshold = threshold
		case 0x02: // Login Success — the server accepted our (offline) login.
			return &LoginResult{Outcome: Success, Detail: username}, read, nil
		case 0x01: // Encryption Request — server is in online-mode.
			return &LoginResult{Outcome: NeedsAuth, Detail: "server requires authentication (online-mode)"}, read, nil
		case 0x00: // Disconnect — server refused us.
			reason, _ := readString(bytes.NewReader(payload))
			return &LoginResult{Outcome: Disconnected, Detail: reason}, read, nil
		default:
			// Ignore anything else (e.g. Login Plugin Request 0x04 from modded
			// servers) and keep reading until a terminal packet or the deadline.
		}
	}
}

// ---- sustained session ----

// HoldResult is the outcome of a sustained session started by Hold. The JSON
// field names are the METRICS contract read by the capture-overhead regression
// test. Failure is "" on success, otherwise one of "drop" (connection closed or
// reset after login), "timeout" (no packet for holdSilenceTimeout, or the
// context ended), "disconnect: <reason>" (server sent Disconnect), or
// "join: <err>" (dial, handshake or login failed).
type HoldResult struct {
	HandshakeLatencyMs    float64   `json:"handshake_latency_ms"`
	JoinDurationSec       float64   `json:"join_duration_sec"`
	HeldSec               float64   `json:"held_sec"`
	HeldFull              bool      `json:"held_full"`
	KeepalivesReceived    int       `json:"keepalives_received"`
	KeepaliveRTTSamplesMs []float64 `json:"keepalive_rtt_samples_ms"`
	PingsSent             int       `json:"pings_sent"`
	PacketsSent           int       `json:"packets_sent"`
	PacketsReceived       int       `json:"packets_received"`
	BytesSent             int       `json:"bytes_sent"`
	BytesReceived         int       `json:"bytes_received"`
	PacketLossPct         float64   `json:"packet_loss_pct"`
	Failure               string    `json:"failure"`
}

// holdSilenceTimeout is how long Hold tolerates without an inbound packet
// before it reports "timeout". It is a variable so tests can shorten it.
var holdSilenceTimeout = 20 * time.Second

// holdPongGrace bounds how long Hold keeps reading after the hold window to
// collect Pong Responses to pings still in flight.
const holdPongGrace = time.Second

// inbound is one frame handed from Hold's reader goroutine to its main loop.
type inbound struct {
	id      int32
	payload []byte
	err     error
}

// Hold logs in to addr (protocol number from Ping, offline mode) and keeps the
// session open for hold, measuring it as it goes. It is a single attempt: it
// does not retry the login, the caller decides about warm-up.
//
// Configuration: Select Known Packs is answered with an empty set, Keep Alive
// and Ping are echoed, and Finish Configuration is acknowledged to enter Play.
// Play: Keep Alive and Ping are echoed, Synchronize Player Position is confirmed
// with its teleport id, a Ping Request is sent every pingEvery, and Pong
// Responses are matched to their request to produce RTT samples. Any Disconnect
// ends the session with a "disconnect" failure.
//
// The returned error is nil exactly when Failure is "".
func Hold(ctx context.Context, addr string, protocol int, user string, hold, pingEvery time.Duration) (res HoldResult, err error) {
	res.KeepaliveRTTSamplesMs = []float64{}
	var (
		recvBytes atomic.Int64
		wr        *writer
		started   = time.Now()
		pending   = map[int64]time.Time{}
		nextPing  int64
	)
	defer func() {
		if wr != nil {
			res.PacketsSent = wr.sentPackets
			res.BytesSent = wr.sentBytes
		}
		res.BytesReceived = int(recvBytes.Load())
		res.PacketLossPct = packetLossPct(res.PingsSent, len(res.KeepaliveRTTSamplesMs))
	}()

	joinFail := func(e error) (HoldResult, error) {
		res.Failure = "join: " + e.Error()
		return res, e
	}

	if hold <= 0 || pingEvery <= 0 {
		return joinFail(fmt.Errorf("minecraft: hold (%s) and ping interval (%s) must be positive", hold, pingEvery))
	}
	if err := validUsername(user); err != nil {
		return joinFail(err)
	}
	host, port, err := splitHostPort(addr)
	if err != nil {
		return joinFail(err)
	}
	conn, err := dial(ctx, addr, 25*time.Second)
	if err != nil {
		return joinFail(err)
	}
	defer func() { _ = conn.Close() }()

	rd := &reader{br: bufio.NewReader(&countingReader{r: conn, n: &recvBytes})}
	wr = &writer{w: conn, threshold: -1}

	login, err := loginPackets(host, port, protocol, user)
	if err != nil {
		return joinFail(err)
	}
	loginSent := time.Now()
	if _, err := conn.Write(login); err != nil {
		return joinFail(fmt.Errorf("minecraft: write login: %w", err))
	}
	wr.sentBytes += len(login)
	wr.sentPackets += 2 // handshake + Login Start

	outcome, loginRead, err := awaitLoginResult(rd, user)
	res.PacketsReceived += loginRead
	if err != nil {
		return joinFail(err)
	}
	if outcome.Outcome != Success {
		return joinFail(fmt.Errorf("login not accepted: %s", outcome.Detail))
	}
	res.HandshakeLatencyMs = msSince(loginSent)

	// Frames written from here on use the compression negotiated during login.
	if rd.compressed {
		wr.threshold = rd.threshold
	}
	// Login Acknowledged. A failed write here means the server already closed.
	if err := wr.writePacket(0x03, nil); err != nil {
		res.Failure = "drop"
		return res, fmt.Errorf("connection reset: %w", err)
	}

	// The login deadline from dial no longer applies; the silence timer below
	// governs how long the server may stay quiet.
	_ = conn.SetDeadline(time.Time{})

	// Reader goroutine: one blocking read per frame, handed to the main loop.
	// Reads cannot be interleaved with short deadlines because a timeout in
	// the middle of a frame would desynchronise the stream.
	frames := make(chan inbound)
	done := make(chan struct{})
	defer close(done)
	go func() {
		defer close(frames)
		for {
			id, payload, err := rd.readPacket()
			select {
			case frames <- inbound{id: id, payload: payload, err: err}:
			case <-done:
				return
			}
			if err != nil {
				return
			}
		}
	}()

	// send writes one packet; on failure it records a drop and returns false.
	var failure string
	send := func(id int32, data []byte) bool {
		if e := wr.writePacket(id, data); e != nil {
			failure, err = "drop", fmt.Errorf("connection reset: %w", e)
			return false
		}
		return true
	}

	var (
		inPlay      bool
		holdElapsed bool
		playStart   time.Time
		holdTimer   *time.Timer
		ticker      *time.Ticker
		holdEnd     <-chan time.Time
		pingTick    <-chan time.Time
		grace       <-chan time.Time
	)
	defer func() {
		if holdTimer != nil {
			holdTimer.Stop()
		}
		if ticker != nil {
			ticker.Stop()
		}
	}()

	idle := time.NewTimer(holdSilenceTimeout)
	defer idle.Stop()
	resetIdle := func() {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(holdSilenceTimeout)
	}

loop:
	for {
		select {
		case <-ctx.Done():
			failure, err = "timeout", fmt.Errorf("timeout: session context ended: %w", ctx.Err())
			break loop

		case <-idle.C:
			failure, err = "timeout", fmt.Errorf("timeout: no packet from server for %s", holdSilenceTimeout)
			break loop

		case <-holdEnd:
			holdElapsed = true
			res.HeldSec = time.Since(playStart).Seconds()
			holdEnd = nil
			pingTick = nil
			if ticker != nil {
				ticker.Stop()
			}
			grace = time.After(holdPongGrace)
			if len(pending) == 0 {
				break loop
			}

		case <-grace:
			break loop

		case <-pingTick:
			nextPing++
			if !send(0x24, i64Bytes(nextPing)) { // Ping Request
				break loop
			}
			pending[nextPing] = time.Now()
			res.PingsSent++

		case fr, ok := <-frames:
			if !ok || fr.err != nil {
				if holdElapsed {
					// The window was already held in full; the server closing
					// while we collected the last Pong Responses is not a drop.
					break loop
				}
				if !ok {
					failure, err = "drop", errors.New("connection reset: connection closed")
				} else {
					failure, err = "drop", fmt.Errorf("connection reset: %w", fr.err)
				}
				break loop
			}
			res.PacketsReceived++
			resetIdle()

			if !inPlay {
				switch fr.id {
				case 0x0e: // Select Known Packs: we have no known packs to report.
					if !send(0x07, []byte{0x00}) {
						break loop
					}
				case 0x04: // Keep Alive: echo it back.
					res.KeepalivesReceived++
					if len(fr.payload) >= 8 && !send(0x04, fr.payload[:8]) {
						break loop
					}
				case 0x05: // Ping: answer with Pong carrying the same id.
					if len(fr.payload) >= 4 && !send(0x05, fr.payload[:4]) {
						break loop
					}
				case 0x03: // Finish Configuration: acknowledge and enter Play.
					if !send(0x03, nil) {
						break loop
					}
					inPlay = true
					playStart = time.Now()
					res.JoinDurationSec = time.Since(started).Seconds()
					holdTimer = time.NewTimer(hold)
					holdEnd = holdTimer.C
					ticker = time.NewTicker(pingEvery)
					pingTick = ticker.C
				case 0x02: // Disconnect (Configuration).
					reason := disconnectReason(fr.payload)
					failure, err = "disconnect: "+reason, fmt.Errorf("connection reset by server Disconnect: %s", reason)
					break loop
				}
				continue
			}

			switch fr.id {
			case 0x27: // Keep Alive (Play): echo it back.
				res.KeepalivesReceived++
				if len(fr.payload) >= 8 && !send(0x1a, fr.payload[:8]) {
					break loop
				}
			case 0x37: // Ping (Play): answer with Pong.
				if len(fr.payload) >= 4 && !send(0x2b, fr.payload[:4]) {
					break loop
				}
			case 0x42: // Synchronize Player Position: confirm the teleport id.
				teleportID, e := readVarInt(bytes.NewReader(fr.payload))
				if e == nil {
					var b bytes.Buffer
					writeVarInt(&b, teleportID)
					if !send(0x00, b.Bytes()) { // Confirm Teleportation
						break loop
					}
				}
			case 0x38: // Pong Response: match the id to its Ping Request.
				if len(fr.payload) >= 8 {
					id := int64(binary.BigEndian.Uint64(fr.payload[:8]))
					if sentAt, ok := pending[id]; ok {
						delete(pending, id)
						res.KeepaliveRTTSamplesMs = append(res.KeepaliveRTTSamplesMs, msSince(sentAt))
					}
				}
			case 0x1d: // Disconnect (Play).
				reason := disconnectReason(fr.payload)
				failure, err = "disconnect: "+reason, fmt.Errorf("connection reset by server Disconnect: %s", reason)
				break loop
			}
		}
	}

	if inPlay && !holdElapsed {
		res.HeldSec = time.Since(playStart).Seconds()
	}
	if failure != "" {
		res.Failure = failure
		res.HeldFull = false
		return res, err
	}
	res.HeldFull = holdElapsed
	return res, nil
}

// countingReader counts the bytes read through it, for BytesReceived.
type countingReader struct {
	r io.Reader
	n *atomic.Int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n.Add(int64(n))
	return n, err
}

// packetLossPct is the share of pings with no Pong Response, in percent.
func packetLossPct(pingsSent, pongs int) float64 {
	if pingsSent == 0 {
		return 0
	}
	return float64(pingsSent-pongs) / float64(pingsSent) * 100
}

// disconnectReason extracts the reason from a Disconnect payload. Since 1.20.3
// the reason is a network NBT text component; a plain text component is a bare
// TAG_String (id 0x08, unsigned 16-bit length, bytes). Anything else is shown
// as hex so the evidence is never lost.
func disconnectReason(payload []byte) string {
	if len(payload) >= 3 && payload[0] == 0x08 {
		n := int(binary.BigEndian.Uint16(payload[1:3]))
		if 3+n <= len(payload) {
			return string(payload[3 : 3+n])
		}
	}
	if len(payload) > 64 {
		return fmt.Sprintf("unparsed text component %x...", payload[:64])
	}
	return fmt.Sprintf("unparsed text component %x", payload)
}

func msSince(t time.Time) float64 {
	return float64(time.Since(t)) / float64(time.Millisecond)
}

func i64Bytes(v int64) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

// ---- wire helpers ----

func dial(ctx context.Context, addr string, timeout time.Duration) (net.Conn, error) {
	d := net.Dialer{}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("minecraft: dial %s: %w", addr, err)
	}
	deadline := time.Now().Add(timeout)
	if dl, ok := ctx.Deadline(); ok && dl.Before(deadline) {
		deadline = dl
	}
	_ = conn.SetDeadline(deadline)
	return conn, nil
}

func splitHostPort(addr string) (string, uint16, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, fmt.Errorf("minecraft: split %q: %w", addr, err)
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		return "", 0, fmt.Errorf("minecraft: port %q: %w", portStr, err)
	}
	return host, uint16(port), nil
}

func handshake(protocol int32, host string, port uint16, nextState int32) []byte {
	var p bytes.Buffer
	writeVarInt(&p, protocol)
	writeString(&p, host)
	_ = binary.Write(&p, binary.BigEndian, port)
	writeVarInt(&p, nextState)
	return buildPacket(0x00, p.Bytes())
}

// buildPacket frames an uncompressed packet: VarInt(length) + VarInt(id) + data.
func buildPacket(id int32, data []byte) []byte {
	var inner bytes.Buffer
	writeVarInt(&inner, id)
	inner.Write(data)
	var out bytes.Buffer
	writeVarInt(&out, int32(inner.Len()))
	out.Write(inner.Bytes())
	return out.Bytes()
}

type reader struct {
	br         *bufio.Reader
	compressed bool
	threshold  int32 // Set Compression threshold; meaningful when compressed
}

// readPacket reads one framed packet, transparently inflating it once
// compression has been negotiated, and returns its id + remaining payload.
func (rd *reader) readPacket() (int32, []byte, error) {
	length, err := readVarInt(rd.br)
	if err != nil {
		return 0, nil, err
	}
	if length <= 0 || length > 8<<20 {
		return 0, nil, fmt.Errorf("minecraft: bad packet length %d", length)
	}
	frame := make([]byte, length)
	if _, err := io.ReadFull(rd.br, frame); err != nil {
		return 0, nil, err
	}
	fr := bytes.NewReader(frame)
	if rd.compressed {
		dataLen, err := readVarInt(fr)
		if err != nil {
			return 0, nil, err
		}
		if dataLen > 0 { // dataLen == 0 means the rest is stored uncompressed
			zr, err := zlib.NewReader(fr)
			if err != nil {
				return 0, nil, fmt.Errorf("minecraft: zlib: %w", err)
			}
			out := make([]byte, dataLen)
			if _, err := io.ReadFull(zr, out); err != nil {
				return 0, nil, fmt.Errorf("minecraft: inflate: %w", err)
			}
			_ = zr.Close()
			fr = bytes.NewReader(out)
		}
	}
	id, err := readVarInt(fr)
	if err != nil {
		return 0, nil, err
	}
	rest, err := io.ReadAll(fr)
	if err != nil {
		return 0, nil, err
	}
	return id, rest, nil
}

// writer frames outgoing packets the way reader expects to read them. Before
// compression is negotiated (threshold < 0) packets are plain. Afterwards a
// packet whose id+data length is at least the threshold is zlib-compressed,
// and smaller ones go out with Data Length 0.
type writer struct {
	w           io.Writer
	threshold   int32
	sentBytes   int // wire bytes written
	sentPackets int
}

func (wr *writer) writePacket(id int32, data []byte) error {
	var inner bytes.Buffer
	writeVarInt(&inner, id)
	inner.Write(data)

	var frame bytes.Buffer
	switch {
	case wr.threshold < 0:
		frame.Write(inner.Bytes())
	case int32(inner.Len()) < wr.threshold:
		writeVarInt(&frame, 0) // Data Length 0: stored uncompressed
		frame.Write(inner.Bytes())
	default:
		writeVarInt(&frame, int32(inner.Len()))
		zw := zlib.NewWriter(&frame)
		if _, err := zw.Write(inner.Bytes()); err != nil {
			return fmt.Errorf("minecraft: deflate packet 0x%02x: %w", id, err)
		}
		if err := zw.Close(); err != nil {
			return fmt.Errorf("minecraft: deflate packet 0x%02x: %w", id, err)
		}
	}

	var out bytes.Buffer
	writeVarInt(&out, int32(frame.Len()))
	out.Write(frame.Bytes())
	n, err := wr.w.Write(out.Bytes())
	wr.sentBytes += n
	if err != nil {
		return fmt.Errorf("minecraft: write packet 0x%02x: %w", id, err)
	}
	wr.sentPackets++
	return nil
}

func writeVarInt(buf *bytes.Buffer, v int32) {
	uv := uint32(v)
	for {
		b := byte(uv & 0x7f)
		uv >>= 7
		if uv != 0 {
			b |= 0x80
		}
		buf.WriteByte(b)
		if uv == 0 {
			return
		}
	}
}

func readVarInt(r io.ByteReader) (int32, error) {
	var result uint32
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			return int32(result), nil
		}
	}
	return 0, errors.New("minecraft: VarInt too long")
}

func writeString(buf *bytes.Buffer, s string) {
	writeVarInt(buf, int32(len(s)))
	buf.WriteString(s)
}

func readString(r *bytes.Reader) (string, error) {
	n, err := readVarInt(r)
	if err != nil {
		return "", err
	}
	if n < 0 || int(n) > r.Len() {
		return "", errors.New("minecraft: bad string length")
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return "", err
	}
	return string(b), nil
}
