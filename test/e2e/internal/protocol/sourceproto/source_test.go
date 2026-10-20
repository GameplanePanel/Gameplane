package sourceproto

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"testing"
	"time"
)

// TestChallenge_RoundTrip tests a successful A2S_GETCHALLENGE exchange.
func TestChallenge_RoundTrip(t *testing.T) {
	t.Parallel()

	// Start a fake UDP server.
	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	// Server that responds to challenge requests.
	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		n, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return // server closed
		}

		// Verify we got a challenge request.
		if n < 5 {
			return
		}
		if binary.LittleEndian.Uint32(buf[0:4]) != connlessHeader {
			return
		}
		if buf[4] != pktChallengeReq {
			return
		}

		// Send challenge response: 0xFFFFFFFF + 'A' + challenge (e.g., 0x12345678).
		challengeNum := uint32(0x12345678)
		resp := make([]byte, 9)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = pktChallengeResp
		binary.LittleEndian.PutUint32(resp[5:9], challengeNum)
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	challenge, err := Challenge(ctx, addr)
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if challenge != 0x12345678 {
		t.Errorf("challenge: got 0x%08x, want 0x12345678", challenge)
	}
}

// TestChallenge_BadHeader tests that a response with a bad header is rejected.
func TestChallenge_BadHeader(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send response with wrong header.
		resp := make([]byte, 9)
		binary.LittleEndian.PutUint32(resp[0:4], 0xDEADBEEF) // wrong
		resp[4] = pktChallengeResp
		binary.LittleEndian.PutUint32(resp[5:9], 0x11223344)
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = Challenge(ctx, addr)
	if err == nil {
		t.Error("Challenge: expected error for bad header, got nil")
	}
}

// TestChallenge_BadPacketType tests rejection of a response with wrong packet type.
func TestChallenge_BadPacketType(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send response with wrong packet type.
		resp := make([]byte, 9)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = 'X' // wrong type
		binary.LittleEndian.PutUint32(resp[5:9], 0x11223344)
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = Challenge(ctx, addr)
	if err == nil {
		t.Error("Challenge: expected error for wrong packet type, got nil")
	}
}

// TestChallenge_TruncatedResponse tests that a truncated response is handled.
func TestChallenge_TruncatedResponse(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send truncated response (only 5 bytes instead of 9).
		resp := make([]byte, 5)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = pktChallengeResp
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = Challenge(ctx, addr)
	if err == nil {
		t.Error("Challenge: expected error for truncated response, got nil")
	}
}

// TestConnect_Accepted tests a successful connection acceptance.
func TestConnect_Accepted(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	serverChallenge := uint32(0x11223344)

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		n, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Verify we got a connect request.
		if n < 5 || binary.LittleEndian.Uint32(buf[0:4]) != connlessHeader {
			return
		}
		if buf[4] != pktConnectReq {
			return
		}

		// Send acceptance: 0xFFFFFFFF + 0x03 (new client).
		resp := make([]byte, 5)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = pktAccept
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, serverChallenge, "TestPlayer", ProtocolSource1)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !result.Accepted {
		t.Errorf("Connect.Accepted: got false, want true")
	}
	if result.RejectMsg != "" {
		t.Errorf("Connect.RejectMsg: got %q, want empty", result.RejectMsg)
	}
}

// TestConnect_RejectedWithMessage tests a connection rejection with a reason.
func TestConnect_RejectedWithMessage(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	serverChallenge := uint32(0xAABBCCDD)
	rejectMsg := "server is full"

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send rejection: 0xFFFFFFFF + 0x63 (disconnect) + reason string + null.
		resp := make([]byte, 0, 64)
		resp = append(resp, 0xFF, 0xFF, 0xFF, 0xFF) // header
		resp = append(resp, pktDisconnect)          // 0x63
		resp = append(resp, []byte(rejectMsg)...)
		resp = append(resp, 0x00) // null terminator
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, serverChallenge, "TestPlayer", ProtocolSource1)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if result.Accepted {
		t.Errorf("Connect.Accepted: got true, want false")
	}
	if result.RejectMsg != rejectMsg {
		t.Errorf("Connect.RejectMsg: got %q, want %q", result.RejectMsg, rejectMsg)
	}
}

// TestConnect_RejectedNoMessage tests rejection without a message string.
func TestConnect_RejectedNoMessage(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send rejection with no message: just 0xFFFFFFFF + 0x63.
		resp := []byte{0xFF, 0xFF, 0xFF, 0xFF, pktDisconnect}
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, 0x00000000, "Bot", ProtocolSource2)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if result.Accepted {
		t.Errorf("Connect.Accepted: got true, want false")
	}
	if result.RejectMsg != "" {
		t.Errorf("Connect.RejectMsg: got %q, want empty", result.RejectMsg)
	}
}

// TestConnect_TruncatedResponse tests handling of a truncated response.
func TestConnect_TruncatedResponse(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send a too-short response (only 2 bytes).
		resp := []byte{0xFF, 0xFF}
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, 0x00000000, "Bot", ProtocolSource1)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	// Truncated response should not panic; it's an inconclusive result.
	if result == nil {
		t.Fatal("Connect returned nil result")
	}
}

// TestConnect_GarbageResponse tests that garbage input doesn't panic.
func TestConnect_GarbageResponse(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send garbage (all zeros, except wrong header).
		resp := make([]byte, 16)
		resp[0] = 0x00
		resp[1] = 0x00
		resp[2] = 0x00
		resp[3] = 0x00
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, 0x00000000, "Bot", ProtocolSource1)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if result == nil {
		t.Fatal("Connect returned nil result")
	}
	// Garbage response should not match any known packet type; neither
	// accepted nor rejected.
	if result.Accepted {
		t.Errorf("Connect.Accepted: got true, want false (inconclusive result)")
	}
}

// TestConnect_PayloadIntegrity verifies the player name and challenge are
// included in the outgoing request packet.
func TestConnect_PayloadIntegrity(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	expectedChallenge := uint32(0xDEADBEEF)
	expectedName := "MyBot"
	expectedProto := ProtocolSource2

	var capturedReq []byte
	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		n, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		capturedReq = buf[:n]

		// Send acceptance so the test doesn't hang.
		resp := make([]byte, 5)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = pktAccept
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, err = Connect(ctx, addr, expectedChallenge, expectedName, expectedProto)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Verify the request structure.
	if len(capturedReq) < 18 { // 4 (header) + 1 (type) + 4 (challenge) + 4 (proto) + 4 (auth) + name + nulls
		t.Fatalf("captured request too short: %d bytes", len(capturedReq))
	}

	// Check header and packet type.
	if binary.LittleEndian.Uint32(capturedReq[0:4]) != connlessHeader {
		t.Errorf("header mismatch in request")
	}
	if capturedReq[4] != pktConnectReq {
		t.Errorf("packet type mismatch: got 0x%02x, want 0x%02x", capturedReq[4], pktConnectReq)
	}

	// Check challenge (offset 5-9).
	recvChallenge := binary.LittleEndian.Uint32(capturedReq[5:9])
	if recvChallenge != expectedChallenge {
		t.Errorf("challenge mismatch: got 0x%08x, want 0x%08x", recvChallenge, expectedChallenge)
	}

	// Check protocol version (offset 9-13).
	recvProto := binary.LittleEndian.Uint32(capturedReq[9:13])
	if recvProto != expectedProto {
		t.Errorf("protocol mismatch: got %d, want %d", recvProto, expectedProto)
	}

	// Check auth protocol (offset 13-17); should be 0 for LAN.
	recvAuth := binary.LittleEndian.Uint32(capturedReq[13:17])
	if recvAuth != 0 {
		t.Errorf("auth protocol mismatch: got %d, want 0", recvAuth)
	}

	// Check player name (offset 17+).
	nameEnd := bytes.IndexByte(capturedReq[17:], 0x00)
	if nameEnd == -1 {
		t.Fatalf("player name not null-terminated in request")
	}
	recvName := string(capturedReq[17 : 17+nameEnd])
	if recvName != expectedName {
		t.Errorf("player name mismatch: got %q, want %q", recvName, expectedName)
	}
}

// TestConnect_ProtocolVersions tests that both Source 1 and Source 2 protocol
// versions can be used.
func TestConnect_ProtocolVersions(t *testing.T) {
	t.Parallel()

	for _, proto := range []uint32{ProtocolSource1, ProtocolSource2} {
		t.Run(fmt.Sprintf("protocol_%d", proto), func(t *testing.T) {
			lc := net.ListenConfig{}
			listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("listen: %v", err)
			}
			defer listener.Close()

			addr := listener.LocalAddr().String()

			go func() {
				defer listener.Close()
				buf := make([]byte, 1024)
				_, remoteAddr, err := listener.ReadFrom(buf)
				if err != nil {
					return
				}

				// Echo: accept any protocol version.
				resp := make([]byte, 5)
				binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
				resp[4] = pktAccept
				listener.WriteTo(resp, remoteAddr)
			}()

			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			result, err := Connect(ctx, addr, 0x11223344, "Bot", proto)
			if err != nil {
				t.Fatalf("Connect(proto=%d): %v", proto, err)
			}
			if !result.Accepted {
				t.Errorf("Connect(proto=%d): not accepted", proto)
			}
		})
	}
}

// TestConnect_MeasuredVersionRejection_0x39 tests rejection response type 0x39
// as measured against a real ceifa/garrysmod:debian server (PR #197, 2026-07-24).
// This is the exact payload the server sent when protocol version 17 was rejected.
// The server identified "game#GameUI_ServerRejectOldVersion" as the reason.
func TestConnect_MeasuredVersionRejection_0x39(t *testing.T) {
	t.Parallel()

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send the exact payload observed from a real Garry's Mod server.
		// Hex breakdown:
		//   ff ff ff ff           - connectionless header (0xFFFFFFFF, LE)
		//   39                    - response type 0x39 (version rejection)
		//   67 61 6d 65           - "game" (literal string)
		//   23                    - '#' (separator)
		//   47 61 6d 65 55 49 ... - "GameUI_ServerRejectOldVersion" (localization token)
		//   00                    - NUL terminator
		resp := []byte{
			0xFF, 0xFF, 0xFF, 0xFF,
			0x39,
			'g', 'a', 'm', 'e',
			'#',
			'G', 'a', 'm', 'e', 'U', 'I', '_', 'S', 'e', 'r', 'v', 'e', 'r',
			'R', 'e', 'j', 'e', 'c', 't', 'O', 'l', 'd', 'V', 'e', 'r', 's', 'i', 'o', 'n',
			0x00,
		}
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, 0x11223344, "Bot", ProtocolSource1)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Verify rejection was detected.
	if result.Accepted {
		t.Errorf("Connect.Accepted: got true, want false")
	}

	// Verify the rejection message was extracted correctly.
	expectedMsg := "game#GameUI_ServerRejectOldVersion"
	if result.RejectMsg != expectedMsg {
		t.Errorf("Connect.RejectMsg: got %q, want %q", result.RejectMsg, expectedMsg)
	}

	// Verify raw bytes are preserved for diagnosis.
	if result.Raw == nil {
		t.Error("Connect.Raw: expected raw bytes, got nil")
	}
}

// TestChallenge_NonLoopbackServer verifies that Challenge works when the server
// peer is on a non-loopback address. This test does NOT catch a loopback-only
// bind regression. Empirically, reintroducing net.Dialer{LocalAddr: &net.UDPAddr{IP: net.IPv4(127,0,0,1)}}
// does not cause this test to fail — all addresses on the same host are locally
// reachable, so a loopback-bound socket can still reach any local interface.
// The actual regression guard for this bug class is the e2e tier: the e2e game
// bot CI job dials a real Kubernetes Service ClusterIP (an off-host, routed
// destination) and did catch this bug once in production.
// See: https://github.com/GameplanePanel/gameplane/issues/197
func TestChallenge_NonLoopbackServer(t *testing.T) {
	// Find a non-loopback IPv4 address on the host.
	nonLoopbackAddr := findNonLoopbackIPv4Source()
	if nonLoopbackAddr == "" {
		t.Skip("no non-loopback IPv4 address found on this host")
	}

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp4", nonLoopbackAddr+":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send challenge response.
		challengeNum := uint32(0xDEADBEEF)
		resp := make([]byte, 9)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = pktChallengeResp
		binary.LittleEndian.PutUint32(resp[5:9], challengeNum)
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	challenge, err := Challenge(ctx, addr)
	if err != nil {
		t.Fatalf("Challenge: %v", err)
	}
	if challenge != 0xDEADBEEF {
		t.Errorf("challenge: got 0x%08x, want 0xDEADBEEF", challenge)
	}
}

// TestConnect_NonLoopbackServer verifies that Connect works when the server
// peer is on a non-loopback address. This test does NOT catch a loopback-only
// bind regression — see TestChallenge_NonLoopbackServer's comment for why. The
// actual regression guard is the e2e tier, which dials a real Service ClusterIP.
func TestConnect_NonLoopbackServer(t *testing.T) {
	// Find a non-loopback IPv4 address on the host.
	nonLoopbackAddr := findNonLoopbackIPv4Source()
	if nonLoopbackAddr == "" {
		t.Skip("no non-loopback IPv4 address found on this host")
	}

	lc := net.ListenConfig{}
	listener, err := lc.ListenPacket(t.Context(), "udp4", nonLoopbackAddr+":0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.LocalAddr().String()

	go func() {
		defer listener.Close()
		buf := make([]byte, 1024)
		_, remoteAddr, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}

		// Send acceptance.
		resp := make([]byte, 5)
		binary.LittleEndian.PutUint32(resp[0:4], connlessHeader)
		resp[4] = pktAccept
		listener.WriteTo(resp, remoteAddr)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	result, err := Connect(ctx, addr, 0x11223344, "TestBot", ProtocolSource1)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	if !result.Accepted {
		t.Errorf("Connect.Accepted: got false, want true")
	}
}

// findNonLoopbackIPv4Source searches for a non-loopback IPv4 address on any
// local interface. Returns an empty string if none is found. This is the source
// package version of the helper.
func findNonLoopbackIPv4Source() string {
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
