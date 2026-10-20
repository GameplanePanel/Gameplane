# T045 libs chunk: independent verification (opus)

Held candidates for `gp-module/` (OD-019, off-git). The companion public verification is `audit/evidence/review-gp-module/verification.md`, which does not mention this item.

## Method

I tried to refute the one candidate in `audit/held/review-gp-module.md` against master `13a859ff`. None of the cited files differ between this branch and master. I read the `steamcmd` preset (`gp-module/internal/archetypes/archetypes.go:117-160`) and the scaffold renderer (`gp-module/internal/scaffold/scaffold.go`). I read the promise the preset makes in `specs/done_010-easy-module-building/contracts/archetypes-contract.md:13` and the contract's own template example at `:54`. I read how the operator picks the game container's user: `operator/api/v1alpha1/gametemplate_types.go:166-171`, and `gameContainerSecurityContext` and `gamePodSecurityContext` in `operator/internal/controller/gameserver_controller.go:1967-2083`. I also read `docs/module-authoring.md:1008-1030` (the `spec.security` section). I pulled no image, so I did not confirm the upstream image's configured user; the tag name is the only evidence, and the confirmation step below covers it. I ran no test or lint suite.

| Candidate | Verdict (kept/rejected) | Severity | Reason |
|---|---|---|---|
| H-gp-module-01 | kept | S3 | Confirmed by reading the code. The preset's description says "non-root user", and `archetypes-contract.md:13` promises "non-root security defaults". The preset's default image is the upstream `root` tag, and the scaffolded template has no `spec.security` block. Without that block the operator sets no container `securityContext` user (`gameContainerSecurityContext` returns nil), so the game runs as the image's own user. One mitigating detail: the contract's own template example at `archetypes-contract.md:54` uses the `:root` image, so the contract contradicts itself, and the characteristics row (the promise) should win. Only newly scaffolded modules are affected, not the shipped catalog. Workaround: the author adds `spec.security` or picks a non-root image. |

### H-gp-module-01

**Location:** `gp-module/internal/archetypes/archetypes.go:121-122` (the `steamcmd` preset's `Description` and `DefaultImage`); `gp-module/internal/scaffold/scaffold.go` (the rendered `spec` never includes `security`).

**Control:** Privilege defaults for scaffolded modules. The `steamcmd` preset, and the dashboard builder's "SteamCMD Dedicated Server" preset that uses the same definition (`api/internal/handlers/modules_builder.go`, archetype listing), is documented as producing a template that runs the game as a non-root user.

**Repro / observation (defensive; confirms whether the control holds):**
1. Read `archetypes.go:121`: the description ends "(Valve UDP ports, save volume, non-root user)". `archetypes-contract.md:13` lists "non-root security defaults" as a primary characteristic of `steamcmd`.
2. Read `archetypes.go:122`: `DefaultImage` is `cm2network/steamcmd:root@sha256:4d830b…`. The tag names the upstream image's root variant.
3. In a scratch directory, run `gp-module init x --archetype steamcmd -y` and read the generated `template.yaml`. It uses that image and has no `spec.security` block. `gp-module validate` reports it clean.
4. Read `operator/internal/controller/gameserver_controller.go:1981-1990`. With no `spec.security` (or one without `runAsUser`/`runAsGroup`), `gameContainerSecurityContext` returns nil, so the pod runs the game as whatever user the image declares. `gametemplate_types.go:166-171` documents this ("Omitted means … the image's own default user").
5. To confirm the image's user, run `docker image inspect <DefaultImage> --format '{{.Config.User}}'`. An empty value, `root` or `0` means the scaffolded game runs as root.

**Expected:** The `steamcmd` preset produces a template whose game container does not run as root. Either the default image is a non-root variant, or the template carries a `spec.security` block with a non-root uid and gid that the image supports. Then the preset description and `archetypes-contract.md:13` are true. The contract example at `:54` is updated to match. A scaffold unit test in `gp-module/internal/scaffold/scaffold_test.go` asserting a non-root default keeps it fixed.

**Actual:** The preset's default is the root image variant and it emits no `spec.security` block, so the documented non-root default is not delivered.
