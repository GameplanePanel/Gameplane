# Gameplane Module Specification: <Display Name>

## 1. Purpose & Scope

- **Game**: `<Canonical Game Title>`
- **Module Slug**: `<module-name>`
- **Role**: Dedicated server module package for Gameplane.
- **Description**: `<Summary of server capabilities, game engine, and multi-player model>`

---

## 2. Container Image & Architecture

- **Base Image**: `<upstream OCI image reference with @sha256: digest>`
- **Architecture**: `linux/amd64` (and `linux/arm64` if supported)
- **Runtime Model**: `<SteamCMD / standalone binary / Wine-Proton / LinuxGSM>`
- **User & Execution Context**: UID `<UID>`, GID `<GID>`, working directory `<WorkingDir>`.

---

## 3. Network Ports & Protocols

Declared ports under `spec.ports`:

| Port Name | Container Port | Protocol | Usage / Purpose |
|---|---|---|---|
| `game` | `<port>` | `UDP` | Primary client game traffic |
| `query` | `<port>` | `UDP` | Server browser & A2S / wire discovery query |
| `rcon` | `<port>` | `TCP` | Remote administrative console |

---

## 4. Storage & Persistence Layout

- **Mount Path**: `<spec.storage.mountPath>`
- **Default Sizing**: `<size>` (e.g. `10Gi`)
- **Persisted Content**:
  - Saved worlds and match state
  - Server configuration files
  - Banlists and player identity records
- **Non-Shadowing Invariant**: The mount path does not shadow entrypoint executables or baked launcher scripts.

---

## 5. Administration & Remote Console (RCON)

- **Protocol**: `<source | battleye | websocket | satisfactory | palworld | nuclearoption | rest | cli | none>`
- **Console Mode**: `<rcon | pty | none>`
- **Authentication**: Password supplied via `<spec.rcon.passwordEnv>` or `<passwordSecretRef>`.
- **Command Support**: Standard in-game administrative commands.

---

## 6. Modding & Workshop Integration

- **Modding Framework**: `<None / Steam Workshop / BepInEx / Custom Vehicles & Plugins>`
- **Mod Directory Path**: `<spec.capabilities.mods.path or versions>`
- **Workshop Synchronization**: `<Automatic via SteamCMD / collection ID / manual volume mount>`

---

## 7. Lifecycle & Graceful Shutdown

- **Stop Command Sequence (`spec.capabilities.lifecycle.stop`)**:
  ```yaml
  capabilities:
    lifecycle:
      stop:
        - "<pre-stop save command>"
  ```
- **Signal Handling**: Container traps `SIGINT` / `SIGTERM` and initiates clean world flush.

---

## 8. Key Invariants & Security

- **User Matching**: `spec.security.runAsUser` matches image user (`<UID>`).
- **Environment**: `spec.env` contains `HOME` if required for SteamCMD.
- **Filesystem Permissions**: `spec.security.fsGroup` configured when dropping privileges from root.

---

## 9. References & Upstream Documentation

- Official Game Server Documentation: `<URL>`
- Upstream Container Repository: `<URL>`
- Steam Dedicated Server AppID: `<AppID>`
