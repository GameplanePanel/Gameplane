# Archetypes Specification & Contract

**Branch**: `010-easy-module-building` | **Date**: 2026-08-27 | **Spec**: [spec.md](../spec.md)

This contract defines the built-in starter archetypes supported by `gp-module init`, specifying their metadata presets, container parameters, port definitions, storage mappings, and configuration schema fields.

---

## 1. Supported Archetypes

| Archetype ID | Target Game Architecture | Common Examples | Primary Characteristics |
| :--- | :--- | :--- | :--- |
| `steamcmd` | Valve SteamCMD Dedicated Server | Palworld, CS2, Valheim, Rust, DayZ | Steam AppID configuration, UDP game and query ports, persistent server data volume, non-root security defaults. |
| `java` | JVM-Based Game Server | Minecraft Java Edition, Vintage Story | Memory-managed Java runtime, `autoFromMemoryLimit` heap calculation, EULA agreement flag, TCP game port, RCON console. |
| `generic` | Standalone Binary or Container Server | Factorio, Terraria, custom engines | Standard container runtime, configurable TCP/UDP port mapping, persistent storage, graceful SIGTERM termination. |

---

## 2. Archetype Definitions

### 2.1 Archetype: `steamcmd`

#### `module.yaml` Defaults:
```yaml
# yaml-language-server: $schema=../.schema/module.schema.json
apiVersion: gameplane.local/module/v1
name: {name}
displayName: {displayName}
version: 1.0.0
game: {name}
categories: [Survival, Co-op]
summary: Dedicated server for {displayName} powered by SteamCMD
homepage: ""
license: MIT
icon: icon.png
```

#### `template.yaml` Defaults:
```yaml
# yaml-language-server: $schema=../.schema/gametemplate.schema.json
apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: {name}
  labels:
    gameplane.local/module: {name}
spec:
  displayName: {displayName}
  game: {name}
  version: 1.0.0
  categories: [Survival, Co-op]
  description: |
    Dedicated game server for {displayName} managed by Gameplane.
  image: {image} # e.g. cm2network/steamcmd:root@sha256:...
  ports:
    - name: game
      containerPort: {primaryPort} # default 27015
      protocol: UDP
      advertise: true
    - name: query
      containerPort: {queryPort}   # default 27016
      protocol: UDP
      advertise: true
  storage:
    size: 20Gi
    mountPath: /serverdata
  env:
    - name: STEAMAPPID
      value: "{appId}"
    - name: SERVER_NAME
      value: "{displayName} Server"
  configSchema:
    - name: SERVER_PASSWORD
      displayName: Server Password
      description: Optional password required to join the server
      type: password
      required: false
    - name: MAX_PLAYERS
      displayName: Max Players
      description: Maximum concurrent player count
      type: int
      default: "16"
      min: 1
      max: 128
  capabilities:
    lifecycle:
      stop:
        - "quit"
```

---

### 2.2 Archetype: `java`

#### `module.yaml` Defaults:
```yaml
# yaml-language-server: $schema=../.schema/module.schema.json
apiVersion: gameplane.local/module/v1
name: {name}
displayName: {displayName}
version: 1.0.0
game: {name}
categories: [Sandbox, Survival]
summary: Java-based dedicated server for {displayName}
homepage: ""
license: MIT
icon: icon.png
```

#### `template.yaml` Defaults:
```yaml
# yaml-language-server: $schema=../.schema/gametemplate.schema.json
apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: {name}
  labels:
    gameplane.local/module: {name}
spec:
  displayName: {displayName}
  game: {name}
  version: 1.0.0
  categories: [Sandbox, Survival]
  description: |
    Java-based dedicated game server for {displayName} managed by Gameplane.
  image: {image} # e.g. eclipse-temurin:21-jre-jammy@sha256:...
  ports:
    - name: game
      containerPort: {primaryPort} # default 25565
      protocol: TCP
      advertise: true
    - name: rcon
      containerPort: 25575
      protocol: TCP
      advertise: false
  storage:
    size: 10Gi
    mountPath: /data
  env:
    - name: EULA
      value: "TRUE"
    - name: ENABLE_RCON
      value: "true"
    - name: RCON_PORT
      value: "25575"
  configSchema:
    - name: MAX_MEMORY
      displayName: Maximum Memory Heap
      description: Maximum memory allocated to the Java Virtual Machine
      type: string
      autoFromMemoryLimit:
        percent: 75
    - name: RCON_PASSWORD
      displayName: RCON Password
      description: Administrative password for remote console access
      type: password
      required: false
  capabilities:
    lifecycle:
      stop:
        - "stop"
```

---

### 2.3 Archetype: `generic`

#### `module.yaml` Defaults:
```yaml
# yaml-language-server: $schema=../.schema/module.schema.json
apiVersion: gameplane.local/module/v1
name: {name}
displayName: {displayName}
version: 1.0.0
game: {name}
categories: [Co-op]
summary: Dedicated game server for {displayName}
homepage: ""
license: MIT
icon: icon.png
```

#### `template.yaml` Defaults:
```yaml
# yaml-language-server: $schema=../.schema/gametemplate.schema.json
apiVersion: gameplane.local/v1alpha1
kind: GameTemplate
metadata:
  name: {name}
  labels:
    gameplane.local/module: {name}
spec:
  displayName: {displayName}
  game: {name}
  version: 1.0.0
  categories: [Co-op]
  description: |
    Dedicated game server for {displayName} managed by Gameplane.
  image: {image}
  ports:
    - name: game
      containerPort: {primaryPort} # default 8080
      protocol: TCP
      advertise: true
  storage:
    size: 5Gi
    mountPath: /data
  env: []
  configSchema: []
```

---

## 3. Metadata Name Handling

The `template.yaml` emitted by `gp-module init` includes `metadata.name` set to the module slug (matching `module.yaml#name`). This field serves three roles:

1. **Scaffold Emission:** `gp-module init` emits `metadata.name` as a placeholder equal to the module slug provided at initialization.
2. **Validator Requirement:** The offline validator (`gp-module validate`) requires `metadata.name` to be present and non-empty in any `template.yaml`; bundles lacking this field fail validation.
3. **Operator Authority:** The Gameplane operator (`gameplane-operator`) authoritatively **overwrites** `metadata.name` with the Module resource's own name during materialization. The emitted value is therefore advisory only and does not constrain the final GameTemplate name.

This design ensures scaffolded bundles are immediately valid and installable, while allowing flexible deployment naming independent of module authorship.

---

## 4. Placeholder Assets

For all archetypes, scaffolding generates:
- `README.md`: Pre-populated with configuration guidance, environment variable summaries, and port documentation.
- `icon.png`: A valid 256x256 RGBA PNG placeholder file with a clean visual background and centered placeholder badge, ensuring bundles pass all OCI layer checks out of the box.
