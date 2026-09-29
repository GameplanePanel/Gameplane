# Web Dashboard & API Contract: Module Builder

**Branch**: `010-easy-module-building` | **Date**: 2026-08-27 | **Spec**: [spec.md](../spec.md)

This contract specifies the REST API endpoints in `api/` and the React frontend components in `web/` that enable authoring, validating, previewing, and installing Gameplane modules directly from the web dashboard.

---

## 1. REST API Endpoints (`api/internal/handlers/modules_builder.go`)

Mounted under `/modules/builder` on the API router:

### 1.1 `POST /modules/builder/scaffold`
Generates initial YAML manifests and assets from archetype presets and user inputs.

**Request Body**:
```json
{
  "name": "my-game",
  "displayName": "My Game",
  "archetype": "steamcmd",
  "image": "ghcr.io/valgulnecron/my-game:latest@sha256:...",
  "ports": [
    { "name": "game", "containerPort": 27015, "protocol": "UDP", "advertise": true }
  ],
  "categories": ["Survival", "Co-op"],
  "summary": "Dedicated server for My Game"
}
```

**Response Body (200 OK)**:
```json
{
  "moduleYaml": "# yaml-language-server: $schema=...\napiVersion: gameplane.local/module/v1\n...",
  "templateYaml": "# yaml-language-server: $schema=...\napiVersion: gameplane.local/v1alpha1\n...",
  "readmeMd": "# My Game\n\n...",
  "iconBase64": "iVBORw0KGgoAAAANSUhEUgAAAQAAAAEACAYAAABccqhm..."
}
```

---

### 1.2 `POST /modules/builder/validate`
Executes instant offline validation on in-memory YAML strings, returning diagnostics.

**Request Body**:
```json
{
  "moduleYaml": "apiVersion: gameplane.local/module/v1\nname: my-game\n...",
  "templateYaml": "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n..."
}
```

**Response Body (200 OK)**:
```json
{
  "clean": false,
  "errorCount": 1,
  "warningCount": 0,
  "findings": [
    {
      "level": "ERROR",
      "ruleId": "image-unpinned",
      "file": "template.yaml",
      "line": 14,
      "message": "default image is not pinned with a digest",
      "remediation": "Pin image with @sha256:... or add # gameplane:floating"
    }
  ]
}
```

---

### 1.3 `POST /modules/builder/preview`
Simulates server creation from `template.yaml` content, evaluating `autoFromMemoryLimit` and env var precedence.

**Request Body**:
```json
{
  "templateYaml": "apiVersion: gameplane.local/v1alpha1\nkind: GameTemplate\n...",
  "versionId": "v1.0",
  "memory": "4Gi",
  "config": {
    "SERVER_PASSWORD": "secretpassword"
  }
}
```

**Response Body (200 OK)**:
```json
{
  "resolvedImage": "example/game:v1.0@sha256:...",
  "effectiveEnv": [
    { "name": "EULA", "value": "TRUE", "source": "template" },
    { "name": "MAX_MEMORY", "value": "3072M", "source": "configSchema" }
  ],
  "computedConfig": {
    "MAX_MEMORY": "3072M"
  },
  "ports": [
    { "name": "game", "containerPort": 25565, "protocol": "TCP", "advertise": true }
  ],
  "storage": {
    "size": "10Gi",
    "mountPath": "/data"
  }
}
```

---

### 1.4 `POST /modules/builder/export`
Bundles the authored files into a valid `.tar.gz` OCI bundle archive.

**Request Body**:
```json
{
  "name": "my-game",
  "moduleYaml": "...",
  "templateYaml": "...",
  "readmeMd": "...",
  "iconBase64": "...",
  "targetSource": "community-upload"
}
```

**Behavior**:
- If `targetSource` is supplied: Writes the bundle files directly into the named `upload`-type `ModuleSource` ConfigMap (`module-upload-{name}`). The operator's upload source controller discovers the module, registers it in the catalog, and makes it available for cluster installation. Returns `201 Created` with `{ "installed": true, "moduleName": "my-game" }`.
- If `targetSource` is omitted: Returns `200 OK` with binary `application/gzip` stream and `Content-Disposition: attachment; filename="my-game.tar.gz"`.

---

## 2. Web UI Component Structure (`web/src/`)

### 2.1 Entry Point on `/modules` (`Modules.tsx`)
- Action header adds a primary **"Create module"** button with a `Plus` icon next to the existing "Upload module" button.
- Clicking opens `BuildModuleDialog`.

### 2.2 `BuildModuleDialog.tsx`
A 3-step modal wizard using HeroUI Modal primitives:

1. **Step 1: Preset & Metadata**
   - Archetype card selector: `SteamCMD Dedicated Server`, `Java Application Server`, `Generic Container`.
   - Name input (with real-time DNS-1123 regex feedback).
   - Display title, summary, and category chips selection (from canonical taxonomy).

2. **Step 2: Container & Ports**
   - Server image input with digest pinning validation badge.
   - Dynamic port list builder (port number, protocol TCP/UDP, advertise checkbox).
   - Storage volume size and mount path.

3. **Step 3: Review, Live Preview & Export**
   - Dual-pane layout:
     - Left pane: Tabbed code viewer (`module.yaml`, `template.yaml`, `README.md`).
     - Right pane: Live validation findings (green "Valid" banner or red error list) + Memory limit preview slider (`1Gi` to `16Gi` showing computed heap).
   - Action footer:
     - "Download .tar.gz" button
     - "Install to Cluster" button (if `upload` ModuleSource exists).

---

## 3. Constitution Principle II Compliance (Design-First)

Before implementing the React components in `web/`:
1. Open `design.pen` using the `pencil` MCP server.
2. Design the `BuildModuleDialog` modal frames (Step 1, Step 2, Step 3 preview state with validation banner).
3. Export the touched frames to `design-export/json/` and `design-export/screenshots/`.
4. Implement the React components in `web/` strictly adhering to the committed Pencil design.
