# Gameplane Game Server Images

This directory contains container definitions for game servers published by Gameplane. Images are built and pushed to GHCR via `.github/workflows/images.yaml`; every push is cryptographically signed with cosign.

## Why Gameplane publishes its own images

1. **No third-party image exists** — some games (e.g., Nuclear Option) have no official or community-maintained server container. Gameplane builds and maintains the wrapper.
2. **License compliance** — we do not redistribute game server binaries. Game files are downloaded at container startup from official sources (Steam, Epic, etc.) into a persistent volume. The EULAs are silent on redistribution; we simply never redistribute.
3. **Consistency** — official images guarantee every pod launched from a Gameplane module uses identical binaries, library versions, and runtime configuration.

Official module templates pin game images by digest (not tag), making pinning immutable and reproducible.

## Layout

```
containers/
├── common/
│   └── steamcmd/
│       ├── Dockerfile            # SteamCMD base for Steam-distributed games
│       └── steam-install.sh       # Shared runtime installer helper
└── games/
    └── nuclear-option/
        ├── Dockerfile            # Game-specific image (FROM common-steamcmd)
        └── entrypoint.sh          # Game-specific startup logic
```

### `common/` — reusable base images

Common bases are built first and used by multiple game images. Today: **`steamcmd/`** — an Ubuntu 26.04 LTS image with the official SteamCMD client at `/usr/bin/steamcmd` and the `steam-install.sh` helper on PATH. The base runs as a non-root user `gameserver` (UID 10000, GID 10000 by default; both are customizable via `GAMESERVER_UID` and `GAMESERVER_GID` build-args).

The base contains **no game files**. Individual games download at runtime into a persistent volume, keeping the image small and avoiding binary redistribution.

### `games/` — game-specific images

Each game has one directory under `containers/games/<name>/` with a **Dockerfile** (inheriting FROM a common base) and **entrypoint.sh** (orchestrating install and launch).

#### Adding a new game

1. Create the directory: `mkdir -p containers/games/<name>`
2. Author `Dockerfile` to inherit FROM the common base (the CI workflow passes `STEAMCMD_BASE_IMAGE` as a build arg with the freshly-pushed base digest) and set up game-specific config.
3. Author `entrypoint.sh` to call `steam-install.sh`, validate the install, and launch the server.
4. Add the game to `.github/workflows/images.yaml` in the `game-images` job matrix:
   ```yaml
   - game: <name>
     context: containers/games/<name>
   ```
5. Push to a feature branch; the workflow builds, signs, and surfaces the digest in the job summary.
6. Once merged, pin the digest in the module's `template.yaml` (see **Reading published digests** below).

#### Dockerfile template

```dockerfile
# The workflow passes STEAMCMD_BASE_IMAGE as a build arg with the pinned base digest.
ARG STEAMCMD_BASE_IMAGE=ghcr.io/gameplanepanel/gameplane/common-steamcmd:latest
FROM ${STEAMCMD_BASE_IMAGE}

# Game-specific setup (if needed). The base is Ubuntu 26.04, so use apt-get.
# RUN apt-get update && apt-get install -y socat && rm -rf /var/lib/apt/lists/*

COPY entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

# Game images must define their own ENTRYPOINT (the base does not).
ENTRYPOINT ["/entrypoint.sh"]
```

#### entrypoint.sh template

```bash
#!/bin/bash
set -euo pipefail

# Download game files if not already installed.
STEAM_APPID=220 \
STEAM_INSTALL_DIR=/data \
STEAM_SKIP_IF_INSTALLED=true \
STEAM_SENTINEL_FILE=/data/srcds_run \
  steam-install.sh

# Run the server.
cd /data
exec ./srcds_run -game csgo -console +mapname de_dust2
```

## Reading published digests and pinning images

When the workflow runs (triggered by a push to `containers/**`), it builds, signs, and publishes both the common base and all game images. Each digest is surfaced in the **job summary** at the bottom of the workflow logs.

To pin an image in a module:

1. Open the workflow run: GitHub Actions → `.github/workflows/images.yaml` → the run triggered by your push.
2. Scroll to the **job summary** (collapsed section under the logs) — it lists every pushed digest.
3. Copy the full digest reference (e.g., `ghcr.io/gameplanepanel/gameplane/nuclear-option@sha256:abc123...`).
4. Paste into the module's `template.yaml` under `spec.image.ref`:
   ```yaml
   spec:
     image:
       ref: ghcr.io/gameplanepanel/gameplane/nuclear-option@sha256:abc123...
   ```
5. Commit and push. The next module build publishes the updated bundle.

**Why digests, not tags?** Tags are mutable; digests are immutable and cryptographically bind to the exact image content. Pinning digests guarantees that every pod launched from a module runs the same binary.

## Steam Installation

### How `steam-install.sh` Works

1. **Checks for a sentinel file** (e.g., the game binary). If it exists and `STEAM_SKIP_IF_INSTALLED=true`, the install is skipped.
2. **Runs SteamCMD** with `+force_install_dir`, `+login anonymous`, `+app_update APPID`, and optional `validate`.
3. **Verifies success** by checking that the sentinel file now exists. SteamCMD can exit 0 even on CDN failures, so we don't trust its exit code alone.
4. **Retries on transient failure** (CDN blips, timeouts) up to `STEAM_RETRY_COUNT` times (default 3) with exponential backoff.
5. **Fails loudly** if the sentinel file is missing after all retries, rather than leaving a corrupted container in a silent restart loop.

### Environment Variables

All parameters are environment variables (no command-line args):

| Variable | Default | Purpose |
|----------|---------|---------|
| `STEAM_APPID` | (required) | The Steam app ID to install (e.g., `220` for Half-Life 2: Deathmatch). |
| `STEAM_INSTALL_DIR` | `/data` | Where to install the game files. |
| `STEAM_VALIDATE` | `false` | Whether to run SteamCMD with the `validate` flag (slower, verifies file integrity). |
| `STEAM_SKIP_IF_INSTALLED` | `true` | If the sentinel file exists, skip the install. Avoids re-downloading on pod restart. |
| `STEAM_SENTINEL_FILE` | (required) | Path to a file that proves install success (e.g., `/data/srcds_run`). Must be provided by the entrypoint. |
| `STEAM_RETRY_COUNT` | `3` | Number of install attempts before giving up. |
| `STEAM_RETRY_DELAY` | `5` | Seconds to wait between retries. |

### Example: Installing Half-Life 2: Deathmatch

```bash
#!/bin/bash
set -euo pipefail

STEAM_APPID=220 \
STEAM_INSTALL_DIR=/data \
STEAM_VALIDATE=false \
STEAM_SKIP_IF_INSTALLED=true \
STEAM_SENTINEL_FILE=/data/srcds_run \
  steam-install.sh

cd /data
exec ./srcds_run -game hl2dm +mapname dm_lockdown
```

## Security & Kubernetes Integration

### Running as Non-Root

The SteamCMD base image runs as a non-root user `gameserver` with default UID 10000 and GID 10000. These are customizable via `GAMESERVER_UID` and `GAMESERVER_GID` build-args if needed by your environment.

When deploying a game server via the Gameplane operator, you **must** set `spec.security.runAsUser` to match the image's UID. By default:

```yaml
spec:
  security:
    runAsUser: 10000
    fsGroup: 10000  # Ensures mounted volumes are writable by the non-root user
```

**Why this matters:** The module validator (`modules/validate.py`, Rule 2: `rule_nonroot_requires_runasuser`) enforces that:

1. If the image declares a non-root User (as this one does), `spec.security.runAsUser` **must** be set — omitting it causes the container to fail because a freshly-provisioned PersistentVolume is root-owned and unwritable by the non-root process.
2. The `spec.security.runAsUser` value **must** match the image's User UID numerically. A mismatch (e.g., `runAsUser: 1001` when the image is UID 10000) causes the same write-permission failure; Project Zomboid (uid 10000, not 1000 as its own README claimed) was the original regression case, documented in `modules/validate.py` lines 14-20.

Additionally, set `spec.security.fsGroup` (matching or compatible with the image UID) to ensure PersistentVolume mounts are writable by the non-root server process. Consider `allowPrivilegeEscalation: false` to prevent the container from gaining additional capabilities at runtime.

When using `runAsUser`, the `$HOME` environment variable must also be set — either baked into the image or declared in the template's `spec.env`. Our SteamCMD base (`ENV HOME=/home/gameserver`, line 112 of the Dockerfile) provides this automatically, so derived game images inherit it. If building a new game image that does not inherit from this base, ensure `HOME` is set; SteamCMD and other build toolkits rely on it (Rule 3: `rule_runasuser_requires_home` in `modules/validate.py` lines 654-693).

### Data Persistence

Game server data (worlds, configurations, logs) must persist across pod restarts. Set up a PersistentVolumeClaim and mount it at the install directory (e.g., `/data`). The Gameplane chart's `storage.mountPath` field handles this.

### RCON & Console

Games expose their console via:

- **RCON** (remote console over a network port): Configure `rcon.protocol` in the GameTemplate.
- **PTY** (terminal emulation): The agent's console handler attaches to container stdin/stdout.

The Gameplane dashboard streams both in the **Console** tab, sourced by the game's template configuration.

## Build & Publish

The published images are built via CI/CD (GitHub Actions) and pushed to a registry (e.g., GitHub Container Registry). Local development uses `make dev-up` to run a kind cluster with a local OCI registry.

For manual testing:

```bash
# Build the base image locally (context is the image's directory)
docker build -t gameplane-steamcmd-base:latest -f Dockerfile containers/common/steamcmd

# Build a game image (context is the image's directory)
docker build -t my-game:latest -f Dockerfile containers/games/my-game

# Run locally (requires a mounted volume for persistence)
docker run -it \
  -v game-data:/data \
  -e STEAM_APPID=220 \
  my-game:latest
```

## Platform support

- **Common bases** — built for `linux/amd64,linux/arm64` (Ubuntu 26.04 LTS, portable).
- **Game images** — built for `linux/amd64` only by default.
  - Game server binaries (especially Unity-based, like Nuclear Option) are x86_64-only.
  - ARM64 support requires a native ARM64 binary from the game publisher, which most do not officially provide.
  - To add ARM64 support: obtain the ARM64 binary, update the Dockerfile and workflow matrix to build `linux/amd64,linux/arm64`.

## Image signing

All published images are signed with cosign using the project's private key. Signatures are recorded in Sigstore's Rekor transparency log. To verify a published image:

```bash
cosign verify --key cosign.pub ghcr.io/gameplanepanel/gameplane/nuclear-option@sha256:...
```

The `cosign.pub` file (committed to the repo root) is the public key for verification.

## Workflow triggers

The `images.yaml` workflow runs on:

- **`workflow_dispatch`** — manual trigger from the GitHub Actions UI.
- **`push` to `containers/**`** — any change to image source.
- **`push` to `.github/workflows/images.yaml`** — any change to the workflow itself.

Images are not rebuilt on changes to operator, API, agent, or other non-image code, keeping the registry lean.

## Validation

Run `modules/validate.py` to check that a game's template and image pass preflight checks:

```bash
# Validate all modules
python3 modules/validate.py

# Validate a specific module
python3 modules/validate.py my-game

# Pin unpinned image digests (before publish)
python3 modules/validate.py --pin
```

See `modules/validate.py` for the full list of checks (no bare shells, non-root User UID matching, RCON protocol validation, credential field types, etc.).
