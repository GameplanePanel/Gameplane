# Held review candidates: gp-module (OD-019, off-git)

- **Date**: 2026-09-24
- **Reviewer tier**: opus (verification pending)
- **Checked against**: `specs/done_010-easy-module-building/contracts/archetypes-contract.md:13,54`; `gp-module/specs.md`; `operator/api/v1alpha1/gametemplate_types.go:166-171` (`spec.security`)
- **Companion notes**: `audit/evidence/review-gp-module/notes.md` (it links here only as "held candidates: 1")

This candidate concerns the privilege a scaffolded module runs with. The description is defensive: the control, where it lives, what correct behaviour is, and how a maintainer confirms it holds. It comes from reading the code and scaffolding into a scratch directory. Nothing was run against a cluster.

## Candidate findings

### H-gp-module-01: The `steamcmd` archetype promises non-root defaults but scaffolds a template that uses the image's `root` variant and has no `spec.security`

- **Location**: `gp-module/internal/archetypes/archetypes.go:121-122` (description and `DefaultImage: "cm2network/steamcmd:root@sha256:…"`); `gp-module/internal/scaffold/scaffold.go:144-166` (the rendered spec never includes `security`)
- **Category**: correctness (security control: privilege defaults)
- **Suggested severity**: S3. It affects modules authors scaffold with `gp-module init --archetype steamcmd` or the dashboard builder's "SteamCMD Dedicated" preset. Shipped modules are not affected.
- **Control and its stated promise**:
  - `specs/done_010-easy-module-building/contracts/archetypes-contract.md:13`: `steamcmd` archetype characteristics include "non-root security defaults".
  - `archetypes.go:121` (the preset's own description, shown in the builder's archetype list via `api/internal/handlers/modules_builder.go:439-476`): "(Valve UDP ports, save volume, non-root user)".
  - How the operator decides the game container's user: `gametemplate_types.go:166-171` says that without `spec.security`, the game runs as "the image's own default user".
- **Observation**:
  1. `gp-module init my-steam --archetype steamcmd -y` (run in a scratch directory) writes a `template.yaml` with `image: cm2network/steamcmd:root@sha256:4d830b…` and no `spec.security` block.
  2. The image tag is the upstream image's `root` variant. Without `spec.security`, the operator doesn't override the user, so the game container runs as whatever that image declares.
  3. Neither `gp-module validate` nor the web builder flags this. The scaffolded module validates clean.
- **Correct behaviour**: The `steamcmd` preset produces a template whose game container does not run as root: either a non-root image variant as the default, or a `spec.security` block with a non-root uid/gid that the image supports. The description and contract then stay true.
- **How a maintainer confirms it holds**: Scaffold with `gp-module init x --archetype steamcmd -y` and check that the resulting template has a non-root image default or a `spec.security` block. Then run `docker image inspect <DefaultImage> --format '{{.Config.User}}'`: a non-empty, non-`root`/non-`0` user means the default is non-root. A scaffold unit test in `internal/scaffold/scaffold_test.go` asserting this keeps it fixed.
