package a2sproto

import (
	"bytes"
	"context"
	"encoding/binary"
	"net"
	"testing"
	"time"
)

// TestQueryInfoImmediate tests A2S_INFO when the server replies immediately
// without a challenge.
func TestQueryInfoImmediate(t *testing.T) {

	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		// Parse the request to verify it's A2S_INFO.
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil // Not an info request, drop it.
		}

		// Build an immediate response (no challenge).
		return buildFakeInfoResponse()
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := QueryInfo(ctx, server.LocalAddr().String())
	if err != nil {
		t.Fatalf("QueryInfo failed: %v", err)
	}

	if info == nil {
		t.Fatal("QueryInfo returned nil Info")
	}
	if info.Name != "Test Server" {
		t.Errorf("got name %q, want %q", info.Name, "Test Server")
	}
	if info.Players != 5 {
		t.Errorf("got players %d, want 5", info.Players)
	}
	if info.MaxPlayers != 20 {
		t.Errorf("got max players %d, want 20", info.MaxPlayers)
	}
}

// TestQueryInfoChallenge tests A2S_INFO when the server sends a challenge first.
func TestQueryInfoChallenge(t *testing.T) {

	challengeSent := false
	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil
		}

		if !challengeSent {
			// First request: no challenge yet. Send a challenge response.
			challengeSent = true
			return buildChallengeResponse()
		}

		// Second request: should have the challenge appended. Verify and respond.
		// (In a real test, we'd validate the challenge matches, but for now just check it's there.)
		if len(req) < 9 {
			return nil // Expected challenge data.
		}

		return buildFakeInfoResponse()
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := QueryInfo(ctx, server.LocalAddr().String())
	if err != nil {
		t.Fatalf("QueryInfo failed: %v", err)
	}

	if info == nil {
		t.Fatal("QueryInfo returned nil Info")
	}
	if info.Name != "Test Server" {
		t.Errorf("got name %q, want %q", info.Name, "Test Server")
	}
}

// TestQueryPlayersChallenge tests A2S_PLAYER with the challenge flow.
// The server must perform the two-step flow: reply with S2C_CHALLENGE to the
// first player request, then verify the client echoes exactly that challenge.
func TestQueryPlayersChallenge(t *testing.T) {
	// The challenge the server will send to the client.
	serverChallenge := []byte{0x78, 0x56, 0x34, 0x12}

	infoCount := 0
	playerRequestCount := 0
	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}

		switch req[4] {
		case requestInfo:
			infoCount++
			// Send immediate info response (no challenge for this test).
			return buildFakeInfoResponse()

		case requestPlayer:
			playerRequestCount++
			if playerRequestCount == 1 {
				// First player request: send a challenge response.
				var buf bytes.Buffer
				buf.WriteByte(0xFF)
				buf.WriteByte(0xFF)
				buf.WriteByte(0xFF)
				buf.WriteByte(0xFF)
				buf.WriteByte(responseChallenge)
				buf.Write(serverChallenge)
				return buf.Bytes()
			}
			// Second player request: verify the challenge matches exactly.
			if len(req) < 9 {
				t.Errorf("second player request too short: expected >= 9 bytes, got %d", len(req))
				return nil
			}
			clientChallenge := req[5:9]
			if !bytes.Equal(clientChallenge, serverChallenge) {
				t.Errorf("client echoed wrong challenge: got %v, want %v", clientChallenge, serverChallenge)
				return nil
			}
			// Challenge matches; return player list.
			return buildFakePlayerResponse()

		default:
			return nil
		}
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	players, err := QueryPlayers(ctx, server.LocalAddr().String())
	if err != nil {
		t.Fatalf("QueryPlayers failed: %v", err)
	}

	if len(players) != 3 {
		t.Errorf("got %d players, want 3", len(players))
	}

	if len(players) > 0 {
		if players[0].Name != "Alice" {
			t.Errorf("got first player name %q, want %q", players[0].Name, "Alice")
		}
		if players[0].Score != 100 {
			t.Errorf("got first player score %d, want 100", players[0].Score)
		}
	}

	if len(players) > 1 {
		if players[1].Name != "Bob" {
			t.Errorf("got second player name %q, want %q", players[1].Name, "Bob")
		}
	}

	if len(players) > 2 {
		if players[2].Name != "Charlie" {
			t.Errorf("got third player name %q, want %q", players[2].Name, "Charlie")
		}
	}

	if playerRequestCount != 2 {
		t.Errorf("expected 2 player requests, got %d", playerRequestCount)
	}
}

// TestQueryInfoRequestFormat verifies that A2S_INFO requests include the required
// "Source Engine Query\0" magic string.
func TestQueryInfoRequestFormat(t *testing.T) {
	requestSeen := false
	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil
		}

		requestSeen = true
		// Verify the magic string is present at bytes 5-24 (25 bytes total).
		expectedMagic := "Source Engine Query\x00"
		if len(req) < 5+len(expectedMagic) {
			t.Errorf("A2S_INFO request too short: got %d bytes, expected at least %d", len(req), 5+len(expectedMagic))
			return nil
		}
		if string(req[5:5+len(expectedMagic)]) != expectedMagic {
			t.Errorf("A2S_INFO request missing magic string: got %v, want %q", req[5:5+len(expectedMagic)], expectedMagic)
		}

		return buildFakeInfoResponse()
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := QueryInfo(ctx, server.LocalAddr().String())
	if err != nil {
		t.Fatalf("QueryInfo failed: %v", err)
	}

	if !requestSeen {
		t.Error("server never received an A2S_INFO request")
	}
}

// TestTruncatedResponse tests that a truncated response produces an error.
func TestTruncatedResponse(t *testing.T) {
	t.Parallel()

	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil
		}
		// Send a truncated response (just header + type, no data).
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint32(headerFourCC))
		buf.WriteByte(responseInfo)
		return buf.Bytes()
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := QueryInfo(ctx, server.LocalAddr().String())
	if err == nil {
		t.Fatal("QueryInfo should have failed on truncated response")
	}
}

// TestWrongHeader tests that a response with an invalid header produces an error.
func TestWrongHeader(t *testing.T) {
	t.Parallel()

	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil
		}
		// Send response with wrong header.
		var buf bytes.Buffer
		binary.Write(&buf, binary.LittleEndian, uint32(0xDEADBEEF))
		buf.WriteByte(responseInfo)
		return buf.Bytes()
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := QueryInfo(ctx, server.LocalAddr().String())
	if err == nil {
		t.Fatal("QueryInfo should have failed on wrong header")
	}
}

// TestDeadlineExceeded tests that a server that never responds produces a deadline error.
func TestDeadlineExceeded(t *testing.T) {
	t.Parallel()

	server := startFakeA2SServer(t, func(_ []byte, _ net.Addr) []byte {
		// Send nothing; let the client timeout.
		return nil
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	_, err := QueryInfo(ctx, server.LocalAddr().String())
	if err == nil {
		t.Fatal("QueryInfo should have timed out")
	}
}

// TestGarbageData tests that garbage data doesn't cause a panic.
func TestGarbageData(t *testing.T) {
	t.Parallel()

	server := startFakeA2SServer(t, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil
		}
		// Send garbage response.
		return []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x49, 0x00, 0xFF, 0xFE}
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err := QueryInfo(ctx, server.LocalAddr().String())
	// We expect an error parsing the garbage; ensure it's not a panic.
	if err == nil {
		t.Logf("Warning: QueryInfo did not error on garbage data (may have parsed it)")
	}
}

// ---- helper functions ----

// fakeA2SServer is a simple UDP server that runs in a goroutine and calls a
// handler function for each request.
type fakeA2SServer struct {
	conn net.PacketConn
}

func (s *fakeA2SServer) LocalAddr() net.Addr {
	return s.conn.LocalAddr()
}

func (s *fakeA2SServer) Close() error {
	return s.conn.Close()
}

// startFakeA2SServer starts a UDP server on 127.0.0.1:0 that calls the
// handler for each received datagram. The handler should return a response
// to send back (or nil to send nothing). The server runs until the test
// completes or the connection is closed.
func startFakeA2SServer(t *testing.T, handler func(req []byte, raddr net.Addr) []byte) *fakeA2SServer {
	lc := net.ListenConfig{}
	conn, err := lc.ListenPacket(t.Context(), "udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to start fake server: %v", err)
	}

	server := &fakeA2SServer{conn: conn}

	go func() {
		defer conn.Close()

		for {
			buf := make([]byte, 4096)
			// Set deadline per read to avoid blocking forever if no request comes.
			_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

			n, raddr, err := conn.ReadFrom(buf)
			if err != nil {
				return // Server closed, deadline, or read error.
			}

			resp := handler(buf[:n], raddr)
			if resp != nil {
				// Send response with a short deadline.
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = conn.WriteTo(resp, raddr)
			}
		}
	}()

	// Give the server time to start listening and be ready to receive.
	time.Sleep(50 * time.Millisecond)

	return server
}

// startFakeA2SServerOnAddr starts a UDP server on the specified IP address
// (with port 0 for auto-assignment). This is used for regression testing to
// ensure the client doesn't bind to loopback when connecting to a non-loopback
// server address.
func startFakeA2SServerOnAddr(t *testing.T, addr string, handler func(req []byte, raddr net.Addr) []byte) *fakeA2SServer {
	lc := net.ListenConfig{}
	conn, err := lc.ListenPacket(t.Context(), "udp4", addr+":0")
	if err != nil {
		t.Fatalf("failed to start fake server on %s: %v", addr, err)
	}

	server := &fakeA2SServer{conn: conn}

	go func() {
		defer conn.Close()

		for {
			buf := make([]byte, 4096)
			_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

			n, raddr, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}

			resp := handler(buf[:n], raddr)
			if resp != nil {
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				_, _ = conn.WriteTo(resp, raddr)
			}
		}
	}()

	time.Sleep(50 * time.Millisecond)

	return server
}

// findNonLoopbackIPv4 searches for a non-loopback IPv4 address on any local
// interface. Returns an empty string if none is found.
func findNonLoopbackIPv4() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}

	for _, addr := range addrs {
		ipnet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}

		ip := ipnet.IP
		// Skip loopback, IPv6, and non-unicast addresses.
		if ip.IsLoopback() || ip.To4() == nil {
			continue
		}

		return ip.String()
	}

	return ""
}

// buildFakeInfoResponse constructs a valid A2S_INFO response.
// Fields are laid out according to the Valve Developer Community spec:
// https://developer.valvesoftware.com/wiki/Server_queries
func buildFakeInfoResponse() []byte {
	var buf bytes.Buffer

	// 0xFFFFFFFF header (little-endian)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	// 0x49 (I) type
	buf.WriteByte(responseInfo)
	// Protocol version (17 for Source engine)
	buf.WriteByte(17)
	// Server name (null-terminated)
	buf.WriteString("Test Server\x00")
	// Map name (null-terminated)
	buf.WriteString("de_dust2\x00")
	// Folder (game directory, null-terminated)
	buf.WriteString("csgo\x00")
	// Game description (null-terminated)
	buf.WriteString("Counter-Strike: Global Offensive\x00")
	// App ID (uint16 LE) — CS:GO is 730
	buf.WriteByte(0xDA) // 730 in LE = 0xDA 0x02
	buf.WriteByte(0x02)
	// Players online
	buf.WriteByte(5)
	// Max players
	buf.WriteByte(20)
	// Bots
	buf.WriteByte(0)
	// Server type ('d' = dedicated, 'l' = listen, 'p' = SourceTV proxy)
	buf.WriteByte('d')
	// Environment ('l' = Linux, 'w' = Windows, 'm' = Mac)
	buf.WriteByte('l')
	// Visibility (0 = public, 1 = private)
	buf.WriteByte(0)
	// VAC secured (0 = no, 1 = yes)
	buf.WriteByte(1)
	// Version string (null-terminated)
	buf.WriteString("1.0.0.0\x00")

	return buf.Bytes()
}

// buildChallengeResponse constructs a valid S2C_CHALLENGE response.
func buildChallengeResponse() []byte {
	var buf bytes.Buffer
	// 0xFFFFFFFF header (LE)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	// Challenge type
	buf.WriteByte(responseChallenge)
	// 4-byte challenge (0x12345678 in LE = 0x78 0x56 0x34 0x12)
	buf.WriteByte(0x78)
	buf.WriteByte(0x56)
	buf.WriteByte(0x34)
	buf.WriteByte(0x12)
	return buf.Bytes()
}

// buildFakePlayerResponse constructs a valid A2S_PLAYER response with three players.
// Fields according to spec: player count (1 byte), then for each player:
//
//	index (1 byte), name (null-terminated), score (int32 LE), duration (float32 LE)
func buildFakePlayerResponse() []byte {
	var buf bytes.Buffer

	// 0xFFFFFFFF header (LE)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	buf.WriteByte(0xFF)
	// 0x44 (D) type
	buf.WriteByte(responsePlayer)
	// Player count
	buf.WriteByte(3)

	// Player 1: Alice
	buf.WriteByte(0) // index
	buf.WriteString("Alice\x00")
	// Score 100 (int32 LE)
	buf.WriteByte(0x64)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	// Duration 1234.5 (float32 LE) — 0x4434A640 as IEEE 754
	buf.WriteByte(0x40)
	buf.WriteByte(0xA6)
	buf.WriteByte(0x34)
	buf.WriteByte(0x44)

	// Player 2: Bob
	buf.WriteByte(1) // index
	buf.WriteString("Bob\x00")
	// Score 85 (int32 LE)
	buf.WriteByte(0x55)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	// Duration 987.25 (float32 LE) — 0x4477C640 as IEEE 754
	buf.WriteByte(0x40)
	buf.WriteByte(0xC6)
	buf.WriteByte(0x77)
	buf.WriteByte(0x44)

	// Player 3: Charlie
	buf.WriteByte(2) // index
	buf.WriteString("Charlie\x00")
	// Score 45 (int32 LE)
	buf.WriteByte(0x2D)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	buf.WriteByte(0x00)
	// Duration 567.75 (float32 LE) — 0x4408C840 as IEEE 754
	buf.WriteByte(0x40)
	buf.WriteByte(0xC8)
	buf.WriteByte(0x08)
	buf.WriteByte(0x44)

	return buf.Bytes()
}

// TestParseInfoMinimal tests parseInfo with a minimal valid response.
func TestParseInfoMinimal(t *testing.T) {
	t.Parallel()

	resp := buildFakeInfoResponse()
	info, err := parseInfo(resp)
	if err != nil {
		t.Fatalf("parseInfo failed: %v", err)
	}
	if info == nil {
		t.Fatal("parseInfo returned nil")
	}
	if info.Protocol != 17 {
		t.Errorf("got protocol %d, want 17", info.Protocol)
	}
	if info.Name != "Test Server" {
		t.Errorf("got name %q, want %q", info.Name, "Test Server")
	}
}

// TestParsePlayersList tests parsePlayer with a valid player response.
func TestParsePlayersList(t *testing.T) {
	t.Parallel()

	resp := buildFakePlayerResponse()
	players, err := parsePlayers(resp)
	if err != nil {
		t.Fatalf("parsePlayers failed: %v", err)
	}
	if len(players) != 3 {
		t.Errorf("got %d players, want 3", len(players))
	}
	if len(players) > 0 && players[0].Name != "Alice" {
		t.Errorf("got first player %q, want Alice", players[0].Name)
	}
}

// TestQueryInfoNonLoopbackServer verifies that QueryInfo works when the server
// peer is on a non-loopback address (e.g., 192.168.1.1:port) rather than
// 127.0.0.1. This test does NOT catch a loopback-only bind regression.
// Empirically, reintroducing net.Dialer{LocalAddr: &net.UDPAddr{IP: net.IPv4(127,0,0,1)}}
// does not cause this test to fail — all addresses on the same host are locally
// reachable, so a loopback-bound socket can still reach any local interface.
// The actual regression guard for this bug class is the e2e tier: the e2e game
// bot CI job dials a real Kubernetes Service ClusterIP (an off-host, routed
// destination) and did catch this bug once in production.
// See: https://github.com/GameplanePanel/gameplane/issues/197
func TestQueryInfoNonLoopbackServer(t *testing.T) {
	// Find a non-loopback IPv4 address on the host.
	nonLoopbackAddr := findNonLoopbackIPv4()
	if nonLoopbackAddr == "" {
		t.Skip("no non-loopback IPv4 address found on this host")
	}

	server := startFakeA2SServerOnAddr(t, nonLoopbackAddr, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}
		if req[4] != requestInfo {
			return nil
		}
		return buildFakeInfoResponse()
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	info, err := QueryInfo(ctx, server.LocalAddr().String())
	if err != nil {
		t.Fatalf("QueryInfo failed: %v", err)
	}

	if info == nil {
		t.Fatal("QueryInfo returned nil Info")
	}
	if info.Name != "Test Server" {
		t.Errorf("got name %q, want %q", info.Name, "Test Server")
	}
}

// TestQueryPlayersNonLoopbackServer verifies that QueryPlayers works when the
// server peer is on a non-loopback address. This test does NOT catch a loopback-only
// bind regression — see TestQueryInfoNonLoopbackServer's comment for why. The
// actual regression guard is the e2e tier, which dials a real Service ClusterIP.
func TestQueryPlayersNonLoopbackServer(t *testing.T) {
	// Find a non-loopback IPv4 address on the host.
	nonLoopbackAddr := findNonLoopbackIPv4()
	if nonLoopbackAddr == "" {
		t.Skip("no non-loopback IPv4 address found on this host")
	}

	serverChallenge := []byte{0x78, 0x56, 0x34, 0x12}
	infoCount := 0
	playerRequestCount := 0

	server := startFakeA2SServerOnAddr(t, nonLoopbackAddr, func(req []byte, _ net.Addr) []byte {
		if len(req) < 5 || binary.LittleEndian.Uint32(req[:4]) != headerFourCC {
			return nil
		}

		switch req[4] {
		case requestInfo:
			infoCount++
			return buildFakeInfoResponse()

		case requestPlayer:
			playerRequestCount++
			if playerRequestCount == 1 {
				var buf bytes.Buffer
				buf.WriteByte(0xFF)
				buf.WriteByte(0xFF)
				buf.WriteByte(0xFF)
				buf.WriteByte(0xFF)
				buf.WriteByte(responseChallenge)
				buf.Write(serverChallenge)
				return buf.Bytes()
			}
			if len(req) < 9 {
				return nil
			}
			clientChallenge := req[5:9]
			if !bytes.Equal(clientChallenge, serverChallenge) {
				return nil
			}
			return buildFakePlayerResponse()

		default:
			return nil
		}
	})
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	players, err := QueryPlayers(ctx, server.LocalAddr().String())
	if err != nil {
		t.Fatalf("QueryPlayers failed: %v", err)
	}

	if len(players) != 3 {
		t.Errorf("got %d players, want 3", len(players))
	}
	if len(players) > 0 && players[0].Name != "Alice" {
		t.Errorf("got first player name %q, want %q", players[0].Name, "Alice")
	}
}
