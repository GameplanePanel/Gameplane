# Authoring a Gameplane Module

A **module** is a versioned, reusable game-server blueprint. Gameplane
distributes modules as OCI artifacts so the same registries that hold your
game-server container images also hold the templates that wire them up.

A module is one OCI artifact carrying a `GameTemplate` manifest, machine-
readable metadata, and optional README/icon. Admins point Gameplane at a
registry by creating a `ModuleSource` resource; users install a module by
creating (or clicking Install on) a `Module` resource, which the operator
materializes into an in-cluster `GameTemplate`.

## Quickstart: Authoring with `gp-module` CLI & Web Builder

Gameplane provides a unified toolkit for creating, verifying, previewing, and packaging modules:

- **Command-Line Interface (`gp-module`)**: First-class Go CLI with Makefile targets.
- **Web Dashboard Module Builder**: Visual 3-step modal wizard available directly on `/modules` in the web dashboard.

### 1. Scaffolding a new module (`init`)

Scaffold a complete, schema-compliant module directory with DNS-1123 validation using archetypes (`steamcmd`, `java`, or `generic`):

```sh
# Using make targets
make module-new NAME=cs2-match ARCHETYPE=steamcmd

# Or using gp-module binary directly
bin/gp-module init cs2-match \
  --archetype=steamcmd \
  --display-name="Counter-Strike 2 Match" \
  --image="ghcr.io/gameplanepanel/cs2:latest@sha256:4b9a8e23f0c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6e7" \
  --ports="27015/udp" \
  --categories="Shooter,Co-op"
```

### 2. Instant Offline Validation (`validate`)

Run static offline linting and validation on metadata, CRD schemas, image digests, port ranges, and configuration types without requiring network or cluster access:

```sh
# Validate a single module
make module-validate MODULE=cs2-match

# Validate all modules with strict diagnostics
bin/gp-module validate modules/* --strict

# Machine-readable JSON output for CI pipelines
bin/gp-module validate modules/cs2-match --json
```

### 3. Dry-Run Config Preview (`preview`)

Simulate server creation from `template.yaml`, evaluating dynamic memory limits (`autoFromMemoryLimit`) and environment variable precedence:

```sh
# Preview runtime config with an 8Gi memory limit
make module-preview MODULE=minecraft-java MEMORY=8Gi

# Simulate with custom user configuration
bin/gp-module preview modules/minecraft-java \
  --memory=8Gi \
  --config="MOTD=My Tournament Server" \
  --json
```

### 4. Packaging and Distribution (`package`)

Enforce asset size limits (icon $\le$ 512 KiB, bundle $\le$ 1 MiB) and export to `.tar.gz` or push directly to an OCI registry:

```sh
# Package into a local tar.gz bundle
make module-package MODULE=cs2-match OUTPUT=dist/cs2-match.tar.gz

# Push OCI artifact to registry
bin/gp-module package modules/cs2-match \
  --registry=ghcr.io/gameplanepanel/gameplane-modules \
  --tag=1.0.0 \
  --tag-latest
```

### 5. Web Dashboard Module Builder

In the Gameplane web dashboard:
1. Navigate to **Modules** in the main navigation.
2. Click **"Create module"** in the top-right header actions.
3. Follow the 3-step modal wizard:
   - **Step 1 (Preset & Metadata)**: Choose an archetype card (`SteamCMD Dedicated`, `Java Server`, or `Generic Container`), enter the module name (validated live against DNS-1123 standards), and select category chips.
   - **Step 2 (Container & Ports)**: Configure container image (with digest pinning verification), dynamic port mappings (TCP/UDP, advertise flag), and storage volume size and mount path.
   - **Step 3 (Review & Export)**: View generated files across tabs (`module.yaml`, `template.yaml`, `README.md`) with live syntax editing, monitor instant offline validation results, simulate memory limit scaling, and either download the `.tar.gz` archive or install directly into your cluster's upload source.

---

## Source layout

The official modules live in the standalone **`GameplanePanel/module`** repo, which
the main repo checks out as the `modules/` submodule. Whether you work in that
repo directly or through the submodule, a module lives on disk as a directory:

```
modules/<name>/
├── template.yaml   # GameTemplate spec (no metadata.name)
├── module.yaml     # Module metadata (see schema below)
├── README.md       # rendered in the catalog detail drawer
└── icon.png        # optional, 256×256 recommended
```

`modules/build.sh` and `make modules-push` are unchanged by the split — the
submodule mounts the `GameplanePanel/module` repo root at `modules/`, so the paths
below still resolve.

### Module documentation (`specs.md`)

Each module directory should include a `specs.md` documenting the game and its
Gameplane integration. This is the canonical reference for operators authoring or
troubleshooting servers: console protocol details (message framing, authentication,
command syntax), available console modes (RCON protocol/port, telnet, WebSocket,
stdin pod-attach), configuration schema fields and their semantics, and
game-specific implementation notes (workarounds, quirks, gotchas the template
handles). Follow the structure documented in
`../specs/done_011-add-missing-module-specs/contracts/specs-md-structure.md`. Enforcement
(validation that specs.md exists and conforms to the structure) lives in the
GameplanePanel/module repo's own CI, not in this repo's `make check-specs`.

### Editor schemas

Both YAML files carry a first-line `# yaml-language-server: $schema=…` modeline
pointing at JSON Schemas under `modules/.schema/`, so editors (VS Code's
`redhat.vscode-yaml`) validate and autocomplete them instead of guessing an
unrelated schema. `module.schema.json` is hand-maintained; `gametemplate.schema.json`
is **generated from the GameTemplate CRD** — after changing CRD spec fields,
regenerate and commit it (in the submodule):

```sh
make module-schema      # hack/gen-module-schema.py → modules/.schema/gametemplate.schema.json
```

`template.yaml` is the same `GameTemplate` you would write today, including
`metadata.name`. `gp-module validate` requires `metadata.name` to be present
and non-empty, and `gp-module init` scaffolds it to match the module name.

## `module.yaml` schema

```yaml
apiVersion: gameplane.local/module/v1
name: minecraft-java                       # required, DNS-1123 label
displayName: Minecraft (Java Edition)      # required
version: 1.0.0                             # required, semver, must match the OCI tag
game: minecraft-java                       # required, free-form game family identifier
categories: [Sandbox, Survival]            # optional, catalog groupings; a module may have several
summary: Vanilla / Paper / Forge / Fabric  # required, one-line description for the card
homepage: https://minecraft.net            # optional
license: MIT                               # optional, SPDX identifier
gameplaneMinVersion: 0.1.0                   # optional, refuse install on older operators (example version)
icon: icon.png                             # optional, filename of the icon layer
```

Field rules:

- `name` is the canonical module identifier within a source. Two modules
  with the same `name` in the same `ModuleSource` are an error.
- `version` is the canonical version string and **must** match the OCI tag
  the bundle is pushed under.
- `categories` groups the module in the dashboard's Modules catalog. A module
  may belong to several at once — Minecraft is reasonably Sandbox, Survival,
  Building, Modded and Creative. The dashboard builds its filter chips from
  the distinct values across installed/available modules, so naming a new
  category here creates its filter button with no frontend change; a module
  appears under every chip it declares. Values are free-form, but the official
  modules stick to this canon:

  **Survival · Sandbox · Shooter · Simulation · Building · Adventure · Horror ·
  Co-op · PvP · Modded · Creative**

  Chips dedupe case-insensitively, so `Survival` and `survival` do not both
  appear. When `categories` is omitted, the dashboard falls back to a
  best-effort heuristic on the `game` slug, and finally to "Other".

  The singular `category: Sandbox` is still accepted for bundles authored
  before this field became a list; it is normalized to `[Sandbox]` on parse.
  New modules should use `categories`.
- `gameplaneMinVersion` is checked at install time against the operator's
  build version; a module that needs a newer operator fails the `Module`
  reconcile with an `IncompatibleOperator` condition. The check is skipped
  for `dev`/unversioned operator builds.

## Bundle format (OCI artifact)

A module bundle is a single OCI artifact:

| Layer            | Required | Media type                                            |
| ---------------- | -------- | ----------------------------------------------------- |
| `module.yaml`    | yes      | `application/vnd.gameplane.module.metadata.v1+yaml`     |
| `template.yaml`  | yes      | `application/vnd.gameplane.module.template.v1+yaml`     |
| `README.md`      | no       | `application/vnd.gameplane.module.readme.v1+md`         |
| `icon.png`       | no       | `image/png`                                           |

Manifest:

- `mediaType: application/vnd.oci.image.manifest.v1+json`
- `artifactType: application/vnd.gameplane.module.v1+json`
- `config: { mediaType: application/vnd.gameplane.module.config.v1+json,
  data: "e30=" }` (the empty JSON object `{}` base64-encoded; we don't use
  the config for now but reserve it for future extensions)
- Each layer carries an `org.opencontainers.image.title` annotation set
  to the filename (`module.yaml`, `template.yaml`, …) so the puller can
  identify layers by name.

Reference shape: `<registry>/<repo>/<name>:<version>`. Push every bundle
with both its semver tag and a moving `latest` tag if it's the default
channel.

## Pushing a bundle

The repo ships `modules/build.sh` which wraps `oras` and pushes a bundle
from a `modules/<name>/` directory. Install `oras` (>= 1.2.0):

```sh
brew install oras                 # macOS
# or download from https://oras.land/docs/installation
```

Then:

```sh
# push every bundle in modules/ to a registry
make modules-push REGISTRY=ghcr.io/gameplanepanel/gameplane-modules

# push a single module to a local kind registry
modules/build.sh push --registry localhost:5001 --name minecraft-java
```

Under the hood `build.sh` runs:

```sh
oras push \
  --artifact-type application/vnd.gameplane.module.v1+json \
  ghcr.io/gameplanepanel/gameplane-modules/minecraft-java:1.0.0 \
  module.yaml:application/vnd.gameplane.module.metadata.v1+yaml \
  template.yaml:application/vnd.gameplane.module.template.v1+yaml \
  README.md:application/vnd.gameplane.module.readme.v1+md \
  icon.png:image/png
```

Private registries: log in once with `oras login <registry>`. The cluster
side uses a `kubernetes.io/dockerconfigjson` secret (referenced from
`ModuleSource.spec.oci.pullSecretRef`) — the same kind kubelet uses for
private images.

## Module sources

`ModuleSource` declares where modules come from. `spec.type` selects
the store; everything except OCI auto-discovers any directory holding
a `module.yaml` (use `spec.allow` to filter by name or glob). Sources
can be managed from the dashboard (admin) or applied as CRs:

```yaml
apiVersion: gameplane.local/v1alpha1
kind: ModuleSource
metadata: { name: community }
spec:
  type: git                  # oci | git | http | local | upload
  git:
    url: https://github.com/example/gameplane-modules
    ref: main                # branch or tag; the resolved commit is the digest
    subPath: modules         # optional scan root inside the repo
    secretRef: { name: gh-creds }   # optional, in the operator namespace
  allow: ["minecraft-*"]     # optional name filter (all types)
  refreshInterval: 30m
```

Per-type config:

| type | config | versioning | auth secret keys |
|---|---|---|---|
| `oci` | `oci.url` + explicit `oci.modules` list | registry tags (semver) | dockerconfigjson via `oci.pullSecretRef` |
| `git` | `git.url`, `ref`, `subPath` | one stream: `module.yaml#version` at the ref; digest = commit | `token` or `username`+`password` (https); `ssh-privatekey` + `known_hosts` (ssh, both required) |
| `http` | `http.url` to a `.tar.gz`/`.zip` | one stream; digest = content hash | `token` (Bearer) or `username`+`password` (Basic) |
| `local` | `local.path` under the operator's `--module-local-root` mount (Helm: `operator.localModules`) | one stream; digest = content hash | — |
| `upload` | none — indexes uploaded bundles | one stream; digest = content hash | — |

**Network safety.** The operator fetches `git`/`http` sources through a
guard that refuses link-local, cloud-metadata (`169.254.169.254`),
unspecified, and multicast destinations — so a source can't be aimed at the
instance-metadata endpoint to steal the operator's credentials. Private and
loopback addresses are allowed, because self-hosted GitLab/Harbor and a
local kind registry legitimately live there.

### Uploaded bundles

`type: upload` sources index ConfigMaps in the operator namespace
labeled `gameplane.local/module-upload: "true"`, each holding one bundle's
files under their canonical names. The dashboard's **Upload module**
flow creates these via `POST /modules/sources/{name}/upload`
(tar.gz/zip, ≤ 900 KiB), but a hand-applied ConfigMap indexes exactly
the same way:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: module-upload-mygame
  namespace: gameplane-system
  labels: { gameplane.local/module-upload: "true" }
binaryData:        # or stringData for plain YAML
  module.yaml: <base64>
  template.yaml: <base64>
```

## Installing a module

Once a `ModuleSource` is configured (Helm chart ships a default one
pointing at `ghcr.io/gameplanepanel/gameplane-modules`), modules show up in the
**Modules** page of the dashboard. Click **Install** to create a
`Module` resource:

```yaml
apiVersion: gameplane.local/v1alpha1
kind: Module
metadata:
  name: minecraft-java          # becomes GameTemplate name
spec:
  source:
    name: default               # ModuleSource name
  name: minecraft-java
  version: 1.0.0                # omit to track the source's latest
```

The operator pulls the artifact, parses `module.yaml` + `template.yaml`,
and creates a `GameTemplate` owned by this `Module`. Deleting the
`Module` deletes the `GameTemplate`. The operator refuses to delete the
module while any `GameServer` references the template; the UI surfaces
this as a clear "still in use by N servers" message.

If a `Module` fails to upgrade (pull error, bad signature, incompatible
operator), the previously-installed `GameTemplate` keeps running unchanged;
`status.previousVersion` records the last-known-good to roll back to by
re-pinning `spec.version`.

### Verifying and pinning bundles

Two independent, opt-in controls harden the supply chain for OCI modules:

**Digest pin** (`Module.spec.digest`) refuses to install unless the resolved
bundle's digest matches exactly — defeating a tag that was moved to point at
new content. Set it to the OCI manifest digest shown in the catalog:

```yaml
spec:
  source: { name: default }
  name: minecraft-java
  version: 1.0.0
  digest: sha256:abc123…        # install fails (DigestMismatch) on any drift
```

**Signature verification** (`ModuleSource.spec.verify`, OCI sources only)
requires every bundle from the source to carry a valid [cosign][cosign]
signature before it is installed. A bundle that is unsigned or signed by the
wrong key/identity fails the install with a `SignatureInvalid` condition.
Keyed:

```yaml
apiVersion: gameplane.local/v1alpha1
kind: ModuleSource
metadata: { name: trusted }
spec:
  type: oci
  oci: { url: ghcr.io/gameplanepanel/gameplane-modules, modules: [{ name: minecraft-java }] }
  verify:
    key: { name: cosign-pub }    # Secret in the operator namespace,
                                 # public key under data "cosign.pub"
```

Keyed verification checks the signature offline by default. Add
`requireTransparencyLog: true` to also require Sigstore (Rekor) inclusion
evidence, so a bundle only installs if its signing event was publicly logged —
which is what makes misuse of a stolen key detectable:

```yaml
  verify:
    key: { name: cosign-pub }
    requireTransparencyLog: true   # needs outbound access to the sigstore
                                   # trust root; leave off when air-gapped
```

It is off by default because a keyed signature is complete without a log entry,
and requiring one would break air-gapped clusters and any private registry
whose bundles are signed offline. The official bundles are logged, so enabling
it against them works. The proof bundled with the signature is checked offline —
there is no live log query per install. The setting has no effect on keyless
verification, which always enforces the log.

Keyless (Fulcio certificate identity) — for sources whose author signs with a
CI OIDC identity instead of a key. The operator needs outbound access to the
sigstore trust root and Rekor:

```yaml
  verify:
    keyless:
      issuer: https://token.actions.githubusercontent.com
      identity: https://github.com/<org>/<repo>/.github/workflows/release.yaml@refs/tags/v1.0.0
```

The official Gameplane bundles are **keyed**-signed (see
[Signing official bundles](#signing-official-bundles) below), so verify them
with the keyed form above. Their signatures are also recorded in the public
Rekor log, but the operator's keyed path checks the signature offline and does
not require log inclusion — so this stays air-gap friendly.

`spec.verify` is rejected on non-OCI sources (cosign signatures are an OCI
concept); `git`/`http`/`local`/`upload` rely on the content digest plus a
`Module.spec.digest` pin instead.

[cosign]: https://docs.sigstore.dev/cosign/overview/

### Signing official bundles

The official `modules/*` bundles are signed by the release pipeline (ECDSA
P-256 key) and recorded in the public Sigstore Rekor transparency log. The
operator's keyed verify path is deliberately **offline** (no Fulcio/Rekor
connectivity required), which keeps air-gapped and self-hosted clusters
functional.

**One-time key setup (maintainer).** Generate a project signing key and store
the halves in the right places — the private key never leaves CI secrets, the
public key ships to users:

```sh
cosign generate-key-pair                 # writes cosign.key (private) + cosign.pub
# CI secrets (Settings → Environments → release-signing): paste each file
#   COSIGN_PRIVATE_KEY = <cosign.key>    COSIGN_PASSWORD = <the passphrase>
# Commit cosign.pub at the repo root — CI drift-checks it against the
# private key on every publish, and it ships as a release asset.
```

The `release.yaml` `modules` job runs `modules/build.sh push --sign
--tlog-upload` on every `v*` tag, pushing each bundle to
`ghcr.io/<owner>/gameplane-modules` and signing it by digest with public Rekor
log recording. The default (no `--tlog-upload`) keeps signing local/offline for
air-gapped authoring. Signing is mandatory: a missing `COSIGN_PRIVATE_KEY`
fails the job rather than publishing unsigned bundles.

**Verifying the official source (operator).** Signing is an OCI concept, so
switch the default source to `type: oci` and turn verification on. The official
module-signing public key already ships in the chart's
`defaultModuleSource.oci.verify.cosignPublicKey`, so you only flip `enabled`:

```yaml
# values.yaml
defaultModuleSource:
  type: oci
  oci:
    verify:
      enabled: true
      # cosignPublicKey ships with the chart (the official ECDSA P-256 key,
      # same as the repo-root cosign.pub);
      # override it only to pin a different signer.
```

The chart writes the key to a `gameplane-module-cosign-pub` Secret in the
operator namespace and sets `spec.verify.key` on the default source.

**Signing your own bundles.** `build.sh --sign` works for any registry; set
`COSIGN_PRIVATE_KEY` (and `COSIGN_PASSWORD` if the key is encrypted) and point a
`ModuleSource.spec.verify.key` Secret at the matching public key:

```sh
export COSIGN_PRIVATE_KEY="$(cat cosign.key)" COSIGN_PASSWORD=…
modules/build.sh push --registry ghcr.io/you/your-modules --sign
```

## Anatomy of a `GameTemplate` spec

The CRD types in `operator/api/v1alpha1/gametemplate_types.go` are the
source of truth; this section documents every author-facing block. The
official modules are the canonical examples: **minecraft-java**
(env-versioned catalog, per-loader mod volumes, three registries,
env-mode modpacks), **valheim** (channel catalog, extract-mode BepInEx
mods, deps-mode Thunderstore packs), **terraria** (tag catalog, pty
console, configFiles), **factorio** (tag catalog, pty console,
wizard-managed server-settings.json, portal registry), **palworld**
(wrapper channels, REST admin API console, pak mods). The full
catalog also includes 7-days-to-die, ark-survival-ascended,
ark-survival-evolved, arma-reforger, beammp, cs2, dayz, dont-starve-together,
enshrouded, euro-truck-simulator-2, farming-simulator-25, fivem, garrys-mod,
hell-let-loose, left-4-dead-2, mount-and-blade-2-bannerlord, nuclear-option,
project-zomboid, rust, satisfactory, squad, team-fortress-2, the-isle,
tmodloader, and v-rising (see [Game Coverage](game-coverage.md) for full protocol and testing status).

### Branding

```yaml
spec:
  icon: icon.png            # bundle file or URL/data-URI shown in the catalog
  categories: [Sandbox]     # optional, catalog groupings (mirrors module.yaml)
  accentColor: "#5b9a3e"    # CSS hex; tints this game's icon + accents
```

`categories` mirrors the `module.yaml` field and drives the Create-Server
template picker's category chips. Optional; the same fallback and legacy-scalar
handling apply. The two lists should agree — `module.yaml` feeds the Modules
catalog, `template.yaml` feeds the Create-Server picker, and they are read by
different code paths.

`accentColor` is how a module carries its own brand color into the
dashboard — the app no longer hardcodes a per-game palette, so a new
game shows the right color without any frontend change. Omit it for a
neutral default.

### Version catalog (`spec.versions`)

`spec.versions` declares the selectable game versions/flavors. The "New
server" wizard shows the catalog as a picker, and an existing server can
switch entries later from **Settings → Version** (the operator re-renders
the StatefulSet and restarts the pod on the new entry).

```yaml
# Digests elided for readability — both refs below must actually be pinned
# as repo:tag@sha256:…; see §Pinning images by digest.
image: itzg/minecraft-server:java21   # fallback when no version is selected
versions:
  - id: "1.21.4-paper"        # what GameServer.spec.version stores (≤ 40 chars)
    displayName: "1.21.4 · Paper"
    image: itzg/minecraft-server:java21   # full image ref for this entry
    loader: paper             # keys into capabilities.mods.loaders (below)
    gameVersion: "1.21.4"     # upstream token passed to mod registries
    default: true             # pre-selected in the wizard (one entry)
    env:                      # appended when selected
      - { name: TYPE, value: PAPER }
      - { name: VERSION, value: "1.21.4" }
```

How authors express "a version" is up to the image (all three styles are
used by the official modules):

- **env-versioned** — one image, per-entry `env` selects the software
  (minecraft-java: itzg `TYPE`/`VERSION`; the tag only pins the Java
  runtime);
- **tag-versioned** — the image tag *is* the game version (factorio:
  `stable`/`latest`/pinned; terraria: `tmodloader-*`);
- **channel** — entries select an upstream channel via env (valheim:
  `stable` vs `public-test`).

Semantics:

- `GameServer.spec.version` selects an entry by `id`; empty falls back to
  the `default: true` entry (else the first). An id that matches no entry
  **fails the server loudly** (phase `Failed`) instead of silently using
  the template image. `GameServer.spec.image` overrides whatever image
  the entry resolves to.
- Env precedence: template `env` < the entry's `env` < schema-resolved
  config < GameServer `spec.env`.
- **Each (version + loader) combination gets its own mod volume** —
  switching entries mounts that combo's PVC and leaves the others intact,
  so a Paper plugin set survives a detour through Fabric. See §Mods.
- At most 64 entries; `loader` must match a key of
  `capabilities.mods.loaders` (or be absent/`vanilla`-style for entries
  with no mod manager). Use the registry's loader token verbatim (e.g.
  Modrinth's `paper`/`fabric`/`neoforge`) so browse filtering works.

### Pinning images by digest

**Every image a user can get without choosing must be pinned to a digest.**
`validate.py` enforces this, and CI fails a module that breaks it.

```yaml
image: itzg/minecraft-server:java21@sha256:f71555873bdaa6…   # required form
```

Write the readable `repo:tag@sha256:…` form rather than a bare
`repo@sha256:…`. Kubernetes resolves by the digest either way — the tag is
decorative and carries no guarantee — but it keeps the ref legible in the
dashboard and in `kubectl describe`, and it is what the refresh tooling
re-resolves against.

*Why:* a floating tag means a pod restart can swap the game binary under a
live world, with no version bump and no changelog. This is not hypothetical.
`passivelemon/terraria-docker:terraria-latest` moved from Terraria 1.4.4.9 to
1.4.5.6, changed the network protocol version, and broke the e2e Terraria bot
— the first anyone knew of it was a red CI job.

What the rule requires, exactly:

| Field | Requirement |
|---|---|
| `spec.image` | **must** be pinned (ERROR) — it runs when no version is selected |
| a `default: true` version entry | **must** be pinned (ERROR) — it runs when the user doesn't choose |
| any other version entry | pinned by default; may float if marked (WARN) |

A non-default entry may deliberately keep moving. Mark its image line and the
pinner will leave it alone:

```yaml
  - id: latest
    displayName: "Experimental channel"
    image: factoriotools/factorio:latest  # gameplane:floating
```

Use this only where tracking upstream *is* the entry's purpose and its
`displayName` makes the drift obvious — factorio's "Experimental channel",
terraria's "tModLoader latest preview". Selecting one is then the user's
explicit, labelled choice rather than a silent default.

Pinning is mechanical; don't hand-write digests:

```sh
make module-pin        # rewrites every modules/*/template.yaml in place
```

It resolves each tag through the registry's standard bearer-token flow (any
OCI registry, not just Docker Hub), preserves the templates' comments, and
leaves any ref it cannot resolve untouched rather than guessing. The digest it
writes is the **tag manifest's** digest — for a multi-arch image that is the
index digest covering every published platform, so a pinned module still runs
on both amd64 and arm64.

Because pinning stops upstream releases from arriving on their own, the
`GameplanePanel/module` repo runs `refresh-pins.yml` monthly to re-resolve every
pin and open a PR. It never auto-merges: each changed digest is a game binary
that changes for every server on its next restart, which is exactly the event
worth a human's attention.

`make module-pin` re-resolves every module in the catalog and is meant for
that periodic refresh job, not for pinning the one module you're authoring.
To pin a single `spec.image` by hand, resolve the tag to a digest with
`docker buildx imagetools inspect <image>` or `crane digest <image>` and
append `@sha256:<digest>` to the image reference yourself.

### Config schema → wizard

Declared `configSchema` fields become inputs in the "New server"
wizard. At reconcile time the operator resolves the server's
`spec.config` against this schema and sets each resolved value as an
env var on the game container.

```yaml
configSchema:
  - name: MAX_PLAYERS
    displayName: Max players
    type: int
    default: "16"
  - name: DIFFICULTY
    type: enum
    enum: [easy, normal, hard]
    default: normal
  - name: SERVER_PASS
    displayName: Password
    type: password
  - name: MAX_MEMORY
    displayName: JVM max heap
    type: string
    default: ""
    autoFromMemoryLimit:
      percent: 75
```

#### Editing configuration after creation

`configSchema` is not only used by the "New server" wizard: the server's
**Settings > Game configuration** section renders the same schema, so every
field stays editable after the server exists. Rules to keep in mind when
writing or revising a module:

- Saving a change always restarts the server (the operator re-renders the
  pod's environment and config files); there is no "apply on next restart"
  mode.
- Only values that differ from your `default` are stored in the server's
  `spec.config`; an unset key means "use the default" (or the
  `autoFromMemoryLimit` value), so changing a default in a later template
  version flows to servers that never overrode it.
- `password` values are write-only in the dashboard: the API never returns a
  stored password, and the form shows an empty "Unchanged" input.
- `min`/`max` (int) and `minLength`/`maxLength` (string, password) are
  checked in the form for early feedback; the operator re-checks them and is
  authoritative.
- **Removing or renaming a field orphans it.** A server that stored a value
  for the old name keeps that key in `spec.config`, and the operator rejects
  unknown keys: the server reports `invalid config: unknown config keys ...`
  and does not roll out changes until the key is removed. The Game
  configuration section lists such keys under "No longer in the template"
  with a Remove button. Prefer adding a new field and keeping old ones for a
  release, or document the rename in your module's upgrade notes.

#### Automatic computation from memory limit (`autoFromMemoryLimit`)

When a field's value is not supplied by the user or a default, `autoFromMemoryLimit`
computes it from the game container's effective memory limit:

```
floor(limit × percent / 100), rendered as whole mebibytes with an "M" suffix
```

This is useful for game memory settings (JVM heap, engine config) that should
scale with whatever resources the server was given:

```yaml
configSchema:
  - name: MAX_MEMORY
    displayName: JVM max heap
    description: Leave empty to size it automatically to 75% of the container memory limit.
    type: string
    autoFromMemoryLimit:
      percent: 75
  - name: INIT_MEMORY
    displayName: JVM initial heap
    type: string
    autoFromMemoryLimit:
      percent: 75
```

With `limits.memory: 4Gi` and `percent: 75`, both fields compute to `3072M`
(75% of 4096 MiB). Resizing the server's memory updates the computed values
on the next restart.

**Rules:**

- `autoFromMemoryLimit` is used only when neither `spec.config[field]` nor
  `default` provides a value — explicit values always win.
- When the template has no memory limit, the field is left unset (no default
  applied).
- The rendered value is a whole number of mebibytes with an `M` suffix
  (e.g., `3072M`).

Real-world example: Minecraft (Java Edition) sizes the JVM heap to avoid
OOM kills. The heap should leave headroom for non-heap JVM memory (metadata,
stacks, etc.), so 75% of the container limit is a safe heuristic:

```yaml
resources:
  limits:
    memory: 4Gi    # 4096 MiB

configSchema:
  - name: MAX_MEMORY
    displayName: JVM max heap
    type: string
    autoFromMemoryLimit:
      percent: 75  # → 3072M
```

| Field | Type | Min | Max | Notes |
|-------|------|-----|-----|-------|
| `percent` | int32 | 1 | 100 | percentage of memory limit |

Materialization rules:

- **Defaults apply at reconcile time.** A key absent from
  `spec.config` resolves to its `default`, so `kubectl apply` of a
  bare GameServer behaves exactly like the wizard (which pre-fills
  defaults).
- **Empty optional values are skipped** — no env var is set at all,
  letting the game image fall back to its own default. "Leave blank
  for an open server" means *unset*, not `PASSWORD=""`.
- **Validation is strict.** Unknown keys, enum violations,
  unparseable `int`/`bool` values, and empty `required` fields fail
  the GameServer (phase `Failed`, message on the `Ready` condition)
  instead of materializing a pod that ignores user intent. Fixing
  `spec.config` recovers automatically.
- **`password` fields never appear inline in the pod spec.** Values
  are stored in an owned `<server>-config` Secret and injected via
  `SecretKeyRef`.
- **Precedence**: template `env` < schema-resolved config <
  GameServer `spec.env` — an explicit env override always wins.
- **`target: file` fields feed `configFiles` templates** instead of
  becoming env vars — see the next section.

### Config files

Games that read a config *file* instead of env vars (Terraria's
`serverconfig.txt` is the intended first adopter) declare
`configFiles`: a list of files the operator renders from the resolved
config values and places under `storage.mountPath` before the game
starts.

```yaml
configSchema:
  - name: MOTD
    type: string
    default: Welcome!
    target: file          # consumed by templates below, never an env var
  - name: SERVER_PASS
    type: password
    target: file
configFiles:
  - path: serverconfig.txt          # relative to storage.mountPath
    template: |
      motd={{ .Values.MOTD }}
      {{ if .Values.SERVER_PASS }}password={{ .Values.SERVER_PASS }}{{ end }}
      world={{ .Server.Name }}
```

`template` is a Go [`text/template`](https://pkg.go.dev/text/template)
rendered with:

- **`.Values`** — every `configSchema` field name mapped to its
  resolved value. Unset optional fields are present as `""`, so
  `{{ if .Values.X }}` guards work; referencing a name outside the
  schema fails the GameServer (`missingkey=error`). Env-target values
  are available too — a value may drive both an env var and a file.
- **`.Server`** — `.Name` and `.Namespace` of the GameServer.

Rules and semantics:

- **Paths are relative to `storage.mountPath`.** Absolute paths,
  `..` segments, unclean paths, and duplicates fail the GameServer.
- **Rendered files live in an owned `<server>-files` Secret** —
  always a Secret, never a ConfigMap, because any template may embed
  a password value.
- **Files are copied onto the data volume by a `config-init`
  container on every pod start.** The operator's rendering wins:
  manual edits to these paths (e.g. via the dashboard's Files tab)
  are overwritten on the next restart. Games may freely rewrite the
  files at runtime — the copy is plain data on the PVC, not a
  read-only mount.
- **The copy runs as root (busybox).** Non-root game images that
  rewrite their config need permissions compatible with root-owned
  files.
- **Changes roll the pod.** Rendered contents are part of the config
  hash, so editing a template or a file-target value restarts the
  server with the new file.
- **Failures are strict.** A parse error, a missing key, or a
  file-target value supplied while the template declares no
  `configFiles` fails the GameServer (phase `Failed`, message on the
  `Ready` condition) instead of silently dropping user intent.

### RCON

The console protocol is selected via `rcon.protocol`. Ten protocols are
supported: `source`, `telnet`, `websocket`, `battleye`, `satisfactory`,
`palworld`, `nuclearoption`, `rest`, `cli`, and `none`. Each game's template declares the one(s) it uses.

#### Source (Valve / Minecraft)

Source-protocol games (Minecraft, CS2, other Valve engine titles):

```yaml
rcon:
  protocol: source
  port: 25575
  passwordEnv: RCON_PASSWORD   # env the game image reads the password from
```

#### Telnet (raw line-based TCP)

Games whose remote console is a raw line-based TCP/telnet session (7 Days
to Die is the reference implementation: connect, send the password as a
line, send a command as a line, read back whatever the server prints):

```yaml
rcon:
  protocol: telnet
  port: 8081
  passwordEnv: TELNET_PASSWORD
```

The agent's telnet client has no message framing to work with — it infers
"reply complete" from a short quiet period on the socket rather than a
length prefix or terminator, so a command that prints nothing at all
still returns (empty output) rather than hanging.

#### WebSocket (Rust WebRcon)

Rust's Facepunch WebRcon protocol (requires `+rcon.web 1` on the game server):

```yaml
rcon:
  protocol: websocket
  port: 28016
  passwordEnv: RCON_PASSWORD
```

#### BattlEye (UDP with checksums)

BattlEye RCon protocol used by DayZ and Arma-family engines (UDP, with
checksum-framed packets and a mandatory client-side keepalive):

```yaml
rcon:
  protocol: battleye
  port: 2305
  passwordEnv: BATTLEYE_PASSWORD
```

#### Satisfactory (HTTPS function-call API)

Satisfactory Dedicated Server's HTTPS function-call API (POST `/api/v1`
with a JSON `"function"` body, bearer-token auth after a `PasswordLogin`
call):

```yaml
rcon:
  protocol: satisfactory
  port: 7777
  # NOT env-injected: the admin claims the password in-game once, then writes
  # it here (path is relative to the game's /config data mount).
  passwordFile: gameplane/rcon-admin-password
```

**Caveat:** Satisfactory's admin password cannot be injected by the operator
at startup — it must be set once **in-game** by the admin before the API
becomes available, which is why this is the one console that uses
`passwordFile` rather than `passwordEnv`. See the Satisfactory module's
README for the manual one-time claim procedure.

#### Palworld (REST admin API)

Palworld Dedicated Server's REST admin API (plain HTTP, not HTTPS; GET/POST
under `/v1/api/...`, HTTP Basic auth with username `admin` on every request).
Palworld's upstream deprecated its source RCON protocol in favor of this
REST API, which supports a real players list and preserves formatting in
announcements:

```yaml
rcon:
  protocol: palworld
  port: 8212
  passwordEnv: ADMIN_PASSWORD
```

#### NuclearOption (JSON request/response)

NuclearOption is a length-prefixed JSON request/response protocol on a
dedicated pod-local remote-command port with no authentication:

```yaml
rcon:
  protocol: nuclearoption
  port: 7779
```

#### REST (Generic HTTP admin API)

Generic HTTP/JSON administrative console APIs (e.g. FiveM txAdmin or Farming
Simulator 25 web admin). The agent issues HTTP POST requests with either
bearer token / `X-TxAdmin-Token` or HTTP Basic credentials, parsing output
from JSON envelopes or raw response bodies:

```yaml
rcon:
  protocol: rest
  port: 40120                     # txAdmin default port (or 8080 for FS25)
  passwordEnv: TXADMIN_TOKEN     # or passwordSecretRef
```

The agent automatically selects the appropriate adapter based on game identifier or port:
- `fivem` (port 40120): txAdmin API (`POST /fxserver/commands`) with `X-TxAdmin-Token` and `Authorization: Bearer`.
- `farming-simulator-25` (port 8080): Web admin API (`POST /api/console`) with HTTP Basic auth.
- Generic REST: `POST /api/command` with JSON `{"command": "<cmd>"}`.

#### CLI (Container Stdin / Local Execution)

Console access over the container's stdin/PTY or local process execution.
Per the resolution recorded in `OPEN-DECISIONS.md` (Option A):
- The web dashboard console continues to attach directly via Kubernetes pod-attach / PTY (`consoleMode: pty`).
- The agent executes lifecycle stop sequences, health checks, and scheduled commands locally via standard input (Unix FIFO pipe or local process execution) without requiring remote TCP networking or an external password secret:

```yaml
rcon:
  protocol: cli
consoleMode: pty
```

#### No console

For games with no usable remote console, set `protocol: none`. See
`consoleMode: pty` below for stdin-driven games that accept console
commands over standard input.

```yaml
rcon:
  protocol: none
```

#### Password handling

The operator mints a password secret per GameServer, injects it into the
game container via `passwordEnv`, and mounts the same value for the agent
— the dashboard's console tab (and every RCON-backed capability) then
works automatically.

#### Password source precedence

When the game image manages its own RCON password file (e.g., Factorio's
`config/rconpw`), use `passwordFile` instead of `passwordEnv`:

```yaml
rcon:
  protocol: source
  port: 25575
  passwordFile: config/rconpw  # path relative to the game data mount
```

In this mode, the operator does not generate a Secret or inject any env var.
The agent reads the password directly from the file on every connection.

The password source is chosen by precedence: `passwordSecretRef` (external
Secret) > `passwordFile` (game-managed) > operator-generated Secret (default).

| Mode | Use case | Secret injected | Env var | Example |
|------|----------|-----------------|---------|---------|
| operator-generated (default) | Operator controls the password | Yes (auto-created) | Via `passwordEnv` | Minecraft with `RCON_PASSWORD` |
| `passwordSecretRef` | Use external credentials | Yes (external) | Via `passwordEnv` | Any game with your own Secret |
| `passwordFile` | Game manages the password | No | No | Factorio with `config/rconpw` |

`authentication` (default `true`) says whether the console requires a password at
all. Set `authentication: false` for a console with no authentication, such as
Nuclear Option's loopback-only remote-command port: the operator then mints no
password Secret, mounts nothing for the agent, and injects no password env var.
It cannot be combined with `passwordSecretRef`, `passwordEnv` or `passwordFile`
(the API rejects the combination). Modules that relied on the old implicit
no-password behaviour for `protocol: nuclearoption` must now set it explicitly.

For games without usable RCON set `protocol: none`. Real cases from the
official modules:

- the game simply has none (Valheim, Terraria);
- the image manages its own RCON password in a file the operator can't
  inject into — use `passwordFile` to read from the data volume.

### Console mode and game log

```yaml
consoleMode: pty              # attach to container stdin/stdout
logPath: /data/logs/latest.log   # agent tails this for the "Game log" view
```

With `rcon.protocol: none`, set `consoleMode: pty` if the server reads
commands on stdin (Terraria, Factorio) — the Console tab then attaches
via the kubelet's pod-attach API instead. Without either, the tab shows
"no live console" and RCON-backed capabilities (players, quiesce,
actions, status) are reported unsupported.

`capabilities.lifecycle.stop` is the one exception: it works for
`consoleMode: pty` games too, since the operator (not the agent) drives
it — it writes the declared commands to the game container's stdin over
its own pod attach, the same mechanism as the Console tab, rather than
going through the agent's RCON connection. The operator picks the
transport per template: RCON when `rcon.protocol` isn't `none`, stdin
pod-attach when it's `none` and `consoleMode: pty`, and — if neither
applies — no graceful stop at all, so the server scales down immediately
on suspend/restart/stop instead of waiting out
`spec.stopGracePeriodSeconds` for a sequence it has no way to run.

`logPath` is an absolute in-container path pointing the agent at the
game's own logfile. Omit it for games that log only to stdout; the Logs
tab's "Container output" source covers those.

### Security context

Some game images refuse to run as root or require a specific uid. Use
`spec.security` to override the pod/container security settings:

```yaml
security:
  runAsUser: 25000        # uid the game container runs as
  runAsGroup: 25000       # gid the game container runs as
  fsGroup: 25000          # gid the kubelet chowns the data volume to
```

All three fields are optional. When set, the operator applies these
values to the game container's `securityContext`; `fsGroup` is set on
the pod spec so the kubelet chowns mounted volumes on startup, letting
a non-root game user write to the PVC without additional init
containers.

Real-world example: ARK: Survival Ascended's image (mschnitzer/asa-linux-server)
runs the server as uid 25000 and cannot initialise Proton (the Windows
compatibility layer) as root — it fails with "Parent directory does not
exist" and the server never reaches readiness. Setting `runAsUser: 25000`,
`runAsGroup: 25000`, and `fsGroup: 25000` works around this:

```yaml
spec:
  security:
    runAsUser: 25000
    runAsGroup: 25000
    fsGroup: 25000
```

| Field | Type | Default | Min | Max | Notes |
|-------|------|---------|-----|-----|-------|
| `runAsUser` | int64 | omitted (image default) | 0 | 4294967295 | uid the game container runs as |
| `runAsGroup` | int64 | omitted (image default) | 0 | 4294967295 | gid the game container runs as |
| `fsGroup` | int64 | omitted (no chown) | 0 | 4294967295 | gid the kubelet chowns volumes to |

### Reserved in-pod ports

The agent sidecar shares the pod's network namespace with the game
container, so any TCP port the agent itself binds is reserved: a game
template that also binds one of these loses (or wins) an arbitrary bind
race with the sidecar container start order. Do not declare a game port
of **8090** (the agent's mTLS control listener) or **9090** (the agent's
plain-HTTP Prometheus metrics listener, `agent/cmd/main.go`'s
`--metrics-addr`) in `spec.network.ports`.

If a template's game genuinely needs one of these ports, pick a
different port for the game and remap it at the game's own config
layer where possible — the agent's ports are not configurable per
template today.

### Capabilities (moderation + backup quiesce)

`spec.capabilities` declares the console commands behind agent
features, so a module adds full Players-tab moderation and safe
backups without any agent code. All of these commands run over RCON,
so they require `rcon.protocol` ≠ `none` — **except `lifecycle.stop`**,
which the operator itself can also run over a `consoleMode: pty` pod
attach (see "Console mode and game log" above).

```yaml
capabilities:
  players:
    kick: "kick {{.Player}}{{if .Reason}} {{.Reason}}{{end}}"
    ban: "ban {{.Player}}{{if .Reason}} {{.Reason}}{{end}}"
    unban: "pardon {{.Player}}"
    banList:
      command: "banlist players"
      entryRegex: '^\s*(?P<name>\w+)\s+was banned by\s+(?P<source>[^:]+?)(?::\s*(?P<reason>.*))?\s*$'
    whitelist:                                 # optional allow-list management
      list: "whitelist list"
      add: "whitelist add {{.Player}}"
      remove: "whitelist remove {{.Player}}"
      listRegex: 'whitelisted player[s()]*:\s*(?P<names>.+)$'
  quiesce:
    quiesce: ["save-off", "save-all flush"]   # run before a backup snapshot
    unquiesce: ["save-on"]                    # run after
    failurePattern: "saving failed"           # output regex that fails the step
  lifecycle:
    stop: ["stop"]                            # graceful-stop console sequence
```

- Moderation commands are Go `text/template`s rendered with `.Player`
  and `.Reason` (reason may be empty — guard with `{{if .Reason}}`).
  The reason is validated with the same input policy as action string params (no control characters, no ; & | $ ` \ " '; max 256 bytes), so templates may interpolate it without extra escaping of those characters.
  Unset actions are reported as unsupported and the UI hides them.
- `banList.entryRegex` matches one banned player per output line via
  the named groups `name` (required), `source` and `reason`.
- `whitelist` adds allow-list management to the Players tab: `add` /
  `remove` are `.Player` templates and `listRegex` extracts the
  comma-separated tail of the `list` command's output via the named
  group `names`.
- `list` lets games with no moderation capability still report the
  online player list. `command` is the console command that prints the
  players (e.g., `/players online` on Factorio, `ShowPlayers` on Palworld).
  `entryRegex` optionally extracts one player name per match (first
  capture group, or whole match if no group); multiline mode (`^` / `$`
  per line). When omitted, a built-in parser is used. This is useful for
  games that have no player-management RCON but do report online players
  — the Players tab then shows who is connected, without kick/ban/whitelist
  actions.

  From `modules/factorio/template.yaml`:

  ```yaml
  capabilities:
    players:
      list:
        command: "/players online"
        entryRegex: "^\\s+(.+?) \\(online\\)$"
  ```

  From `modules/palworld/template.yaml` (note: Palworld uses the REST
  admin API `protocol: palworld`, not RCON, so the players list is
  fetched via the REST endpoint, not a console command).
- The quiesce sequence runs in order; any command error — or output
  matching `failurePattern` (case-insensitive) — aborts the backup. If
  at least one earlier command in the sequence already succeeded,
  `unquiesce` then runs best-effort so the game is never left paused;
  a failure on the *first* command means nothing was paused yet, so
  `unquiesce` is skipped. Games that can't quiesce simply omit the
  block; backups proceed without pausing.
- `lifecycle.stop` runs before the pod is scaled down (stop button,
  restarts): the operator issues the sequence — over RCON when the
  template has it, otherwise over a stdin pod-attach for `consoleMode:
  pty` — and waits for the server to exit on its own, so a SIGTERM never
  interrupts an in-progress world save (`["Save", "Shutdown 1"]` on
  Palworld, `["stop"]` on Minecraft, `["save", "exit"]` for a
  stdin-driven game like Terraria). Omit it for games that save on
  SIGTERM, or for games with neither RCON nor `consoleMode: pty` — there,
  declaring it has no effect since the operator has no transport to run
  it over and scales straight down instead.

> The agent has **no per-game special-casing** — every capability above
> (and the two below) comes from this block. A template that declares
> nothing reports those features unsupported and the UI hides them.

#### Actions

`capabilities.actions` declares named operator buttons on the server
detail page. Each runs a console command built from a Go `text/template`
rendered with the parameters the user fills in, sent over RCON.

```yaml
capabilities:
  actions:
    - id: broadcast                 # stable, unique within the template
      displayName: Broadcast message
      description: Send a chat message to everyone.   # shown in the dialog
      icon: megaphone               # optional lucide-react icon name
      command: "say {{.Params.message}}"
      params:
        - name: message             # referenced as {{.Params.message}}
          displayName: Message
          type: string              # string | int | bool | enum
          required: true
    - id: set-weather
      displayName: Set weather
      command: "weather {{.Params.weather}}"
      confirm: false                # require a confirm step in the UI
      danger: false                 # red styling for destructive actions
      params:
        - name: weather
          type: enum
          enum: ["clear", "rain", "thunder"]
          default: clear
    - id: save-world
      displayName: Save world
      command: "save-all"           # no params is fine
    - id: announce-restart
      displayName: Announce restart
      group: Server                 # buttons are grouped by this label
      danger: true
      commands:                     # a sequence, run in order
        - 'say "Server restarting in 60s"'
        - "save-all"
        - "stop"
```

- **`command` vs `commands`** — set exactly one. `command` is a single
  console command; `commands` is a sequence run in order (each a Go
  template rendered with the same `.Params`), mirroring how
  `capabilities.lifecycle.stop` and `capabilities.quiesce` express
  sequences. A mid-sequence failure aborts the remaining steps.
- **`group`** (optional) labels a section on the server detail page, so a
  game with many actions reads as World / Server / Players groups rather
  than one flat list. Ungrouped actions fall under a trailing "Actions"
  section; if no action declares a group the card stays a flat list.
- **`transport`** (optional, `rcon` | `stdin`) selects how the commands
  reach the game. Empty resolves to `rcon` when `rcon.protocol` isn't
  `none`, else `stdin`. Stdin actions are written to the game container's
  stdin via pod-attach — this is what lets a `consoleMode: pty` game
  (Terraria, Don't Starve Together) carry actions at all. Stdin is
  **fire-and-forget**: no output is returned inline (it appears in the
  Console tab), so the dashboard shows "sent" rather than command output.
- Parameter values are validated by `type` and sanitized before
  rendering: CR/LF, other control characters and the shell/RCON metacharacters ``; & | $ ` \ " '`` are rejected so a value
  can never chain a second console command. `int`/`bool`/`enum` values
  must parse/match; missing optional params fall back to `default`. The
  same validation runs on both transports and on both the API and the
  agent — the shared `gameaction` module — so neither is trusted blind.
- A command template that fails to parse disables only that one action
  (logged), never the whole panel.
- **`currentFrom`** (optional, on a param) names a
  `capabilities.status.metrics[].id`. When the dialog opens, the dashboard
  pre-fills the param with that metric's live value, so a "set" action
  starts on the current setting. An `enum` param takes the option that
  matches case-insensitively (a reading of `Easy` selects `easy`), a `bool`
  takes `true`/`false` and an `int` takes an integer. With no reading, or
  no valid match, the param falls back to `default`; the user's own choice
  always wins. Readings come from the Status metrics below, so they need
  RCON. Minecraft's `set-difficulty` uses `currentFrom: difficulty`.

#### Status

`capabilities.status.metrics` declares live readouts on the Overview
tab. Each runs an RCON command and extracts a value via a named-group
regex (the group must be `value`).

```yaml
capabilities:
  status:
    metrics:
      - id: seed
        displayName: World seed
        command: "seed"
        regex: 'Seed: \[(?P<value>-?\d+)\]'
      - id: difficulty
        displayName: Difficulty
        command: "difficulty"
        regex: 'The difficulty is (?P<value>\w+)'
        unit: ""                    # optional suffix, e.g. "ms", "TPS"
```

Values are cached for a few seconds. A metric whose command errors or
doesn't match this cycle is shown with an empty value rather than
dropped, so the panel layout stays stable. When the game has no RCON the
endpoint returns nothing and the dashboard omits the panel.

#### Mods

`capabilities.mods` declares how the dashboard manages this game's
mods/plugins. Most games store mods in a directory on the data volume;
the dashboard lists, installs, uploads, updates, and removes them
generically by calling the agent — no RCON required. Some games, however,
download their own mods by ID (a Steam Workshop id, a CurseForge project id,
etc.) passed via a command-line flag or environment variable; for those,
use `idList` instead. The three layouts are mutually exclusive by convention
— the schema requires at least one but does not reject a template that sets several:

##### File-based mods (Path or Loaders) vs ID-based mods (IDList)

**File-based mods** (the default):

```yaml
# Per-loader map (use with spec.versions):
mods:
  loaders:
    paper:  { displayName: Plugins (Paper), path: plugins, extensions: [".jar"] }
# Or a single shared directory:
mods:
  path: mods
  extensions: [".jar"]
```

**ID-based mods** (games that download by id):

```yaml
mods:
  idList:
    env: ASA_START_PARAMS           # env var the id list is projected into
    separator: ","                  # joins ids; default ","
    format: " -mods={{ids}}"        # token "{{ids}}" is replaced; default "{{ids}}"
    mode: append                    # "replace" (default) or "append" this env
  registry:
    providers: [...]                # optional; curated registry browsing
  # Note: install/allowedHosts are omitted for idList games — the
  # operator projects ids, the server downloads them itself
```

**When to use each:**

- **File-based**: the agent manages the mods directory, downloads files,
  and handles installs/removal. Games like Minecraft (Forge/Fabric plugins),
  Valheim (BepInEx), and Palworld (.pak files).
- **ID-based**: the game server downloads its own mods given a list of ids.
  The operator renders the id list into an env var or flag; the agent never
  downloads anything. The dashboard can still browse a curated registry
  (e.g., CurseForge for ARK) to help users find mods, but the install is
  just projecting ids, not file transfers. Useful when the game has no
  accessible mods directory but does accept id lists.

Real-world example: ARK: Survival Ascended's image downloads mods via
CurseForge ids appended to its launch parameters. The operator renders
the user's selected ids into the `ASA_START_PARAMS` env var (append
mode, so user-supplied params survive):

```yaml
mods:
  idList:
    env: ASA_START_PARAMS
    format: " -mods={{ids}}"
    separator: ","
    mode: append
  registry:
    providers:
      - provider: curseforge
```

| Field | Type | Default | Max | Notes |
|-------|------|---------|-----|-------|
| `env` | string | required | 64 chars | env var the rendered id list is projected into |
| `separator` | string | `,` | 4 chars | string joining ids, e.g. `,` or `;` |
| `format` | string | `{{ids}}` | 128 chars | plain string replacement of the literal token `{{ids}}` — not a Go template |
| `mode` | enum | `replace` | — | `replace` (set env to format result) or `append` (concatenate onto existing env) |

File-based layouts support two patterns:

```yaml
capabilities:
  mods:
    # Per-loader map (use with spec.versions): the active version's
    # `loader` selects the directory, and every (version + loader) combo
    # gets its OWN PVC — switching versions never clobbers another
    # combo's mod set. Max 16 keys.
    loaders:
      paper:  { displayName: Plugins (Paper), path: plugins, extensions: [".jar"] }
      fabric: { displayName: Mods (Fabric),   path: mods,    extensions: [".jar"] }
      bepinex:
        path: bepinex/plugins
        extensions: [".zip"]
        extract: true               # unpack archives into per-mod folders
    install:
      allowedHosts:                 # SSRF allowlist (required for URL installs)
        - cdn.modrinth.com
        - ".curseforge.com"         # leading dot → host + subdomains
      maxSizeMB: 512                # default 256, max 4096
    registry:                       # optional in-app browse — see below
      providers:
        - provider: modrinth
```

```yaml
capabilities:
  mods:
    path: mods                      # legacy single directory, relative to
    extensions: [".jar"]            #   storage.mountPath — for games with
    install: { ... }                #   no version/loader dimension
```

- **Listing/removal** work with or without an `install` block. A version
  entry whose `loader` has no map key (e.g. vanilla) gets no Mods tab at
  all.
- **URL install** downloads a user-supplied URL into the active
  directory. It is refused unless the URL's host matches `allowedHosts`
  (exact host or a `.suffix` for domain + subdomains) *and* the resolved
  address is publicly routable — the agent blocks loopback, private
  (RFC1918/ULA), link-local, and metadata addresses so it can't be
  tricked into fetching cluster-internal services. Downloads are
  size-capped and redirects are re-checked against the allowlist.
- **Upload** (the install page's third mode) sends a local file to the
  agent as multipart. Same filename/extension/size checks; works even
  without an `install` block, since an upload carries no SSRF risk —
  handy for locally built mods (e.g. `.pak` files on Palworld).
  The default per-file limit is 256 MiB; a module's `maxSizeMB` overrides
  it. The dashboard nginx, default ingress and API allow a complete
  multipart upload request of up to 512 MiB, including framing. A file
  configured at 512 MiB cannot use all of that limit in a local upload;
  larger module limits remain usable for URL installs. Custom ingress
  limits can further restrict uploads.
- **Extract mode** (`extract: true`) unpacks downloaded/uploaded `.zip`
  archives into a per-mod folder (BepInEx-style layouts); listing and
  removal then operate on folders. Zip-slip and total-size are guarded.
- Filenames are sanitized against path traversal; each `path` must be
  relative with no `..`.

**Install manifest.** Every mod volume carries a hidden
`.gameplane-mods.json` ledger the agent maintains: registry installs
record their provider/project/version, uploads record provider
`upload`, and files placed outside the panel show as *unmanaged*. This
powers the Mods tab's provenance badges, the batch update check
(`GET /servers/{name}/mods/updates`), and one-click in-place upgrades —
module authors get all of it for free; there is nothing to declare.

##### Registry browse (`capabilities.mods.registry`)

Declaring `registry.providers` turns the install page's browse mode on:
the dashboard searches the registry filtered to the active version's
`loader` + `gameVersion` and one-click installs the chosen file through
the same allowlisted download path (so keep the registry's CDN hosts in
`allowedHosts`).

The block below shows every supported provider's declaration shape for
reference — a single `providers` list may hold **at most 8**, so declare only
the ones your module actually needs.

```yaml
registry:
  providers:
    - provider: modrinth            # Minecraft mods/plugins (keyless)
      modpacks:                     # optional Modpacks tab — see below
        refEnv: MODRINTH_MODPACK
        env: [{ name: TYPE, value: MODRINTH }]
    - provider: curseforge          # needs an admin API key + curseforgeGameID
      curseforgeGameID: 432         # required for curseforge (Minecraft 432, ARK 83374)
    - provider: hangar              # PaperMC plugins (keyless)
    - provider: thunderstore        # BepInEx games (keyless)
      community: valheim            # required: the community slug
      modpacks: {}                  # deps-mode packs (no refEnv)
    - provider: factorio            # official Factorio mod portal
    - provider: steam               # Steam Workshop; needs an admin API key
      steamAppID: 4000              # required: the app to browse (4000 = Garry's Mod)
      modpacks:                     # optional: browse Workshop Collections
        refEnv: WORKSHOP_COLLECTION
    - provider: nexus               # Nexus Mods; needs an admin API key, browse-only
      community: skyrimspecialedition # required: the Nexus game domain slug
    - provider: spigot              # SpigotMC plugins via the Spiget API (keyless)
    - provider: github              # GitHub Releases; browses ONE repo (keyless, rate-limited)
      github:
        owner: someorg             # required: the repository owner
        repo: somemod              # required: the repository name
    - provider: umod                # uMod / Oxide plugins (keyless)
```

- Up to 8 providers; the first is the dashboard's default and a switch
  appears when there's more than one. A provider whose engine needs a
  key (`curseforge`, `steam`, `nexus`) is hidden until an admin sets one
  via the dashboard (`PUT /admin/registries/{provider}/secret`, backed by
  a labelled Secret) — CurseForge alone also accepts the legacy
  `--curseforge-api-key` startup flag as a fallback when no admin key is
  set.
- `thunderstore` requires `community`; `nexus` reuses the same field as
  its game domain slug (e.g. `stardewvalley`, `skyrimspecialedition` —
  see [Nexus's game list](https://nexusmods.com) for the exact slug);
  `steam` requires `steamAppID` instead; `github` requires the `github`
  block (`owner`/`repo`). The others ignore all of these.
- **`factorio` is browse-only**: the portal's downloads require the
  player's own factorio.com credentials, which Gameplane never stores —
  files are flagged `requiresAuth` and the dashboard hands the user to
  the from-URL form to append `?username=…&token=…` themselves. Keep
  `mods.factorio.com` / `.factorio.com` in `allowedHosts`.
- **`steam` is browse-only, for a different reason**: Steam Workshop
  content has no HTTP download URL at all — it's fetched by `steamcmd`
  running inside the game container, using the app's own depot
  credentials. Browsing still works (titles, thumbnails, subscriber
  counts via the Steam Web API), but `Versions` never returns an
  installable file, so the Mods tab shows "No compatible files." for it.
  The one place this becomes genuinely one-click: declare `modpacks` with
  `refEnv` pointing at the game image's Workshop-collection-id variable
  (`WORKSHOP_COLLECTION` on most `steamcmd`-based images,
  `HOST_WORKSHOP_COLLECTION` on some, e.g. joedwards32/cs2's
  `CS2_HOST_WORKSHOP_COLLECTION`) — the Modpacks tab then browses
  Collections instead of individual items, and installing one just pins
  its id into that env var exactly like any other env-mode modpack.
- **`nexus` is browse-only too, and deliberately stays that way**: Nexus's
  real download links are minted per-caller (this API pod) and are
  premium-account-gated, but the *agent* pod in the game namespace is
  what actually performs the download — a different egress path a link
  minted for the API pod's IP is not reliably valid for. The agent's
  download path also doesn't sniff content-type, so a non-premium page
  URL would silently install as an HTML document. `Versions` therefore
  never returns a file for Nexus, on purpose — there is no
  `requiresAuth`/from-URL fallback for it the way Factorio has, because
  Nexus has no self-service URL a user could complete themselves.
- **`spigot`** browses SpigotMC's plugin catalog through the third-party
  Spiget API (SpigotMC itself has no public REST API). Most resources
  install one-click, but some are flagged "external" (hosted on the
  author's own site, not on SpigotMC) or "premium" (a paid purchase Spiget
  has no way to authorize) — those report zero files for every version, the
  same "No compatible files." fallback as Steam/Nexus/Factorio-without-
  credentials, rather than a broken or misleading download link. Keep
  `api.spiget.org` **and** `cdn.spiget.org` in `allowedHosts` — search/
  metadata calls hit the former, and a hosted download 302s to the latter.
- **`github` browses exactly one repository's Releases**, bound by the
  `github.owner`/`github.repo` fields — GitHub has no cross-repo mod
  search, unlike every other provider here. Its "search" is a client-side
  filter over that one repo's release list, not a ranked search. Anonymous
  GitHub API calls are rate-limited to 60/hour per source IP, shared by
  every module using this provider from the same cluster egress address.
  Keep **both** `github.com` **and** `.githubusercontent.com` in
  `allowedHosts` — a release asset's `browser_download_url` starts on
  `github.com` but 302s to a `*.githubusercontent.com` host, and the agent
  re-checks the allowlist on every redirect hop, so allowlisting only the
  first host makes the download fail partway through.
- **`umod`** (formerly Oxide) covers the Rust/Hurtworld/7 Days to Die
  plugin ecosystem. Downloaded files are raw `.cs` C# source — Oxide/uMod
  compiles them inside the game process at runtime, so a `.cs` file is the
  normal, complete artifact, not a build step Gameplane skipped. Keep
  `umod.org` in `allowedHosts`; downloads are served directly from that
  host with no redirect.

##### Mod-portal credentials (Factorio)

Registry providers can declare a `credentialsSecretRef` to inject
authentication into downloads. Currently used by Factorio:

```yaml
registry:
  providers:
    - provider: factorio
      credentialsSecretRef: { name: factorio-creds }
```

The Secret must live in the GameServer's namespace and contain `username`
and `token` keys:

```yaml
apiVersion: v1
kind: Secret
metadata: { name: factorio-creds, namespace: gameplane-games }
type: Opaque
data:
  username: <base64-encoded-username>
  token: <base64-encoded-api-token>
```

The agent mounts the Secret read-only at `/etc/gameplane/mod-creds/factorio/`
and transparently appends `username` and `token` query parameters to download
URLs during install. Missing or unreadable credential files are handled
gracefully (installs proceed without credentials). The credentials are never
logged or included in error messages.

##### Modpacks (`providers[].modpacks`)

Declaring `modpacks` on a provider adds a Modpacks tab. Two install
modes, chosen by `refEnv`:

- **env-mode** (`refEnv` set): installing a pack pins its slug into the
  named env on the GameServer (plus any fixed `env` listed) and the
  server restarts — the game image installs the pack itself on boot
  (Modrinth packs on the itzg image). One pack active per server.
- **deps-mode** (`modpacks: {}`): the pack is resolved into its
  dependency mods, which install one-by-one through the normal install
  path (Thunderstore/BepInEx packs).

### Probes

```yaml
probes:
  readiness:
    httpGet: { path: /health, port: 8080 }
    initialDelaySeconds: 30
    failureThreshold: 10
```

Many game images don't expose HTTP. The `itzg/minecraft-server` image
ships a `mc-health` exec probe; Source images usually expose nothing
useful, so readiness falls back to TCP on the game port.

### Storage layout

`spec.storage.mountPath` is where the PVC is mounted inside the game
container. Pick the path the game writes its world/config files to —
backups snapshot this whole directory.

## Testing a new module locally

```sh
# 1. write modules/<name>/{template.yaml,module.yaml,README.md}
# 2. push to the local kind registry
modules/build.sh push --registry localhost:5001 --name <name>

# 3. wait for the default ModuleSource to pick it up (≤ refreshInterval)
kubectl get modulesource default -o jsonpath='{.status.modules[*].name}'

# 4. install via UI or CR
kubectl apply -f - <<EOF
apiVersion: gameplane.local/v1alpha1
kind: Module
metadata: { name: <name> }
spec:
  source: { name: default }
  name: <name>
EOF

# 5. verify the GameTemplate showed up
kubectl get gametemplate <name> -o yaml
```

Bump `module.yaml#version`, re-push under the new tag, and the catalog
reports an upgrade available within `refreshInterval`. Click **Upgrade**
in the UI (or `kubectl patch module <name> --type merge -p
'{"spec":{"version":"<v>"}}'`). Keep `template.yaml`'s `spec.version` in
lockstep with the bundle version — the official modules do, and drift
between the two confuses pinning.

Worth exercising manually for modules that declare `spec.versions` and
per-loader mods:

```sh
# switch the running server to another catalog entry (Settings → Version
# does the same) and watch the StatefulSet re-render
kubectl patch gameserver <gs> -n gameplane-games --type merge \
  -p '{"spec":{"version":"<other-id>"}}'

# each (version+loader) combo gets its own PVC; the previous combo's
# volume must survive the switch
kubectl get pvc -n gameplane-games | grep <gs>-mods-
```

From the dashboard, also try the Mods tab end to end: install one from
the registry (badge should show provider + version), **Check updates**,
upload a local file (badge `upload`), and remove it.
