# gameproto — Specification

**Status:** pre-v1 (v0.3.0)  
**Module / package:** `github.com/GameplanePanel/gameplane/gameproto`  
**Dependencies:** stdlib only (Go 1.26+)

## Purpose

Shared wire-protocol parser for game server handshakes (Minecraft Java Edition and Terraria) that classifies inbound connections to determine whether they are join attempts (player connecting) or status queries (server-list ping). Used by the sentinel wake-on-connect daemon to distinguish a real player from a server-list probe without corrupting the connection stream, so the sentinel can answer status pings immediately without waking the server while holding join attempts open until the real game pod comes up.

## Responsibilities

1. Parse Minecraft Java Edition handshake frames (VarInt-framed packets) and classify them by next-state (Status vs. Login).
2. Parse Terraria connection frames and identify ConnectRequest messages as join attempts.
3. Preserve consumed bytes for replay to the real game pod, via a `Consumed` field that tracks exactly what was read, enabling lossless hand-off once the upstream connection is established.
4. Distinguish a genuine join from a server-list status ping or other probe without consuming more of the connection than necessary.
5. Defend against hostile input: bounded reads, explicit max packet sizes, no unbounded allocations from length prefixes, no panics.
6. Build protocol-native responses (Minecraft status replies, disconnect messages) for the sentinel to send without waking the server.

## Non-goals / boundaries

- Does **not** execute joins — only parses handshakes and classifies them. Execution (gameplay, RCON, etc.) is outside the scope.
- Does **not** validate player credentials or enforceRBAC — classification is protocol-only, not business logic.
- Does **not** persist state or maintain connections — parsing and response building are pure, stateless functions.
- Does **not** interact with Kubernetes objects directly — the sentinel and its callers own the operator integration.
- Does **not** support join-attempt forwarding or address rewriting — replies and consumed bytes are protocol-native, unmodified.

## Directory & Package Layout

```
gameproto/
├── gameproto.go              # Kind enumeration, ErrStatusPingUnsupported
├── classifier.go             # Classifier interface, ClassificationResult, Detail interface, 
│                             # and concrete MinecraftDetail/TerrariaDetail types
├── registry.go               # Protocol Registry (classifierRegistry map, Lookup, ListRegistered)
├── minecraft.go              # MinecraftClassifier implementing Classifier interface (wraps unexported
│                             # parsing and response-building helpers)
├── terraria.go               # TerrariaClassifier implementing Classifier interface (wraps unexported
│                             # parsing and response-building helpers)
├── demo.go                   # Reference/stub DemoClassifier implementation (validates registry pattern)
├── minecraft_test.go         # Minecraft codec tests (+ classifier tests)
├── terraria_test.go          # Terraria codec tests (+ classifier tests)
├── classifier_golden_test.go # Golden/characterization tests locking in Classify() output across input scenarios
├── demo_test.go              # DemoClassifier stub tests
├── gameproto_test.go         # Kind.String() unit test
├── registry_test.go          # Protocol Registry tests
├── go.mod                    # (module, stdlib-only)
└── .testcoverage.yml         # 90% coverage gate
```

Single package; no subdirectories. Core abstractions (Classifier interface, ClassificationResult, Detail) in classifier.go. Registry and all protocol implementations co-located in the same package.

## External Interface / Contracts

### Classification Kind (Enumeration)

The `Kind` enum classifies a connection attempt:

```go
type Kind int

const (
  Join    Kind = iota  // Player is attempting to connect; server should wake
  Status               // Server-list ping or query; server should answer without waking
  Unknown              // Bytes did not parse as either join or status
)
```

**Unchanged.** The Kind enumeration and its string representation are preserved exactly from the pre-refactor API. Callers continue to use it to determine whether to wake the server (Join), answer without waking (Status), or close (Unknown).

### Classifier Interface

The **Classifier** interface encapsulates all protocol-specific logic for classifying an incoming connection and generating protocol-native responses. Each game protocol (Minecraft, Terraria, future) provides one Classifier implementation, registered in the central Protocol Registry.

```go
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
```

**Minecraft and Terraria Implementations:**

- **`MinecraftClassifier`** (in `minecraft.go`): Implements all four Classifier methods by delegating to existing unexported parsing and response-building functions. Returns a ClassificationResult with MinecraftDetail carrying ProtocolVersion, NextState, and ServerAddr fields. SupportsStatusPing() returns true.

- **`TerrariaClassifier`** (in `terraria.go`): Implements all four Classifier methods by delegating to existing unexported parsing and response-building functions. Returns a ClassificationResult with TerrariaDetail carrying the Version field. SupportsStatusPing() returns false (Terraria has no out-of-band status ping concept).

**Design Invariant**: The Classifier implementations **call the existing unexported handshake parsing and response-building functions** — they do not rewrite any parsing logic. This ensures byte-for-byte equivalence with the pre-refactor behavior and eliminates duplication. The parsing logic is defined once; Classifiers wrap it in an interface.

### ClassificationResult Struct

The **ClassificationResult** is the unified result type returned by all Classifiers, carrying the classification outcome, the bytes consumed during parsing, and optional protocol-specific detail.

```go
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
```

**Preconditions and Postconditions:**

- On a non-error path (error == nil), `*ClassificationResult` is **always non-nil**, including when Kind == Unknown. This is load-bearing: the caller needs `result.Consumed` to replay already-read bytes to the real server or a generic handler.
- On an error path (error != nil), result may be nil.
- The Consumed field contains exactly the bytes read during handshake parsing, enabling lossless stream replay: `len(Consumed) + len(remaining_in_bufio.Reader) == len(original_stream)`.
- For Unknown classification, Detail is nil.
- For Join or Status, Detail is non-nil and implements the Detail interface (one of MinecraftDetail, TerrariaDetail, etc.).

### Detail Interface

The **Detail** interface represents protocol-specific metadata parsed from a handshake. Each protocol implements a concrete Detail type carrying its specific fields.

```go
type Detail interface {
	// ProtocolName returns the name of the protocol ("minecraft", "terraria", etc.)
	// for debugging and logging.
	ProtocolName() string
}
```

**Concrete Implementations:**

- **`MinecraftDetail`**: Holds ProtocolVersion (int32), NextState (int32: 1=Status, 2=Login), and ServerAddr (string). ProtocolName() returns "minecraft".
- **`TerrariaDetail`**: Holds Version (string from ConnectRequest). ProtocolName() returns "terraria".

### Stream Replay Contract

The sentinel must classify a connection without consuming more bytes than necessary, so it can replay them to the real game pod once it comes up. Classifiers accept a `*bufio.Reader` and read from it directly. Any bytes they consume are returned in `ClassificationResult.Consumed`; anything left in the reader's internal buffer (pipelined data) stays there for the caller to consume.

**Replay sequence:**
1. Call `Lookup("protocol-name").Classify(br)` with a fresh `*bufio.Reader` wrapping the client connection.
2. Receive the classified `*ClassificationResult` carrying Kind, Consumed bytes, and protocol-specific Detail.
3. Once the upstream (game pod) connection is ready, write `result.Consumed` to it first.
4. Then continue copying from the same `*bufio.Reader` (for pipelined bytes) and the client connection (for new data).

This contract ensures: `Consumed + remaining(bufio.Reader) == original stream`, so the game pod receives exactly what the client sent, in order, with no data loss or duplication.

### Protocol Registry

The **Protocol Registry** is a central, compile-time lookup structure mapping protocol names to Classifier implementations. The registry is initialized with a bare package-level map literal and is read-only at runtime.

```go
// classifierRegistry is a static, compile-time map mapping protocol names to Classifiers.
// Duplicate keys are a Go compile error.
var classifierRegistry = map[string]Classifier{
	"minecraft": &MinecraftClassifier{},
	"terraria":  &TerrariaClassifier{},
	"demo":      &DemoClassifier{},
}

// Lookup retrieves a Classifier by protocol name.
// Returns (classifier, true) if found, (nil, false) if not found.
// Never panics on unknown names; caller must handle lookup miss gracefully.
func Lookup(name string) (Classifier, bool) {
	classifier, ok := classifierRegistry[name]
	return classifier, ok
}

// ListRegistered returns a sorted slice of all registered protocol names
// for debugging and audit purposes. Useful for verifying registry completeness.
func ListRegistered() []string {
	names := make([]string, 0, len(classifierRegistry))
	for name := range classifierRegistry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

**Design Rationale:**

- **Bare map literal** (not a struct wrapper) makes the registry greppable and auditable in one place (all registrations are visible in `gameproto/registry.go`).
- **Compile-time initialization** via map literal prevents duplicate keys (Go compiler error) and requires no runtime setup code.
- **No registration function** at runtime; all protocols are known at compile time. This simplifies review and testing.

### Adding a New Protocol

To add a new game protocol (e.g., "factorio"):

1. **Create a new file** `gameproto/factorio.go` defining a FactorioClassifier struct that implements the Classifier interface:
   ```go
   type FactorioClassifier struct{}

   func (c *FactorioClassifier) Classify(br *bufio.Reader) (*ClassificationResult, error) {
       // Handshake parsing logic (may call unexported helper functions)
       // Return ClassificationResult with Kind (Join/Status/Unknown), Consumed bytes, and Detail
   }

   func (c *FactorioClassifier) SupportsStatusPing() bool {
       // Return true if protocol supports out-of-band status pings, false otherwise
   }

   func (c *FactorioClassifier) BuildStatusResponse(payload string) ([]byte, error) {
       // If SupportsStatusPing() is true, build and return status response bytes
       // Otherwise return error
   }

   func (c *FactorioClassifier) BuildDisconnect(reason string) ([]byte, error) {
       // Build and return disconnect message bytes
   }
   ```

2. **Create a detail type** (also in `factorio.go`):
   ```go
   type FactorioDetail struct {
       // Protocol-specific fields parsed from the handshake
       Version string
       // ... other fields
   }

   func (d *FactorioDetail) ProtocolName() string { return "factorio" }
   ```

3. **Register in the map** (in `gameproto/registry.go`):
   ```go
   var classifierRegistry = map[string]Classifier{
       "minecraft": &MinecraftClassifier{},
       "terraria":  &TerrariaClassifier{},
       "factorio":  &FactorioClassifier{},  // Add this line
   }
   ```

4. **Write tests** (in `gameproto/factorio_test.go`):
   - Unit tests for Classify() and response builders
   - Comparison/equivalence tests if migrating from an existing implementation

5. **Register the protocol with the operator's API too.** The steps above only make the sentinel able to classify the new protocol; a `GameTemplate`'s `wakeProtocol` field is a Kubernetes CRD enum (`operator/api/v1alpha1/gametemplate_types.go`, `+kubebuilder:validation:Enum=...`) that must list the new protocol name before any `GameTemplate` can select it. Add the name to that enum, then run `make generate && make manifests` and commit the regenerated `zz_generated.deepcopy.go`, `operator/config/crd/*.yaml`, `charts/gameplane/charts/gameplane-crds/crds/*.yaml`, and `charts/gameplane/crd-manifests/*.yaml` (an enum change does not touch RBAC). Without this step, `kubectl apply` on a template using the new `wakeProtocol` value is rejected with `Unsupported value`, even though the sentinel recognizes it.

6. **That's it for shared Go code.** No further changes to sentinel/main.go, gameproto.go, or any other shared package are needed beyond the CRD enum update above.

**Reference Template:** See `gameproto/demo.go` for a minimal stub implementation showing the pattern. DemoClassifier implements all four methods but always classifies as Unknown; it serves as a worked example that demonstrates the registry pattern requires zero edits to shared code when adding a protocol.

## Handshake Codecs

### Minecraft Java Edition

**Handshake packet structure:**
```
[VarInt] packet_length
  [VarInt] packet_id (0x00 for Handshake)
  [VarInt] protocol_version
  [String] server_address (VarInt length + UTF-8 bytes)
  [UInt16 BE] server_port
  [VarInt] next_state (1 = Status, 2 = Login)
```

**Parsing:** Read the frame length (VarInt, 5 bytes max), reject if > 512 (minecraftMaxPacketSize), read the frame data, then parse packet fields inside. Strings are bounded at 32KB (Minecraft protocol limit). The packet ID must be 0x00; any other value is a parse error (`Classify` returns `nil, err`, matching the `Classifier` interface contract, not a non-nil Unknown result).

**Classification:** next_state == 2 → Join; next_state == 1 → Status; anything else → Unknown.

### Terraria

**Message frame structure:**
```
[UInt16 LE] total_length (includes the 2-byte length field itself)
[UInt8] message_type
[payload...]
```

**ConnectRequest payload:**
```
[7-bit-encoded-int] version_string_length
[UTF-8] version_string
```

**Parsing:** Read 3-byte header (2-byte length LE + 1-byte type), validate length (>= 3, <= 65535), read payload (length - 3), then parse the message type. Only ConnectRequest (type 1) indicates a join; all other message types return a non-nil Unknown result, but a ConnectRequest payload that fails to parse (e.g., a malformed version string) is a parse error (`Classify` returns `nil, err`), not a non-nil Unknown result. Frame-level failures (a length prefix that is too short or exceeds 65535, or a truncated header or payload read) are likewise parse errors and return `nil, err`, not a non-nil Unknown result.

**Classification:** message_type == 1 → Join; otherwise → Unknown.

### VarInt and 7-bit-encoded-int Codecs

**Minecraft VarInt (signed 32-bit, big-endian continuation bits):**
- 1–5 bytes; high bit (0x80) set = continuation, clear = final byte.
- Each byte contributes 7 bits; result is cast from uint32 to int32.
- Rejects malformed sequences: overflow (6+ bytes) or values that set bits beyond bit 31.

**Terraria 7-bit-encoded-int (signed 32-bit, little-endian continuation bits):**
- Similar principle but different bit layout (MSB is continuation, LSBs are payload).
- 1–5 bytes; rejects overflow (6+ bytes).

Both defend against untrusted length prefixes: they bound the decoder loop to 5 iterations and reject any continuation bit on the 5th byte (signaling a 6th byte is needed).

## Kind Classification Invariants

1. **Only JOINED depth satisfies a covered-* status.** Classification alone (without joining) is PARTIAL; only test cases that establish a game connection to verify handshake replay satisfy JOINED depth.
2. **Status and Unknown are distinct.** Status means the sentinel can and should answer with a protocol-native reply (Minecraft status JSON). Unknown means the bytes did not parse; the sentinel closes without replying or waking.
3. **Protocol-native only.** Classifiers operate on wire bytes only; no game state, player database, or server configuration is consulted. Join vs. Status is determined entirely by the handshake.
4. **Classification is deterministic.** Given the same wire bytes, `MinecraftClassifier.Classify()` and `TerrariaClassifier.Classify()` always return the same Kind (barring bugs). The refactor does not change parsing semantics — existing callers continue to receive the same classification outcomes as before.
5. **Parsing is read-safe.** The frame length limits, bounded VarInt loops, and max-packet-size gates ensure parsing terminates and doesn't allocate based on untrusted input.
6. **Kind enumeration is unchanged.** The Kind constants (Join=0, Status=1, Unknown=2) and Kind.String() method are preserved exactly; existing callers continue to work without changes.

## Bounded-Read Defences

1. **Frame length limits:**
   - Minecraft: 512 bytes max (handshakes are much smaller; this prevents unbounded allocation from a huge length prefix).
   - Terraria: 65535 bytes max (16-bit length field limit; reasonable for a single message).

2. **String length bounds:**
   - Minecraft: 32KB max (Minecraft protocol limit for string payloads).
   - Terraria: 32KB max (terrariaMaxStringLength, a defensive cap enforced in readTerrariaString before the remaining-payload check; not derived from the encoding).

3. **VarInt decoder loops:**
   - Both Minecraft and Terraria VarInt decoders loop exactly 5 times. If the 5th byte has the continuation bit set, the decoder rejects the input (overflow), rather than reading a 6th byte.

4. **io.ReadFull** (not io.Read):
   - The classifiers use `io.ReadFull` to read frame data and payloads. This ensures either the full count is read or an error is returned; partial reads are treated as errors (truncated input).

5. **Metadata hostname detection:**
   - Not present in gameproto itself, but the caller (sentinel) can use response builders to craft safe replies. The builders escape JSON (Minecraft) and validate payload sizes (Terraria).

## Invariants the Sentinel Depends On

1. **Consumed bytes are correct and complete.** The sentinel replays them to the game pod as-is; if they're truncated, duplicated, or corrupted, the join fails. The Classifier interface preserves the Consumed field exactly as the old facade functions did.

2. **The bufio.Reader is not consumed beyond the handshake.** The sentinel re-uses the same reader to forward pipelined data (e.g., Login Start after Handshake) and later game protocol traffic. Classifiers read only the bytes needed to make a classification decision; remaining data stays in the reader's buffer.

3. **Status replies are protocol-native and don't wake the server.** `classifier.BuildStatusResponse(payload)` returns bytes the client expects; sending them doesn't trigger any game-logic side effects. Callers must check `classifier.SupportsStatusPing()` before calling this method.

4. **Disconnect messages bounce the client cleanly.** `classifier.BuildDisconnect(reason)` returns protocol-native rejection messages; the client receives them, displays the reason, and closes the connection. Each Classifier implementation (MinecraftClassifier, TerrariaClassifier) provides this method to build disconnect packets.

5. **Unknown classification doesn't assume anything.** If `classifier.Classify()` returns a result with Kind == Unknown, the sentinel closes without waking or replying. The client is left to time out naturally.

6. **ClassificationResult is always non-nil on success.** Even when Kind == Unknown, `classifier.Classify()` returns a non-nil result (with Consumed bytes for replay). Callers never see a nil result unless an error occurred.

## Testing & Coverage

**Test structure:**

- **`minecraft_test.go`:** Table-driven tests for VarInt codec, string parsing, handshake classification (various protocol versions, next-states, edge cases), response building (JSON escaping, large payloads), error cases (truncated input, oversized frames). Includes tests for MinecraftClassifier methods on identical wire bytes.
- **`terraria_test.go`:** Similar coverage for 7-bit-encoded-int, Terraria message framing, ConnectRequest parsing, various message types (Join, Unknown, etc.), Disconnect message building. Tests for TerrariaClassifier methods.
- **`gameproto_test.go`:** Unit test for `Kind.String()` across all declared values plus an out-of-range value.
- **`classifier_golden_test.go`:** Golden/characterization tests exercising `MinecraftClassifier.Classify()` and `TerrariaClassifier.Classify()` end-to-end across input scenarios, including the replay contract (Consumed bytes can be re-read without duplication).
- **`demo_test.go`:** Tests for `DemoClassifier.Classify()`, verifying it always returns a non-nil result with Kind == Unknown, nil Consumed, nil Detail, and nil error.
- **`registry_test.go`:** Tests for Lookup(name) success/failure, ListRegistered() output format and sorting, registry completeness. Covers registry structure (all expected protocols registered, no duplicates).

**Key test scenarios:**

- **Replay contract verification:** Classify a handshake, replay the Consumed bytes to a mock upstream, verify the game pod receives the exact bytes.
- **Pipelined data preservation:** Classify a handshake with additional Login Start bytes following; verify the bufio.Reader still has those bytes available.
- **Bounds enforcement:** Oversized frame lengths, oversized strings, malformed VarInts, truncated input — all return errors or Unknown without panicking.
- **Response builders:** Status JSON escaping, chat-component encoding, Terraria NetworkText framing.
- **Registry validation:** New protocols can be added with one file + one registry entry; no other changes required.
- **Classifier interface compliance:** Each Classifier implementation fully implements all four methods (Classify, SupportsStatusPing, BuildStatusResponse, BuildDisconnect) and handles errors correctly.
- **Detail contract verification:** Unknown classifications produce nil Detail; Join/Status classifications produce non-nil Detail carrying protocol-specific metadata.

**Coverage gate:** 90% (enforced by `.testcoverage.yml`). The small gap is in error paths (e.g., malformed VarInts that exceed bounds) and less-critical response-builder edge cases. The refactor must maintain this threshold despite code migration between modules (lines moved from sentinel to gameproto).

## Non-Goals (What Gameproto Does NOT Do)

- **Does not execute joins.** Classification and response building only; the sentinel dispatcher decides whether to wake or reply. The Classifier interface preserves this separation of concerns.
- **Does not validate server addresses or ports.** The ServerAddr field in MinecraftDetail is the raw string the client sent; the sentinel doesn't validate it, only replays it.
- **Does not interact with DNS or network sockets.** Parsing is pure; callers provide a bufio.Reader wrapping a real connection.
- **Does not implement the full game protocol.** Handshake parsing only; full Login, Status, play, etc. phases are the game's job, not gameproto's.
- **Does not authenticate players.** The classified Kind is protocol-based, not player-database-based.
- **Does not provide telemetry or logging.** Callers (sentinel, tests) own logging classified Kind and any parse errors.
- **Does not manage the protocol registry at runtime.** Registration is compile-time via a bare map literal in `gameproto/registry.go`; no runtime registration calls or dynamic updates.

## Dependencies

**Stdlib only:**
- `bufio` — buffered readers for connection streams.
- `bytes` — byte buffers for packet assembly and frame capture.
- `encoding/binary` — big-endian (Minecraft) and little-endian (Terraria) integer encoding.
- `encoding/json` — validating that a status-response payload is well-formed JSON.
- `errors` — error wrapping and identity.
- `fmt` — error formatting.
- `io` — io.Reader, io.ReadFull, io.ByteReader interfaces.
- `sort` — sorting registered protocol names for ListRegistered() output.
- `strconv` — integer parsing (port numbers).
- `strings` — string operations (handshake address parsing).

No external modules.

## Security Considerations

1. **Denial of Service (unbounded allocation):** The frame length limits (512 and 65535) and VarInt loop bounds (5 iterations) prevent a malicious client from causing unbounded memory allocation. The sentinel can safely call the classifiers on untrusted inbound connections.

2. **Stream corruption (pipelined bytes):** The Consumed field tracks exactly what was read, enabling lossless replay. If the sentinel forwards the bufio.Reader's remaining bytes without re-reading Consumed, the game pod receives the correct stream in order.

3. **Status spoofing (information disclosure):** `BuildStatusResponse(payload)` returns exactly what the sentinel provides (no added metadata). The sentinel is responsible for populating the JSON with safe values (no version strings, cluster names, online player counts, etc., per CLAUDE.md login privacy rule).

4. **Response injection (protocol-native escaping):** `BuildDisconnect(reason)` escapes the reason string for JSON safety (quotes, control chars) in Minecraft's case, and validates payload sizes in Terraria's case. The sentinel relies on this; if it bypassed the builder, it could send malformed packets.

## References

- **`gameproto/gameproto.go`** — Kind enumeration, ErrStatusPingUnsupported error constant.
- **`gameproto/classifier.go`** — Classifier interface, ClassificationResult struct, Detail interface, MinecraftDetail, TerrariaDetail concrete types.
- **`gameproto/registry.go`** — Protocol Registry (classifierRegistry map literal, Lookup, ListRegistered functions).
- **`gameproto/minecraft.go`** — MinecraftClassifier implementation and unexported parsing/response-building helpers.
- **`gameproto/terraria.go`** — TerrariaClassifier implementation and unexported parsing/response-building helpers.
- **`gameproto/demo.go`** — DemoClassifier reference implementation (demonstrates registry pattern requires zero edits to shared code when adding a protocol).
- **`sentinel/main.go`** — usage: `Lookup(wakeProtocol).Classify()` and response builder methods (BuildStatusResponse, BuildDisconnect) for hold-and-forward logic. Calls ListRegistered() at startup for validation.
- **`sentinel/main_test.go`** — tests for registry-based dispatcher; test doubles show how to construct wire bytes for testing.
- **`test/e2e/*_bot_e2e_test.go`** (e.g. `minecraft_bot_e2e_test.go`, `terraria_bot_e2e_test.go`) and `test/e2e/wake_on_connect_e2e_test.go` — e2e tests that launch real game bots (or simulate a wake-on-connect dial) to verify handshake replay doesn't corrupt joins.
- **`go.work`** — workspace linking gameproto to sentinel, operator, api, and other Go modules.
- **`docs/architecture.md`** — overview of sentinel's wake-on-connect feature and gameproto's role.
