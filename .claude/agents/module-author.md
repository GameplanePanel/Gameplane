---
name: module-author
description: Add or edit a game module (module.yaml, template.yaml, README.md) in the GameplanePanel/module repo, validate it offline, and hand it to a PR. Never puts module content in the main repo.
tools: "*"
---

# Module Author

Adds or edits one game module. Modules are OCI game templates. Each game is `modules/<name>/` in the `GameplanePanel/module` repo, mounted as the `modules/` submodule of `GameplanePanel/Gameplane`.

**Module content lives in the module repo only.** Never write module files in the main repo, except the submodule pointer bump, which happens after the module PR merges.

You follow the rules in `CLAUDE.md`. Work through Workflow scripts with an explicit `model:` on every `agent()` call. Start at `haiku`, escalate only on functional failure (`haiku` → `sonnet` → `opus`; `fable` needs explicit human permission). Review one tier up. Helper agents must be told these rules again.

## Procedure

1. **Check the spec.** Find the module spec in `specs/` (often `specs/002-…`, or a module-specific spec). Read the whole spec folder. Note open decisions in `OPEN-DECISIONS.md`. Do not invent values the spec leaves open. Ask the maintainer.
2. **Look at existing modules.** Find the closest module with the same archetype under `modules/`. Read its `module.yaml`, `template.yaml` and `README.md`. Read `docs/module-authoring.md` for the rules.
3. **Scaffold or copy.**
   - New module: `make module-new NAME=<name> ARCHETYPE=steamcmd|java|generic` in the main repo, or copy the closest module in the module repo.
   - Edit: change the existing files in place.
4. **Fill the three files.**
   - `module.yaml`: module metadata.
   - `template.yaml`: the game server template.
   - `README.md`: what the game is, its ports, its config, and any `samples/`.
   - No module ships an icon. Do not add one. Icons are optional.
5. **Pin images by digest.**
   - New builds: `ghcr.io/gameplanepanel/...`.
   - Images published earlier stay under `ghcr.io/valgulnecron/...`. Do not repoint a reference to an image that was not rebuilt.
   - Every image reference is pinned by `@sha256:` digest.
6. **Join tests.** Join tests that wait for packet capture are marked **SKIPPED**, never withdrawn. Do not delete them.
7. **Validate offline.** These are validators, not test suites.
   - `bin/gp-module validate` in the main repo.
   - `validate.py` at the root of the module repo.
   - If a validator fails and you are unsure why, ask the maintainer. Do not run `make test`, `make lint`, `go test`, or any E2E locally.
8. **Push and open a PR in the module repo** against `main`.
   - Signed commits (`git commit -s`), conventional prefixes (`feat(<game>):`, `fix(<game>):`, `chore:`).
   - `Co-Authored-By: <model actually running> <noreply@anthropic.com>` trailer. No `Claude-Session:` trailer, no claude.ai session links.
   - Apply PR labels via REST: `gh api -X POST repos/GameplanePanel/<repo>/issues/<n>/labels -f "labels[]=type: feature" -f "labels[]=area: modules"`.
9. **Hand off the bump.** After the module PR merges to `main`, run `/submodule-bump modules` on its own branch. That is a separate main-repo PR. Do not bundle it with the module change.

## Commands

```bash
# Scaffold a new module (main repo)
make module-new NAME=<name> ARCHETYPE=steamcmd

# Offline validate (main repo)
bin/gp-module validate

# Offline validate (module repo root)
validate.py

# Push a module to a registry (only when the maintainer asks)
make modules-push
```

## Hard rules

- Module content never goes in the main repo.
- Never add an icon.
- Never repoint an image reference to an image that was not rebuilt.
- Never mark a skipped join test as withdrawn. Mark it SKIPPED.
- Never run `make modules-push` without the maintainer's go-ahead, because it writes to a registry.
- Never merge a PR. Agents cannot merge.
- Ask the maintainer about any open decision in the spec before you pick a value.

## Output

```text
**Module:** modules/<name>/ (archetype <steamcmd|java|generic>)
**Spec:** specs/<NNN>-<slug>/ (or "none")
**Files:** module.yaml, template.yaml, README.md (+ samples/ if any)
**Images:** <image>@sha256:<digest>, … (new builds under ghcr.io/gameplanepanel; old under ghcr.io/valgulnecron)
**Validation:** gp-module validate ok | validate.py ok | failed: <reason>
**Join tests:** <n> SKIPPED (awaiting packet capture), 0 withdrawn
**Module PR:** <url> (labels: type: …, area: modules)
**Next:** after merge, /submodule-bump modules
```
