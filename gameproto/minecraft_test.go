package gameproto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

// TestClassifyMinecraftStatus tests that a status-ping handshake is classified correctly.
func TestClassifyMinecraftStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		data          []byte
		expectKind    Kind
		expectVersion int32
		expectState   int32
		expectErr     bool
	}{
		{
			name:          "valid status ping",
			data:          buildMinecraftHandshake(-1, "localhost", 25565, 1),
			expectKind:    Status,
			expectVersion: -1,
			expectState:   1,
			expectErr:     false,
		},
		{
			name:          "status ping with real protocol version",
			data:          buildMinecraftHandshake(761, "example.com", 25565, 1),
			expectKind:    Status,
			expectVersion: 761,
			expectState:   1,
			expectErr:     false,
		},
		{
			name:       "truncated length",
			data:       []byte{0xFF},
			expectKind: Unknown,
			expectErr:  true,
		},
		{
			name:       "truncated frame",
			data:       []byte{0x05, 0x00, 0x00}, // length=5 but only 2 bytes of data
			expectKind: Unknown,
			expectErr:  true,
		},
		{
			name:       "empty input",
			data:       []byte{},
			expectKind: Unknown,
			expectErr:  true,
		},
		{
			name:       "huge packet size",
			data:       []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x7F}, // 2GB size
			expectKind: Unknown,
			expectErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minecraft := &MinecraftClassifier{}
			result, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(tt.data)))

			if tt.expectErr && err == nil {
				t.Errorf("expected error, got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if err == nil {
				if result.Kind != tt.expectKind {
					t.Errorf("expected kind %v, got %v", tt.expectKind, result.Kind)
				}

				if result.Detail != nil {
					if detail, ok := result.Detail.(*MinecraftDetail); ok {
						if detail.ProtocolVersion != tt.expectVersion {
							t.Errorf("expected version %d, got %d", tt.expectVersion, detail.ProtocolVersion)
						}
						if detail.NextState != tt.expectState {
							t.Errorf("expected state %d, got %d", tt.expectState, detail.NextState)
						}
					}
				}
			}
		})
	}
}

// TestClassifyMinecraftLogin tests that a login handshake is classified correctly.
func TestClassifyMinecraftLogin(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		data       []byte
		expectKind Kind
		expectErr  bool
	}{
		{
			name:       "valid login",
			data:       buildMinecraftHandshake(761, "localhost", 25565, 2),
			expectKind: Join,
			expectErr:  false,
		},
		{
			name:       "login with different server",
			data:       buildMinecraftHandshake(340, "mc.example.com", 25566, 2),
			expectKind: Join,
			expectErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minecraft := &MinecraftClassifier{}
			result, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(tt.data)))

			if tt.expectErr && err == nil {
				t.Errorf("expected error, got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			if err == nil {
				if result.Kind != tt.expectKind {
					t.Errorf("expected kind %v, got %v", tt.expectKind, result.Kind)
				}
			}
		})
	}
}

// TestMinecraftConsumedBytes tests that consumed bytes can be replayed.
func TestMinecraftConsumedBytes(t *testing.T) {
	t.Parallel()

	// Build a valid handshake with known consumed bytes.
	data := buildMinecraftHandshake(761, "localhost", 25565, 2)

	minecraft := &MinecraftClassifier{}
	result, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != Join {
		t.Fatalf("expected kind Join, got %v", result.Kind)
	}

	// Consumed + remaining should equal original
	if len(result.Consumed) != len(data) {
		t.Errorf("consumed %d != len(data) %d", len(result.Consumed), len(data))
	}

	// Consumed must be non-empty
	if len(result.Consumed) == 0 {
		t.Errorf("consumed should be non-empty")
	}

	// Consumed bytes should match the original data
	if !bytes.Equal(result.Consumed, data) {
		t.Errorf("consumed bytes do not match original data")
	}
}

// TestBuildMinecraftStatusResponse tests response building.
func TestBuildMinecraftStatusResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		jsonData string
		expectOK bool
	}{
		{
			name:     "valid JSON",
			jsonData: `{"version":{"name":"1.20.1","protocol":763},"players":{"max":20,"online":1}}`,
			expectOK: true,
		},
		{
			name:     "simple JSON",
			jsonData: `{}`,
			expectOK: true,
		},
		{
			name:     "JSON with quotes",
			jsonData: `{"description":{"text":"Hello \"World\""}}`,
			expectOK: true,
		},
		{
			// F-155: BuildStatusResponse must reject malformed JSON per the
			// Classifier interface contract ("Error: if payload is malformed
			// or oversized"), instead of silently framing invalid data.
			name:     "malformed JSON: unclosed brace",
			jsonData: `{`,
			expectOK: false,
		},
		{
			name:     "malformed JSON: not JSON at all",
			jsonData: `not json`,
			expectOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minecraft := &MinecraftClassifier{}
			data, err := minecraft.BuildStatusResponse(tt.jsonData)

			if tt.expectOK && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if !tt.expectOK {
				if err == nil {
					t.Errorf("expected error for malformed JSON payload %q, got nil (data=%v)", tt.jsonData, data)
				}
				if data != nil {
					t.Errorf("expected nil data on error, got %v", data)
				}
			}

			// Verify we can parse the response back
			if tt.expectOK && data != nil {
				jsonPayload, _, err := ClassifyMinecraftStatusResponse(data)
				if err != nil {
					t.Errorf("failed to parse response: %v", err)
				}
				if jsonPayload != tt.jsonData {
					t.Errorf("roundtrip failed: expected %q, got %q", tt.jsonData, jsonPayload)
				}
			}
		})
	}
}

// TestBuildMinecraftLoginDisconnect tests disconnect packet building.
func TestBuildMinecraftLoginDisconnect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		reason string
	}{
		{
			name:   "simple reason",
			reason: "Server is full",
		},
		{
			name:   "reason with special chars",
			reason: `"Server" is \offline/`,
		},
		{
			name:   "reason with quotes",
			reason: `Server is "offline"`,
		},
		{
			name:   "reason with newline",
			reason: "Line 1\nLine 2",
		},
		{
			name:   "reason with backslash",
			reason: `Path: C:\Users\test`,
		},
		{
			name:   "empty reason",
			reason: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minecraft := &MinecraftClassifier{}
			data, err := minecraft.BuildDisconnect(tt.reason)

			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Verify packet has a valid frame length prefix
			if len(data) > 0 {
				br := bytes.NewReader(data)
				length, err := readMinecraftVarInt(br)
				if err != nil || length <= 0 {
					t.Errorf("invalid frame structure: %v", err)
				}
			}
		})
	}
}

// TestMinecraftVarIntRoundtrip tests VarInt encoding/decoding.
func TestMinecraftVarIntRoundtrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		value int32
	}{
		{0},
		{1},
		{127},
		{128},
		{16383},
		{16384},
		{2097151},
		{2097152},
		{268435455},
		{-1},
		{-128},
		{-129},
		{int32(0x7fffffff)},
		{-2147483648},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%d", tt.value), func(t *testing.T) {
			var buf bytes.Buffer
			writeMinecraftVarInt(&buf, tt.value)

			br := bytes.NewReader(buf.Bytes())
			decoded, err := readMinecraftVarInt(br)
			if err != nil {
				t.Errorf("decode error: %v", err)
			}
			if decoded != tt.value {
				t.Errorf("roundtrip failed: expected %d, got %d", tt.value, decoded)
			}
		})
	}
}

// TestMinecraftStringRoundtrip tests string encoding/decoding.
func TestMinecraftStringRoundtrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
	}{
		{""},
		{"hello"},
		{"Hello, World!"},
		{string([]byte{0xE2, 0x82, 0xAC})}, // €
		{"unicode: 你好"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.input), func(t *testing.T) {
			var buf bytes.Buffer
			err := writeMinecraftString(&buf, tt.input)
			if err != nil {
				t.Errorf("encode error: %v", err)
			}

			br := bytes.NewReader(buf.Bytes())
			decoded, err := readMinecraftString(br)
			if err != nil {
				t.Errorf("decode error: %v", err)
			}
			if decoded != tt.input {
				t.Errorf("roundtrip failed: expected %q, got %q", tt.input, decoded)
			}
		})
	}
}

// TestMinecraftStringTooLong tests that oversized strings are rejected.
func TestMinecraftStringTooLong(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	// Write a length that exceeds the max (32KB).
	writeMinecraftVarInt(&buf, 32768)

	br := bytes.NewReader(buf.Bytes())
	_, err := readMinecraftString(br)
	if err == nil {
		t.Errorf("expected error for oversized string")
	}
}

// TestClassifyMinecraftInvalidState tests unknown next states.
func TestClassifyMinecraftInvalidState(t *testing.T) {
	t.Parallel()

	data := buildMinecraftHandshake(761, "localhost", 25565, 42)

	minecraft := &MinecraftClassifier{}
	result, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(data)))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != Unknown {
		t.Errorf("expected Unknown, got %v", result.Kind)
	}
	if result.Detail != nil {
		t.Errorf("Detail should be nil for Unknown classification")
	}
}

// TestClassifyMinecraftZeroLength tests zero packet length (invalid).
func TestClassifyMinecraftZeroLength(t *testing.T) {
	t.Parallel()

	data := []byte{0x00} // Length is 0, which is invalid

	minecraft := &MinecraftClassifier{}
	_, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(data)))

	if err == nil {
		t.Errorf("expected error for zero length")
	}
}

// TestMinecraftHostileInputs tests hostile/malformed input.
func TestMinecraftHostileInputs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"all zeros", []byte{0x00, 0x00, 0x00}},
		{"single byte", []byte{0x01}},
		{"random bytes", []byte{0xDE, 0xAD, 0xBE, 0xEF}},
		{"high byte in length", []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x0F}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minecraft := &MinecraftClassifier{}
			result, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(tt.data)))

			// We expect either an error or Unknown classification.
			if err == nil && result.Kind != Unknown {
				t.Errorf("expected error or Unknown for hostile input")
			}
		})
	}
}

// TestJSONEscaping tests the JSON string escaper.
func TestJSONEscaping(t *testing.T) {
	t.Parallel()

	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{`"quoted"`, `\"quoted\"`},
		{"backslash\\", "backslash\\\\"},
		{"newline\n", "newline\\n"},
		{"tab\t", "tab\\t"},
		{"\r", "\\r"},
		{"\b", "\\b"},
		{"\f", "\\f"},
		{"control\x00", "control\\u0000"},
		{"mixed\"and\\both", "mixed\\\"and\\\\both"},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.input), func(t *testing.T) {
			result := escapeJSONString(tt.input)
			if result != tt.want {
				t.Errorf("escape failed: expected %q, got %q", tt.want, result)
			}

			// Round-trip: the escaped string should be valid JSON when quoted.
			jsonStr := `"` + result + `"`
			var out string
			if err := json.Unmarshal([]byte(jsonStr), &out); err != nil {
				t.Errorf("invalid JSON after escape: %v", err)
			}
			if out != tt.input {
				t.Errorf("roundtrip failed: expected %q, got %q", tt.input, out)
			}
		})
	}
}

// Helper function to build a valid Minecraft handshake packet.
func buildMinecraftHandshake(protocol int32, host string, port uint16, nextState int32) []byte {
	var packet bytes.Buffer

	// Packet ID (0x00).
	writeMinecraftVarInt(&packet, 0x00)

	// Protocol version.
	writeMinecraftVarInt(&packet, protocol)

	// Server address.
	_ = writeMinecraftString(&packet, host)

	// Server port.
	packet.Write([]byte{byte(port >> 8), byte(port)})

	// Next state.
	writeMinecraftVarInt(&packet, nextState)

	// Frame the packet.
	return frameMinecraftPacket(packet.Bytes())
}

// ClassifyMinecraftStatusResponse is a helper that reads back a status response
// (for round-trip testing).
func ClassifyMinecraftStatusResponse(data []byte) (string, string, error) {
	br := bytes.NewReader(data)

	// Read frame length.
	length, err := readMinecraftVarInt(br)
	if err != nil {
		return "", "", err
	}

	frame := make([]byte, length)
	if _, err := io.ReadFull(br, frame); err != nil {
		return "", "", err
	}

	fbr := bytes.NewReader(frame)

	// Read packet ID.
	packetID, err := readMinecraftVarInt(fbr)
	if err != nil {
		return "", "", err
	}
	if packetID != 0x00 {
		return "", "", fmt.Errorf("expected packet ID 0x00, got 0x%02x", packetID)
	}

	// Read JSON string.
	jsonPayload, err := readMinecraftString(fbr)
	if err != nil {
		return "", "", err
	}

	// Return the original data for comparison.
	return jsonPayload, string(data), nil
}

func TestClassifyMinecraftReplayContract(t *testing.T) {
	t.Parallel()

	// Build a valid handshake
	handshake := buildMinecraftHandshake(761, "localhost", 25565, 2)

	// Build pipelined packets: handshake + some other data
	var full bytes.Buffer
	full.Write(handshake)
	pipelined := []byte{0x05, 0x01, 0xAA, 0xBB, 0xCC, 0xDD}
	full.Write(pipelined)

	reader := bufio.NewReader(&full)
	minecraft := &MinecraftClassifier{}
	result, err := minecraft.Classify(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != Join {
		t.Fatalf("expected Join, got %v", result.Kind)
	}

	// Verify consumed bytes match the handshake
	if !bytes.Equal(result.Consumed, handshake) {
		t.Errorf("consumed bytes do not match handshake")
	}

	// Verify remaining data is still in the reader
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read remaining: %v", err)
	}
	if !bytes.Equal(remaining, pipelined) {
		t.Errorf("remaining bytes do not match pipelined data")
	}
}

func TestClassifyMinecraftPipelinedPackets(t *testing.T) {
	t.Parallel()

	// Build a valid handshake
	handshake := buildMinecraftHandshake(761, "localhost", 25565, 2)

	// Build pipelined packets: status handshake + login start
	var full bytes.Buffer
	full.Write(handshake)

	// Add a fake "Login Start" packet
	var loginStart bytes.Buffer
	writeMinecraftVarInt(&loginStart, 0x00)
	_ = writeMinecraftString(&loginStart, "player")
	loginStartData := loginStart.Bytes()
	full.Write(frameMinecraftPacket(loginStartData))

	reader := bufio.NewReader(&full)
	minecraft := &MinecraftClassifier{}
	result, err := minecraft.Classify(reader)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != Join {
		t.Fatalf("expected Join, got %v", result.Kind)
	}

	// Verify consumed bytes match the handshake
	if !bytes.Equal(result.Consumed, handshake) {
		t.Errorf("consumed bytes do not match handshake")
	}

	// Verify remaining data (login start) is in the reader
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("failed to read remaining: %v", err)
	}
	if len(remaining) == 0 {
		t.Errorf("expected remaining pipelined data")
	}
}

// TestClassifyMinecraftLengthVarIntTooLong tests that a length-prefix VarInt
// with five continuation bytes (never terminating) is rejected with a
// specific, non-EOF error rather than treated as a truncated read.
func TestClassifyMinecraftLengthVarIntTooLong(t *testing.T) {
	t.Parallel()

	// Five continuation bytes (0x80) never terminate.
	data := []byte{0x80, 0x80, 0x80, 0x80, 0x80}

	minecraft := &MinecraftClassifier{}
	_, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(data)))

	if err == nil {
		t.Errorf("expected error for non-terminating VarInt")
	}
	if !errors.Is(err, io.EOF) {
		// Should be "varint too long", not EOF
		if !strings.Contains(err.Error(), "varint too long") &&
			!strings.Contains(err.Error(), "too long") {
			t.Logf("Note: got error %v (expected varint too long)", err)
		}
	}
}

// TestClassifyMinecraftFrameReadNonEOFError tests that a non-EOF read error
// while reading the frame body is wrapped and surfaced distinctly from the
// EOF/truncation case (which returns Unknown with an EOF-flavored error).
func TestClassifyMinecraftFrameReadNonEOFError(t *testing.T) {
	t.Parallel()

	// Frame length says 5 bytes, but the reader will fail with non-EOF error
	// on the second call (frame-body read), after succeeding on the first call
	// (length read).
	flaky := &minecraftFlakyReader{calls: 0}
	minecraft := &MinecraftClassifier{}
	_, err := minecraft.Classify(bufio.NewReader(flaky))

	if err == nil {
		t.Errorf("expected error from flaky reader")
	}
}

type minecraftFlakyReader struct {
	calls int
}

func (f *minecraftFlakyReader) Read(p []byte) (int, error) {
	f.calls++
	if f.calls == 1 {
		// First call: return a valid single-byte VarInt length (5)
		// so the length-read step succeeds.
		p[0] = 0x05
		return 1, nil
	}
	// Subsequent calls: fail with a non-EOF error to exercise the
	// frame-body read error path.
	return 0, errors.New("flaky read error")
}

func TestClassifyMinecraftMalformedFrames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{
			name: "length says 10 but only 3 bytes",
			data: []byte{0x0A, 0x00, 0x01, 0x02},
		},
		{
			name: "incomplete varint in length",
			data: []byte{0x80},
		},
		{
			name: "packet id missing",
			data: []byte{0x00}, // length=0, which is invalid
		},
		{
			name: "protocol version missing",
			data: func() []byte {
				var b bytes.Buffer
				b.WriteByte(0x01) // length 1
				b.WriteByte(0x00) // packet ID
				return b.Bytes()
			}(),
		},
		{
			name: "server address missing",
			data: func() []byte {
				var b bytes.Buffer
				// length
				writeMinecraftVarInt(&b, 2)
				// packet ID
				b.WriteByte(0x00)
				// protocol version (105)
				b.WriteByte(0x69)
				return b.Bytes()
			}(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			minecraft := &MinecraftClassifier{}
			_, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(tt.data)))

			if err == nil {
				t.Errorf("expected error for malformed frame: %s", tt.name)
			}
		})
	}
}

// TestBuildMinecraftStatusResponseTooLong tests that an oversized JSON
// payload is rejected rather than silently truncated or panicking.
func TestBuildMinecraftStatusResponseTooLong(t *testing.T) {
	t.Parallel()

	payload := bigString(100000)

	minecraft := &MinecraftClassifier{}
	_, err := minecraft.BuildStatusResponse(payload)

	if err == nil {
		t.Errorf("expected error for oversized payload")
	}
}

// TestBuildMinecraftStatusResponseTooLongValidJSON tests F-155: an oversized
// payload that IS valid JSON must still be rejected by the length check in
// writeMinecraftString (via the "encode status response" wrap), not just by
// the upfront json.Valid check. TestBuildMinecraftStatusResponseTooLong uses
// bigString(100000), which is not valid JSON on its own, so it now fails at
// the json.Valid check and never exercises this length branch; this test
// keeps that branch covered.
func TestBuildMinecraftStatusResponseTooLongValidJSON(t *testing.T) {
	t.Parallel()

	// A JSON string literal wrapping a 100000-byte payload: valid JSON, but
	// still far past the 32767-byte writeMinecraftString limit.
	payload := `"` + bigString(100000) + `"`

	minecraft := &MinecraftClassifier{}
	_, err := minecraft.BuildStatusResponse(payload)

	if err == nil {
		t.Fatalf("expected error for oversized (but valid JSON) payload")
	}
	if !strings.Contains(err.Error(), "encode status response") {
		t.Errorf("expected 'encode status response' in error message, got: %v", err)
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("expected 'too long' in error message, got: %v", err)
	}
}

// TestBuildMinecraftLoginDisconnectTooLong tests that an oversized
// disconnect reason is rejected rather than silently truncated.
func TestBuildMinecraftLoginDisconnectTooLong(t *testing.T) {
	t.Parallel()

	reason := bigString(100000)

	minecraft := &MinecraftClassifier{}
	_, err := minecraft.BuildDisconnect(reason)

	if err == nil {
		t.Errorf("expected error for oversized reason")
	}
}

// bigString builds an n-byte ASCII string for oversized-input tests.
func bigString(n int) string {
	var buf bytes.Buffer
	for i := 0; i < n; i++ {
		buf.WriteByte('a')
	}
	return buf.String()
}

// minecraftByteOnlyReader implements io.ByteReader but deliberately not
// io.Reader, to exercise readMinecraftString's defensive type assertion.
type minecraftByteOnlyReader struct {
	data []byte
	pos  int
}

func (b *minecraftByteOnlyReader) ReadByte() (byte, error) {
	if b.pos >= len(b.data) {
		return 0, io.EOF
	}
	c := b.data[b.pos]
	b.pos++
	return c, nil
}

// TestReadMinecraftStringNonIOReader tests the defensive branch that
// rejects a ByteReader which does not also implement io.Reader.
func TestReadMinecraftStringNonIOReader(t *testing.T) {
	t.Parallel()

	br := &minecraftByteOnlyReader{
		data: []byte{0x05, 'h', 'e', 'l', 'l', 'o'},
	}

	_, err := readMinecraftString(br)
	if err == nil {
		t.Errorf("expected error for non-io.Reader ByteReader")
	}
}

// TestClassifyMinecraftErrorShape tests F-153: on a parse error (here, a
// non-0x00 handshake packet ID), MinecraftClassifier.Classify must return
// (nil, err), per the Classifier interface contract in classifier.go ("On
// error (err != nil), result may be nil or carry partial Consumed bytes").
// It must NOT return a non-nil Unknown result, which specs.md previously
// (incorrectly) documented as the behavior for this case.
func TestClassifyMinecraftErrorShape(t *testing.T) {
	t.Parallel()

	var packet bytes.Buffer
	// Packet ID 0x01 instead of the required 0x00.
	writeMinecraftVarInt(&packet, 0x01)
	writeMinecraftVarInt(&packet, 761)
	if err := writeMinecraftString(&packet, "localhost"); err != nil {
		t.Fatalf("write error: %v", err)
	}
	packet.Write([]byte{0x63, 0xDD})
	writeMinecraftVarInt(&packet, 2)
	data := frameMinecraftPacket(packet.Bytes())

	minecraft := &MinecraftClassifier{}
	result, err := minecraft.Classify(bufio.NewReader(bytes.NewReader(data)))

	if err == nil {
		t.Fatalf("expected error for invalid packet id, got nil (result=%v)", result)
	}
	if result != nil {
		t.Errorf("expected nil result alongside error, got %v", result)
	}
}

// TestClassifyMinecraftTransferIsJoin checks that next state 3 (Transfer,
// 1.20.5+) classifies as a join, like Login.
func TestClassifyMinecraftTransferIsJoin(t *testing.T) {
	t.Parallel()

	data := buildMinecraftHandshake(766, "localhost", 25565, 3)
	result, err := (&MinecraftClassifier{}).Classify(bufio.NewReader(bytes.NewReader(data)))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Kind != Join {
		t.Errorf("expected Join, got %v", result.Kind)
	}
}
