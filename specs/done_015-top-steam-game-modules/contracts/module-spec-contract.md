# Contract: Gameplane Module Layout & Metadata

**Feature**: `015-top-steam-game-modules`  
**Contract Version**: `1.0.0`  
**Status**: Normative  

---

## 1. Directory Layout Requirement

Every module in `modules/<name>/` MUST contain exactly the following layout:

```text
modules/<name>/
├── module.yaml          # Module metadata manifest (schema: .schema/module.schema.json)
├── template.yaml        # GameTemplate CRD definition (schema: .schema/gametemplate.schema.json)
├── README.md            # Operator & administrator guide
├── specs.md             # Constitution Principle IV architectural specification
└── samples/
    └── gameserver.yaml  # Ready-to-run sample GameServer manifest
```

---

## 2. Specification Document (`specs.md`) Contract

Per Constitution Principle IV, every module's `specs.md` MUST include these exact headings:

1. `# Gameplane Module Specification: <Display Name>`
2. `## 1. Purpose & Scope`
3. `## 2. Container Image & Architecture`
4. `## 3. Network Ports & Protocols`
5. `## 4. Storage & Persistence Layout`
6. `## 5. Administration & Remote Console (RCON)`
7. `## 6. Modding & Workshop Integration`
8. `## 7. Lifecycle & Graceful Shutdown`
9. `## 8. Key Invariants & Security`
10. `## 9. References & Upstream Documentation`

---

## 3. Preflight Validator Integration Contract

The module MUST pass `modules/validate.py` with 0 errors:

- **Rule 1 (Interactive Shell Detection)**: When an image CMD is `/bin/bash` without ENTRYPOINT, `spec.command` must be explicitly provided.
- **Rule 2 (User ID Consistency)**: When an image User is non-root, `spec.security.runAsUser` must match.
- **Rule 3 (SteamCMD HOME Variable)**: `HOME` env must be explicitly declared when running as non-root on SteamCMD-based images.
- **Rule 4 (Mount Path Safety)**: `storage.mountPath` must never shadow the image's entrypoint binary or launch script.
- **Rule 5 (Image Digest Pinning)**: All concrete versions must resolve to valid sha256 digests.
