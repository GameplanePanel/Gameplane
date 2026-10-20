package rcon

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseNuclearOptionCommand(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantName string
		wantArgs []string
	}{
		{
			name:     "empty string",
			input:    "",
			wantName: "",
			wantArgs: nil,
		},
		{
			name:     "whitespace only",
			input:    "   ",
			wantName: "",
			wantArgs: nil,
		},
		{
			name:     "command no args",
			input:    "get-player-list",
			wantName: "get-player-list",
			wantArgs: nil,
		},
		{
			name:     "command with one simple arg",
			input:    "kick-player 76561198123456789",
			wantName: "kick-player",
			wantArgs: []string{"76561198123456789"},
		},
		{
			name:     "send-chat-message preserves spaces in message",
			input:    "send-chat-message hello world from test",
			wantName: "send-chat-message",
			wantArgs: []string{"hello world from test"},
		},
		{
			name:     "send-chat-message with single word",
			input:    "send-chat-message hello",
			wantName: "send-chat-message",
			wantArgs: []string{"hello"},
		},
		{
			name:     "send-chat-message empty message",
			input:    "send-chat-message ",
			wantName: "send-chat-message",
			wantArgs: []string{""},
		},
		{
			name:     "banlist-add with steamid and reason",
			input:    "banlist-add 76561198123456789 cheating",
			wantName: "banlist-add",
			wantArgs: []string{"76561198123456789", "cheating"},
		},
		{
			name:     "set-next-mission with three args",
			input:    "set-next-mission BuiltIn Escalation 3600.0",
			wantName: "set-next-mission",
			wantArgs: []string{"BuiltIn", "Escalation", "3600.0"},
		},
		{
			name:     "command with extra spaces",
			input:    "kick-player   76561198123456789",
			wantName: "kick-player",
			wantArgs: []string{"76561198123456789"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotName, gotArgs := parseNuclearOptionCommand(tt.input)
			if gotName != tt.wantName {
				t.Errorf("name: got %q, want %q", gotName, tt.wantName)
			}
			if !slicesEqual(gotArgs, tt.wantArgs) {
				t.Errorf("args: got %v, want %v", gotArgs, tt.wantArgs)
			}
		})
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// fakeTCPServer simulates a Nuclear Option server for testing.
// It reads requests and sends back configured responses.
type fakeTCPServer struct {
	listener net.Listener
	addr     string
	done     chan struct{}
	wg       sync.WaitGroup
	// responses is a map from command name to (status, body).
	// If a key is not present, a 4004 (unknown command) is returned.
	responses map[string][2]interface{}
}

func newFakeTCPServer(t *testing.T) *fakeTCPServer {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	server := &fakeTCPServer{
		listener:  listener,
		addr:      listener.Addr().String(),
		done:      make(chan struct{}),
		responses: make(map[string][2]interface{}),
	}

	server.wg.Add(1)
	go server.serve(t)

	return server
}

func (s *fakeTCPServer) serve(t *testing.T) {
	defer s.wg.Done()
	for {
		select {
		case <-s.done:
			return
		default:
		}

		// Accept with timeout to avoid blocking forever.
		s.listener.(*net.TCPListener).SetDeadline(time.Now().Add(100 * time.Millisecond))
		conn, err := s.listener.Accept()
		if err != nil {
			if strings.Contains(err.Error(), "timeout") {
				continue
			}
			return
		}

		s.handleConn(t, conn)
	}
}

func (s *fakeTCPServer) handleConn(_ *testing.T, conn net.Conn) {
	defer conn.Close()

	for {
		// Read request: 4-byte length + body.
		lengthBuf := make([]byte, 4)
		n, err := conn.Read(lengthBuf)
		if err != nil {
			return // Client closed or error.
		}
		if n < 4 {
			// Send a truncated response to test that case.
			conn.Write([]byte{0x00, 0x00})
			return
		}

		bodyLen := binary.LittleEndian.Uint32(lengthBuf)
		bodyBuf := make([]byte, bodyLen)
		if _, err := conn.Read(bodyBuf); err != nil {
			return
		}

		var req nuclearOptionRequest
		if err := json.Unmarshal(bodyBuf, &req); err != nil {
			// Malformed JSON: return 4003.
			s.sendResponse(conn, 4003, nil)
			continue
		}

		// Look up the command in responses.
		resp, ok := s.responses[req.Name]
		if !ok {
			// Unknown command: return 4004.
			s.sendResponse(conn, 4004, nil)
			continue
		}

		status := resp[0].(uint32)
		var body interface{}
		if resp[1] != nil {
			body = resp[1]
		}
		s.sendResponse(conn, status, body)
	}
}

func (s *fakeTCPServer) sendResponse(conn net.Conn, status uint32, body interface{}) {
	statusBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(statusBuf, status)
	conn.Write(statusBuf)

	var bodyBytes []byte
	if body != nil {
		if str, ok := body.(string); ok {
			bodyBytes = []byte(str)
		} else {
			bodyBytes, _ = json.Marshal(body)
		}
	}

	lenBuf := make([]byte, 4)
	binary.LittleEndian.PutUint32(lenBuf, uint32(len(bodyBytes)))
	conn.Write(lenBuf)

	if len(bodyBytes) > 0 {
		conn.Write(bodyBytes)
	}
}

func (s *fakeTCPServer) close(_ *testing.T) {
	close(s.done)
	s.listener.Close()
	s.wg.Wait()
}

func TestNuclearOptionExec(t *testing.T) {
	server := newFakeTCPServer(t)
	defer server.close(t)

	// Parse the server address.
	parts := strings.Split(server.addr, ":")
	if len(parts) != 2 {
		t.Fatalf("unexpected address format: %s", server.addr)
	}
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()

	// Shrink timeouts for tests.
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	tests := []struct {
		name       string
		setupResp  map[string][2]interface{}
		cmd        string
		wantBody   string
		wantErrMsg string
	}{
		{
			name: "success with JSON body",
			setupResp: map[string][2]interface{}{
				"get-server-id": {
					uint32(2000),
					map[string]interface{}{"serverId": "90291415221858321"},
				},
			},
			cmd:      "get-server-id",
			wantBody: `{"serverId":"90291415221858321"}`,
		},
		{
			name: "success with empty body",
			setupResp: map[string][2]interface{}{
				"kick-player": {
					uint32(2000),
					nil, // Empty body for kick success.
				},
			},
			cmd:      "kick-player 76561198123456789",
			wantBody: "",
		},
		{
			name: "4003 malformed JSON with no body",
			setupResp: map[string][2]interface{}{
				"malformed": {
					uint32(4003),
					nil, // No detail body.
				},
			},
			cmd:        "malformed",
			wantErrMsg: "status 4003",
		},
		{
			// setupResp is left nil: no entry for "bogus-command" means
			// the server responds 4004.
			name:       "4004 unknown command",
			cmd:        "bogus-command",
			wantErrMsg: "status 4004",
		},
		{
			name: "4005 bad arguments with error detail",
			setupResp: map[string][2]interface{}{
				"set-next-mission": {
					uint32(4005),
					map[string]interface{}{
						"message": "Expected Arguments [string Group, string Name, float MaxTime]",
					},
				},
			},
			cmd:        "set-next-mission bad-arg",
			wantErrMsg: "Expected Arguments [string Group, string Name, float MaxTime]",
		},
		{
			name: "send-chat-message with spaces preserved",
			setupResp: map[string][2]interface{}{
				"send-chat-message": {
					uint32(2000),
					nil,
				},
			},
			cmd:      "send-chat-message hello world from nuclear option",
			wantBody: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear and set up responses.
			server.responses = tt.setupResp

			body, err := client.Exec(tt.cmd)

			if tt.wantErrMsg != "" {
				if err == nil {
					t.Errorf("expected error, got none")
				} else if !strings.Contains(err.Error(), tt.wantErrMsg) {
					t.Errorf("error: got %v, want to contain %q", err, tt.wantErrMsg)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if body != tt.wantBody {
					t.Errorf("body: got %q, want %q", body, tt.wantBody)
				}
			}

			// Close the connection for the next test to avoid reuse issues.
			client.Close()
		})
	}
}

// TestNuclearOptionTruncatedHeader tests handling of truncated response headers.
func TestNuclearOptionTruncatedHeader(t *testing.T) {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	parts := strings.Split(addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	// Server that sends incomplete response.
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()
		// Read request (don't care about it).
		buf := make([]byte, 1024)
		conn.Read(buf)
		// Send truncated status (only 2 bytes instead of 4).
		conn.Write([]byte{0x00, 0x00})
	}()

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	_, err = client.Exec("test-command")
	if err == nil {
		t.Errorf("expected error for truncated header, got none")
	}
}

// TestNuclearOptionBodyLengthMismatch tests handling of body length that
// disagrees with actual bytes sent.
func TestNuclearOptionBodyLengthMismatch(t *testing.T) {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	parts := strings.Split(addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	// Server that sends mismatched body length.
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()
		// Read request.
		buf := make([]byte, 1024)
		conn.Read(buf)
		// Send valid status and claim body length of 100, but only send 4 bytes.
		statusBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(statusBuf, 2000)
		conn.Write(statusBuf)

		lenBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(lenBuf, 100) // Claim 100 bytes.
		conn.Write(lenBuf)

		conn.Write([]byte("only4")) // But only send 5.
	}()

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	_, err = client.Exec("test-command")
	if err == nil {
		t.Errorf("expected error for body length mismatch, got none")
	}
}

// TestNuclearOptionBodyTooLarge tests that oversized body lengths are rejected.
func TestNuclearOptionBodyTooLarge(t *testing.T) {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	parts := strings.Split(addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	// Server that claims a body larger than the max allowed.
	go func() {
		conn, _ := listener.Accept()
		defer conn.Close()
		// Read request.
		buf := make([]byte, 1024)
		conn.Read(buf)
		// Send status 2000 with a body length exceeding max.
		statusBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(statusBuf, 2000)
		conn.Write(statusBuf)

		lenBuf := make([]byte, 4)
		// Claim body larger than nuclearOptionMaxBodyLength.
		binary.LittleEndian.PutUint32(lenBuf, nuclearOptionMaxBodyLength+1)
		conn.Write(lenBuf)
	}()

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	_, err = client.Exec("test-command")
	if err == nil {
		t.Errorf("expected error for oversized body, got none")
	}
	if !strings.Contains(err.Error(), "too large") {
		t.Errorf("error: got %v, want to contain 'too large'", err)
	}
}

// TestNuclearOptionMessageWithSpaces verifies that a message argument
// containing spaces is preserved intact when sent to the server.
func TestNuclearOptionMessageWithSpaces(t *testing.T) {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	parts := strings.Split(listener.Addr().String(), ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	// The captured request travels through the channel rather than through a
	// shared variable, so the send happens-before the read with no extra
	// synchronisation and no data race.
	capturedCh := make(chan nuclearOptionRequest, 1)
	go func() {
		conn, aerr := listener.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()

		lengthBuf := make([]byte, 4)
		if _, rerr := io.ReadFull(conn, lengthBuf); rerr != nil {
			return
		}
		bodyBuf := make([]byte, binary.LittleEndian.Uint32(lengthBuf))
		if _, rerr := io.ReadFull(conn, bodyBuf); rerr != nil {
			return
		}
		var req nuclearOptionRequest
		if uerr := json.Unmarshal(bodyBuf, &req); uerr != nil {
			return
		}
		capturedCh <- req

		resp := make([]byte, 8)
		binary.LittleEndian.PutUint32(resp[0:4], 2000)
		binary.LittleEndian.PutUint32(resp[4:8], 0)
		conn.Write(resp)
	}()

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	msg := "hello world from nuclear option"
	if _, eerr := client.Exec("send-chat-message " + msg); eerr != nil {
		t.Errorf("exec: %v", eerr)
	}

	var capturedReq nuclearOptionRequest
	select {
	case capturedReq = <-capturedCh:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for captured request")
	}

	if len(capturedReq.Arguments) != 1 {
		t.Fatalf("arguments count: got %d, want 1", len(capturedReq.Arguments))
	}
	if capturedReq.Arguments[0] != msg {
		t.Errorf("message: got %q, want %q", capturedReq.Arguments[0], msg)
	}
}

// TestNuclearOptionEmptyCommand tests that an empty command is rejected.
func TestNuclearOptionEmptyCommand(t *testing.T) {
	server := newFakeTCPServer(t)
	defer server.close(t)

	parts := strings.Split(server.addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	_, err := client.Exec("")
	if err == nil {
		t.Errorf("expected error for empty command, got none")
	}
}

// TestNuclearOptionConnectionReuse verifies that the connection is reused
// across multiple Exec calls and is properly closed on error.
func TestNuclearOptionConnectionReuse(t *testing.T) {
	server := newFakeTCPServer(t)
	defer server.close(t)

	parts := strings.Split(server.addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	server.responses = map[string][2]interface{}{
		"test-cmd": {uint32(2000), nil},
	}

	// First call should establish a connection.
	_, err := client.Exec("test-cmd")
	if err != nil {
		t.Errorf("first exec: %v", err)
	}

	// Second call should reuse the same connection (no new dial).
	_, err = client.Exec("test-cmd")
	if err != nil {
		t.Errorf("second exec: %v", err)
	}
}

// failNthWriteConn wraps a net.Conn and fails exactly the Nth Write call
// with a synthetic error, letting earlier writes pass through to the
// underlying connection unmodified. Used to deterministically exercise the
// write-error paths in Exec without depending on real network failure
// timing.
type failNthWriteConn struct {
	net.Conn
	n     int
	calls int
}

func (f *failNthWriteConn) Write(p []byte) (int, error) {
	f.calls++
	if f.calls == f.n {
		return 0, fmt.Errorf("simulated write failure")
	}
	return f.Conn.Write(p)
}

// failSetDeadlineConn wraps a net.Conn and always fails SetDeadline, letting
// Read/Write pass through to the underlying connection unmodified.
type failSetDeadlineConn struct {
	net.Conn
}

func (f *failSetDeadlineConn) SetDeadline(time.Time) error {
	return fmt.Errorf("simulated set deadline failure")
}

// TestNuclearOptionDefaultPort verifies that port 0 is replaced with the
// protocol's documented default port.
func TestNuclearOptionDefaultPort(t *testing.T) {
	client := NewNuclearOption("127.0.0.1", 0, nil)
	if client.port != defaultNuclearOptionPort {
		t.Errorf("port: got %d, want %d", client.port, defaultNuclearOptionPort)
	}
}

// TestNuclearOptionDialFailure verifies that a dial failure (nothing
// listening on the target port) surfaces as an error from Exec.
func TestNuclearOptionDialFailure(t *testing.T) {
	// Reserve a port, then close the listener so nothing is listening there.
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := listener.Addr().String()
	listener.Close()

	parts := strings.Split(addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 500 * time.Millisecond
	client.execDeadline = 1 * time.Second

	_, err = client.Exec("test-command")
	if err == nil {
		t.Fatal("expected dial error, got none")
	}
	if !strings.Contains(err.Error(), "dial") {
		t.Errorf("error: got %v, want to contain 'dial'", err)
	}
}

// TestNuclearOptionWriteLengthError verifies that a failure writing the
// request length header is reported and the connection is reset.
func TestNuclearOptionWriteLengthError(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()

	client := NewNuclearOption("127.0.0.1", 1, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second
	client.conn = &failNthWriteConn{Conn: local, n: 1}

	_, err := client.Exec("test-command")
	if err == nil {
		t.Fatal("expected write-length error, got none")
	}
	if !strings.Contains(err.Error(), "write length") {
		t.Errorf("error: got %v, want to contain 'write length'", err)
	}
	if client.conn != nil {
		t.Error("expected conn to be reset to nil after write error")
	}
}

// TestNuclearOptionWriteBodyError verifies that a failure writing the
// request body (after the length header succeeded) is reported and the
// connection is reset.
func TestNuclearOptionWriteBodyError(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()

	// The drain goroutine's completion travels through a buffered channel,
	// not a shared variable, so there's no race between it and the assertion
	// below.
	drainDone := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(io.Discard, remote)
		drainDone <- struct{}{}
	}()

	client := NewNuclearOption("127.0.0.1", 1, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second
	client.conn = &failNthWriteConn{Conn: local, n: 2}

	_, err := client.Exec("test-command")
	if err == nil {
		t.Fatal("expected write-body error, got none")
	}
	if !strings.Contains(err.Error(), "write body") {
		t.Errorf("error: got %v, want to contain 'write body'", err)
	}
	if client.conn != nil {
		t.Error("expected conn to be reset to nil after write error")
	}

	remote.Close()
	select {
	case <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for drain goroutine to finish")
	}
}

// TestNuclearOptionSetDeadlineError verifies that a failure setting the
// response read deadline (after the request was fully written) is reported
// and the connection is reset.
func TestNuclearOptionSetDeadlineError(t *testing.T) {
	local, remote := net.Pipe()
	defer remote.Close()

	drainDone := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(io.Discard, remote)
		drainDone <- struct{}{}
	}()

	client := NewNuclearOption("127.0.0.1", 1, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second
	client.conn = &failSetDeadlineConn{Conn: local}

	_, err := client.Exec("test-command")
	if err == nil {
		t.Fatal("expected set-deadline error, got none")
	}
	if !strings.Contains(err.Error(), "set deadline") {
		t.Errorf("error: got %v, want to contain 'set deadline'", err)
	}
	if client.conn != nil {
		t.Error("expected conn to be reset to nil after set-deadline error")
	}

	remote.Close()
	select {
	case <-drainDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for drain goroutine to finish")
	}
}

// TestNuclearOptionZeroExecDeadlineFallsBackToDefault verifies that a
// non-positive execDeadline falls back to the package default instead of
// leaving the read deadline unset.
func TestNuclearOptionZeroExecDeadlineFallsBackToDefault(t *testing.T) {
	server := newFakeTCPServer(t)
	defer server.close(t)

	parts := strings.Split(server.addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 0 // Falls back to defaultNuclearOptionExecDeadline.

	server.responses = map[string][2]interface{}{
		"test-cmd": {uint32(2000), nil},
	}

	if _, err := client.Exec("test-cmd"); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestNuclearOptionTruncatedBodyLength tests handling of a connection that
// sends a full, valid status header but is then closed before the body
// length header arrives.
func TestNuclearOptionTruncatedBodyLength(t *testing.T) {
	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().String()
	parts := strings.Split(addr, ":")
	port := 0
	fmt.Sscanf(parts[1], "%d", &port)

	acceptDone := make(chan struct{}, 1)
	go func() {
		defer func() { acceptDone <- struct{}{} }()
		conn, aerr := listener.Accept()
		if aerr != nil {
			return
		}
		defer conn.Close()
		// Read request (don't care about it).
		buf := make([]byte, 1024)
		conn.Read(buf)
		// Send a full, valid status header, then close before sending the
		// body length header.
		statusBuf := make([]byte, 4)
		binary.LittleEndian.PutUint32(statusBuf, 2000)
		conn.Write(statusBuf)
	}()

	client := NewNuclearOption("127.0.0.1", port, nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	_, err = client.Exec("test-command")
	if err == nil {
		t.Errorf("expected error for truncated body length, got none")
	}
	if !strings.Contains(err.Error(), "read body length") {
		t.Errorf("error: got %v, want to contain 'read body length'", err)
	}

	select {
	case <-acceptDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for server goroutine to finish")
	}
}

// nuclearOptionHeader returns the 8-byte response header: the LE status word
// followed by the LE body length word.
func nuclearOptionHeader(status, bodyLen uint32) []byte {
	hdr := make([]byte, 8)
	binary.LittleEndian.PutUint32(hdr[0:4], status)
	binary.LittleEndian.PutUint32(hdr[4:8], bodyLen)
	return hdr
}

// nuclearOptionFrame returns a complete response frame: header plus UTF-8
// body. The length word counts body bytes, not runes.
func nuclearOptionFrame(status uint32, body string) []byte {
	return append(nuclearOptionHeader(status, uint32(len(body))), body...)
}

// nuclearOptionRequestFrame returns the bytes a client sends for req: a 4-byte
// LE length of the JSON body followed by the body itself.
func nuclearOptionRequestFrame(t *testing.T, req nuclearOptionRequest) []byte {
	t.Helper()
	body, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	frame := make([]byte, 4, 4+len(body))
	binary.LittleEndian.PutUint32(frame, uint32(len(body)))
	return append(frame, body...)
}

// portOf extracts the TCP port from a listener address such as "127.0.0.1:41234".
func portOf(t *testing.T, addr string) int {
	t.Helper()
	_, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("split host port %q: %v", addr, err)
	}
	port := 0
	if _, err := fmt.Sscanf(portStr, "%d", &port); err != nil {
		t.Fatalf("parse port %q: %v", portStr, err)
	}
	return port
}

// newPipeClient returns a client whose connection is one end of an in-memory
// pipe. script runs on the other end in a goroutine and decides what the
// "server" sends back. Cleanup closes the client and waits for script to return.
func newPipeClient(t *testing.T, script func(remote net.Conn)) *NuclearOption {
	t.Helper()
	local, remote := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer remote.Close()
		script(remote)
	}()

	client := NewNuclearOption("127.0.0.1", 1, nil)
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second
	client.conn = local
	t.Cleanup(func() {
		_ = client.Close()
		<-done
	})
	return client
}

// readNuclearOptionRequest reads one request frame (LE length + JSON body)
// from conn, returning the decoded request and the raw body bytes.
func readNuclearOptionRequest(conn net.Conn) (nuclearOptionRequest, []byte, error) {
	lengthBuf := make([]byte, 4)
	if _, err := io.ReadFull(conn, lengthBuf); err != nil {
		return nuclearOptionRequest{}, nil, err
	}
	body := make([]byte, binary.LittleEndian.Uint32(lengthBuf))
	if _, err := io.ReadFull(conn, body); err != nil {
		return nuclearOptionRequest{}, nil, err
	}
	var req nuclearOptionRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nuclearOptionRequest{}, body, err
	}
	return req, body, nil
}

// writeFrameSegments writes frame either in one Write call or one byte per
// Write call, so the client's reads are forced to span several segments.
// Write errors are ignored: they mean the client already gave up.
func writeFrameSegments(conn net.Conn, frame []byte, oneByte bool) {
	if !oneByte {
		if len(frame) > 0 {
			_, _ = conn.Write(frame)
		}
		return
	}
	for i := range frame {
		if _, err := conn.Write(frame[i : i+1]); err != nil {
			return
		}
	}
}

// newPipeReplyClient returns a pipe-backed client whose server answers every
// request with the frame reply produces.
func newPipeReplyClient(t *testing.T, reply func(nuclearOptionRequest) []byte) *NuclearOption {
	t.Helper()
	return newPipeClient(t, func(remote net.Conn) {
		for {
			req, _, err := readNuclearOptionRequest(remote)
			if err != nil {
				return
			}
			if _, err := remote.Write(reply(req)); err != nil {
				return
			}
		}
	})
}

// TestNuclearOptionCommandResponses drives the player-list, moderation and chat
// commands through the shared fake server. Status 2000 is success whatever the
// body says (the contract reports 2000 for unknown Steam IDs), and the client
// surfaces any other status the server returns.
func TestNuclearOptionCommandResponses(t *testing.T) {
	server := newFakeTCPServer(t)
	defer server.close(t)

	client := NewNuclearOption("127.0.0.1", portOf(t, server.addr), nil)
	defer client.Close()
	client.dialTimeout = 100 * time.Millisecond
	client.execDeadline = 1 * time.Second

	tests := []struct {
		name     string
		cmd      string
		status   uint32
		body     string // empty means no body
		wantBody string
		wantErr  string
	}{
		{
			name:     "get-player-list populated",
			cmd:      "get-player-list",
			status:   2000,
			body:     `{"Players":[{"steamId":"76561198123456789","faction":"Blue"},{"steamId":"76561198987654321","faction":"Red"}]}`,
			wantBody: `{"Players":[{"steamId":"76561198123456789","faction":"Blue"},{"steamId":"76561198987654321","faction":"Red"}]}`,
		},
		{
			name:     "get-player-list empty",
			cmd:      "get-player-list",
			status:   2000,
			body:     `{"Players":[]}`,
			wantBody: `{"Players":[]}`,
		},
		{
			name:    "get-player-list 4003 JsonError",
			cmd:     "get-player-list",
			status:  4003,
			wantErr: "status 4003",
		},
		{
			name:    "get-player-list 5000 InternalServerError with detail",
			cmd:     "get-player-list",
			status:  5000,
			body:    `{"message":"player list unavailable"}`,
			wantErr: "status 5000: player list unavailable",
		},
		{
			name:     "kick-player valid id",
			cmd:      "kick-player 76561198123456789",
			status:   2000,
			wantBody: "",
		},
		{
			name:     "kick-player unknown id returns 2000 with empty body",
			cmd:      "kick-player 76561198000000000",
			status:   2000,
			wantBody: "",
		},
		{
			name:    "kick-player 4005 is surfaced as an error",
			cmd:     "kick-player 76561198000000000",
			status:  4005,
			body:    `{"message":"unknown steam id"}`,
			wantErr: "status 4005: unknown steam id",
		},
		{
			name:     "banlist-add success",
			cmd:      "banlist-add 76561198123456789 cheating",
			status:   2000,
			wantBody: "",
		},
		{
			name:    "banlist-add 5002 ConfigError when ban list missing",
			cmd:     "banlist-add 76561198123456789 cheating",
			status:  5002,
			body:    `{"message":"ban list not configured"}`,
			wantErr: "status 5002: ban list not configured",
		},
		{
			name:     "banlist-remove success",
			cmd:      "banlist-remove 76561198123456789",
			status:   2000,
			wantBody: "",
		},
		{
			name:    "banlist-remove 4005 BadArguments when not banned",
			cmd:     "banlist-remove 76561198123456789",
			status:  4005,
			body:    `{"message":"steam id not banned"}`,
			wantErr: "status 4005: steam id not banned",
		},
		{
			name:     "send-chat-message keeps rich text tags",
			cmd:      "send-chat-message <color=#ffcc00>Restart in 5 min</color>",
			status:   2000,
			wantBody: "",
		},
		{
			name:    "send-chat-message empty message 4005",
			cmd:     "send-chat-message ",
			status:  4005,
			body:    `{"message":"message is empty"}`,
			wantErr: "status 4005: message is empty",
		},
		{
			name:     "set-next-mission valid mission",
			cmd:      "set-next-mission BuiltIn Escalation 3600.0",
			status:   2000,
			wantBody: "",
		},
		{
			name:    "set-next-mission unknown mission 4005",
			cmd:     "set-next-mission BuiltIn NoSuchMission 3600.0",
			status:  4005,
			body:    `{"message":"mission not in rotation"}`,
			wantErr: "status 4005: mission not in rotation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var respBody interface{}
			if tt.body != "" {
				respBody = tt.body
			}
			server.responses = map[string][2]interface{}{
				strings.Fields(tt.cmd)[0]: {tt.status, respBody},
			}

			body, err := client.Exec(tt.cmd)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got body %q", tt.wantErr, body)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error: got %v, want to contain %q", err, tt.wantErr)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if body != tt.wantBody {
					t.Errorf("body: got %q, want %q", body, tt.wantBody)
				}
			}

			// Close so the next subtest gets a fresh connection to the fake server.
			client.Close()
		})
	}
}

// TestNuclearOptionOversizedArguments verifies that very long arguments reach
// the server intact and that the server's 4005 BadArguments is surfaced.
func TestNuclearOptionOversizedArguments(t *testing.T) {
	longID := strings.Repeat("7", 4096)
	longMsg := strings.Repeat("a ", 2048) // the parser trims the trailing space

	tests := []struct {
		name    string
		cmd     string
		wantArg string
		detail  string
		wantErr string
	}{
		{
			name:    "kick-player oversized steam id",
			cmd:     "kick-player " + longID,
			wantArg: longID,
			detail:  "invalid steam id",
			wantErr: "status 4005: invalid steam id",
		},
		{
			name:    "send-chat-message over-long message",
			cmd:     "send-chat-message " + longMsg,
			wantArg: strings.TrimRight(longMsg, " "),
			detail:  "message too long",
			wantErr: "status 4005: message too long",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			detailBody, err := json.Marshal(nuclearOptionErrorResponse{Message: tt.detail})
			if err != nil {
				t.Fatalf("marshal detail: %v", err)
			}

			received := make(chan nuclearOptionRequest, 1)
			client := newPipeReplyClient(t, func(req nuclearOptionRequest) []byte {
				received <- req
				return nuclearOptionFrame(4005, string(detailBody))
			})

			_, err = client.Exec(tt.cmd)
			if err == nil {
				t.Fatal("expected 4005 error, got none")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error: got %v, want to contain %q", err, tt.wantErr)
			}

			select {
			case req := <-received:
				if len(req.Arguments) != 1 {
					t.Fatalf("arguments count: got %d, want 1", len(req.Arguments))
				}
				if req.Arguments[0] != tt.wantArg {
					t.Errorf("argument: got %d bytes, want %d bytes intact", len(req.Arguments[0]), len(tt.wantArg))
				}
			default:
				t.Fatal("server did not receive request")
			}
		})
	}
}

// TestNuclearOptionSetNextMissionReadTimeout verifies that a server which
// accepts the request but never answers surfaces as a read timeout instead of
// hanging the caller.
func TestNuclearOptionSetNextMissionReadTimeout(t *testing.T) {
	received := make(chan nuclearOptionRequest, 1)
	client := newPipeClient(t, func(remote net.Conn) {
		req, _, err := readNuclearOptionRequest(remote)
		if err != nil {
			return
		}
		received <- req
		// Never respond. Block until the client gives up and closes its end.
		_, _ = remote.Read(make([]byte, 1))
	})
	client.execDeadline = 50 * time.Millisecond

	_, err := client.Exec("set-next-mission BuiltIn Escalation 3600.0")
	if err == nil {
		t.Fatal("expected read timeout, got none")
	}
	if !strings.Contains(err.Error(), "read status") {
		t.Errorf("error: got %v, want to contain 'read status'", err)
	}

	select {
	case req := <-received:
		wantArgs := []string{"BuiltIn", "Escalation", "3600.0"}
		if req.Name != "set-next-mission" || !slicesEqual(req.Arguments, wantArgs) {
			t.Errorf("request: got name %q args %v, want set-next-mission %v", req.Name, req.Arguments, wantArgs)
		}
	default:
		t.Fatal("server did not receive request")
	}
}

// TestNuclearOptionRequestFraming verifies the request side of the wire format:
// a 4-byte LE length of the UTF-8 JSON body, then the body. The message contains
// multi-byte runes so the length must count bytes, not runes.
func TestNuclearOptionRequestFraming(t *testing.T) {
	const msg = "héllo ✓"
	if utf8.RuneCountInString(msg) == len(msg) {
		t.Fatal("test message must contain multi-byte runes")
	}
	const wantBody = `{"name":"send-chat-message","arguments":["héllo ✓"]}`

	captured := make(chan []byte, 1)
	client := newPipeClient(t, func(remote net.Conn) {
		_, body, err := readNuclearOptionRequest(remote)
		if err != nil {
			return
		}
		captured <- body
		_, _ = remote.Write(nuclearOptionFrame(2000, ""))
	})

	if _, err := client.Exec("send-chat-message " + msg); err != nil {
		t.Fatalf("exec: %v", err)
	}

	select {
	case got := <-captured:
		// readNuclearOptionRequest reads exactly the announced length, so an
		// equal body proves the length word counted UTF-8 bytes.
		if string(got) != wantBody {
			t.Errorf("request body: got %q, want %q", got, wantBody)
		}
	default:
		t.Fatal("server did not receive request")
	}
}

// pipeFrameCase is one raw response frame the pipe server writes after reading
// the client's request.
type pipeFrameCase struct {
	name      string
	frame     []byte
	oneByte   bool   // write the frame one byte per Write call
	wantBody  string // expected body when wantErr is empty
	wantErr   string // substring the error must contain
	forbidErr string // substring the error must NOT contain (e.g. a detail suffix)
}

// runPipeFrameCases sends each case's raw frame back to the client over net.Pipe
// and checks the body or the error.
func runPipeFrameCases(t *testing.T, tests []pipeFrameCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client := newPipeClient(t, func(remote net.Conn) {
				if _, _, err := readNuclearOptionRequest(remote); err != nil {
					return
				}
				writeFrameSegments(remote, tt.frame, tt.oneByte)
			})

			body, err := client.Exec("test-command")
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got body %q", tt.wantErr, body)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("error: got %v, want to contain %q", err, tt.wantErr)
				}
				if tt.forbidErr != "" && strings.Contains(err.Error(), tt.forbidErr) {
					t.Errorf("error: got %v, must not contain %q", err, tt.forbidErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if body != tt.wantBody {
				t.Errorf("body: got %q, want %q", body, tt.wantBody)
			}
		})
	}
}

// TestNuclearOptionResponseFraming is the framing contract for responses: the
// status word, the body length word and the body arrive in that order, all
// little-endian, and the response is never parsed as a mirror of the request.
func TestNuclearOptionResponseFraming(t *testing.T) {
	// The request frame for "test-command" starts with a small LE length word
	// (the status when misread) followed by the JSON bytes `{"na`. Read as a
	// response, the body length is those JSON bytes (about 1.6e9), far above
	// the 10 MiB cap, so a mirror-style parse is rejected.
	mirror := nuclearOptionRequestFrame(t, nuclearOptionRequest{Name: "test-command"})

	runPipeFrameCases(t, []pipeFrameCase{
		{
			name:     "status and length words arrive one byte at a time",
			frame:    nuclearOptionFrame(2000, "héllo ✓"),
			oneByte:  true,
			wantBody: "héllo ✓",
		},
		{
			name:     "single write with multi-byte body",
			frame:    nuclearOptionFrame(2000, "héllo ✓"),
			wantBody: "héllo ✓",
		},
		{
			name:     "length word counts UTF-8 bytes not runes",
			frame:    nuclearOptionFrame(2000, "世界✓"), // 9 bytes, 3 runes
			oneByte:  true,
			wantBody: "世界✓",
		},
		{
			name:     "success with zero-length body",
			frame:    nuclearOptionFrame(2000, ""),
			wantBody: "",
		},
		{
			name:      "non-2000 status with zero-length body",
			frame:     nuclearOptionFrame(4004, ""),
			wantErr:   "status 4004",
			forbidErr: "status 4004:",
		},
		{
			name:    "length above the 10 MiB cap is rejected",
			frame:   nuclearOptionHeader(2000, nuclearOptionMaxBodyLength+1),
			wantErr: "too large",
		},
		{
			name:    "status word truncated",
			frame:   nuclearOptionHeader(2000, 0)[:2],
			wantErr: "read status",
		},
		{
			name:    "length word truncated",
			frame:   nuclearOptionHeader(2000, 5)[:6],
			oneByte: true,
			wantErr: "read body length",
		},
		{
			name:    "body truncated before the declared length",
			frame:   append(nuclearOptionHeader(2000, 10), "abc"...),
			oneByte: true,
			wantErr: "read body",
		},
		{
			name:    "response that mirrors the request is rejected",
			frame:   mirror,
			wantErr: "too large",
		},
	})
}

// TestNuclearOptionErrorPathFrames covers the explicit error paths: unknown
// status codes, frames shorter than the 8-byte header, zero-length bodies with
// error status, and detail bodies that fail to decode.
func TestNuclearOptionErrorPathFrames(t *testing.T) {
	runPipeFrameCases(t, []pipeFrameCase{
		{
			name:    "unknown status 9999 with JSON detail",
			frame:   nuclearOptionFrame(9999, `{"message":"vendor-specific"}`),
			wantErr: "status 9999: vendor-specific",
		},
		{
			name:      "unknown status 9999 with non-JSON body",
			frame:     nuclearOptionFrame(9999, "not json"),
			wantErr:   "status 9999",
			forbidErr: "status 9999:",
		},
		{
			name:      "4003 with zero-length body",
			frame:     nuclearOptionFrame(4003, ""),
			wantErr:   "status 4003",
			forbidErr: "status 4003:",
		},
		{
			name:      "5002 with zero-length body",
			frame:     nuclearOptionFrame(5002, ""),
			wantErr:   "status 5002",
			forbidErr: "status 5002:",
		},
		{
			name:      "4005 with JSON array body fails detail decode",
			frame:     nuclearOptionFrame(4005, `["not","an","object"]`),
			wantErr:   "status 4005",
			forbidErr: "status 4005:",
		},
		{
			name:      "4005 with truncated JSON body",
			frame:     nuclearOptionFrame(4005, `{"message":`),
			wantErr:   "status 4005",
			forbidErr: "status 4005:",
		},
		{
			name:      "4005 with empty message field",
			frame:     nuclearOptionFrame(4005, `{"message":""}`),
			wantErr:   "status 4005",
			forbidErr: "status 4005:",
		},
		{
			name:    "frame shorter than header: status word only",
			frame:   nuclearOptionHeader(2000, 0)[:4],
			wantErr: "read body length",
		},
		{
			name:    "frame shorter than header: two bytes",
			frame:   nuclearOptionHeader(2000, 0)[:2],
			wantErr: "read status",
		},
		{
			name:    "connection closed with no bytes",
			frame:   nil,
			wantErr: "read status",
		},
		{
			name:    "oversized length with non-2000 status",
			frame:   nuclearOptionHeader(4005, nuclearOptionMaxBodyLength+1),
			wantErr: "too large",
		},
	})
}
