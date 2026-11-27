package gameproto

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// minecraftMaxPacketSize is the maximum allowed packet size. Minecraft packets
// sent at the handshake stage are much smaller; this prevents unbounded
// allocation from a malicious length prefix.
const minecraftMaxPacketSize = 512

// minecraftClassifyResult holds the result of classifying a Minecraft handshake.
type minecraftClassifyResult struct {
	// ProtocolVersion is the version field from the handshake packet.
	ProtocolVersion int32

	// NextState is the requested next protocol state after handshake.
	// 1 = Status, 2 = Login, 3 = Transfer (1.20.5+).
	NextState int32

	// ServerAddr is the server address (host:port) the client sent in the handshake.
	ServerAddr string

	// Consumed holds the bytes read from the input to parse this handshake.
	// Can be replayed: len(consumed) + len(remaining) == len(original).
	Consumed []byte
}

// classifyMinecraftHandshake reads and parses the Handshake packet to determine
// whether the connection is a Join (login state) or Status (status state).
// The caller-provided bufio.Reader is used directly, preserving any pipelined
// data in its internal buffer for the caller to consume later.
func classifyMinecraftHandshake(br *bufio.Reader) (Kind, *minecraftClassifyResult, error) {
	// Accumulate consumed bytes
	var consumed bytes.Buffer

	// Read the frame length (VarInt).
	length, err := readMinecraftVarIntWithCapture(br, &consumed)
	if err != nil {
		if errors.Is(err, io.EOF) {
			return Unknown, nil, err
		}
		return Unknown, nil, fmt.Errorf("read handshake length: %w", err)
	}

	if length <= 0 || length > minecraftMaxPacketSize {
		return Unknown, nil, fmt.Errorf("handshake packet length %d out of range", length)
	}

	// Read the packet data into a buffer.
	frame := make([]byte, length)
	if _, err = io.ReadFull(br, frame); err != nil {
		return Unknown, nil, fmt.Errorf("read handshake frame: %w", err)
	}

	// Add frame data to consumed
	consumed.Write(frame)

	// Parse the handshake packet.
	frameReader := bytes.NewReader(frame)

	// Read packet ID (must be 0x00 for Handshake).
	packetID, err := readMinecraftVarInt(frameReader)
	if err != nil {
		return Unknown, nil, fmt.Errorf("read packet id: %w", err)
	}
	if packetID != 0x00 {
		return Unknown, nil, fmt.Errorf("expected handshake packet id 0x00, got 0x%02x", packetID)
	}

	// Read protocol version (VarInt).
	protocolVersion, err := readMinecraftVarInt(frameReader)
	if err != nil {
		return Unknown, nil, fmt.Errorf("read protocol version: %w", err)
	}

	// Read server address (string).
	serverAddr, err := readMinecraftString(frameReader)
	if err != nil {
		return Unknown, nil, fmt.Errorf("read server address: %w", err)
	}

	// Read server port (uint16 big-endian).
	var portBytes [2]byte
	if _, err := io.ReadFull(frameReader, portBytes[:]); err != nil {
		return Unknown, nil, fmt.Errorf("read server port: %w", err)
	}
	port := binary.BigEndian.Uint16(portBytes[:])

	// Read next state (VarInt): 1 = Status, 2 = Login, 3 = Transfer (1.20.5+).
	nextState, err := readMinecraftVarInt(frameReader)
	if err != nil {
		return Unknown, nil, fmt.Errorf("read next state: %w", err)
	}

	result := &minecraftClassifyResult{
		ProtocolVersion: protocolVersion,
		NextState:       nextState,
		ServerAddr:      serverAddr + ":" + strconv.Itoa(int(port)),
		Consumed:        consumed.Bytes(),
	}

	switch nextState {
	case 1: // Status
		return Status, result, nil
	case 2, 3: // Login, Transfer (1.20.5+): both continue into the login state
		return Join, result, nil
	default:
		return Unknown, result, nil
	}
}

// buildMinecraftStatusResponse builds a JSON status response packet.
// Format: packet_id (0x00) + string(json).
func buildMinecraftStatusResponse(jsonPayload string) ([]byte, error) {
	// Reject a malformed payload up front, per the Classifier interface
	// contract (classifier.go / specs.md): BuildStatusResponse errors if the
	// payload "is malformed or oversized".
	if !json.Valid([]byte(jsonPayload)) {
		return nil, errors.New("build status response: payload is not valid JSON")
	}

	var inner bytes.Buffer

	// Packet ID (0x00 for Status Response).
	writeMinecraftVarInt(&inner, 0x00)

	// JSON response as a string.
	if err := writeMinecraftString(&inner, jsonPayload); err != nil {
		return nil, fmt.Errorf("encode status response: %w", err)
	}

	// Frame the packet with a length prefix.
	return frameMinecraftPacket(inner.Bytes()), nil
}

// buildMinecraftLoginDisconnect builds a Disconnect packet for the login state.
// Format: packet_id (0x00) + chat(reason).
// The reason is a chat component, which for simplicity we encode as a plain JSON string.
func buildMinecraftLoginDisconnect(reason string) ([]byte, error) {
	var inner bytes.Buffer

	// Packet ID (0x00 for Disconnect in the Login state).
	writeMinecraftVarInt(&inner, 0x00)

	// Reason as a chat component (JSON string).
	chatJSON := `{"text":"` + escapeJSONString(reason) + `"}`
	if err := writeMinecraftString(&inner, chatJSON); err != nil {
		return nil, fmt.Errorf("encode disconnect reason: %w", err)
	}

	return frameMinecraftPacket(inner.Bytes()), nil
}

// readMinecraftVarInt reads a VarInt (5-byte max) from r.
func readMinecraftVarInt(r io.ByteReader) (int32, error) {
	return readMinecraftVarIntWithCapture(r, io.Discard)
}

// readMinecraftVarIntWithCapture reads a VarInt and writes its bytes to a buffer.
func readMinecraftVarIntWithCapture(r io.ByteReader, w io.Writer) (int32, error) {
	var result uint32
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		if _, err := w.Write([]byte{b}); err != nil {
			return 0, fmt.Errorf("write captured byte: %w", err)
		}
		result |= uint32(b&0x7f) << (7 * i)
		if b&0x80 == 0 {
			// This is the last byte (no continuation bit set).
			// On the 5th byte (i=4), only 4 bits are valid (bits 28-31).
			// If b&0x7f > 0x0F, we're trying to set bits beyond bit 31.
			if i == 4 && (b&0x7f) > 0x0f {
				return 0, fmt.Errorf("varint value out of range for int32")
			}
			// Safe to convert uint32 result to int32 (all uint32 patterns map to valid int32).
			resultInt := int32(result)
			return resultInt, nil
		}
		// Continuation bit is set; if we're at byte 5 (i=4), a 6th byte would be needed.
		if i == 4 {
			return 0, errors.New("varint too long")
		}
	}
	return 0, errors.New("varint too long")
}

// writeMinecraftVarInt writes a VarInt to w.
func writeMinecraftVarInt(w *bytes.Buffer, v int32) {
	// Minecraft VarInts are signed 32-bit integers using two's complement.
	// Convert to uint32 to get the bit pattern for encoding.
	// For two's complement, this cast is always safe (no bit loss).
	// Negative values reinterpret as large unsigned values; this is intentional.
	// int32 to uint32 conversion is safe: all int32 values fit in uint32 range.
	signedVal := v
	uv := uint32(signedVal)
	for {
		b := byte(uv & 0x7f)
		uv >>= 7
		if uv != 0 {
			b |= 0x80
		}
		w.WriteByte(b)
		if uv == 0 {
			return
		}
	}
}

// readMinecraftString reads a UTF-8 string from r, prefixed by a VarInt length.
func readMinecraftString(r io.ByteReader) (string, error) {
	length, err := readMinecraftVarInt(r)
	if err != nil {
		return "", err
	}
	if length < 0 || length > 32767 { // 32KB max string
		return "", fmt.Errorf("string length %d out of range", length)
	}

	// Convert ByteReader to Reader. If r is a bufio.Reader or bytes.Reader, it implements io.Reader.
	rr, ok := r.(io.Reader)
	if !ok {
		return "", fmt.Errorf("reader does not implement io.Reader")
	}

	buf := make([]byte, length)
	if _, err := io.ReadFull(rr, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// writeMinecraftString writes a UTF-8 string to w, prefixed by a VarInt length.
func writeMinecraftString(w *bytes.Buffer, s string) error {
	slen := len(s)
	if slen > 32767 {
		return fmt.Errorf("string too long: %d > 32767", slen)
	}
	writeMinecraftVarInt(w, int32(slen))
	w.WriteString(s)
	return nil
}

// frameMinecraftPacket frames packet data by prefixing it with a VarInt length.
func frameMinecraftPacket(data []byte) []byte {
	var out bytes.Buffer
	dlen := len(data)
	if dlen > 0x7fffffff {
		// Data is too large to encode as a VarInt; return empty to signal error,
		// though callers should prevent this through other means.
		dlen = 0x7fffffff
	}
	writeMinecraftVarInt(&out, int32(dlen))
	out.Write(data)
	return out.Bytes()
}

// escapeJSONString escapes a string for use in a JSON string literal.
// Handles backslashes, quotes, and control characters.
func escapeJSONString(s string) string {
	var sb strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			sb.WriteString(`\"`)
		case '\\':
			sb.WriteString(`\\`)
		case '\b':
			sb.WriteString(`\b`)
		case '\f':
			sb.WriteString(`\f`)
		case '\n':
			sb.WriteString(`\n`)
		case '\r':
			sb.WriteString(`\r`)
		case '\t':
			sb.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&sb, `\u%04x`, r)
			} else {
				sb.WriteRune(r)
			}
		}
	}
	return sb.String()
}

// MinecraftClassifier implements the Classifier interface for Minecraft (Java Edition).
type MinecraftClassifier struct{}

// Classify reads a Minecraft handshake and returns a ClassificationResult.
// It delegates to the unexported classifyMinecraftHandshake function and repackages
// the result into the unified ClassificationResult type. Detail is nil for Unknown
// classification and non-nil for Join/Status, per the contract in specs.md.
func (c *MinecraftClassifier) Classify(br *bufio.Reader) (*ClassificationResult, error) {
	kind, result, err := classifyMinecraftHandshake(br)
	if err != nil {
		return nil, err
	}

	// Repackage into ClassificationResult, wrapping minecraftClassifyResult fields
	// in the Detail interface. Detail is nil for Unknown, non-nil for Join/Status.
	var detail Detail
	if kind == Join || kind == Status {
		detail = &MinecraftDetail{
			ProtocolVersion: result.ProtocolVersion,
			NextState:       result.NextState,
			ServerAddr:      result.ServerAddr,
		}
	}

	return &ClassificationResult{
		Kind:     kind,
		Consumed: result.Consumed,
		Detail:   detail,
	}, nil
}

// SupportsStatusPing returns true because Minecraft supports out-of-band status pings.
func (c *MinecraftClassifier) SupportsStatusPing() bool {
	return true
}

// BuildStatusResponse builds a Minecraft status response packet.
// It delegates to the unexported buildMinecraftStatusResponse function.
func (c *MinecraftClassifier) BuildStatusResponse(jsonPayload string) ([]byte, error) {
	return buildMinecraftStatusResponse(jsonPayload)
}

// BuildDisconnect builds a Minecraft disconnect packet.
// It delegates to the unexported buildMinecraftLoginDisconnect function.
func (c *MinecraftClassifier) BuildDisconnect(reason string) ([]byte, error) {
	return buildMinecraftLoginDisconnect(reason)
}
