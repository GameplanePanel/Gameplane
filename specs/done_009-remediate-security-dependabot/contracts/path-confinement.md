# Path Confinement Helper Contract

## Two-Function Design

This contract specifies two complementary functions for path confinement, not one.

- **`ConfinePath(root, component)`**: validates a **single path component** (e.g., a filename). Used by operations that work on individual names (remove, download, single file operations).
- **`ConfineRelPath(root, relPath)`**: validates a **multi-segment relative subtree path** (e.g., archive entry names). Used by operations that extract or scan archive entries, which legitimately contain nested paths.

**Why the split?** During implementation, applying single-component rules to archive extraction rejected valid entries like `config/settings.json` and `.gitkeep` because they contain separators or leading dots. Archive entry names are relative paths, not single components, so they need a separate validator. Using the wrong function would break mod installation.

---

## Function 1: ConfinePath (Single Component)

### Signature

```go
// ConfinePath validates an untrusted single path component against a root directory 
// and returns a safe absolute path confined within the root, or an error if the 
// component violates confinement rules.
//
// root: absolute path to a trusted root directory (e.g., /var/mods)
// component: untrusted single path name, with no separators (e.g., "mymod", not "my/mod")
// 
// Returns: a cleaned, absolute path confined within root, or an error if validation fails.
// The returned value must be used as-is; do not re-derive the path from component.
func ConfinePath(root, component string) (string, error)
```

---

## Contract Guarantees

### Postcondition (always true when error is nil)

1. **Returned path is cleaned**: The returned value satisfies `filepath.Clean(result) == result` — no redundant separators, no `.` or `..` elements.

2. **Returned path is confined**: One of the following holds:
   - `result == root` (component resolved to the root itself)
   - `strings.HasPrefix(result, root + string(os.PathSeparator))` (component resolved strictly under root with separator)

   This ensures the resolved path cannot escape the sandbox, even after symlink resolution.

3. **Both root and target are resolved**: If the returned path exists on disk and contains symlinks, those symlinks have been resolved. The deepest existing ancestor has also been resolved. Both the final target and all symlinks in its path remain inside root.

---

## Validation Rules (Rejection Table)

A call to `ConfinePath(root, component)` MUST return an error if any of the following is true:

| Condition | Reason | Example |
|-----------|--------|---------|
| `component == ""` | Empty path is meaningless | `ConfinePath("/var/mods", "") → error` |
| `component == "."` | Resolves to root; call `ConfinePath(root, "")` or handle explicitly | `ConfinePath("/var/mods", ".") → error` |
| `component == ".."` | Attempts traversal | `ConfinePath("/var/mods", "..") → error` |
| `strings.HasPrefix(component, "../")` or `component == ".."` | Traversal prefix | `ConfinePath("/var/mods", "../../../etc") → error` |
| `strings.HasPrefix(component, "/")` | Absolute path; use root instead | `ConfinePath("/var/mods", "/etc/passwd") → error` |
| `strings.Contains(component, string(os.PathSeparator))` | Separators indicate directory traversal; component MUST be a single path element | `ConfinePath("/var/mods", "subdir/file") → error` |
| `strings.HasPrefix(component, ".")` | Leading dot; component begins with `.` but is not exactly `"."` or `".."` | `ConfinePath("/var/mods", ".hidden") → error` |
| `len(component) > 200` | Length limit prevents excessively long path attacks | `ConfinePath("/var/mods", strings.Repeat("a", 201)) → error` |
| Contains control characters (U+0000–U+001F, U+007F) | Control chars in paths are always dangerous | `ConfinePath("/var/mods", "file\x00.txt") → error` |
| Contains backslash `\` | Windows path separator; normalize to forward slash or reject | `ConfinePath("/var/mods", "file\\dir") → error` |
| `filepath.Join(root, component)` escapes root after `Clean()` | The joined path, after cleaning, does not fall within root | (see symlink handling below) |
| Symlink target resolves outside root | The symlink, after resolution, points outside the sandbox | `ConfinePath("/var/mods", "link") → error` when `link → /etc/passwd` |
| Deepest existing ancestor is outside root | A symlink component in an ancestor path points outside root | `ConfinePath("/var/mods/foo/bar", "baz")` where `/var/mods/foo` is a symlink to `/tmp` |

---

## Symlink Handling (Critical)

When `component` or any ancestor directory is a symlink:

1. **Resolve the target**: Call `filepath.EvalSymlinks()` or equivalent to resolve the symlink to its final target.
2. **Check the final target**: After resolution, verify that the target path satisfies the postcondition (confined within root).
3. **Check intermediate ancestors**: Any ancestor directory that is a symlink MUST also resolve to a path inside root.
4. **Reject if escape is detected**: If a symlink escapes root (either directly or via its target), return an error immediately.

**Example**:
```
root = "/var/mods"
/var/mods/config is a symlink to /etc/config
component = "config/password"

After filepath.Join: /var/mods/config/password
After Clean: /var/mods/config/password
After EvalSymlinks on ancestor: /var/mods/config → /etc/config
Result: /etc/config/password is outside /var/mods → ERROR
```

---

## Function 2: ConfineRelPath (Multi-Segment Relative Path)

### Signature

```go
// ConfineRelPath validates an untrusted relative path against a root directory and 
// returns a safe absolute path confined within the root, or an error if the path 
// violates confinement rules.
//
// root: absolute path to a trusted root directory (e.g., /var/mods)
// relPath: untrusted relative path, potentially multi-segment (e.g., "config/settings.json", ".gitkeep")
// 
// Returns: a cleaned, absolute path confined within root, or an error if validation fails.
// The returned value must be used as-is; do not re-derive the path from relPath.
//
// Intended use: validating archive entry names during extraction (zip, tar, etc.).
func ConfineRelPath(root, relPath string) (string, error)
```

### Postcondition (always true when error is nil)

Same as `ConfinePath`:

1. **Returned path is cleaned**: The returned value satisfies `filepath.Clean(result) == result`.
2. **Returned path is confined**: One of the following holds:
   - `result == root`
   - `strings.HasPrefix(result, root + string(os.PathSeparator))`
3. **Both root and target are resolved**: Symlinks are resolved; both final target and deepest existing ancestor remain inside root.

### Validation Rules (Rejection Table)

A call to `ConfineRelPath(root, relPath)` MUST return an error if any of the following is true:

| Condition | Reason | Example |
|-----------|--------|---------|
| `relPath == ""` | Empty path is meaningless | `ConfineRelPath("/var/mods", "") → error` |
| `strings.HasPrefix(relPath, "/")` | Absolute path; use root instead | `ConfineRelPath("/var/mods", "/etc/passwd") → error` |
| After normalizing backslashes to forward slashes, `filepath.Clean(relPath) == ".."` | Attempts traversal to parent | `ConfineRelPath("/var/mods", "..") → error` |
| After normalizing backslashes to forward slashes, the result begins with `"../"` | Traversal prefix | `ConfineRelPath("/var/mods", "../../../etc") → error` |
| Any path segment (split on `/`) equals `".."` | Traversal via segment | `ConfineRelPath("/var/mods", "config/../../../etc") → error` |
| Contains control characters (U+0000–U+001F, U+007F) | Control chars in paths are always dangerous | `ConfineRelPath("/var/mods", "file\x00.txt") → error` |
| `len(relPath) > 4096` | Length limit prevents excessively long path attacks | (arbitrary large nested path) |
| `filepath.Join(root, relPath)` escapes root after `Clean()` | The joined path, after cleaning, does not fall within root | (see symlink handling below) |
| Symlink target resolves outside root | The symlink, after resolution, points outside the sandbox | `ConfineRelPath("/var/mods", "link")` → error when `link → /etc/passwd` |

**Normalization before rejection checks**: Backslashes are converted to forward slashes first (since zip entries may use either on any platform), then rejection checks are applied to the normalized form.

**What is allowed**: Leading dots on individual segments (e.g., `.gitkeep`, `.hidden/config`), interior separators (e.g., `config/settings.json`), and nested directory structures. The constraint is that after `filepath.Clean` and symlink resolution, the final absolute path must remain confined to root.

### Symlink Handling (Critical)

Same as `ConfinePath`: resolve all symlinks (both the final target and all ancestors) and verify they remain within root. See the `ConfinePath` symlink section above for detailed examples.

---

## Call Sites — Consolidation Required

### ConfinePath (single-component call sites)

The following locations currently use ad-hoc path validation and MUST be migrated to use `ConfinePath`:

| File | Lines | Current Pattern | Note |
|------|-------|-----------------|------|
| `agent/internal/mods/mods.go` | 389 | `os.RemoveAll(target)` in `removeEntry` | Inline Join+Clean+HasPrefix guard; consolidate into ConfinePath call |
| `agent/internal/mods/mods.go` | 391 | `os.Remove(target)` in `removeEntry` | Same guard as line 389 |
| `agent/internal/mods/mods.go` | 446 | `os.Rename(tmpName, filepath.Join(...))` in `download()` | Guard is in caller (safeName); move into ConfinePath, called in download() |
| `agent/internal/mods/mods.go` | 486 | `os.RemoveAll(final)` in `swapInArchive` | Join+Clean+HasPrefix guard; consolidate |
| `agent/internal/mods/mods.go` | 490 | `os.Rename(staging, final)` in `swapInArchive` | Same guard as line 486 |
| `agent/internal/mods/mods.go` | 594 | `os.Stat(target)` in `remove()` | In-function Join+Clean+HasPrefix guard; consolidate |

### ConfineRelPath (multi-segment relative path call sites)

| File | Lines | Current Pattern | Note |
|------|-------|-----------------|------|
| `agent/internal/mods/mods.go` | 508 | (unzipInto) archive entry extraction | Archive entries are relative paths, not single components; use ConfineRelPath instead of ConfinePath |

---

## Migration Pattern

After both `ConfinePath` and `ConfineRelPath` are defined, all call sites MUST:

1. **Choose the correct function**:
   - **Single component** (filename, name property): Use `ConfinePath(root, component)`.
   - **Relative path with separators** (archive entry, nested path): Use `ConfineRelPath(root, relPath)`.

2. **Call the helper before any file operation**:
   ```go
   // For a single component (e.g., in removeEntry, download, swapInArchive, remove):
   target, err := ConfinePath(h.dir, name)
   if err != nil {
     return fmt.Errorf("path confinement failed: %w", err)
   }
   os.RemoveAll(target)
   
   // For a relative path (e.g., in unzipInto):
   target, err := ConfineRelPath(h.dir, archiveEntry)
   if err != nil {
     return fmt.Errorf("path confinement failed: %w", err)
   }
   os.MkdirAll(filepath.Dir(target), 0755)
   // Create file at target
   ```

3. **Use the returned value**: Never re-derive the path from the raw input. The returned value is what has been validated; re-deriving defeats the sanitizer recognition.

4. **Remove ad-hoc guards**: The old inline Join+Clean+HasPrefix checks MUST be deleted, not duplicated. The chosen helper is the single source of validation.

---

## Deliberate Deviation: Inline Re-checks Retained at Four Call Sites (2026-08-30)

**Amendment Status**: Item 4 is intentionally **not applied** at four locations in `agent/internal/mods/mods.go`:

| Function | Lines | Inline Check Retained |
|----------|-------|----------------------|
| `removeEntry` | ~389–393 | Yes |
| `swapInArchive` | ~521–526 | Yes |
| `unzipInto` | ~559–562 | Yes |
| `remove` | ~647–652 | Yes |

**Rationale for Deviation**

Feature 009 Phase A's primary goal was to make path-confinement guards legible to CodeQL's taint-tracking model, allowing automated detection of path-escape attempts. At these four call sites, an inline re-check (Join+Clean+HasPrefix) is **retained after calling the helper**, with a comment: "Re-check inline so the guard is visible at the point of use."

This placement at the point of OS file operation (e.g., immediately before `os.RemoveAll(target)`) appears to be what allows CodeQL to track the validated path through to the system call. Removing it without evidence risks re-raising CodeQL alerts #7–#13 (the original security findings that motivated this contract) without gaining any additional safety.

**Invariant Preserved**

Item 3 of the Migration Pattern is **fully honored**: the value passed to the file operation is always the result returned by the helper (never re-derived from the raw input). The helper remains the sole source of validated paths. The inline re-check adds defence-in-depth—it re-asserts a postcondition the helper already guarantees, but makes that guarantee visible to static analysis at the critical juncture.

**Future Review**

This deviation should be reconsidered if:
- A future CodeQL run demonstrates that inline re-checks are no longer necessary for alerts #7–#13 to remain clear.
- The confinement helpers are strengthened or static analysis tools evolve in ways that make the redundancy plainly safe to remove.

---

## Testing Requirements

### For ConfinePath:
- Unit tests MUST cover every rejection case in the single-component validation table above.
- Symlink tests MUST verify both escape attempts (symlink target outside root) and safe cases (symlink target inside root).
- Integration tests MUST verify that call sites (removeEntry, download, swapInArchive, remove) use the returned validated path and do not re-derive from the raw input.

### For ConfineRelPath:
- Unit tests MUST cover every rejection case in the multi-segment validation table above.
- Tests MUST verify that valid archive entries are accepted:
  - `config/settings.json` (nested path) → should succeed
  - `.gitkeep` (leading dot, no separators) → should succeed
  - `.hidden/config` (leading dot on first segment, interior separator) → should succeed
- Tests MUST verify that zip-slip entries are rejected: `../../etc/passwd`, `../../../`, etc.
- Symlink tests MUST verify both escape attempts and safe cases.
- Integration test MUST verify that `unzipInto` uses the returned validated path and does not re-derive from the archive entry name.

---

## Relationship to Existing Helpers

- **`safeName()`** (agent/internal/mods/mods.go:625–645): Rejects path components with `.`, `..`, leading dot, len>200, control chars, `/`, `\`. Remains in use for initial filtering; `ConfinePath` is more comprehensive and includes symlink resolution.
- **`archiveFolderName()`** (agent/internal/mods/mods.go:557–565): Strips archive suffix (`.tar.gz`, `.tgz`, `.zip`). Orthogonal to confinement; works alongside `ConfineRelPath`.
- **`resolve()`** (agent/internal/files/files.go:57–98): The existing most-complete confinement helper, used for arbitrary file-service requests. Both `ConfinePath` and `ConfineRelPath` SHOULD follow `resolve()`'s pattern for consistency across the agent.
- **`extractUploadArchive()`** (api/internal/handlers/module_upload.go:290–363): API-side archive extraction; uses `path.Clean` + reject absolute/`..`/`../` prefix. Should be aligned with the consolidated agent-side confinement via `ConfineRelPath`.
