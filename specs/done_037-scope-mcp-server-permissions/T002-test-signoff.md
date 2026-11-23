# T002: existing tests touched by spec 037

Checked on master `73389d7a` and mcp `3e070f85` (2026-11-22). Result: **no existing test needs a change**, so no sign-off is needed. If an implementation task finds otherwise, it stops and asks valgul with a sign-off card (file, test, before/after, why).

| File | Test | Why it stays unchanged |
|---|---|---|
| `mcp-server/main_test.go` | `TestClientHasNoMutatingMethods` | `kube.Client` keeps the same read-only exported methods; the panel client gets its own new test (T034). |
| `mcp-server/main_test.go` | `TestRegisteredToolsAreReadOnly` | Tool names, `readOnlyHint` and the registry list are unchanged; `newMCPServer(&kube.Client{})` still compiles because `*kube.Client` implements the new `Reader` interface. No test asserts input schemas. |
| `mcp-server/main_test.go` | `TestToolsListAndGetHappyPath`, `TestProposeFixTool`, `TestListEventsHandler_StripsAndBounds` and the helper tests | Break-glass mode with `GAMEPLANE_MCP_NAMESPACES` unset keeps today's behaviour; new arguments are optional; single-cluster output is unchanged. |
| `mcp-server/internal/kube/client_test.go` | all | `NewFrom` and the List/Get methods keep their signatures; the allow-list is opt-in by env. |
| `test/e2e/optional_components_e2e_test.go` | `TestOptionalComponents_Lifecycle` subtest `mcp` (lines 342-400) | Uses tool names (unchanged), lists GameServers in the games namespace (in scope) and expects an error for `kind=clusters` (still an error). Only a comment mentions the ClusterRole; comments are not changed. |
| `api/internal/auth/sessions_*_test.go` | all | Cookie path of `Authenticate` is unchanged; no test sends an `Authorization` header to it (the only `Bearer` uses are OIDC tests). |
| `api/internal/rbac/rbac_test.go`, `catalog_test.go` | rule and catalog cross-checks | New rules use existing catalog keys (`servers:read`, `*`, `""`); no test counts rules. |
