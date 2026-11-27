package gameproto

import "bufio"

// Classifier is the interface that encapsulates protocol-specific logic for
// classifying an incoming connection and generating protocol-native responses.
// Each game protocol (Minecraft, Terraria, future) provides one Classifier
// implementation.
//
// The Classifier is responsible for:
//   - Handshake parsing (reading from bufio.Reader, interpreting wire format)
//   - Classification decision (is this Join / Status / Unknown?)
//   - Bytes-consumed tracking (for stream replay contract)
//   - Response building (Status messages, Disconnect messages)
//   - Capability declaration (supports status pings? yes/no)
type Classifier interface {
	// Classify reads a handshake from br and returns a *ClassificationResult or error.
	// On success (err == nil), result is always non-nil; Kind field indicates
	// Join, Status, or Unknown. On error (err != nil), result may be nil or carry
	// partial Consumed bytes for stream replay.
	//
	// Invariant: Classify returns a non-nil *ClassificationResult on every
	// non-error path, including Kind == Unknown, because the caller needs
	// result.Consumed to replay the already-read bytes. A nil result is only
	// valid together with a non-nil error.
	//
	// The caller must create br as bufio.NewReader(conn) and continue using it
	// for the rest of the connection. Any pipelined data remains in br's buffer.
	Classify(br *bufio.Reader) (*ClassificationResult, error)

	// SupportsStatusPing declares whether this protocol supports out-of-band
	// status pings (e.g., Minecraft's ping/pong). Terraria returns false here.
	//
	// If false, the caller must not attempt to call BuildStatusResponse for
	// classifications with kind == Status; doing so is a caller contract violation.
	SupportsStatusPing() bool

	// BuildStatusResponse builds a status-ping reply. Only valid if
	// SupportsStatusPing() returns true and kind == Status.
	//
	// Input: payload string (JSON for Minecraft, format is protocol-specific).
	// Output: framed packet bytes ready to write to the connection.
	// Error: if payload is malformed or oversized, or if this protocol does
	// not support status pings (should not happen if SupportsStatusPing()
	// was checked first).
	BuildStatusResponse(payload string) ([]byte, error)

	// BuildDisconnect builds a disconnect/timeout message to send to a client
	// that initiated a join but was rejected (e.g., due to server timeout or
	// explicit rejection).
	//
	// Input: reason string (human-readable explanation).
	// Output: framed packet bytes ready to write to the connection.
	// Error: if reason is oversized or contains characters this protocol
	// cannot encode.
	BuildDisconnect(reason string) ([]byte, error)
}

// ClassificationResult is the unified result type returned by all Classifiers.
// It carries the classification outcome, the bytes consumed during parsing
// (enabling lossless stream replay), and optionally a protocol-specific detail
// object.
type ClassificationResult struct {
	// Kind is the classification outcome: Join, Status, or Unknown.
	// Always present.
	Kind Kind

	// Consumed holds the actual bytes read from the input during handshake
	// parsing. Always present (may be empty for Unknown or on error).
	// Enables lossless stream replay: original_stream == Consumed + remaining_in_bufio.Reader.
	Consumed []byte

	// Detail is a protocol-specific detail object carrying parsed handshake
	// fields (server address, protocol version, player name, etc.).
	// Conditionally present: nil for Unknown classification; non-nil for Join/Status.
	// Type: *MinecraftDetail, *TerrariaDetail, or other protocol implementations.
	// Caller must type-assert: if md, ok := result.Detail.(*MinecraftDetail) { ... }.
	Detail Detail
}

// Detail is an interface for protocol-specific metadata parsed from a handshake.
// Each protocol (Minecraft, Terraria, future) implements a concrete Detail type
// carrying its specific fields.
type Detail interface {
	// ProtocolName returns the name of the protocol ("minecraft", "terraria", etc.)
	// for debugging and logging.
	ProtocolName() string
}

// MinecraftDetail holds protocol-specific metadata parsed from a Minecraft handshake.
// It implements the Detail interface.
type MinecraftDetail struct {
	// ProtocolVersion is the version field from the handshake packet.
	// Value varies by Minecraft client version.
	ProtocolVersion int32

	// NextState is the requested next protocol state after handshake.
	// 1 = Status (server-list ping), 2 = Login (player join attempt),
	// 3 = Transfer (transferred player join, 1.20.5+).
	NextState int32

	// ServerAddr is the server address (host:port) the client sent in the handshake.
	ServerAddr string
}

// ProtocolName returns "minecraft" for debugging and logging.
func (d *MinecraftDetail) ProtocolName() string {
	return "minecraft"
}

// TerrariaDetail holds protocol-specific metadata parsed from a Terraria
// ConnectRequest. It implements the Detail interface.
type TerrariaDetail struct {
	// Version is the protocol version string from the ConnectRequest.
	Version string
}

// ProtocolName returns "terraria" for debugging and logging.
func (d *TerrariaDetail) ProtocolName() string {
	return "terraria"
}
