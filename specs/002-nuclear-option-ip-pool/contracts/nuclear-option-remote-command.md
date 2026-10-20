# Nuclear Option Remote-Command Wire Protocol Contract

**Feature**: 002-nuclear-option-ip-pool  
**Phase**: 1 (Contracts)  
**Date**: 2026-08-21  
**Status**: Proposal for Code Review  

This document specifies the wire protocol for the Nuclear Option dedicated server's remote-command interface (spec FR-006 to FR-012). All details are **PUBLISHER-OFFICIAL** and transcribed exactly from the game's developer documentation to prevent fabrication and drift.

---

## Source of Truth

**Primary Source**: Shockfront Studios' official Nuclear Option Server Tools documentation, `ServerCommands/Readme.md`, retrieved from <https://github.com/Shockfront-Studios/Nuclear-Option-Server-Tools>.

**Live Observation (2026-08-22)**: This contract has been verified against a real running Nuclear Option server on a 3-node k3s cluster (kubelab). The server was instantiated via anonymous SteamCMD install of app 3930080, launched with the `-ServerRemoteCommands` flag, and probed over TCP 127.0.0.1:7779. Observed build hash: `cb745d6c44f1`. All response formats, status codes, command names, and edge cases in this document that are marked **VERIFIED** are transcribed exactly from live packet captures below in the "Live Protocol Verification" section.

**Previous Local Copy (Obsolete)**: The prior transcript at `/tmp/claude-1000/.../516a9c4f-.../scratchpad/nocmd.md` is no longer available. The live capture dated 2026-08-22 supersedes it as the primary evidence.

All claims in this document are quoted directly from the official source or marked **VERIFIED** if confirmed against the live server, unless otherwise marked **UNVERIFIED**.

---

## Enablement

**Activation**: The remote-command console is disabled by default and MUST be explicitly enabled by the operator.

**Launch Flag**: `-ServerRemoteCommands [port]`

**Port Specification**:
- If a port number is provided in the flag, the server listens on that port.
- If no port is provided (i.e., flag is `-ServerRemoteCommands` alone), the server defaults to port **7779**.

**Transport**: **TCP** (not UDP). The server listens for incoming TCP connections on the specified port.

**Authentication**: The remote-command protocol defines **NO authentication mechanism, NO password validation, and NO handshake**. Any client with network access to this port can send commands. (See Security Constraints, below.)

---

## Request Framing (Client → Server)

### Format

All requests follow a strict length-prefixed format:

1. **4 Bytes** (Little-Endian): Length of the JSON payload (the byte count of the UTF-8 JSON string that follows).
2. **N Bytes**: UTF-8 JSON string representing the command message.

### Command Message Structure

The JSON payload MUST be a single object with the following structure:

```json
{
    "name": "command-name",
    "arguments": [
        "argument 0",
        "argument 1",
        "argument 2"
    ]
}
```

**Fields**:
- `name` (string, required): The command name (must match one of the recognized command names below).
- `arguments` (array of strings, required): Command arguments. May be empty for commands that take no arguments. Each argument is a UTF-8 string. The server validates argument count and type per command.

### Example Request

To send the `get-player-list` command (which takes no arguments):

```
[0x09, 0x00, 0x00, 0x00]         # Length: 9 bytes (little-endian)
{"name":"get-player-list","arguments":[]}
```

To send `kick-player` with a Steam ID:

```
[0x45, 0x00, 0x00, 0x00]         # Length: 69 bytes
{"name":"kick-player","arguments":["0123456789"]}
```

---

## Response Framing (Server → Client)

### Format (Asymmetric to Request)

Responses are **NOT** a mirror of the request format. The response framing is distinctly different:

1. **4 Bytes** (Little-Endian): Status code (integer, e.g., `2000` for Success, `4000` for BadRequest).
2. **4 Bytes** (Little-Endian): Length of the JSON body (may be `0` if no body is present).
3. **N Bytes** (only if length > 0): UTF-8 JSON body.

**CRITICAL ASYMMETRY**: The most common implementation error is to treat the response as a mirror of the request. It is not. The response is status code (4 bytes) + body length (4 bytes) + optional body. Request is length (4 bytes) + body. These are structurally different.

### Example Response

Success with a JSON body:

```
[0xd0, 0x07, 0x00, 0x00]         # Status: 2000 (little-endian)
[0x2a, 0x00, 0x00, 0x00]         # Body length: 42 bytes
{"Players":[{"steamId":"123","faction":"Boscali"}]}
```

Success with no body (e.g., `kick-player` success):

```
[0xd0, 0x07, 0x00, 0x00]         # Status: 2000
[0x00, 0x00, 0x00, 0x00]         # Body length: 0
```

Error response:

```
[0x0f, 0x0f, 0x00, 0x00]         # Status: 4003 (JsonError, little-endian)
[0x00, 0x00, 0x00, 0x00]         # Body length: 0 (no error detail)
```

---

## Status Codes & Error Mapping

The server returns an integer status code (4 bytes, little-endian) in the response. The complete status-code vocabulary is:

| Code | Name | HTTP Analog | Interpretation | Agent-Level Go Error |
|------|------|-------------|-----------------|----------------------|
| **2000** | Success | 200 OK | Command executed successfully. Body may contain result data or be absent. | `nil` — no error. Parse the body if present. |
| **4000** | BadRequest | 400 Bad Request | Malformed request (generic client error not covered by more specific codes below). | `ErrBadRequest` or `fmt.Errorf("bad request")` |
| **4001** | BadHeader | 400 Bad Request | Error in the request header (e.g., length field is corrupted or incomplete). | `ErrBadHeader` or `fmt.Errorf("bad request header")` |
| **4002** | BadLength | 400 Bad Request | Invalid length value (negative, overflow, or exceeds maximum allowed size). | `ErrBadLength` or `fmt.Errorf("invalid request length")` |
| **4003** | JsonError | 400 Bad Request | Failed to parse the `CommandMessage` JSON (malformed JSON, missing required fields, type mismatch). | `ErrJsonError` or `fmt.Errorf("failed to parse JSON")` |
| **4004** | UnknownCommand | 404 Not Found | The command name is not recognized by the server. | `ErrUnknownCommand` or `fmt.Errorf("unknown command: %s", name)` |
| **4005** | BadArguments | 400 Bad Request | The command was recognized, but the provided arguments are invalid, missing, or out of range. | `ErrBadArguments` or `fmt.Errorf("invalid arguments for command %s: %s", name, detail)` |
| **5000** | InternalServerError | 500 Internal Server Error | General server-side error (unclassified). | `ErrInternalServerError` or `fmt.Errorf("server error")` |
| **5001** | CommandError | 500 Internal Server Error | An error occurred during the execution of the command's logic (e.g., player not found when trying to kick, mission name invalid). | `ErrCommandError` or `fmt.Errorf("command failed: %s", detail)` |
| **5002** | ConfigError | 500 Internal Server Error | The server configuration prevents the command from completing (e.g., no ban lists are configured, so `banlist-add` cannot write). | `ErrConfigError` or `fmt.Errorf("config error: %s", detail)` |

**Agent Implementation Guidance**:
- Status codes 2000 are success; parse and return the response body (if present) to the user.
- Status codes 4xxx are client errors; report to the user as an invalid request or command error.
- Status codes 5xxx are server errors; report to the user as a server-side failure (may be retryable depending on the specific reason).
- Never assume a response body is present; check the body-length field. If it is 0, there is no JSON to parse.

---

## Complete Command List & Specifications

The server registers 20 commands at startup. This document previously listed 19. The registered command set, confirmed via live observation on 2026-08-22, is presented below with their request format, required arguments, and response body format. 

**CRITICAL FINDING — Three Contradictions with Published Assumption**:

1. **`set-next-mission` argument count**: The contract previously assumed a single mission-name argument. Live observation shows the server rejects this with error 4005: `Expected Arguments [string Group, string Name, float MaxTime]` — the signature is **three arguments** (mission group, mission name, and duration), not one. The contract has been corrected in section 9 below.

2. **Moderation commands return 2000 even for nonexistent targets**: `kick-player`, `banlist-add`, and `banlist-remove` were assumed to return error 4005 for an unknown Steam ID. Live observation shows all three return **status 2000 with an empty response body** even when the target player is not connected or the Steam ID is not present. Success therefore does NOT confirm the target existed; the operator must verify outcomes through other means (e.g., observing a player disconnect after a kick, or checking a subsequent ban-list response).

3. **`get-player-list` response wrapper key is capital-P**: The contract previously listed `"Players"` without confirmation. Live observation confirms the wrapper key is **`"Players"` with a capital P**, not lowercase. The per-entry shape (steamId, faction, displayName) could not be fully verified because no player was connected during the probe; the list returned empty as `{"Players":[]}`.

Where the live capture covers a command, that observed behavior is marked **VERIFIED**. Where the capture did not reach a command (no player was connected to test player-list hydration, no error scenarios were exhaustively probed), it remains **UNVERIFIED**.

**Second live capture (2026-10-11, T019, no player connected)**: run against a server from the repo template on kubelab, over TCP 7779 from inside the game container (4-byte LE length prefix + JSON; response 4-byte LE status + 4-byte LE body length + body). Observed:

| Command | Status | Body | Server log |
|---|---|---|---|
| `get-player-list` | 2000 | `{"Players":[]}` | none notable |
| `send-chat-message "hello from gameplane"` | 2000 | empty | `Error running send-chat-message on main thread: System.NullReferenceException` in `RunSendChatMessage` |
| `banlist-add 76561198000000001` | 2000 | empty | `Adding … to list`; appends to `ban_list.txt` only if the file already exists (see `modules/nuclear-option/specs.md` §4) |
| `banlist-remove 76561198000000001` | 2000 | empty | `Removed … from list`; removes the line from `ban_list.txt` if the file exists |
| `kick-player 76561198000000002` (not connected) | 2000 | empty | `RunKickPlayer failed to find user …, but adding them to kicked list so they can not rejoin` |
| `set-next-mission BuiltIn Escalation 3600` | 2000 | empty | none |
| `set-next-mission Bogus Nope 10` | 2000 | empty | none |
| unknown command name | 4004 | empty | none |

Consequences: (1) status 2000 does not mean the action worked: `send-chat-message` returned 2000 while the server threw a `NullReferenceException` (cause unknown; it may be only the no-player case, and chat with a player connected was **not observed**); (2) `kick-player` for an ID that is not connected still puts the ID on the kicked list. Per-entry `get-player-list` shape with a player, and the `kick-player`/`send-chat-message` effects on a connected client, remain **UNVERIFIED** (no client available).

### 1. `update-ready`

**Purpose**: Notifies the server that a component is ready, likely to progress a startup sequence.

**Arguments**: None.

**Request**:
```json
{"name": "update-ready", "arguments": []}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 2. `send-chat-message`

**Purpose**: Sends a server message to be displayed in the in-game chat. Supports Rich Text formatting (see official source).

**Arguments**: 
- `arguments[0]` (required): The chat message string. Supports Unity TextMeshPro rich text tags (e.g., `<color=#ff0000><b>Alert:</b></color>`).

**Request**:
```json
{"name": "send-chat-message", "arguments": ["<color=#ff0000><b>Alert:</b></color> Important server message."]}
```

**Response**: Status 2000, **body is empty** (VERIFIED 2026-08-22: status 2000, bodylen 0).

---

### 3. `reload-config`

**Purpose**: Instructs the dedicated server to reload its configuration.

**Arguments**:
- `arguments[0]` (optional): Path to a new config file. If omitted, the server reloads the previously-used config file.

**Request (with new path)**:
```json
{"name": "reload-config", "arguments": ["Optional/path/to/new/config.json"]}
```

**Request (reload previous)**:
```json
{"name": "reload-config", "arguments": []}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 4. `get-mission-time`

**Purpose**: Retrieves the current and maximum mission time (in seconds).

**Arguments**: None.

**Request**:
```json
{"name": "get-mission-time", "arguments": []}
```

**Response**: Status 2000, body (VERIFIED 2026-08-22):
```json
{
    "currentTime": 0.0,
    "maxTime": 0.0
}
```

**Note**: If no players are on the server, both values are 0. Values are in seconds (float format).

---

### 5. `get-mission`

**Purpose**: Retrieves the currently running mission and the next mission scheduled.

**Arguments**: None.

**Request**:
```json
{"name": "get-mission", "arguments": []}
```

**Response**: Status 2000, body (VERIFIED 2026-08-22):
```json
{
    "currentMission": {
        "Key": {
            "Group": "",
            "Name": ""
        },
        "MaxTime": 0.0
    },
    "nextMission": {
        "Key": {
            "Group": "BuiltIn",
            "Name": "Escalation"
        },
        "MaxTime": 7200.0
    }
}
```

**Note**: When the server has just started with no mission running, `currentMission` has an empty `Group` and `Name` with `MaxTime: 0.0`. The `nextMission` is populated from the configured rotation.

---

### 6. `get-server-id`

**Purpose**: Retrieves the Steam ID of the server.

**Arguments**: None.

**Request**:
```json
{"name": "get-server-id", "arguments": []}
```

**Response**: Status 2000, body (VERIFIED 2026-08-22):
```json
{"serverId": "90291415221858321"}
```

**Note**: The `serverId` is a 17-digit numeric string representing the server's Steam Game Server ID.

---

### 7. `get-player-list`

**Purpose**: Retrieves the list of currently connected players.

**Arguments**: None.

**Request**:
```json
{"name": "get-player-list", "arguments": []}
```

**Response**: Status 2000, body (VERIFIED 2026-08-22 — empty list observed):
```json
{
    "Players": []
}
```

**Note (from official source)**: The dedicated server returns only `steamId` and `faction` fields. The `displayName` field has been removed because the server runs headlessly and does not cache player names. The external server (dashboard/admin console) can fetch display names using Steam's Web API if needed.

**CRITICAL FINDING — Per-Entry Shape Unverified**: Live observation on 2026-08-22 captured an empty player list (`{"Players":[]}`), so the per-entry structure (exact field names, data types, presence of optional fields) could not be confirmed against the live server. The assumed shape from official documentation is:
```json
{
    "steamId": "0123456789",
    "faction": "Boscali"
}
```
However, **this per-entry schema remains UNVERIFIED**. The wrapper key `"Players"` (capital P) is confirmed (re-confirmed 2026-10-11, status 2000, `{"Players":[]}`, 14 bytes); the per-entry contents are not. Not observed on 2026-10-11 either: no game client was available to connect a player, and the join test is skipped awaiting a packet capture (spec FR-004 amendment). The exact field set stays UNVERIFIED until a player connects.

**RESOLVED — display names are hydrated outside this protocol**: Spec 002 FR-007 and User Story 3 AC1 promise a player list with display names in the dashboard. Those requirements **stand as written and are not amended**. This protocol cannot supply display names, and it is not being extended to do so: the resolution is a Steam Web API display-name lookup performed in the **API server** (`api/internal/...`), layered on top of the wire response. The question is closed; no further spec-owner decision is pending.

#### Display-Name Hydration Is Out of Scope for This Contract

The following is stated explicitly so that nobody implements a phantom field in the protocol codec:

- **The wire contract is unchanged.** The `get-player-list` response body is exactly the shape documented above: wrapper key `Players` (capital `P`), objects with `steamId` (lowercase `d`) and `faction`. **There is no `displayName` field on the wire, and none may be added to the codec, the frame parser, or the agent's response struct.**
- **The agent returns exactly what the game returns** — `steamId` and `faction`, nothing more. Adding, defaulting, or synthesizing a name inside the agent is out of scope and prohibited.
- **Name resolution happens in the API server**, not in the agent. This placement is forced, not stylistic: `charts/gameplane/templates/networkpolicies.yaml` declares `default-deny-egress` (policy at line 24, `podSelector: {}` at line 28) over every pod in the games namespace; the only outbound path is the opt-in `allow-game-public-egress` policy (line 149) that exists for SteamCMD downloads. A resolver in the game pod would therefore require a new egress hole in every game pod plus distribution of the Steam Web API key to every game pod. The API server runs in the control-plane namespace and gives one egress path, one Secret, and one shared cache.
- **Outbound calls go through this repo's `netguard` SSRF dial-guard**, using the strict `IsPublic` policy (Steam's Web API is a public internet endpoint). The endpoint is `ISteamUser/GetPlayerSummaries/v2`, which accepts up to 100 steamids per call, so the resolver batches rather than issuing one request per player.
- **The Steam Web API key is optional.** When it is absent, unreachable, timed out, or an id fails to resolve, the player list still renders with the raw Steam ID in place of a name. Name resolution must never block, fail, or error the player-list response (spec SC-004 bounds a moderation command result at 5 seconds, so the lookup carries a hard timeout and degrades rather than exceeding it).
- **Moderation keys on `steamId` only.** `kick-player`, `banlist-add`, and `banlist-remove` continue to take the Steam ID as their argument. A resolved name is display-only and must never become the identifier used for a moderation action.

The presentation-layer entities (wire vs. presentation player entry, the resolution cache, and the API-key configuration) are modelled in `data-model.md`, section 8.

---

### 8. `set-time-remaining`

**Purpose**: Sets the remaining time for the current mission (in seconds).

**Arguments**:
- `arguments[0]` (required): Time in seconds (float format, e.g., `"600.0"`).

**Request**:
```json
{"name": "set-time-remaining", "arguments": ["600.0"]}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 9. `set-next-mission`

**Purpose**: Sets the mission to be loaded after the current one concludes.

**Arguments** (CORRECTED 2026-08-22):
- `arguments[0]` (required): Mission group (string, e.g., `"BuiltIn"`).
- `arguments[1]` (required): Mission name (string, e.g., `"Escalation"`).
- `arguments[2]` (required): Max time in seconds (float format, e.g., `"3600.0"`).

**Request**:
```json
{"name": "set-next-mission", "arguments": ["BuiltIn", "Escalation", "3600.0"]}
```

**CRITICAL CONTRADICTION**: The contract previously assumed a single mission-name argument. Live observation on 2026-08-22 shows the server rejects a single-argument invocation with error status **4005**: `{"message":"Expected Arguments [string Group, string Name, float MaxTime]"}`. The signature is **three arguments** (group, name, duration), as now listed above. Any client implementation must provide all three.

**Response**: Status 2000, **body is empty** (VERIFIED 2026-10-11: `set-next-mission BuiltIn Escalation 3600` returned status 2000, bodylen 0). The same empty 2000 came back for a nonexistent group and name (`Bogus Nope 10`), so the server does not validate the mission against its mission list at command time. A rejected mission, if any, shows only in the server log; the response cannot confirm the mission exists.

---

### 10. `kick-player`

**Purpose**: Kicks a player from the server.

**Arguments**:
- `arguments[0]` (required): Steam ID (unsigned long as string, e.g., `"0123456789"`).

**Request**:
```json
{"name": "kick-player", "arguments": ["0123456789"]}
```

**Response**: Status 2000, **body is empty** (VERIFIED 2026-08-22: status 2000, bodylen 0).

**CRITICAL CONTRADICTION**: The contract previously assumed that a nonexistent Steam ID would return error 4005. Live observation on 2026-08-22 shows the command returns **status 2000 with an empty body even when the target player is not connected**. Success therefore does NOT indicate the target existed. The operator must verify outcomes through other means (e.g., observing the player disconnect, or querying the player list before and after the kick).

Note (from official source): Kicked player cannot rejoin until the server restarts.

---

### 11. `unkick-player`

**Purpose**: Removes a player from the kick list, allowing them to rejoin.

**Arguments**:
- `arguments[0]` (required): Steam ID (unsigned long as string).

**Request**:
```json
{"name": "unkick-player", "arguments": ["0123456789"]}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 12. `clear-kicked-player`

**Purpose**: Removes a single player from the kick list by Steam ID (singular form; see also `clear-kicked-players` below for clearing all).

**Arguments**:
- `arguments[0]` (required): Steam ID (unsigned long as string).

**Request**:
```json
{"name": "clear-kicked-player", "arguments": ["0123456789"]}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 13. `clear-kicked-players`

**Purpose**: Clears the entire list of kicked players, allowing all to rejoin.

**Arguments**: None.

**Request**:
```json
{"name": "clear-kicked-players", "arguments": []}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 14. `banlist-reload`

**Purpose**: Reloads the ban list from the list of files configured in the server config.

**Arguments**: None.

**Request**:
```json
{"name": "banlist-reload", "arguments": []}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

**Note (from official source)**: Will only add new IDs. Use `banlist-clear` before reload if you want to remove all IDs first before reloading.

---

### 15. `banlist-add`

**Purpose**: Adds a Steam ID to the ban list (appends it to the first configured ban file).

**Arguments**:
- `arguments[0]` (required): Steam ID (unsigned long as string, e.g., `"0123456789"`).
- `arguments[1]` (optional): Reason (string, e.g., `"cheating"`).

**Request (with reason)**:
```json
{"name": "banlist-add", "arguments": ["0123456789", "cheating"]}
```

**Request (without reason)**:
```json
{"name": "banlist-add", "arguments": ["0123456789"]}
```

**Response**: Status 2000, **body is empty** (VERIFIED 2026-08-22: status 2000, bodylen 0).

**CRITICAL CONTRADICTION**: The contract previously assumed that a nonexistent Steam ID would return error 4005. Live observation on 2026-08-22 shows the command returns **status 2000 with an empty body even when the Steam ID is not connected or not known to the server**. Success therefore does NOT indicate the target existed or the ban was needed. The operator must verify outcomes through other means (e.g., attempting a join with the banned ID, or querying the ban list).

---

### 16. `banlist-remove`

**Purpose**: Removes a Steam ID from the ban list and from the first configured ban file.

**Arguments**:
- `arguments[0]` (required): Steam ID (unsigned long as string).

**Request**:
```json
{"name": "banlist-remove", "arguments": ["0123456789"]}
```

**Response**: Status 2000, **body is empty** (VERIFIED 2026-08-22: status 2000, bodylen 0).

**CRITICAL CONTRADICTION**: The contract previously assumed that a nonexistent Steam ID would return error 4005. Live observation on 2026-08-22 shows the command returns **status 2000 with an empty body even when the Steam ID is not in the ban list**. Success therefore does NOT indicate the ID was actually banned. The operator must verify outcomes through other means (e.g., querying the ban list before and after the removal).

---

### 17. `banlist-clear`

**Purpose**: Clears the ban list loaded in the Authenticator. Does NOT modify ban list files.

**Arguments**: None.

**Request**:
```json
{"name": "banlist-clear", "arguments": []}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 18. `get-mission-rotation`

**Purpose**: Retrieves the current mission rotation configuration, type, and next override status.

**Arguments**: None.

**Request**:
```json
{"name": "get-mission-rotation", "arguments": []}
```

**Response**: Status 2000, body (VERIFIED 2026-08-22):
```json
{
    "rotationType": "Sequence",
    "rotation": [
        {
            "Key": {
                "Group": "BuiltIn",
                "Name": "Escalation"
            },
            "MaxTime": 7200.0
        },
        {
            "Key": {
                "Group": "BuiltIn",
                "Name": "Terminal Control"
            },
            "MaxTime": 7200.0
        }
    ],
    "hasNextOverride": false,
    "nextOverride": {
        "Key": {
            "Group": "",
            "Name": ""
        },
        "MaxTime": 0.0
    }
}
```

**Note**: When no override is set, `hasNextOverride` is `false` and `nextOverride` contains empty strings and zero values.

---

### 19. `set-mission-rotation`

**Purpose**: Sets the mission rotation configuration in memory and reloads the configuration.

**Arguments**:
- `arguments[0]` (required): Entire rotation configuration serialized as a JSON string. (Because `arguments` is an array of strings, complex objects must be JSON-encoded as a single string.)

**Request Example**:
```json
{
    "name": "set-mission-rotation",
    "arguments": [
        "{\"rotationType\":\"Sequence\",\"clearNextOverride\":false,\"rotation\":[{\"Key\":{\"Group\":\"BuiltIn\",\"Name\":\"Escalation\"},\"MaxTime\":7200.0}]}"
    ]
}
```

**Nested JSON Structure** (the string in `arguments[0]` deserializes to):
```json
{
    "rotationType": "Sequence",
    "clearNextOverride": false,
    "rotation": [
        {
            "Key": {
                "Group": "BuiltIn",
                "Name": "Escalation"
            },
            "MaxTime": 7200.0
        }
    ]
}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

### 20. `clear-next-mission`

**Purpose**: Clears the next overridden mission, reverting back to the standard rotation.

**Arguments**: None.

**Request**:
```json
{"name": "clear-next-mission", "arguments": []}
```

**Response**: Status 2000, body: **UNVERIFIED — response body shape not specified upstream; confirm against a live server**.

---

## Live Protocol Verification — 2026-08-22

**Test Environment**: 3-node k3s cluster (kubelab). Nuclear Option dedicated server deployed from Steam app 3930080 via anonymous SteamCMD install. Build hash: `cb745d6c44f1`. Binary: `NuclearOptionServer.x86_64` (native Linux, 891 MB total installed footprint).

**Invocation**: `./NuclearOptionServer.x86_64 -batchmode -nographics -ServerRemoteCommands`. Server bound to 127.0.0.1:7779 (TCP, loopback only). No player was connected during the probe.

**Captured Responses** (status code + body length + body, as observed over TCP):

| Command | Status | Body Length | Response Body |
|---------|--------|------------|---|
| `get-player-list` (no args) | 2000 | 14 | `{"Players":[]}` |
| `get-server-id` (no args) | 2000 | 32 | `{"serverId":"90291415221858321"}` |
| `get-mission` (no args) | 2000 | 142 | `{"currentMission":{"Key":{"Group":"","Name":""},"MaxTime":0.0},"nextMission":{"Key":{"Group":"BuiltIn","Name":"Escalation"},"MaxTime":7200.0}}` |
| `get-mission-time` (no args) | 2000 | 33 | `{"currentTime":0.0,"maxTime":0.0}` |
| `get-mission-rotation` (no args) | 2000 | 260 | `{"rotationType":"Sequence","rotation":[{"Key":{"Group":"BuiltIn","Name":"Escalation"},"MaxTime":7200.0},{"Key":{"Group":"BuiltIn","Name":"Terminal Control"},"MaxTime":7200.0}],"hasNextOverride":false,"nextOverride":{"Key":{"Group":"","Name":""},"MaxTime":0.0}}` |
| `send-chat-message` (with message) | 2000 | 0 | (empty) |
| `set-next-mission` (1 arg only) | 4005 | 75 | `{"message":"Expected Arguments [string Group, string Name, float MaxTime]"}` |
| `banlist-add` (with Steam ID) | 2000 | 0 | (empty) |
| `banlist-remove` (with Steam ID) | 2000 | 0 | (empty) |
| `kick-player` (with Steam ID) | 2000 | 0 | (empty) |
| `bogus-command` (invalid command) | 4004 | 0 | (empty) |
| `malformed-json` (invalid JSON) | 4003 | 0 | (empty) |

**Registered Commands at Server Startup** (from `[ServerRemoteCommands] Adding command` log lines):
1. `banlist-add`
2. `banlist-clear`
3. `banlist-reload`
4. `banlist-remove`
5. `clear-kicked-player` ← **NEW — not in previous contract, now item 12**
6. `clear-kicked-players`
7. `clear-next-mission`
8. `get-mission`
9. `get-mission-rotation`
10. `get-mission-time`
11. `get-player-list`
12. `get-server-id`
13. `kick-player`
14. `reload-config`
15. `send-chat-message`
16. `set-mission-rotation`
17. `set-next-mission`
18. `set-time-remaining`
19. `unkick-player`
20. `update-ready`

**Total: 20 commands** (previous contract listed 19; missing `clear-kicked-player` singular form).

**Network Footprint** (via `/proc/net`):
- TCP LISTEN 127.0.0.1:7779 (remote-command port, loopback only)
- TCP LISTEN 127.0.0.1:38455 (internal, purpose unknown)
- UDP bound on ports 7778 and 45793 (query and ephemeral)
- **UDP port 7777 NOT bound** — this contradicts the assumption that UDP 7777 is the game join port

**Readiness Signal** (from server startup logs):
```
3.903: [DedicatedServerManager] Waiting for Players before loading next map
```
This log line appears at approximately 3.9 seconds after startup and marks the point at which the server is ready to accept connections.

**Log Configuration**:
- Via `RunServer.sh`: logs written to `./logs/server-$(date +%Y-%m-%d-%H-%M-%S).log`
- Flag: `-logFile <path>` overrides the log location
- CRD Control: `-DedicatedServer <path/to/config.json>` overrides the config location (also controls log rotation if specified in config)

**Auto-Generated Configuration Schema** (first boot, as observed in `DedicatedServerConfig.json`):
```json
{
  "MissionDirectory": "/home/steam/NuclearOption-Missions",
  "ModdedServer": false,
  "Hidden": false,
  "ServerName": "Nuclear Option Server",
  "Port": {
    "IsOverride": false,
    "Value": 0
  },
  "QueryPort": {
    "IsOverride": false,
    "Value": 0
  },
  "Password": "",
  "MaxPlayers": 16,
  "BanListPaths": ["ban_list.txt"],
  "DisableErrorKick": false,
  "ErrorKickImmuneListPaths": [],
  "NoPlayerStopTime": 30.0,
  "PostMissionDelay": 30.0,
  "RotationType": 0,
  "MissionRotation": [
    {
      "Key": {
        "Group": "BuiltIn",
        "Name": "Escalation"
      },
      "MaxTime": 7200.0
    },
    {
      "Key": {
        "Group": "BuiltIn",
        "Name": "Terminal Control"
      },
      "MaxTime": 7200.0
    }
  ],
  "VoteKick": {
    "Enabled": true,
    "PassRatio": 0.6000000238418579,
    "MinVotes": 3,
    "AutoBanThreshold": 3,
    "VoteDuration": 45.0,
    "ResolutionDisplayTime": 20.0,
    "NewVoteLockout": 10.0,
    "RequesterCooldown": 300.0
  }
}
```

**Note on Port 7777**: The assumed UDP game join port 7777 was NOT observed listening on this server instance. Port 7778 is bound (query port), and the server logs indicate `SteamGameServer.LogOnAnonymous` and `Set Advertise Server: True`, suggesting that game client traffic is routed through Steam's game-server networking rather than a raw UDP 7777 socket. This is a significant finding; the module's join test and documentation must clarify this mechanism before claiming join-coverage. See the spec.md amendments for follow-up questions on this finding.

---

## Security Constraints (CRITICAL)

**NO AUTHENTICATION, NO PASSWORD, NO HANDSHAKE**

The remote-command protocol defines:
- No username/password mechanism.
- No TLS/SSL encryption.
- No digital signature or HMAC validation.
- No session token or nonce.
- No challenge-response handshake.

**Any client with network access to the port can send any command.**

**Consequences of Exposed Port**:
- An unauthenticated attacker with access to this port can:
  - Kick or ban players (destructive).
  - Change the mission rotation (disrupting play).
  - Broadcast chat messages (griefing).
  - Retrieve the list of connected players and their Steam IDs (reconnaissance).

**Hard Requirement for Gameplane**:
1. **MUST NOT advertise the remote-command port** on the externally exposed Service (LoadBalancer, NodePort, etc.).
2. **MUST only reach this port via pod-local loopback** (localhost/127.0.0.1) from the agent sidecar.
3. **MUST NOT allow external client connections** to this port.

**Implementation Detail** (per spec FR-006):
- The agent sidecar (`agent/internal/rcon/` or a new `agent/internal/nuclearoption/` package) connects to `localhost:7779` (or the configured port) over TCP.
- The game container exposes the remote-command port internally; the agent reaches it without leaving the pod.
- The operator must not manually expose this port to the network.
- If a user manually adds this port to the exposed Service (via `ServiceAnnotations` or other means), they are bypassing security; Gameplane MUST detect and warn against this.

**Consequence if Violated**: An operator who accidentally exposes this port to the internet exposes their server to remote takeover (arbitrarily kick/ban players, change settings, gather player data).

---

## Agent-Side Protocol Identifier & Integration

The existing console protocol allowlist (per CLAUDE.md memory, "Console protocols phase 2") includes:
- `telnet` (implemented in `agent/internal/rcon/telnet.go`)
- `websocket` (implemented in `agent/internal/rcon/websocket.go`)
- `battleye` (implemented in `agent/internal/rcon/battleye.go`)
- `satisfactory` (implemented in `agent/internal/rcon/satisfactory.go`)
- `palworld` (implemented in `agent/internal/rcon/palworld.go`)
- `none` (no console protocol)

A new protocol identifier for Nuclear Option must be added:
- **Protocol Name**: `nuclearoption` (lowercase, single word, no separators; does NOT need to match the module name — existing protocols already differ this way).
- **Location**: New implementation file `agent/internal/rcon/nuclearoption.go` (or similar) containing a client that speaks the length-prefixed JSON wire format described above.
- **Registration**: The allowlist in the agent (wherever protocols are enumerated) must include `"nuclearoption"`.

**Also Update**: The module repository (`gameplane-module`, checked out as `modules/` submodule) maintains its own `validate.py` script with a `RCON_PROTOCOLS` allowlist (per CLAUDE.md memory). After this contract is implemented:
1. The agent must support `"nuclearoption"` protocol.
2. The module repo's `validate.py` must also add `"nuclearoption"` to its allowlist so that `nuclear-option/template.yaml` is valid when it declares `rcon.protocol: nuclearoption`.

---

## File & Artifact References

- **Agent RCON Package**: `agent/internal/rcon/` (telnet.go, websocket.go, battleye.go, satisfactory.go, palworld.go, rcon.go)
- **Console Package**: `agent/internal/console/console.go` (WebSocket server for console endpoints)
- **Module Repository Validation**: `gameplane-module` repo, `validate.py` (maintains `RCON_PROTOCOLS` allowlist)
