package minecraftproto

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestVarIntRoundTrip tests encoding and decoding of VarInt across the valid range.
func TestVarIntRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []int32{
		// single byte
		0, 1, 127,
		// two bytes
		128, 255, 16383,
		// three bytes
		16384, 1<<20 - 1, 1<<21 - 1,
		// four bytes
		1 << 21, 1<<27 - 1, 1<<28 - 1,
		// five bytes and negatives
		1 << 28, (1 << 31) - 1, -1, -128,
	}
	for _, v := range tests {
		t.Run(fmt.Sprintf("%d", v), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			writeVarInt(&buf, v)
			// Check that encoded length never exceeds 5 bytes.
			if buf.Len() > 5 {
				t.Fatalf("VarInt %d encoded to %d bytes; want <= 5", v, buf.Len())
			}
			// Decode and verify round-trip.
			decoded, err := readVarInt(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("readVarInt failed: %v", err)
			}
			if decoded != v {
				t.Fatalf("round-trip mismatch: got %d, want %d", decoded, v)
			}
		})
	}
}

// TestVarIntTooLong tests that reading more than 5 bytes returns an error.
func TestVarIntTooLong(t *testing.T) {
	t.Parallel()
	// Construct a 6-byte VarInt (all continuation bits set, then one more).
	tooLong := []byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x00}
	_, err := readVarInt(bytes.NewReader(tooLong))
	if err == nil || !strings.Contains(err.Error(), "too long") {
		t.Fatalf("readVarInt with 6 bytes should error; got %v", err)
	}
}

// TestStringRoundTrip tests encoding and decoding of length-prefixed strings.
func TestStringRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []string{
		"", "a", "hello", "Minecraft", "a" + strings.Repeat("b", 1000),
	}
	for _, s := range tests {
		t.Run(fmt.Sprintf("%d-chars", len(s)), func(t *testing.T) {
			t.Parallel()
			var buf bytes.Buffer
			writeString(&buf, s)
			decoded, err := readString(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("readString failed: %v", err)
			}
			if decoded != s {
				t.Fatalf("round-trip mismatch: got %q, want %q", decoded, s)
			}
		})
	}
}

// TestPingWithFakeServer tests Ping against a fake local server.
func TestPingWithFakeServer(t *testing.T) {
	t.Parallel()
	// Start a fake server that echoes a well-formed status response.
	lc := net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Spawn a goroutine to accept one connection and send a status response.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // server closed
		}
		defer conn.Close()

		// Read the handshake + status request (don't validate, just drain).
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.ReadAll(conn)

		// Send a valid status response packet.
		status := Status{
			Version: struct {
				Name     string `json:"name"`
				Protocol int    `json:"protocol"`
			}{Name: "1.21.4", Protocol: 767},
			Players: struct {
				Max    int `json:"max"`
				Online int `json:"online"`
			}{Max: 20, Online: 1},
		}
		jsonBytes, _ := json.Marshal(status)

		// Build the response: packet ID 0x00 + JSON as a string.
		var payload bytes.Buffer
		writeString(&payload, string(jsonBytes))
		response := buildPacket(0x00, payload.Bytes())

		_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
		_, _ = conn.Write(response)
	}()

	// Give the server goroutine time to accept.
	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	st, err := Ping(ctx, addr)
	if err != nil {
		t.Fatalf("Ping failed: %v", err)
	}
	if st == nil {
		t.Fatal("Ping returned nil Status")
	}
	if st.Version.Name != "1.21.4" {
		t.Fatalf("got version %q, want 1.21.4", st.Version.Name)
	}
	if st.Version.Protocol != 767 {
		t.Fatalf("got protocol %d, want 767", st.Version.Protocol)
	}
	if st.Players.Online != 1 {
		t.Fatalf("got online %d, want 1", st.Players.Online)
	}
}

// TestLoginLongUsernameRejected tests that a username > 16 characters is
// rejected before any bytes are sent to the server.
func TestLoginLongUsernameRejected(t *testing.T) {
	t.Parallel()
	longName := "gameplane-e2e-bot-toolong"
	if len(longName) <= 16 {
		t.Fatalf("test setup error: username is only %d chars", len(longName))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Call Login with a too-long username. Should fail immediately without
	// dialing the server.
	_, err := Login(ctx, "127.0.0.1:12345", 767, longName)
	if err == nil {
		t.Fatal("Login with 16+ char username should error")
	}
	if !strings.Contains(err.Error(), "at most 16") {
		t.Fatalf("error should mention 16-char limit; got: %v", err)
	}
}

// TestLoginConnectFailure tests that a dial failure is propagated.
func TestLoginConnectFailure(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	// Use a port that's almost certainly not listening.
	_, err := Login(ctx, "127.0.0.1:54321", 767, "bot")
	if err == nil {
		t.Fatal("Login to closed port should error")
	}
}

// TestTruncatedPacketError tests that a truncated response produces an error.
func TestTruncatedPacketError(t *testing.T) {
	t.Parallel()
	lc := net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Server that sends incomplete data then closes.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		// Drain the request (handshake + status request).
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, _ = io.ReadAll(conn)
		// Send only 3 bytes (an incomplete VarInt length field).
		_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
		conn.Write([]byte{0x80, 0x80, 0x80})
		// Close, forcing a read error in the client.
	}()

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = Ping(ctx, addr)
	if err == nil {
		t.Fatal("Ping with truncated response should error")
	}
}

// TestGarbageResponseError tests that garbage data produces an error.
func TestGarbageResponseError(t *testing.T) {
	t.Parallel()
	lc := net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Server that sends garbage.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second))
		_, _ = io.ReadAll(conn)
		_ = conn.SetWriteDeadline(time.Now().Add(1 * time.Second))
		// Send a packet with ID 0xFF (not 0x00) to trigger an error.
		conn.Write(buildPacket(0xFF, []byte("garbage")))
	}()

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	_, err = Ping(ctx, addr)
	if err == nil {
		t.Fatal("Ping with wrong packet ID should error")
	}
	if !strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("error should mention unexpected; got: %v", err)
	}
}

// TestContextDeadlineEnforced tests that the context deadline is respected.
func TestContextDeadlineEnforced(t *testing.T) {
	t.Parallel()
	lc := net.ListenConfig{}
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	defer ln.Close()

	addr := ln.Addr().String()

	// Server that accepts but never sends a response.
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = io.ReadAll(conn)
		// Block forever without sending anything.
		select {}
	}()

	time.Sleep(50 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err = Ping(ctx, addr)
	if err == nil {
		t.Fatal("Ping with tight context deadline should timeout")
	}
}

// TestStringLengthValidation tests that readString rejects invalid lengths.
func TestStringLengthValidation(t *testing.T) {
	t.Parallel()
	// Construct a VarInt indicating 100 bytes of string data, but provide only 10.
	var buf bytes.Buffer
	writeVarInt(&buf, 100)
	buf.Write([]byte("short"))

	_, err := readString(bytes.NewReader(buf.Bytes()))
	if err == nil {
		t.Fatal("readString should error on truncated data")
	}
}

// TestFrameRoundTrip checks that writer and reader agree on plain and
// compressed framing, including the Data Length 0 path for small packets.
func TestFrameRoundTrip(t *testing.T) {
	t.Parallel()
	for _, threshold := range []int32{-1, 8} {
		for _, size := range []int{0, 3, 600} {
			var buf bytes.Buffer
			wr := &writer{w: &buf, threshold: threshold}
			payload := bytes.Repeat([]byte{0x5a}, size)
			if err := wr.writePacket(0x24, payload); err != nil {
				t.Fatalf("writePacket(threshold=%d, size=%d): %v", threshold, size, err)
			}
			if wr.sentPackets != 1 || wr.sentBytes != buf.Len() {
				t.Fatalf("writer counters: packets=%d bytes=%d, buffer=%d", wr.sentPackets, wr.sentBytes, buf.Len())
			}
			rd := &reader{br: bufio.NewReader(&buf), compressed: threshold >= 0, threshold: threshold}
			id, got, err := rd.readPacket()
			if err != nil {
				t.Fatalf("readPacket(threshold=%d, size=%d): %v", threshold, size, err)
			}
			if id != 0x24 || !bytes.Equal(got, payload) {
				t.Fatalf("round-trip mismatch (threshold=%d, size=%d): id=0x%02x len=%d", threshold, size, id, len(got))
			}
		}
	}
}

// ---- sustained session (Hold) tests ----
//
// These tests are deliberately not t.Parallel: they shorten the package-level
// holdSilenceTimeout that Hold reads.

// sessionSeen records what the fake server observed from the client.
type sessionSeen struct {
	knownPacksEmpty bool
	configKeepAlive int64
	configPong      int32
	finishAck       bool
	playKeepAlive   int64
	playPong        int32
	teleportID      int32
	pingRequests    int
}

// startFakeServer accepts one loopback connection and runs script on it. The
// script reads client packets with rd and writes server packets with wr; the
// connection is closed when it returns. The returned channel closes then too.
func startFakeServer(t *testing.T, script func(rd *reader, wr *writer)) (string, <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen failed: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		script(&reader{br: bufio.NewReader(conn)}, &writer{w: conn, threshold: -1})
	}()
	return ln.Addr().String(), finished
}

// serveLogin plays the server side of the login: it reads the handshake and
// Login Start, optionally enables compression at threshold 8, sends Login
// Success and reads Login Acknowledged.
func serveLogin(rd *reader, wr *writer, compress bool) error {
	for i := 0; i < 2; i++ { // handshake, Login Start
		if _, _, err := rd.readPacket(); err != nil {
			return err
		}
	}
	if compress {
		var p bytes.Buffer
		writeVarInt(&p, 8)
		if err := wr.writePacket(0x03, p.Bytes()); err != nil { // Set Compression
			return err
		}
		wr.threshold = 8
		rd.compressed = true
		rd.threshold = 8
	}
	success := append(make([]byte, 16), 0) // UUID, empty name, no properties
	if err := wr.writePacket(0x02, success); err != nil {
		return err
	}
	id, _, err := rd.readPacket()
	if err != nil {
		return err
	}
	if id != 0x03 {
		return fmt.Errorf("expected Login Acknowledged, got 0x%02x", id)
	}
	return nil
}

// serveConfig sends Known Packs, a Keep Alive and a Ping, then sends Finish
// Configuration and reads the client's replies until it acknowledges it.
func serveConfig(rd *reader, wr *writer, seen *sessionSeen) error {
	_ = wr.writePacket(0x0e, []byte{0x00}) // Select Known Packs (empty)
	_ = wr.writePacket(0x04, i64Bytes(1001))
	_ = wr.writePacket(0x05, i32Bytes(77))
	_ = wr.writePacket(0x03, nil) // Finish Configuration
	for {
		id, payload, err := rd.readPacket()
		if err != nil {
			return err
		}
		switch id {
		case 0x07:
			seen.knownPacksEmpty = len(payload) == 1 && payload[0] == 0
		case 0x04:
			if len(payload) >= 8 {
				seen.configKeepAlive = int64(binary.BigEndian.Uint64(payload))
			}
		case 0x05:
			if len(payload) >= 4 {
				seen.configPong = int32(binary.BigEndian.Uint32(payload))
			}
		case 0x03:
			seen.finishAck = true
			return nil
		}
	}
}

// servePlay sends a Keep Alive, a Ping and a Synchronize Player Position, and
// (when compress is set) a large unknown Play packet so the client has to
// inflate a compressed frame. It then answers client traffic until the client
// closes the connection.
func servePlay(rd *reader, wr *writer, seen *sessionSeen, compress bool) {
	_ = wr.writePacket(0x27, i64Bytes(2002))
	_ = wr.writePacket(0x37, i32Bytes(88))
	var pos bytes.Buffer
	writeVarInt(&pos, 5) // teleport id; the rest of the position is ignored
	pos.Write(make([]byte, 24))
	_ = wr.writePacket(0x42, pos.Bytes())
	if compress {
		_ = wr.writePacket(0x2c, make([]byte, 512)) // ignored by the client
	}
	for {
		id, payload, err := rd.readPacket()
		if err != nil {
			return
		}
		switch id {
		case 0x1a:
			if len(payload) >= 8 {
				seen.playKeepAlive = int64(binary.BigEndian.Uint64(payload))
			}
		case 0x2b:
			if len(payload) >= 4 {
				seen.playPong = int32(binary.BigEndian.Uint32(payload))
			}
		case 0x00:
			seen.teleportID, _ = readVarInt(bytes.NewReader(payload))
		case 0x24:
			if len(payload) >= 8 {
				seen.pingRequests++
				_ = wr.writePacket(0x38, payload[:8]) // Pong Response, same id
			}
		}
	}
}

func i32Bytes(v int32) []byte {
	b := make([]byte, 4)
	binary.BigEndian.PutUint32(b, uint32(v))
	return b
}

// nbtString encodes s as a network NBT TAG_String, the plain text component
// used for Disconnect reasons.
func nbtString(s string) []byte {
	b := []byte{0x08, byte(len(s) >> 8), byte(len(s))}
	return append(b, s...)
}

// TestHoldFullSession runs a complete sustained session with and without
// compression and checks every metric the METRICS contract reports.
func TestHoldFullSession(t *testing.T) {
	for _, compress := range []bool{false, true} {
		t.Run(fmt.Sprintf("compress=%t", compress), func(t *testing.T) {
			seen := &sessionSeen{}
			addr, finished := startFakeServer(t, func(rd *reader, wr *writer) {
				if err := serveLogin(rd, wr, compress); err != nil {
					return
				}
				if err := serveConfig(rd, wr, seen); err != nil {
					return
				}
				servePlay(rd, wr, seen, compress)
			})

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			res, err := Hold(ctx, addr, 769, "tester", 1500*time.Millisecond, 200*time.Millisecond)
			if err != nil {
				t.Fatalf("Hold failed: %v (result %+v)", err, res)
			}
			<-finished

			if res.Failure != "" || !res.HeldFull {
				t.Fatalf("want full hold without failure; got failure=%q held_full=%t", res.Failure, res.HeldFull)
			}
			if res.HeldSec < 1.5 {
				t.Errorf("held_sec = %.3f, want >= 1.5", res.HeldSec)
			}
			if res.HandshakeLatencyMs <= 0 || res.JoinDurationSec <= 0 {
				t.Errorf("timings not recorded: handshake=%.3fms join=%.3fs", res.HandshakeLatencyMs, res.JoinDurationSec)
			}
			if res.KeepalivesReceived != 2 { // one Configuration, one Play
				t.Errorf("keepalives_received = %d, want 2", res.KeepalivesReceived)
			}
			if res.PingsSent < 3 {
				t.Errorf("pings_sent = %d, want >= 3 over 1.5s at 200ms", res.PingsSent)
			}
			if len(res.KeepaliveRTTSamplesMs) != res.PingsSent {
				t.Errorf("rtt samples = %d, pings sent = %d; every ping should be answered",
					len(res.KeepaliveRTTSamplesMs), res.PingsSent)
			}
			if res.PacketLossPct != 0 {
				t.Errorf("packet_loss_pct = %.2f, want 0", res.PacketLossPct)
			}
			if seen.pingRequests != res.PingsSent {
				t.Errorf("server saw %d ping requests, client sent %d", seen.pingRequests, res.PingsSent)
			}
			if res.PacketsSent == 0 || res.BytesSent == 0 || res.PacketsReceived == 0 || res.BytesReceived == 0 {
				t.Errorf("counters not recorded: %+v", res)
			}

			if !seen.knownPacksEmpty {
				t.Error("client did not answer Select Known Packs with an empty set")
			}
			if seen.configKeepAlive != 1001 || seen.configPong != 77 {
				t.Errorf("configuration echoes: keep alive=%d pong=%d", seen.configKeepAlive, seen.configPong)
			}
			if !seen.finishAck {
				t.Error("client did not acknowledge Finish Configuration")
			}
			if seen.playKeepAlive != 2002 || seen.playPong != 88 {
				t.Errorf("play echoes: keep alive=%d pong=%d", seen.playKeepAlive, seen.playPong)
			}
			if seen.teleportID != 5 {
				t.Errorf("teleport confirm id = %d, want 5", seen.teleportID)
			}
		})
	}
}

// TestHoldServerClosesMidHold checks that a connection closed by the server
// after Play has started is reported as a drop.
func TestHoldServerClosesMidHold(t *testing.T) {
	seen := &sessionSeen{}
	addr, finished := startFakeServer(t, func(rd *reader, wr *writer) {
		if err := serveLogin(rd, wr, false); err != nil {
			return
		}
		if err := serveConfig(rd, wr, seen); err != nil {
			return
		}
		_ = wr.writePacket(0x27, i64Bytes(3))
		// Returning closes the connection while the hold is still running.
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := Hold(ctx, addr, 769, "tester", 10*time.Second, 200*time.Millisecond)
	<-finished
	if err == nil {
		t.Fatal("Hold should fail when the server closes mid-hold")
	}
	if res.Failure != "drop" {
		t.Fatalf("failure = %q, want \"drop\"", res.Failure)
	}
	if res.HeldFull {
		t.Error("held_full must be false for a dropped session")
	}
	if !strings.Contains(err.Error(), "connection reset") {
		t.Errorf("error %q should describe a connection reset", err)
	}
}

// TestHoldServerDisconnects checks that a Disconnect in Play is reported with
// its reason.
func TestHoldServerDisconnects(t *testing.T) {
	addr, finished := startFakeServer(t, func(rd *reader, wr *writer) {
		seen := &sessionSeen{}
		if err := serveLogin(rd, wr, false); err != nil {
			return
		}
		if err := serveConfig(rd, wr, seen); err != nil {
			return
		}
		_ = wr.writePacket(0x1d, nbtString("server restarting")) // Disconnect (Play)
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := Hold(ctx, addr, 769, "tester", 10*time.Second, 200*time.Millisecond)
	<-finished
	if err == nil {
		t.Fatal("Hold should fail on Disconnect")
	}
	if res.Failure != "disconnect: server restarting" {
		t.Fatalf("failure = %q, want the disconnect reason", res.Failure)
	}
}

// TestHoldSilentServerTimesOut checks that a server that goes quiet before
// Finish Configuration ends the hold with "timeout" once the silence limit is
// reached.
func TestHoldSilentServerTimesOut(t *testing.T) {
	prev := holdSilenceTimeout
	holdSilenceTimeout = 300 * time.Millisecond
	t.Cleanup(func() { holdSilenceTimeout = prev })

	addr, finished := startFakeServer(t, func(rd *reader, wr *writer) {
		if err := serveLogin(rd, wr, false); err != nil {
			return
		}
		_ = wr.writePacket(0x0e, []byte{0x00})
		_ = wr.writePacket(0x04, i64Bytes(9))
		// Never send Finish Configuration: stay silent until the client gives up.
		for {
			if _, _, err := rd.readPacket(); err != nil {
				return
			}
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := Hold(ctx, addr, 769, "tester", 10*time.Second, time.Second)
	<-finished
	if err == nil {
		t.Fatal("Hold should fail when the server never finishes configuration")
	}
	if res.Failure != "timeout" {
		t.Fatalf("failure = %q, want \"timeout\"", res.Failure)
	}
	if res.HeldFull || res.KeepalivesReceived != 1 {
		t.Errorf("held_full=%t keepalives=%d; want false and 1", res.HeldFull, res.KeepalivesReceived)
	}
}

// TestHoldRejectsBadArguments checks that non-positive durations and overlong
// usernames fail before anything is dialed.
func TestHoldRejectsBadArguments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := Hold(ctx, "127.0.0.1:1", 769, "bot", 0, time.Second); err == nil {
		t.Error("zero hold should be rejected")
	}
	if _, err := Hold(ctx, "127.0.0.1:1", 769, "bot", time.Second, 0); err == nil {
		t.Error("zero ping interval should be rejected")
	}
	if _, err := Hold(ctx, "127.0.0.1:1", 769, "gameplane-e2e-bot-toolong", time.Second, time.Second); err == nil ||
		!strings.Contains(err.Error(), "at most 16") {
		t.Errorf("overlong username should be rejected; got %v", err)
	}
}
