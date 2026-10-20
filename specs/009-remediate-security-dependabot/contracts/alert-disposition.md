# Alert Disposition Record

## Policy

Vulnerabilities and false-positive alerts are resolved in strict priority order:

1. **REFACTOR**: Code is restructured to use patterns CodeQL recognizes as safe, clearing the alert automatically on the next default-branch analysis. Refactoring is attempted first and is always preferred.
2. **DISMISS**: Only when refactoring provably cannot clear the alert (e.g. a safe TLS configuration using `InsecureSkipVerify` with no alternative API), the alert is dismissed via the GitHub code-scanning API with `dismissed_reason=false positive` and a written justification. **Every dismissal requires maintainer sign-off before submission.** Dismissal is never a substitute for a fix where a fix is possible.
3. **FIX**: For real defects, the code is corrected.

**In-source suppressions are absolutely forbidden.** No `//nolint`, `//#nosec`, or any other directive is permitted in source code. All dismissals are GitHub-side actions only, recorded in `dismissed_comment` with the alert record as public documentation.

---

## Alert #1: go/clear-text-logging

**Rule ID**: `go/clear-text-logging`  
**Site**: `api/internal/kube/watch.go:40`  
**Code Path**: Logged string variable `secretKey` (value is a field name like `"kubeconfig"`) inside a cluster watch error handler.

**Existing Guard**:
```go
slog.Warn("cluster watch: failed to load cluster", "cluster", name, "err", err)
```
Taint source is the *string variable* `secretKey`, which holds a field name, not secret bytes. Kubeconfig bytes never enter any log call.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Rename the `secretKey` variable to remove the heuristic taint source (its value is a field name like `"kubeconfig"`, not a secret itself; renaming removes CodeQL's semantic inference). This is cosmetic with respect to actual security, since the logging is already safe. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
The taint source is a string variable containing a kubeconfig field name 
(e.g. "kubeconfig"), not secret bytes. Kubeconfig bytes are never passed 
to log output; this alert analyzes the variable type, not the data flow. 
Field name strings are safe to log.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/1 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='The taint source is a string variable containing a kubeconfig field name (e.g. "kubeconfig"), not secret bytes. Kubeconfig bytes are never passed to log output; this alert analyzes the variable type, not the data flow. Field name strings are safe to log.'
```

---

## Alert #2: go/clear-text-logging

**Rule ID**: `go/clear-text-logging`  
**Site**: `api/internal/kube/watch.go:54`  
**Code Path**: Same pattern in the UpdateFunc path.

**Existing Guard**:
```go
slog.Warn("cluster watch: ...", "err", err)
```
Same analysis as Alert #1: taint source is field name, not secret bytes.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Same as Alert #1 — rename `secretKey` to remove the heuristic taint source. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
Same as Alert #1: taint source is a string variable containing a field name, 
not sensitive credential bytes. Field name strings are safe to log.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/2 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Same as Alert #1: taint source is a string variable containing a field name, not sensitive credential bytes. Field name strings are safe to log.'
```

---

## Alert #3: go/disabled-certificate-check

**Rule ID**: `go/disabled-certificate-check`  
**Site**: `agent/internal/rcon/satisfactory.go:199`  
**Code Path**: TLS configuration with conditional InsecureSkipVerify.

**Existing Guard**:
```go
tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12}
if isLoopbackHost(host) {
  tlsCfg.InsecureSkipVerify = true
}
```
`isLoopbackHost` (lines 220–226) accepts only literal "localhost" or a parsed loopback IP. Package doc (lines 60–76) records the rationale: Satisfactory generates a self-signed cert with no way to supply a CA; the dial never leaves the pod.

**Verdict**: FALSE POSITIVE  
**Disposition**: DISMISSED (2026-08-29)
**Maintainer sign-off**: ValgulNecron, 2026-08-29 (T025)
**Submitted**: `PATCH /code-scanning/alerts/3` — `state=dismissed`, `dismissed_reason="false positive"`. Confirmed by API response: `state=dismissed by=ValgulNecron`.

**Justification**: This alert is verified as unclearable by refactoring. CodeQL's `go/disabled-certificate-check` flags the `InsecureSkipVerify = true` assignment itself and ignores surrounding guards, comments, and TLS configuration. Removing the assignment entirely (e.g., via custom `VerifyPeerCertificate` with pinning) would clear the alert, but Satisfactory regenerates its self-signed certificate on restart, making certificate pinning unmaintainable. The surrounding `isLoopbackHost` guard proves the assignment is safe for its intended use case, but CodeQL does not analyse it. This dismissal is planned and verified.

**Dismissal Justification**:
```
InsecureSkipVerify is set only when isLoopbackHost(host) succeeds, which 
accepts only the literal string "localhost" or a loopback IP (127.x.x.x, 
::1) — see agent/internal/rcon/satisfactory.go:220–226. Satisfactory 
generates a self-signed cert on startup with no API to supply a CA. Since 
the connection is always local (inside the pod), cert verification is 
redundant. See the package documentation at lines 60–76 for the full 
rationale.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/3 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='InsecureSkipVerify is set only when isLoopbackHost(host) succeeds, which accepts only the literal string "localhost" or a loopback IP (127.x.x.x, ::1) — see agent/internal/rcon/satisfactory.go:220–226. Satisfactory generates a self-signed cert on startup with no API to supply a CA. Since the connection is always local (inside the pod), cert verification is redundant. See the package documentation at lines 60–76 for the full rationale.'
```

---

## Alert #4: go/disabled-certificate-check

**Rule ID**: `go/disabled-certificate-check`  
**Site**: `test/e2e/internal/satisfactory/app.go:188`  
**Code Path**: TLS configuration with unconditional InsecureSkipVerify.

**Existing Guard**: None. `InsecureSkipVerify = true` is set unconditionally inside `queryServerState` with no loopback guard on `addr`.

**Verdict**: REAL DEFECT  
**Disposition**: FIX

**Planned Fix**: Add a loopback guard identical to the production agent code (lines 220–226 in `agent/internal/rcon/satisfactory.go`). The test must dial only localhost or loopback IPs; if any other target is provided, the dial must fail rather than disable verification.

---

## Alert #5: go/request-forgery

**Rule ID**: `go/request-forgery`  
**Site**: `agent/internal/mods/mods.go:405`  
**Code Path**: `h.client.Do(req)` inside `downloadTemp`.

**Existing Guard**:
```
Client is netguard.HTTPClient(2*time.Minute, netguard.IsPublic) (~line 758) 
with a CheckRedirect that caps at 5 hops and re-runs hostAllowed per hop. 
Callers validate scheme (http/https only) and hostAllowed(u.Hostname(), 
h.allowed) against the module's CRD allowlist before dialing.
```

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR attempted, dismissal EXPECTED

**Planned Refactor Attempt**: The guard uses a dial-time IP guard (netguard's IsPublic policy) and a hostname allowlist (module CRD allowlist). Refactoring will be attempted to make the allowlist check more explicit and locally visible at the call site. However, CodeQL's `go/request-forgery` query rarely accepts dial-time IP guards or hostname allowlists as barriers; this alert is expected to persist after refactoring attempts and likely to end in dismissal.

**Dismissal Justification**:
```
Client creation wraps the dial with netguard.HTTPClient (agent/internal/mods/mods.go:758), 
which enforces IsPublic policy at dial time. Before Do(), callers 
(install(), upload()) validate scheme ∈ {http, https} and call 
hostAllowed(u.Hostname(), h.allowed) against the module's CRD 
allowlist, rejecting private ranges. CheckRedirect re-validates 
hostAllowed per redirect hop, capped at 5 hops. The request is 
guarded by allowlist and netguard SSRF dial policy.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/5 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Client creation wraps the dial with netguard.HTTPClient (agent/internal/mods/mods.go:758), which enforces IsPublic policy at dial time. Before Do(), callers (install(), upload()) validate scheme ∈ {http, https} and call hostAllowed(u.Hostname(), h.allowed) against the module'"'"'s CRD allowlist, rejecting private ranges. CheckRedirect re-validates hostAllowed per redirect hop, capped at 5 hops. The request is guarded by allowlist and netguard SSRF dial policy.'
```

---

## Alert #6: go/request-forgery

**Rule ID**: `go/request-forgery`  
**Site**: `api/internal/ws/dialer.go:281`  
**Code Path**: `p.http.Do(upReq)` in `httpProxyLimit`.

**Existing Guard**:
```
Validates isDNS1123Label(ns) && isDNS1123Label(name) (lines 252–256) 
and builds the URL through a url.URL struct with a hardcoded https 
scheme and an in-cluster host from p.agentHost(name, ns). wsProxy 
(lines 162–215) does the identical check at 176–179.
```

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR attempted, dismissal EXPECTED

**Planned Refactor Attempt**: The guard uses hardcoded scheme and DNS1123Label hostname validation. Refactoring will be attempted to make this constraint more explicit in the URL construction. However, like Alert #5, CodeQL's `go/request-forgery` query rarely accepts hostname validation alone as a barrier; this alert is expected to persist after refactoring attempts and likely to end in dismissal.

**Dismissal Justification**:
```
URL is constructed via url.URL struct with hardcoded https scheme and 
host from p.agentHost(name, ns) (line 281). Both httpProxyLimit and its 
caller wsProxy validate DNS1123Label format on namespace and server name 
at lines 176–179 and 252–256, rejecting non-alphanumeric values and 
special characters. The host is computed from cluster-local DNS names, 
not user input, and the scheme is hardcoded, not parsed from untrusted 
data.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/6 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='URL is constructed via url.URL struct with hardcoded https scheme and host from p.agentHost(name, ns) (line 281). Both httpProxyLimit and its caller wsProxy validate DNS1123Label format on namespace and server name at lines 176–179 and 252–256, rejecting non-alphanumeric values and special characters. The host is computed from cluster-local DNS names, not user input, and the scheme is hardcoded, not parsed from untrusted data.'
```

---

## Alert #7: go/zipslip

**Rule ID**: `go/zipslip`  
**Site**: `agent/internal/mods/mods.go:508`  
**Code Path**: `unzipInto` function (lines 497–553).

**Existing Guard**:
```go
// Pre-check at line 511
if filepath.Join(dstClean, filepath.Clean(f.Name)) escapes dstClean { skip }

// Post-clean re-check at lines 527–532
if escapes dstClean { return errors.New("zip-slip attempt") }
```

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Replace the `continue`-on-escape branch with single-exit validation that errors on any escaping entry, so no tainted path flows on any code branch. This should make the validation explicit to CodeQL's data-flow analysis. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
Function unzipInto performs a pre-check at line 511: 
filepath.Join(dstClean, filepath.Clean(f.Name)) is tested against dstClean 
with HasPrefix; entries escaping the root are rejected with errors.New at 
lines 527–532. The guard covers both relative traversal (../../) and absolute 
paths in archive entries.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/7 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Function unzipInto performs a pre-check at line 511: filepath.Join(dstClean, filepath.Clean(f.Name)) is tested against dstClean with HasPrefix; entries escaping the root are rejected with errors.New at lines 527–532. The guard covers both relative traversal (../../) and absolute paths in archive entries.'
```

---

## Alert #8: go/path-injection

**Rule ID**: `go/path-injection`  
**Site**: `agent/internal/mods/mods.go:389`  
**Code Path**: `os.RemoveAll(target)` in `removeEntry`.

**Existing Guard**:
```go
// Lines 381–385: in-function guard in removeEntry
target := filepath.Join(dstClean, filepath.Clean(name))
if !strings.HasPrefix(target, dstClean+string(os.PathSeparator)) && target != dstClean {
  return fmt.Errorf("path escape attempt: %q", name)
}
```
Caller `remove()` also runs `safeName(name)` at line 586.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Migrate onto the single confinement helper specified in `contracts/path-confinement.md`. The validation and the use of the path will sit in the same function; the used path will be the helper's return value. This makes the control flow clearer to CodeQL's analysis. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
removeEntry validates the path at lines 381–385: filepath.Join, Clean, 
then HasPrefix against dstClean (root or a subdir). The caller remove() 
pre-validates with safeName() at line 586, rejecting paths with `..`, 
leading dots, separators, and control characters. The combination of 
caller validation and in-function HasPrefix guard confines the target 
path to the sandbox.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/8 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='removeEntry validates the path at lines 381–385: filepath.Join, Clean, then HasPrefix against dstClean (root or a subdir). The caller remove() pre-validates with safeName() at line 586, rejecting paths with `..`, leading dots, separators, and control characters. The combination of caller validation and in-function HasPrefix guard confines the target path to the sandbox.'
```

---

## Alert #9: go/path-injection

**Rule ID**: `go/path-injection`  
**Site**: `agent/internal/mods/mods.go:391`  
**Code Path**: `os.Remove(target)` in `removeEntry`, same guards as Alert #8.

**Existing Guard**: Same as Alert #8.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Same as Alert #8 — migrate onto the single confinement helper. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
Same guard as Alert #8: removeEntry validates at lines 381–385 with 
filepath.Join, Clean, and HasPrefix. Caller remove() pre-validates with 
safeName() at line 586.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/9 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Same guard as Alert #8: removeEntry validates at lines 381–385 with filepath.Join, Clean, and HasPrefix. Caller remove() pre-validates with safeName() at line 586.'
```

---

## Alert #10: go/path-injection

**Rule ID**: `go/path-injection`  
**Site**: `agent/internal/mods/mods.go:446`  
**Code Path**: `os.Rename(tmpName, filepath.Join(h.dir, name))` in `download()`.

**Existing Guard**:
```
Guard is in the CALLER, not the function. Both callers (install() at line 196 
and upload() at line 306) run safeName(name) before calling download().
```

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Migrate onto the single confinement helper. The existing guard is split between caller and callee; moving validation into `download()` itself using the centralized helper makes the safety transparent. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
Guard is in the caller: both install() (line 196) and upload() (line 306) 
call safeName(name) before invoking download(), rejecting paths with `..`, 
leading dots, separators, control characters, and length > 200. 
Consolidated path confinement will move this guard into download() itself.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/10 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Guard is in the caller: both install() (line 196) and upload() (line 306) call safeName(name) before invoking download(), rejecting paths with `..`, leading dots, separators, control characters, and length > 200. Consolidated path confinement will move this guard into download() itself.'
```

---

## Alert #11: go/path-injection

**Rule ID**: `go/path-injection`  
**Site**: `agent/internal/mods/mods.go:486`  
**Code Path**: `os.RemoveAll(final)` in `swapInArchive`.

**Existing Guard**:
```go
// Lines 478–482: Clean+Join+Clean+HasPrefix guard
final := filepath.Join(h.dir, archiveFolderName(safeName(...)))
```
Caller passes `archiveFolderName(safeName(...))`.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Migrate onto the single confinement helper. The current guard is a composition of `safeName()` and `archiveFolderName()` in the caller; using the centralized helper makes the pattern uniform across all path operations. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
swapInArchive validates at lines 478–482 with filepath.Join, Clean on the 
result, then HasPrefix check at line 482. Caller wraps safeName() with 
archiveFolderName(), both of which reject dangerous path components. The 
combination confines the staging folder to the sandbox.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/11 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='swapInArchive validates at lines 478–482 with filepath.Join, Clean on the result, then HasPrefix check at line 482. Caller wraps safeName() with archiveFolderName(), both of which reject dangerous path components. The combination confines the staging folder to the sandbox.'
```

---

## Alert #12: go/path-injection

**Rule ID**: `go/path-injection`  
**Site**: `agent/internal/mods/mods.go:490`  
**Code Path**: `os.Rename(staging, final)` in `swapInArchive`, same guards as Alert #11, after the check at line 482.

**Existing Guard**: Same as Alert #11.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Same as Alert #11 — migrate onto the single confinement helper. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
Same as Alert #11: swapInArchive validates at lines 478–482 with 
filepath.Join, Clean, and HasPrefix. Caller pre-validates with 
safeName() and archiveFolderName().
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/12 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Same as Alert #11: swapInArchive validates at lines 478–482 with filepath.Join, Clean, and HasPrefix. Caller pre-validates with safeName() and archiveFolderName().'
```

---

## Alert #13: go/path-injection

**Rule ID**: `go/path-injection`  
**Site**: `agent/internal/mods/mods.go:594`  
**Code Path**: `os.Stat(target)` in `remove()`.

**Existing Guard**:
```go
// Lines 591–593: in-function guard
target := filepath.Join(h.dir, filepath.Clean(name))
if !strings.HasPrefix(target, h.dir+string(os.PathSeparator)) && target != h.dir {
  return fmt.Errorf("path escape attempt")
}
// Line 586: caller validation
safeName(name)
```
Guard is in the SAME function.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Migrate onto the single confinement helper. The in-function guard can be replaced with the centralized helper, consolidating all path-safety checks in one place. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
remove() validates at lines 591–593 with filepath.Join, Clean, and 
HasPrefix against the sandbox root. Validation occurs in the same 
function before os.Stat(target), guarding against traversal and 
absolute paths. safeName() at line 586 provides defense-in-depth.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/13 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='remove() validates at lines 591–593 with filepath.Join, Clean, and HasPrefix against the sandbox root. Validation occurs in the same function before os.Stat(target), guarding against traversal and absolute paths. safeName() at line 586 provides defense-in-depth.'
```

---

## Alert #14: go/uncontrolled-allocation-size

**Rule ID**: `go/uncontrolled-allocation-size`  
**Site**: `api/internal/audit/audit.go:834`  
**Code Path**: `out := make([]Event, 0, limit)` inside `(*Auditor).Page`.

**Existing Guard**:
```go
// Lines 820–822: in-function clamping guard
if limit <= 0 || limit > 500 {
  limit = 100
}
```
Handler `api/internal/handlers/audit.go:25` parses the raw value with `strconv.Atoi` but does not clamp.

**Verdict**: FALSE POSITIVE  
**Disposition**: REFACTOR first, DISMISS contingent

**Planned Refactor**: Clamp the untrusted value at the handler layer as well (`api/internal/handlers/audit.go:25`), so the allocation bound is enforced before the value reaches the store layer. Introduce a named constant for the 500 maximum and clamp to a new bounded variable rather than reassigning the parameter. This makes the control flow and trust boundaries explicit. Dismissal is the fallback only if a post-merge CodeQL analysis still reports this alert.

**Dismissal Justification**:
```
Auditor.Page clamps the limit at lines 820–822, replacing any value 
<= 0 or > 500 with a safe default of 100 before the allocation at line 834. 
The handler does not clamp, but the method itself is the trust boundary and 
enforces the limit regardless of caller input. Capacity is bounded to 500 
entries maximum.
```

**API Call**:
```bash
gh api -X PATCH /repos/GameplanePanel/Gameplane/code-scanning/alerts/14 \
  -f state=dismissed -f dismissed_reason=false positive \
  -f dismissed_comment='Auditor.Page clamps the limit at lines 820–822, replacing any value <= 0 or > 500 with a safe default of 100 before the allocation at line 834. The handler does not clamp, but the method itself is the trust boundary and enforces the limit regardless of caller input. Capacity is bounded to 500 entries maximum.'
```

---

## Summary

| Alert | Rule | Site | Verdict | Disposition |
|-------|------|------|---------|-------------|
| 1 | clear-text-logging | api/kube/watch.go:40 | False Positive | Refactor first, Dismiss contingent |
| 2 | clear-text-logging | api/kube/watch.go:54 | False Positive | Refactor first, Dismiss contingent |
| 3 | disabled-cert-check | agent/rcon/satisfactory.go:199 | False Positive | DISMISSED 2026-08-29 |
| 4 | disabled-cert-check | test/e2e/.../satisfactory/app.go:188 | Real Defect | Fix |
| 5 | request-forgery | agent/mods/mods.go:405 | False Positive | Refactor attempted, Dismiss expected |
| 6 | request-forgery | api/ws/dialer.go:281 | False Positive | Refactor attempted, Dismiss expected |
| 7 | zipslip | agent/mods/mods.go:508 | False Positive | Refactor first, Dismiss contingent |
| 8 | path-injection | agent/mods/mods.go:389 | False Positive | Refactor first, Dismiss contingent |
| 9 | path-injection | agent/mods/mods.go:391 | False Positive | Refactor first, Dismiss contingent |
| 10 | path-injection | agent/mods/mods.go:446 | False Positive | Refactor first, Dismiss contingent |
| 11 | path-injection | agent/mods/mods.go:486 | False Positive | Refactor first, Dismiss contingent |
| 12 | path-injection | agent/mods/mods.go:490 | False Positive | Refactor first, Dismiss contingent |
| 13 | path-injection | agent/mods/mods.go:594 | False Positive | Refactor first, Dismiss contingent |
| 14 | uncontrolled-alloc | api/audit/audit.go:834 | False Positive | Refactor first, Dismiss contingent |

**Total**: 1 planned dismissal (alert #3, verified unclearable), 2 expected dismissals (alerts #5–#6, post-refactor), 9 contingent dismissals (alerts #1–#2, #7–#14, refactor-first policy), 1 real fix (alert #4). **Latent gap:** `agent/internal/rcon/websocket.go` (~line 292, `ensureLocked`) dials `ws://<baseURL>/<escapedPw>` with no netguard policy; flagged as defence-in-depth rather than an active vulnerability.

---

## Re-triage: 4 new alerts on PR #285 (2026-08-29)

**Summary**: The Phase A refactoring strategy — consolidating path validation into single-exit ConfinePath/ConfineRelPath helpers and attempting to make SSRF guards "explicit to CodeQL's taint analysis" — has NOT cleared alerts #5, #7, #10, and #14. Instead, code changes have caused line-number shifts, and CodeQL now reports four RELOCATED instances of the same vulnerabilities at new line numbers. One is a REAL BUG; three are FALSE POSITIVES with fixable root causes.

### New Alert 1: mods.go:426 (go/request-forgery) — RELOCATED from #5, ROOT CAUSE FOUND

**CodeQL Report**:
```
Title: "Uncontrolled data used in network request"
Message: "The [URL](1) of this request depends on a [user-provided value](2)."
Location: agent/internal/mods/mods.go:426 (h.client.Do(req))
```

**Enclosing Function**: `downloadTemp` (lines 403–459), same as original alert #5.

**Runtime Guard**:
```go
u, err := url.Parse(rawURL)                           // Line 412
if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
  if err != nil {
    return "", 0, fmt.Errorf("parse url: %w", err)
  }
  return "", 0, errors.New("invalid url scheme or host")
}
if !hostAllowed(u.Hostname(), h.allowed) {           // Line 419
  return "", 0, errHostNotAllowed
}
req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)  // Line 422
resp, err := h.client.Do(req)                        // Line 426
```

**Verdict**: REAL BUG — Taint is not cleared.

**Why the Code is NOT Safe**: The guard validates the URL through parsing and hostname checking, but continues to use the ORIGINAL `rawURL` parameter in `http.NewRequestWithContext(line 422). While `url.Parse` confirms the scheme is http/https and the host passes `hostAllowed`, CodeQL's taint analysis tracks the parameter `rawURL` as tainted and does not recognize that validation on a *parsed* version (`u`) clears the taint on the *original string*. The code validates the URL but does not produce or use a "sanitized" return value. For CodeQL to recognize the guard, the code must reconstruct a URL string from the validated parsed version:

```go
// Guard must return/use a taint-cleared string, not validate-in-place
u, err := url.Parse(rawURL)
if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
  return "", 0, errors.New("invalid url")
}
if !hostAllowed(u.Hostname(), h.allowed) {
  return "", 0, errHostNotAllowed
}
sanitizedURL := u.String()  // <-- Reconstruct from validated parsed version
req, err := http.NewRequestWithContext(ctx, http.MethodGet, sanitizedURL, nil)
```

**Recommended Disposition**: RESTRUCTURE

**Concrete Restructure Plan**:
Replace line 422 with a sanitized URL constructed from the validated parsed version. Extract `u.String()` and use it as the request URL instead of continuing to use the original `rawURL` parameter. This makes CodeQL's taint tracking visible: the new string comes from the validated parsed object `u`, not from the untrusted parameter.

---

### New Alert 2: mods.go:475 (go/path-injection) — RELOCATED from #10

**CodeQL Report**:
```
Title: "Uncontrolled data used in path expression"
Message: "This path depends on a [user-provided value](1)."
Location: agent/internal/mods/mods.go:475 (os.Rename(tmpName, finalPath))
```

**Enclosing Function**: `download` (lines 462–480), same as original alert #10.

**Runtime Guard**:
```go
finalPath, err := ConfinePath(h.dir, name)  // Line 470
if err != nil {
  _ = os.Remove(tmpName)
  return 0, err
}
if err := os.Rename(tmpName, finalPath); err != nil {  // Line 475 — uses confined finalPath
```

**Verdict**: FALSE POSITIVE — Guard result IS used.

**Why the Code IS Safe**: `ConfinePath` validates the untrusted `name` parameter and returns a confined absolute path (`finalPath`) or an error. Line 475 uses the *returned* `finalPath` from the guard, not the original tainted `name`. The guard result is the operand to `os.Rename`. However, CodeQL may not recognize `ConfinePath` as a sufficient guard because it is not a built-in function and its implementation is not obvious to CodeQL's taint-analysis rules without deep inter-procedural analysis.

**Recommended Disposition**: DISMISS

**Dismissal Justification**:
```
download() uses ConfinePath(h.dir, name) at line 470 to validate and 
resolve the destination path. ConfinePath (agent/internal/mods/confinement.go) 
validates that the component does not contain path traversal (../ or /), 
separators, or control characters, and returns a cleaned absolute path 
confined within the sandbox root or an error. Line 475 uses the returned 
`finalPath` (the guard result) directly in os.Rename, not the original 
tainted `name` parameter. The code is safe; the issue is CodeQL's lack of 
inter-procedural visibility into ConfinePath's confinement guarantee.
```

---

### New Alert 3: mods.go:544–589 (go/zipslip) — RELOCATED from #7

**CodeQL Report**:
```
Title: "Arbitrary file access during archive extraction (\"Zip Slip\")"
Message: "Unsanitized archive entry, which may contain '..', is used in 
a [file system operation](1–3)."
Location: agent/internal/mods/mods.go:544–589 (unzipInto loop)
```

**Enclosing Function**: `unzipInto` (lines 535–591), same as original alert #7.

**Runtime Guard**:
```go
for _, f := range zr.File {                           // Line 544
  target, confineErr := ConfineRelPath(dstClean, f.Name)  // Line 549
  if confineErr != nil {
    return fmt.Errorf("zip-slip: %w", confineErr)    // Line 551
  }
  // Re-check inline so the guard is visible at the point of use.
  target = filepath.Clean(target)                    // Line 554 — re-cleans
  if target != dstClean && !strings.HasPrefix(target, dstClean+string(os.PathSeparator)) {
    return fmt.Errorf("zip-slip: %w", ErrEscapesRoot)  // Line 556
  }
  if f.FileInfo().IsDir() {
    if err := os.MkdirAll(target, 0o750); err != nil {  // Line 559 — uses confined target
  } else {
    out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, moduleFileMode)  // Line 571
```

**Verdict**: FALSE POSITIVE — Guard result IS used, but inline re-cleaning may confuse analysis.

**Why the Code IS Safe**: `ConfineRelPath` (lines 158–250 of confinement.go) validates the untrusted archive entry name against multi-segment path rules, resolves symlinks, and returns a confined absolute path or an error. Line 549 uses the guard result. Lines 554–557 re-check the confined path with `filepath.Clean` and a HasPrefix guard; this is redundant but safe (Clean normalizes paths without un-confining them). Lines 559 and 571 use the confined `target`, not the original tainted `f.Name`. The issue is likely that CodeQL does not recognize `ConfineRelPath` as a taint-clearing guard, combined with confusion from the re-application of `filepath.Clean` at line 554.

**Recommended Disposition**: RESTRUCTURE or DISMISS

**Concrete Restructure Plan** (Simpler Fix):
Remove the redundant re-check at lines 554–557. The guard at line 549 already validates and confines the path with explicit error handling. The re-check was added for "visibility at the point of use" but may actually confuse CodeQL by reintroducing analysis uncertainty on the already-confined path:

```go
target, confineErr := ConfineRelPath(dstClean, f.Name)
if confineErr != nil {
  return fmt.Errorf("zip-slip: %w", confineErr)
}
// No re-check needed; ConfineRelPath returns a confined path or errors
if f.FileInfo().IsDir() {
  if err := os.MkdirAll(target, 0o750); err != nil {
```

**Alternative Dismissal Justification** (if restructure is not done):
```
unzipInto uses ConfineRelPath(dstClean, f.Name) at line 549 to validate 
and confine each archive entry. ConfineRelPath validates multi-segment 
paths, rejects traversal (..) and absolute paths, resolves symlinks, and 
returns a confined absolute path or an error (lines 158–250, 
confinement.go). Lines 559–589 use the returned `target` in mkdir/stat/open 
operations. The re-check at lines 554–557 is redundant but not incorrect. 
Archive extraction is guarded; CodeQL's issue is lack of inter-procedural 
visibility into ConfineRelPath.
```

---

### New Alert 4: audit.go:844 (go/uncontrolled-allocation-size) — RELOCATED from #14

**CodeQL Report**:
```
Title: "Slice memory allocation with excessive size value"
Message: "This memory allocation depends on a [user-provided value](1)."
Location: api/internal/audit/audit.go:844 (out := make([]Event, 0, pageSize))
```

**Enclosing Function**: `(*Auditor).Page` (lines 827–853), same as original alert #14.

**Runtime Guard**:
```go
func (a *Auditor) Page(req *http.Request, limit int, before int64) ([]Event, error) {
  // Clamp the limit into a new variable so taint analysis recognizes it as bounded.
  pageSize := limit                                    // Line 829
  if pageSize <= 0 || pageSize > MaxAuditPageSize {   // Line 830
    pageSize = DefaultAuditPageSize                    // Line 831
  }
  // ... query at lines 833–839 ...
  out := make([]Event, 0, pageSize)                   // Line 844 — uses clamped pageSize
```

**Verdict**: FALSE POSITIVE — Allocation is bounded.

**Why the Code IS Safe**: `pageSize` is copied from the untrusted input `limit` at line 829 and then immediately clamped at lines 830–832. Any value ≤ 0 or > MaxAuditPageSize (500) is replaced with `DefaultAuditPageSize` (100). The allocation at line 844 uses the clamped `pageSize`, ensuring the slice capacity cannot exceed 500 elements. The refactor from the original code (which reassigned the parameter `limit` directly) to creating a separate `pageSize` variable was intended to make this taint-clearing explicit to CodeQL, but CodeQL may still treat the allocation as depending on the untrusted parameter if it does not recognize the intermediate variable assignment as a taint barrier.

**Recommended Disposition**: RESTRUCTURE (lightweight) or DISMISS

**Concrete Restructure Plan** (Micro-Optimization for CodeQL Clarity):
Introduce an explicit constant name and reduce the clamping logic to a single assignment:

```go
const AuditPageSizeLimit = 500
const AuditPageSizeDefault = 100

// Taint is cleared by the ternary; if-clamping may confuse analysis
func (a *Auditor) Page(req *http.Request, limit int, before int64) ([]Event, error) {
  pageSize := limit
  if pageSize <= 0 || pageSize > AuditPageSizeLimit {
    pageSize = AuditPageSizeDefault
  }
  out := make([]Event, 0, pageSize)
```

This is already the current state. Alternative: Use a math.Min helper to make the operation more explicit (though this does not improve the situation).

**Alternative Dismissal Justification** (if no restructure):
```
Auditor.Page clamps the limit at lines 830–832, assigning DefaultAuditPageSize 
(100) to any value outside [1, MaxAuditPageSize (500)]. The allocation at 
line 844 uses the clamped pageSize, not the untrusted parameter. The handler 
layer (api/internal/handlers/audit.go) does not clamp, but the method 
itself is the trust boundary and enforces the limit regardless of caller 
input. Capacity is bounded to 500 entries maximum.
```

---

## Synthesis and Recommendations

**Key Finding**: The Phase A refactoring strategy has NOT cleared these four alerts. All four are **relocated instances** of the original #5, #7, #10, and #14 findings, caused by code reorganization (c3cd0f1 "refactor(agent): route mod path operations through the confinement helpers"). The root causes are:

1. **Alert 1 (mods.go:426, request-forgery)**: REAL BUG. The guard validates a parsed URL but continues to use the original tainted parameter string. Requires reconstruction of the URL from the validated parsed version.

2. **Alert 2 (mods.go:475, path-injection)**: FALSE POSITIVE. ConfinePath guard result IS used; CodeQL lacks inter-procedural visibility.

3. **Alert 3 (mods.go:544–589, zipslip)**: FALSE POSITIVE. ConfineRelPath guard result IS used; CodeQL lacks inter-procedural visibility; optional: remove redundant re-check to improve clarity.

4. **Alert 4 (audit.go:844, uncontrolled-allocation-size)**: FALSE POSITIVE. Allocation is bounded after clamping; CodeQL may not recognize the intermediate variable as a taint barrier.

**Action Plan**:
- **Alert 1**: RESTRUCTURE (required). Modify `downloadTemp` to use `u.String()` instead of `rawURL` in the HTTP request.
- **Alerts 2 & 4**: DISMISS (false positives with clear justifications).
- **Alert 3**: RESTRUCTURE (optional, improves clarity) to remove redundant re-check, or DISMISS.

| Alert | Location | Rule | Verdict | Disposition | Severity |
|-------|----------|------|---------|-------------|----------|
| New 1 | mods.go:426 | request-forgery | Real bug | Restructure | Critical |
| New 2 | mods.go:475 | path-injection | False positive | Dismiss | N/A |
| New 3 | mods.go:544–589 | zipslip | False positive | Dismiss or restructure | N/A |
| New 4 | audit.go:844 | uncontrolled-alloc | False positive | Dismiss | N/A |

---

## T066 — re-triage of the 4 CodeQL alerts on PR #285 (2026-08-29)

**Question**: are the 4 alerts CodeQL raises against PR #285 genuinely new defects, or the
Phase A originals relocated by the refactor?

**Answer: relocated, not new.** Each maps 1:1 onto an original alert, displaced only by the
line-count change the refactor introduced.

| PR #285 annotation | Rule | Original |
|---|---|---|
| `agent/internal/mods/mods.go:431` | Uncontrolled data used in network request | #5 (`:405` on master) |
| `agent/internal/mods/mods.go:480` | Uncontrolled data used in path expression | #10 (`:446`) |
| `agent/internal/mods/mods.go:549` | Zip Slip | #7 (`:508`) |
| `api/internal/audit/audit.go:844` | Slice allocation with excessive size | #14 (`:834`) |

Read from check run 99097398101's annotations. No fifth finding appeared, and no rule id
changed — so the refactor neither introduced nor removed a defect class.

### The refactor attempt, and its outcome

Per the maintainer decision recorded in HANDOFF.md §3.1 ("try one more refactor first"), one
further change was made and pushed as `7294db7`: `downloadTemp` now builds its request from
the validated `*url.URL` rather than the raw string —

```go
req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
```

**It did not clear the alert.** CodeQL re-analysed `7294db7` and reported the same
`go/request-forgery` finding, merely shifted from `:426` to `:431` by the five comment lines
the change added. This is direct evidence, not inference: the alert moved by exactly the
number of lines inserted above it.

The refactor is nonetheless kept. It is behaviourally identical (`http.NewRequestWithContext`
re-parses `rawURL` to the same URL) and it is strictly better hygiene for the validated value
to be the one that flows into the request.

### Why the remaining three were not restructured

Only `go/request-forgery` ever had an identified refactor candidate. For path-injection,
zipslip and uncontrolled-allocation there is **no hypothesis** for what shape would satisfy
the analyzer. Speculatively restructuring archive-extraction and path-confinement code — the
agent's actual sandbox boundary — without one is churn carrying real regression risk, on code
whose correctness is what stops a mod archive escaping its volume. Not attempted, deliberately.

### Disposition

Per the maintainer's pre-agreed fallback (HANDOFF.md §3.1: *"If it does not clear the alerts,
dismiss via the code-scanning API"*), all four are dismissed as false positives once PR #285
lands on master, using the justifications already drafted in this file for alerts #5, #7, #10
and #14. The guards each dismissal cites were re-verified against the current source, not
taken from the earlier draft.

Constitution Principle III is not implicated: dismissal is repository metadata carrying an
audit trail, not an in-source suppression. The branch contains **zero** `//nolint`,
`#nosec`, `eslint-disable` or `@ts-ignore` directives — verified by
`git diff master...009-remediate-security-dependabot -- '*.go' '*.ts' '*.tsx' | grep -E ...`,
which returns 0 matches (SC-005).

**Sequencing note**: CodeQL only re-analyses the default branch, so alerts #1–#14 cannot
reach `fixed` until #285 merges. The re-query (T045) and the dismissals (T047) therefore run
strictly after the merge (T062), never before.

---

## FINAL STATE — recorded 2026-08-29 after PR #285 merged to master (T053)

Master at `e1a9d2c`. CodeQL re-analysed the default branch; the result is
**0 open alerts — 7 fixed, 12 dismissed.** SC-001 is met.

### Two corrections this file needs

**1. `dismissed_comment` is capped at 280 characters.** Every justification drafted above is
500–755 characters, so *none* of them could be submitted as written — the API rejects them
with HTTP 422 (`Only 280 characters are allowed`). The comments actually submitted are the
condensed forms below. The long-form text above remains the durable rationale; this file, not
the GitHub comment field, is the record of record.

**2. The remediation renumbered the alerts.** Refactoring moved the flagged code, so CodeQL
closed the originals as `fixed` and opened *new alert numbers* at the new lines rather than
tracking them. The "4 alerts" the PR check reported was only the subset introduced by that
PR's own diff; master carried **11** open once analysis completed. Any future re-triage must
query master, not the PR check.

### Alert-by-alert outcome

| # | Rule | Location | Outcome |
|---|---|---|---|
| 1 | go/clear-text-logging | api/internal/kube/watch.go:40 | **fixed** |
| 2 | go/clear-text-logging | api/internal/kube/watch.go:54 | **fixed** |
| 3 | go/disabled-certificate-check | agent/internal/rcon/satisfactory.go:199 | dismissed 2026-08-29 (earlier) |
| 4 | go/disabled-certificate-check | test/e2e/internal/satisfactory/app.go:218 | dismissed 2026-08-29 |
| 5 | go/request-forgery | agent/internal/mods/mods.go:431 | dismissed 2026-08-29 |
| 6 | go/request-forgery | api/internal/ws/dialer.go:297 | dismissed 2026-08-29 |
| 7 | go/zipslip | agent/internal/mods/mods.go:508 | **fixed** (reopened as #16) |
| 8 | go/path-injection | agent/internal/mods/mods.go:389 | **fixed** (reopened as #19) |
| 9 | go/path-injection | agent/internal/mods/mods.go:391 | **fixed** (reopened as #20) |
| 10 | go/path-injection | agent/internal/mods/mods.go:446 | **fixed** (reopened as #17) |
| 11 | go/path-injection | agent/internal/mods/mods.go:527 | dismissed 2026-08-29 |
| 12 | go/path-injection | agent/internal/mods/mods.go:531 | dismissed 2026-08-29 |
| 13 | go/path-injection | agent/internal/mods/mods.go:649 | dismissed 2026-08-29 |
| 14 | go/uncontrolled-allocation-size | api/internal/audit/audit.go:834 | **fixed** (reopened as #18) |
| 16 | go/zipslip | agent/internal/mods/mods.go:549 | dismissed 2026-08-29 |
| 17 | go/path-injection | agent/internal/mods/mods.go:480 | dismissed 2026-08-29 |
| 18 | go/uncontrolled-allocation-size | api/internal/audit/audit.go:844 | dismissed 2026-08-29 |
| 19 | go/path-injection | agent/internal/mods/mods.go:395 | dismissed 2026-08-29 |
| 20 | go/path-injection | agent/internal/mods/mods.go:397 | dismissed 2026-08-29 |

Alert #15 does not exist in the repository's alert sequence.

Note the pattern in #7/#8/#9/#10/#14: each shows `fixed` **only because its code moved**, with
the identical finding reopening at the new line. Reading those five as genuine fixes would
overstate the remediation. The seven true fixes are #1, #2 (logging) plus those five
relocations' predecessors; the substantive wins are the clear-text-logging pair, which CodeQL
now agrees are clean.

### Comments as actually submitted (each verified <= 280 chars, guards re-verified against
### master before submission — the line numbers in the long-form drafts above had gone stale)

- **#4** — Loopback-only gate rejects non-loopback hosts before the TLS config is built (app.go:198-199). Self-signed cert, pod-local, e2e harness only. Same disposition as #3.
- **#5** — Scheme+host validated on the parsed URL (mods.go:412-422); request built from it (:428). netguard.IsPublic runs on the RESOLVED IP at dial time (:806, :812-823), defeating DNS rebinding. Redirects re-check the allowlist (:814-821).
- **#6** — URL built via url.URL with hardcoded https scheme and host from agentHost() (dialer.go:274-281); namespace and pod name validated as DNS-1123 labels.
- **#11 / #12** — swapInArchive resolves via ConfinePath (mods.go:515-518), re-checks Clean + HasPrefix against the cleaned root (:520-526) before the RemoveAll/Rename.
- **#13** — Handler resolves via ConfinePath, re-checks Clean + HasPrefix, returns HTTP 400 before the os.Stat (mods.go:642-648); name pre-validated by safeName at :629.
- **#16** — unzipInto gates every entry through ConfineRelPath (mods.go:554-557) and re-checks Clean + HasPrefix (:559-562) before any write.
- **#17** — download resolves with ConfinePath(h.dir, name) (mods.go:475) before the rename (:480).
- **#18** — limit clamped to DefaultAuditPageSize (100) when <=0 or >MaxAuditPageSize (500) at audit.go:828-831; allocation at :844 uses the clamped value.
- **#19 / #20** — removeEntry resolves via ConfinePath (mods.go:384-387), re-checks Clean + HasPrefix (:389-394) before the RemoveAll/Remove.

**Principle III holds**: no in-source suppression was added. Verified on master —
`git diff` over `*.go`/`*.ts`/`*.tsx` for this feature returns 0 matches for
`//nolint`, `// eslint-disable` and `// @ts-ignore` (SC-005).

---

## SUPERSEDING NOTE — recorded 2026-08-30 after PR #290 merged to master

**This section supersedes the "0 open alerts" assertion above.** The "FINAL STATE" section was
written as of 2026-08-29 and is now stale.

### Alert #21: Reopened alert #12 after line-shift renumbering

On 2026-08-30, CodeQL raised alert #21 (`go/path-injection`, `agent/internal/mods/mods.go:531`)
— the same finding that was dismissed as #12 on 2026-08-29. **This is not a new vulnerability.**

**Root cause**: PR #290 (T078, symlink rejection in `swapInArchive`) modified
`agent/internal/mods/mods.go` and shifted line numbers. CodeQL's dismissal for alert #12
binds to the alert **number**, not the code **location**. When lines shifted, the original
code-at-line:531 (the `os.Rename` call) was no longer trackable as "#12" — CodeQL re-analysed
the default branch and raised the same finding as a fresh alert #21.

This is the fourth instance of this renumbering pattern: #7/#8/#9/#10/#14 were marked `fixed`
and reopened as #16–#20 during the initial remediation (T045/T062); #12 now reopens as #21
after the follow-up edit.

**Disposition of #21** (T080, 2026-08-30):

- **State**: dismissed
- **Reason**: false positive  
- **Comment submitted**: "swapInArchive resolves via ConfinePath and returns on failure
  (mods.go:515-518), then re-checks filepath.Clean + HasPrefix against the cleaned root
  (:520-526) before this rename. Folder name pre-validated by safeName. See
  contracts/alert-disposition.md." (249 characters)

**Corrected final state**: Master at `ad3e30a` (after PR #290 merged). CodeQL re-analysed and
raised #21; it was dismissed the same day. **0 open alerts — 7 fixed, 13 dismissed.** SC-001
remains met.

### The recurring cost: CodeQL dismissal binds to alert number, not code location

A dismissed CodeQL alert is recorded by *alert number* in the GitHub database. The dismissal
does NOT transfer if the code moves. Future edits in `agent/internal/mods/mods.go` that shift
line numbers will re-raise the same false positives under fresh alert numbers — each requiring
re-dismissal via the GitHub UI, with re-submission of the 280-character justification.

For any future maintainer: when editing `agent/internal/mods/mods.go`, expect that dismissals
#5, #11, #12 (#21), #13, #16, #17, #19, #20 may re-raise if lines shift. The long-form
rationale for each is recorded in the comments section above; the 280-character cap limits what
can be re-submitted. Re-dismissals add no new insight to the audit trail but are an unavoidable
tax on the file's operational footprint.

