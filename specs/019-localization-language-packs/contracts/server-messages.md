# Contract: Localizable Server Messages

**Feature**: `019-localization-language-packs` | **Binding modules**: `api/internal/httperr`, `api/internal/messages`, `web/src/lib/api.ts` | **Status**: Binding

## 1. Negotiation

| Request `Accept` | Error response |
|---|---|
| lists `application/problem+json` explicitly with an effective quality value > 0 | `Content-Type: application/problem+json`, body §2 |
| anything else (including absent, `*/*`, `application/*`, `application/json`, or `application/problem+json;q=0`) | unchanged: `Content-Type: text/plain; charset=utf-8`, body = English text + `\n` exactly as today |

The header is parsed as comma-separated media ranges with optional `q` parameters (RFC 9110 §12.5.1); wildcards never select problem details, and `q=0` means not acceptable. Success responses are unaffected. The dashboard always sends `Accept: application/json, application/problem+json`.

## 2. Problem body (RFC 9457 + extensions)

```json
{
  "type": "about:blank",
  "status": 403,
  "title": "Forbidden",
  "detail": "namespace not permitted",
  "messageId": "server.namespace_forbidden",
  "args": {}
}
```

- `detail` MUST equal the text the plain-text response would carry, without the trailing `\n` that `http.Error` appends to the plain-text body (SC-010 concerns the plain-text body, which keeps that newline).
- `messageId` is present for every message in the registry; responses from code paths not yet migrated carry `"messageId": "server.generic.<status>"` (e.g. `server.generic.400`) so the dashboard still shows a translated generic sentence followed by `detail`.
- `args` values are strings or numbers; names match the English entry's placeholders.
- 5xx keep today's behaviour: `detail` is the status text, never internal error text.
- Unauthenticated endpoints (`/auth/*`, public shares) only ever use generic ids (`server.auth.invalid_credentials`, `server.generic.*`) — never ids or args that reveal account existence (FR-030, CLAUDE.md rule 3).

## 3. Go API

```go
// api/internal/messages
type ID string
func New(id ID, args map[string]any) *httperr.Message  // English rendered from the registry

// api/internal/httperr
type Message struct { ID string; Args map[string]any; Status int; English string }
func (m *Message) Error() string                        // English
func WriteMessage(w http.ResponseWriter, r *http.Request, m *Message)
// Write/WriteCode/classify: sentinel errors map to registry ids; errors.As(*Message) honoured.
```

Registry entries are `{ID, English ICU, Description}`; `i18n-check --sync-server` writes them into `i18n/locales/en/app.json`; CI fails on drift.

## 4. Dashboard

`APIError` gains `messageId?: string` and `args?: Record<string, string | number>`. Display: `intl.formatMessage({id: messageId}, args)` when the id exists in the active catalog; for `server.generic.*` ids the dashboard appends `detail` after the translated sentence; when the id is not in the catalog, `detail` alone. Plain-text responses (older API) keep today's behaviour.

## 5. Operator condition reasons

Translation id `condition.<type>.<reason>` (lower-cased, e.g. `condition.ready.imagepullfailed`), args from `status.conditionArgs[<Type>]`. Fallback: the condition's English `message`.
