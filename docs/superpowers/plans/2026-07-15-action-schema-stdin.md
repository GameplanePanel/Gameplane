# Action Schema & Stdin Transport Implementation Plan (Phase 3)

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`).

**Goal:** Let a quick action run a *sequence* of commands, be grouped in the UI, and reach the game over either RCON (agent) or stdin (API pod-attach) — so `consoleMode: pty` games (Terraria, DST) can have actions too. Extract the security-critical param validation into a shared `gameaction/` module both the agent and API import.

**Architecture:** Today `/servers/{name}/actions/run` is a pure mTLS proxy to the agent, which resolves params, renders the command template, and runs it over RCON. This phase (a) moves param-resolution/rendering into a new top-level Go module `gameaction/`; (b) adds `commands`/`transport`/`group` to the CRD; (c) teaches the agent to run a `commands` sequence over RCON; (d) makes the API a real handler that branches — rcon actions still proxy to the agent, stdin actions the API executes itself via pods/attach (it already holds that RBAC and does it for the Console tab); (e) reworks the dashboard card to group buttons and show a "sent" state for stdin.

**Tech Stack:** Go 1.25 (`text/template`, controller-tools v0.20.1, client-go remotecommand/SPDY), React 18 + TS strict, Vitest, Pencil.

## Global Constraints

- **Phase 3 of** `docs/superpowers/specs/2026-07-14-console-protocols-categories-actions-design.md`. Read §3 first.
- **Do NOT run tests/linters locally** (CLAUDE.md rule 8); CI is the source of truth. Compile checks (`go build`, `go vet`, `tsc --noEmit`) are allowed and required.
- **`go vet ./...` SKIPS build-tagged files.** After operator test edits run `cd operator && go vet -tags=envtest ./...` too.
- **After CRD type edits: `make generate && make manifests`**, commit the regenerated `zz_generated.deepcopy.go`, `operator/config/crd/*.yaml`, `operator/config/rbac/*.yaml`, `charts/gameplane/crds/*.yaml` in the SAME commit (rule 7).
- **Sign commits** `git -c commit.gpgsign=false commit -s`. Conventional prefixes. **Fix, don't silence** (rule 4). **`%w`** wrapping (rule 6). **TS strict, no `any`** (rule 5).
- **Coverage gates:** a new `gameaction/.testcoverage.yml`; the agent gate stays 90% (extraction moves covered lines out, so re-baseline agent only if CI's ratchet demands it — do NOT lower without cause); api gate 80%; web 92/76/82/92.
- **The operator is authoritative** (rule 10): the API stays a UX/transport layer. It resolves *which transport* and executes stdin, but the action *definitions* live in the CRD the operator reconciles. Do not put action business logic in a reconciler — actions are a runtime concern, correctly API/agent-side.
- **Never stage the `modules` submodule pointer** unless a task says so.

### The security invariant (do not weaken)

`gameaction` holds the ONLY defense against console injection: `hasControl` rejects CR/LF and control bytes in string params, so a param value can't chain a second console command. BOTH the agent and the API must call `Resolve` before rendering. The API validating first does not excuse the agent from validating — the agent's token-authed endpoint is its own trust boundary. Every transport validates.

---

## File Structure

| File | Responsibility | Task |
|---|---|---|
| `gameaction/go.mod`, `gameaction/action.go` | NEW module: `Param`, `Resolve`, `Compile`/`Command.Render` | 1 |
| `gameaction/action_test.go` | validation/injection/render unit tests | 1 |
| `gameaction/.testcoverage.yml` | coverage gate | 1 |
| `go.work`, `Makefile`, `.github/workflows/ci.yaml` | wire the module in | 1 |
| `agent/go.mod` | require+replace gameaction | 1 |
| `agent/internal/actions/actions.go` | import gameaction; delete local dup logic | 1 |
| `agent/internal/caps/caps.go` | add `Commands`/`Transport`/`Group` to `ServerAction` | 2 |
| `operator/api/v1alpha1/gametemplate_types.go` | `Commands`/`Transport`/`Group` + CEL xor rule | 2 |
| generated CRD/deepcopy/rbac/chart | `make generate manifests` | 2 |
| `agent/internal/actions/actions.go` | run a `Commands` sequence over RCON | 3 |
| `api/internal/kube/stdin.go` | NEW `WriteStdinLines` helper | 4 |
| `api/internal/handlers/actions.go` | NEW real handler: branch rcon↔stdin | 4 |
| `api/cmd/main.go` | mount the new handler in place of the proxy | 4 |
| `api/go.mod` | require+replace gameaction | 4 |
| `design.pen` (Pencil) | grouped buttons, sent-state, overflow | 5 |
| `web/src/types.ts` | `commands`/`transport`/`group` on `ServerActionDecl` | 6 |
| `web/src/components/server/ServerActionsCard.tsx` | groups + sent-state | 6 |
| `docs/module-authoring.md`, `CHANGELOG.md` | document the new fields | 7 |

Task 5 (design) precedes Task 6 (React) — rule 1.

---

## Task 1: Extract the `gameaction/` module (pure refactor)

**Files:**
- Create: `gameaction/go.mod`, `gameaction/action.go`, `gameaction/action_test.go`, `gameaction/.testcoverage.yml`
- Modify: `go.work`, `Makefile:35`, `.github/workflows/ci.yaml` (go matrix), `agent/go.mod`, `agent/internal/actions/actions.go`
- Precedent to copy exactly: `netguard/` (its go.mod, .testcoverage.yml, the require+replace pattern).

**Interfaces produced (agent Task 3 and API Task 4 both consume these):**
```go
package gameaction

// Param is a declared action input. Mirrors the CRD's ActionParamSpec and
// the agent's caps.ActionParam — one canonical shape both importers share.
type Param struct {
	Name        string
	DisplayName string
	Description string
	Type        string   // "string" | "int" | "bool" | "enum"; "" == string
	Default     string
	Enum        []string
	Required    bool
}

// Resolve validates raw user values against the declared params and returns
// the sanitized map. It rejects control characters in string params (the
// console-injection guard), enforces types, the 512-char cap, enum
// membership, and required-ness.
func Resolve(decls []Param, got map[string]string) (map[string]string, error)

// Command is a parsed action command template.
type Command struct { /* unexported *template.Template */ }

// Compile parses one command template (missingkey=error). name is only for
// error messages.
func Compile(name, command string) (*Command, error)

// Render executes the template with the resolved params, returning the
// trimmed command string. Params are exposed to the template as .Params.
func (c *Command) Render(params map[string]string) (string, error)
```

- [ ] **Step 1: Create the module skeleton**

`gameaction/go.mod` (mirror `netguard/go.mod` exactly — same go version, no deps):
```
module github.com/GameplanePanel/gameplane/gameaction

go 1.25.0
```

- [ ] **Step 2: Write `gameaction/action.go`**

Move the logic verbatim from `agent/internal/actions/actions.go` — `resolveParams`→`Resolve`, `validateParam`, `hasControl`, and the template compile/execute — adapting names to the exported API above. The 512-char cap and the `r < 0x20 || r == 0x7f` control check must be preserved byte-for-byte. `Render` executes with `struct{ Params map[string]string }{params}` exactly as the agent does today.

```go
package gameaction

import (
	"fmt"
	"strconv"
	"strings"
	"text/template"
)

type Param struct {
	Name        string
	DisplayName string
	Description string
	Type        string
	Default     string
	Enum        []string
	Required    bool
}

func Resolve(decls []Param, got map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(decls))
	for _, p := range decls {
		val, ok := got[p.Name]
		if !ok || val == "" {
			val = p.Default
		}
		if p.Required && strings.TrimSpace(val) == "" {
			return nil, fmt.Errorf("parameter %q is required", p.Name)
		}
		if val == "" {
			out[p.Name] = ""
			continue
		}
		clean, err := validateParam(p, val)
		if err != nil {
			return nil, err
		}
		out[p.Name] = clean
	}
	return out, nil
}

func validateParam(p Param, val string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(p.Type)) {
	case "int":
		if _, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err != nil {
			return "", fmt.Errorf("parameter %q must be an integer", p.Name)
		}
		return strings.TrimSpace(val), nil
	case "bool":
		switch strings.ToLower(strings.TrimSpace(val)) {
		case "true", "false":
			return strings.ToLower(strings.TrimSpace(val)), nil
		}
		return "", fmt.Errorf("parameter %q must be true or false", p.Name)
	case "enum":
		for _, e := range p.Enum {
			if val == e {
				return val, nil
			}
		}
		return "", fmt.Errorf("parameter %q must be one of the declared options", p.Name)
	default:
		if hasControl(val) {
			return "", fmt.Errorf("parameter %q must not contain control characters", p.Name)
		}
		if len(val) > 512 {
			return "", fmt.Errorf("parameter %q is too long (max 512)", p.Name)
		}
		return val, nil
	}
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

type Command struct {
	tmpl *template.Template
}

func Compile(name, command string) (*Command, error) {
	t, err := template.New(name).Option("missingkey=error").Parse(command)
	if err != nil {
		return nil, fmt.Errorf("parse action command %q: %w", name, err)
	}
	return &Command{tmpl: t}, nil
}

func (c *Command) Render(params map[string]string) (string, error) {
	var sb strings.Builder
	if err := c.tmpl.Execute(&sb, struct{ Params map[string]string }{params}); err != nil {
		return "", fmt.Errorf("render action command: %w", err)
	}
	return strings.TrimSpace(sb.String()), nil
}
```

- [ ] **Step 3: Write `gameaction/action_test.go`**

Port the injection + validation assertions from `agent/internal/actions/actions_test.go` down to the unit level. Cover: `TestResolve_Defaults`, `TestResolve_RequiredMissing`, `TestResolve_RejectsControlChars` (the `"hi\nstop"` case — the security regression guard), `TestResolve_IntBoolEnum` (each type's valid+invalid), `TestResolve_TooLong` (513 chars), `TestCompile_BadTemplate` (returns error), `TestRender_Params` (`"say {{.Params.message}}"` → `"say hi"`), `TestRender_MissingKey` (a template referencing an undeclared key errors). Aim ≥ 91% (matches netguard's floor).

- [ ] **Step 4: `gameaction/.testcoverage.yml`** (mirror netguard's):
```yaml
# Coverage threshold gate for the shared gameaction module — the console-
# injection guard and template renderer shared by the agent and API.
profile: coverage/unit.out
github-action-output: false
threshold:
  file: 0
  package: 0
  total: 91
```

- [ ] **Step 5: Wire the module into the workspace + build + CI**

`go.work` — add `./gameaction` to the `use (...)` block (put it next to `./netguard`).
`Makefile:35` — `GO_MODULES := netguard gameaction operator api agent audit-syslog-bridge telemetry-receiver mcp-server`.
`.github/workflows/ci.yaml` — add `gameaction` to the go job's `matrix.module` list.
`agent/go.mod` — add, mirroring the netguard lines:
```
require github.com/GameplanePanel/gameplane/gameaction v0.0.0
replace github.com/GameplanePanel/gameplane/gameaction => ../gameaction
```

- [ ] **Step 6: Rewire the agent to use gameaction (delete the dup)**

In `agent/internal/actions/actions.go`: delete `resolveParams`, `validateParam`, `hasControl`, and the inline template parse/execute. `compiled` now holds `*gameaction.Command`. `compile` calls `gameaction.Compile(s.ID, s.Command)`. The `run` handler calls `gameaction.Resolve(paramsToGameaction(act.spec.Params), body.Params)` then `act.cmd.Render(params)`. Add a tiny adapter `func toGameactionParams(ps []caps.ActionParam) []gameaction.Param` (field-for-field copy). Everything else in `run` (status codes, error non-leak, empty-command 422) stays.

- [ ] **Step 7: Trim the agent's now-redundant tests**

`agent/internal/actions/actions_test.go` keeps the HANDLER-level tests (status codes, RCON-not-leaked, unknown-action, disabled) but the pure param-validation assertions now live in gameaction. Leave the handler injection test (`TestRun_RejectsInjection`) — it proves the wiring still rejects, end to end.

- [ ] **Step 8: Compile-check everything**
```sh
cd gameaction && go build ./... && go vet ./...
cd agent && go build ./... && go vet ./...
```
Verify `grep -rn "func hasControl" agent/` returns nothing (the dup is gone).

- [ ] **Step 9: Commit** (pure refactor; agent behavior identical):
```
feat(gameaction): extract shared action param-validation module

Carves the console-injection guard and command-template renderer out of
the agent into a top-level module both the agent and (next) the API import,
the same way netguard was split out. No behavior change.
```

---

## Task 2: CRD schema — commands, transport, group

**Files:** `operator/api/v1alpha1/gametemplate_types.go` (ServerActionSpec), regenerated artifacts, `agent/internal/caps/caps.go`, an envtest.

- [ ] **Step 1: Add the fields to `ServerActionSpec`** (after `Command`):
```go
	// Commands runs several console commands in order (mutually exclusive
	// with Command). Mirrors capabilities.quiesce / lifecycle.stop, which
	// already express sequences as a plain []string. Each is a Go template
	// rendered with .Params, same as Command.
	// +kubebuilder:validation:MaxItems=16
	// +optional
	Commands []string `json:"commands,omitempty"`

	// Transport selects how the commands reach the game. Empty resolves to
	// rcon when rcon.protocol != none, else stdin (pod-attach) — so a
	// pty-console game like Terraria can carry actions too.
	// +kubebuilder:validation:Enum=rcon;stdin
	// +optional
	Transport string `json:"transport,omitempty"`

	// Group labels a section on the server detail page (e.g. "World").
	// +kubebuilder:validation:MaxLength=24
	// +optional
	Group string `json:"group,omitempty"`
```
Make `Command` `+optional` (it was required) since an action may now use `Commands` instead. Add a CEL rule on the struct enforcing exactly one:
```go
// +kubebuilder:validation:XValidation:rule="has(self.command) != (has(self.commands) && size(self.commands) > 0)",message="set exactly one of command or commands"
```
This CEL sits inside `capabilities.actions` (MaxItems=32) with `commands` MaxItems=16 and bounded string lengths — within the apiserver's CEL cost budget (see the CRD CEL cost-budget memory).

- [ ] **Step 2: `make generate && make manifests`.** Verify `charts/gameplane/crds/` changed and the CEL rule + new props appear in the generated YAML.

- [ ] **Step 3: Mirror the fields in `agent/internal/caps/caps.go` `ServerAction`:**
```go
	Commands  []string `json:"commands,omitempty"`
	Transport string   `json:"transport,omitempty"`
	Group     string   `json:"group,omitempty"`
```

- [ ] **Step 4: envtest** in `operator/internal/controller/gametemplate_envtest_test.go`: a template with `commands: [a, b]` and no `command` applies; one with BOTH `command` and `commands` is rejected (`apierrors.IsInvalid`); one with neither is rejected. (Use the `-tags=envtest` vet check.)

- [ ] **Step 5: Compile-check** `cd operator && go vet ./... && go vet -tags=envtest ./...` and `cd agent && go vet ./...`. **Commit** (source + regen together).

---

## Task 3: Agent runs a Commands sequence over RCON

**Files:** `agent/internal/actions/actions.go`, `agent/internal/actions/actions_test.go`.

The agent only ever executes rcon actions (the API routes stdin ones elsewhere), so this is: when `Commands` is set, render and `Exec` each in order, concatenating output; a mid-sequence error aborts and returns the failure.

- [ ] **Step 1 (test first):** add `TestRun_Sequence` — an action with `commands: ["save-off", "save-all"]` calls `Exec` twice in order; the fake RCON records both; the response `raw` concatenates the two outputs (newline-joined). Add `TestRun_SequenceAbortsOnError` — the 2nd command's `Exec` errors; the 3rd is never called; handler returns 502.

- [ ] **Step 2:** `compile` builds a `[]*gameaction.Command` when `Commands` is set, else a single-element slice from `Command`. `run` resolves params once, renders+Execs each command in order, aborts on the first error, and joins outputs with `"\n"`. Preserve the empty-command 422 (if a rendered step is empty). Keep the RCON-error non-leak.

- [ ] **Step 3:** compile-check, commit.

---

## Task 4: API stdin transport

**Files:** `api/go.mod` (require+replace gameaction), `api/internal/kube/stdin.go` (new), `api/internal/handlers/actions.go` (new), `api/cmd/main.go` (mount), an envtest/unit test.

**Context:** `/servers/{name}/actions/run` is registered as a proxy in `api/internal/ws/dialer.go` (`p.httpProxy("/actions/run")`). It becomes a real handler that fetches the template, resolves the transport, and branches.

- [ ] **Step 1: `WriteStdinLines` in `api/internal/kube/stdin.go`.** Port `operator/internal/controller/gameserver_stop_attach.go` almost verbatim, hung off `*kube.Client` (which has `.Typed` and `.Config`). Signature:
```go
// WriteStdinLines attaches to a game container's stdin via pods/attach and
// writes each line followed by \n, then tears the session down. Fire-and-
// forget: attach doesn't EOF-close, so it's bounded by a timeout. Mirrors
// the operator's stop-attach and the Console tab's attach shape (TTY:true,
// Stdout requested+discarded — a tty/non-tty mismatch behaves differently
// across runtimes).
func (c *Client) WriteStdinLines(ctx context.Context, ns, pod, container string, lines []string) error
```
Use `corev1.PodAttachOptions{Container: container, Stdin:true, Stdout:true, TTY:true}`, `remotecommand.NewSPDYExecutor(c.Config, "POST", url)`, a 10s bounded context, and treat `context.DeadlineExceeded/Canceled` as success (same as the operator).

- [ ] **Step 2: `handlers/actions.go` — the branching handler.** It must:
  1. resolve namespace (`scope.Resolve`), read `name` param, decode `{id, params}`.
  2. fetch the GameServer CR → its `spec.templateRef.name` → the GameTemplate CR (via `kube.Client.Dynamic`; find the existing CR-get helper other handlers use — do NOT hand-roll GVR strings if a helper exists).
  3. find the action by `id` in `tmpl.spec.capabilities.actions`; 404 if absent.
  4. resolve transport: explicit `action.transport`, else `rcon` if `tmpl.spec.rcon.protocol != "" && != "none"`, else `stdin`.
  5. **rcon** → delegate to the existing proxy path (reuse `proxy.httpProxy`/`agentHost` — inject the proxy, or keep the proxy mounted and have this handler forward). Simplest: keep the agent proxy reachable and call it for rcon; only stdin is new logic here.
  6. **stdin** → `gameaction.Resolve` the params, render each of `command`/`commands` with `gameaction.Compile().Render()`, then `kube.Client.WriteStdinLines(ctx, ns, pod, "game", renderedLines)`. Respond `{ok:true, raw:""}` (fire-and-forget — output surfaces in the Console stream, not here). The pod name is the server's pod; find how other handlers resolve a server's pod name (likely `<name>-0` for the StatefulSet, or a lookup).

  **Validation is mandatory here too** — call `gameaction.Resolve` before rendering. The API is a trust boundary; do not skip it because "the UI validated".

- [ ] **Step 3: mount it.** In `api/cmd/main.go`, replace the `POST /servers/{name}/actions/run` proxy registration with the new handler, keeping the same `servers:write` RBAC middleware (path-based, unchanged). The handler still needs the proxy for the rcon branch — pass it in.

- [ ] **Step 4: test.** Unit-test the transport resolver (explicit stdin; default rcon when protocol set; default stdin when protocol none/empty). Test the stdin branch against a fake `stdinWriter` interface (extract an interface so the test injects a fake — do not attach to a real pod in a unit test): assert the rendered lines reach the writer and params are validated (an injection attempt is rejected with 400, writer NOT called). An envtest can cover the CR-fetch. Budget api logins (rule: ~7 admin logins/bucket).

- [ ] **Step 5:** compile-check (`cd api && go build ./... && go vet ./...`), commit.

---

## Task 5: Design pass — grouped actions + sent-state (Pencil)

**Files:** `design.pen` via Pencil MCP only (never Read/cat/rm it — rule 2). Prerequisite: the user must have `design.pen` open in the Pencil GUI (an empty in-memory doc has wiped it before — if `open_document` returns empty, STOP and ask).

The Quick Actions card (`ServerActionsCard`, on the server detail page) becomes: buttons grouped under labelled sections (World / Server / Players), an ungrouped fallback section, and a distinct **"sent"** confirmation state for stdin (fire-and-forget) actions vs the existing output echo for rcon. 4–8 buttons should not overflow.

- [ ] Open + orient (`get_editor_state{include_schema:true}` → find the server-detail screen and the actions card). Screenshot before.
- [ ] Design grouped sections + the sent-state chip, following the file's lunaris tokens (`c:Mode:Dark`, semantic `$c:--*`, no raw hex). Screenshot both light and dark.
- [ ] Ask the user to Ctrl/Cmd-S. Verify `git diff --stat design.pen` is ADDITIVE. Commit with the touched node ids in the message.

---

## Task 6: Web — grouped card + transport-aware result

**Files:** `web/src/types.ts`, `web/src/components/server/ServerActionsCard.tsx`, `.test.tsx`.

- [ ] **Step 1:** `types.ts` `ServerActionDecl` gains `commands?: string[]; transport?: "rcon" | "stdin"; group?: string;`.
- [ ] **Step 2:** `ServerActionsCard.tsx`: group `actions` by `group` (undefined → a trailing "Actions" section), render labelled sections per the design, and apply the chosen overflow treatment. For a run whose action `transport === "stdin"` (or resolves to stdin), the success state reads *"{name} sent"* rather than echoing `raw` (there is none). Keep the param dialog, danger styling, RBAC gating.
- [ ] **Step 3:** update `.test.tsx`: a grouped set renders section headers; a stdin action shows "sent" not output; existing no-param/param-dialog/viewer-disabled tests still pass. `tsc --noEmit` clean.
- [ ] **Step 4:** commit.

---

## Task 7: Docs

- [ ] `docs/module-authoring.md`: document `commands`, `transport`, `group` on an action, the command↔commands xor, and that stdin actions are fire-and-forget (output appears in the Console tab, not inline). Note pty-console games can now carry actions.
- [ ] `CHANGELOG.md` Unreleased: the action schema gains sequences/transport/grouping; a shared `gameaction` module; stdin actions for pty-console games. Keep to a couple of bullets (top-of-Unreleased is a conflict hotspot — append to the existing section).

---

## Self-Review

| Spec §3 requirement | Task |
|---|---|
| `commands`/`transport`/`group` on ServerActionSpec + command↔commands xor | 2 |
| transport default = "prefer protocol that returns output" (rcon if protocol≠none) | 4 (resolver) |
| RCON actions run in the agent; sequences iterate + concat | 3 |
| stdin actions run in the API via pods/attach; fire-and-forget | 4 |
| `WriteStdinLines` modelled on gameserver_stop_attach.go | 4 |
| shared `gameaction/` module, both agent+API import; validation not duplicated | 1, 4 |
| both transports validate (agent is its own trust boundary) | 1 (agent), 4 (API) |
| grouped card + sent-state, design-first | 5, 6 |

**Risks:** (a) the API must fetch the template in the action path — new but other handlers already read CRs; find the existing helper, don't hand-roll GVRs. (b) resolving the server's pod name for stdin — reuse an existing resolver. (c) the rcon branch must keep proxying to the agent unchanged so no existing action regresses — the cleanest split leaves the proxy in place and only adds the stdin branch. (d) CEL cost budget — the xor rule is simple and the arrays are bounded, but only the two envtest jobs catch a budget breach.

**PR boundaries:** Task 1 (gameaction extraction) ships alone — pure refactor, easy review. Tasks 2+3 (schema + agent sequences) ship together. Task 4 (API stdin) alone. Tasks 5+6+7 (design+UI+docs) together. Four PRs.
