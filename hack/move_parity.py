"""Move parity gate for spec 028 split-big-packages PRs (tasks T004 and T006).

`names` proves that every Go Test/Fuzz/Example/Benchmark function and every Vitest or
Playwright title chain survives a move. `test-edits` proves that each changed test file
differs from its base only by the mechanical edits OD-6 and OD-9 allow (see
specs/028-split-big-packages/OPEN-DECISIONS.md), as listed in the PR's move brief.
`ci` runs both for every move brief that the change adds or edits. Python stdlib only.
"""

import argparse
import difflib
import posixpath
import re
import subprocess
import sys
from collections import Counter
from pathlib import Path


ROOT = Path(__file__).resolve().parent.parent
BRIEF_DIR = "specs/028-split-big-packages/briefs"
KINDS = ["package", "imports", "qualified-names", "mock-paths", "envtest-paths", "new-testmain"]
GO_FUNC = re.compile(r"^func ((?:Test|Fuzz|Example|Benchmark)\w*)\(", re.MULTILINE)
WEB_CALL = re.compile(
    r"""^(\s*)(?:test\.)?(describe|it|test)(?:\.\w+)*(?:\([^()\n]*\))?\s*\(\s*(['"`])((?:\\.|(?!\3).)*)\3""")
GO_PACKAGE = re.compile(r"^package\s+(\w+)$")
GO_IMPORT_BLOCK = re.compile(r"^import\s*\($")
GO_IMPORT_LINE = re.compile(r'^import\s+(?:[\w.]+\s+)?"[^"]*"$')
TS_IMPORT_FROM = re.compile(r"""from\s+(['"]).*\1\s*;?\s*$""")
TS_IMPORT_BARE = re.compile(r"""import\s+(['"]).*\1\s*;?\s*$""")
MOCK_CALL = re.compile(
    r"""((?:\bvi\.(?:mock|doMock|importActual)|\bimport)\(\s*)(['"`])((?:\\.|(?!\2).)*)\2""")
JOIN_DOTDOT = re.compile(r'"\.\.",\s*')


def git(root, *args):
    """Run git in root and return its stdout."""
    return subprocess.run(["git", *args], cwd=root, check=True, capture_output=True,
                          text=True, encoding="utf-8", errors="replace").stdout


def read_blobs(root, rev, paths):
    """Return {path: text} for files at rev, read with one git cat-file --batch call."""
    if not paths:
        return {}
    request = "".join(f"{rev}:{path}\n" for path in paths).encode("utf-8")
    data = subprocess.run(["git", "cat-file", "--batch"], cwd=root, check=True,
                          input=request, capture_output=True).stdout
    texts, pos = {}, 0
    for path in paths:
        end = data.index(b"\n", pos)
        fields = data[pos:end].decode("utf-8", errors="replace").split()
        pos = end + 1
        if len(fields) < 3 or fields[1] == "missing":
            raise ValueError(f"{rev}:{path} is not a file at that revision")
        size = int(fields[2])
        texts[path] = data[pos:pos + size].decode("utf-8", errors="replace")
        pos += size + 1
    return texts


def is_test(path):
    """Return True for Go test files and the Vitest and Playwright specs this gate covers."""
    return (path.endswith("_test.go")
            or (path.startswith("web/") and path.endswith((".test.ts", ".test.tsx")))
            or (path.startswith("web/e2e/") and path.endswith(".spec.ts")))


def web_keys(text):
    """Return the "web:" title chains of the describe/it/test calls in one spec file."""
    keys, stack = [], []
    for line in text.splitlines():
        match = WEB_CALL.match(line)
        if not match:
            continue
        indent, kind, title = len(match[1]), match[2], match[4]
        while stack and stack[-1][0] >= indent:
            stack.pop()
        keys.append("web:" + " > ".join([name for _, name in stack] + [title]))
        if kind == "describe":
            stack.append((indent, title))
    return keys


def test_keys(path, text):
    """Return the comparable keys for one test file; the package path is dropped on purpose."""
    if path.endswith(".go"):
        module = "test/e2e" if path.startswith("test/e2e/") else path.split("/", 1)[0]
        return [f"{module}:{name}" for name in GO_FUNC.findall(text) if name != "TestMain"]
    return web_keys(text)


def collect_keys(root, rev):
    """Return a Counter of comparable test keys at one revision."""
    listing = git(root, "ls-tree", "-r", "-z", "--name-only", rev).split("\0")
    paths = [path for path in listing if path and is_test(path)]
    texts = read_blobs(root, rev, paths)
    keys = Counter()
    for path in paths:
        keys.update(test_keys(path, texts[path]))
    return keys


def cmd_names(root, base, head):
    """Compare test keys between two revisions and return the exit code."""
    before, after = collect_keys(root, base), collect_keys(root, head)
    missing, extra = before - after, after - before
    if not missing and not extra:
        print(f"equal, {sum(before.values())} tests")
        return 0
    for key, count in sorted(missing.items()):
        print(f"missing: {key} (x{count})")
    for key, count in sorted(extra.items()):
        print(f"extra: {key} (x{count})")
    return 1


def brief_tables(text):
    """Return {section number: data rows} for the markdown tables of a move brief."""
    tables, section, in_table = {}, None, False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("## "):
            match = re.match(r"##\s+(\d+)\b", stripped)
            section, in_table = (int(match[1]) if match else None), False
        elif not stripped.startswith("|"):
            in_table = False
        elif not in_table:
            in_table = True
        else:
            cells = [cell.strip().replace("`", "").strip()
                     for cell in stripped.strip("|").split("|")]
            if section is not None and not all(re.fullmatch(r":?-+:?", c) for c in cells):
                tables.setdefault(section, []).append(cells)
    return tables


def parse_brief(text):
    """Return (moves, exports, copies) from sections 3, 4 and 5 of a move brief."""
    tables = brief_tables(text)
    moves = {row[0]: row[1] for row in tables.get(3, []) if len(row) >= 2}
    exports = [(row[0], row[1], row[2]) for row in tables.get(4, []) if len(row) >= 3]
    copies = {}
    for row in tables.get(5, []):
        match = re.search(r"copy of\s+(\S+)", row[1]) if len(row) >= 2 else None
        if match:
            copies[row[0]] = match[1]
    return moves, exports, copies


def pair_files(root, base, head, moves, copies):
    """Return (pairs, failures) for the test files changed between base and head.

    pairs holds (base path, head path, is_copy); failures maps a path to its reason.
    """
    diff = git(root, "diff", "--name-status", "-M50%", base, head)
    pairs, failures, added, deleted = [], {}, [], []
    for line in diff.splitlines():
        fields = line.split("\t")
        status = fields[0][:1]
        if status == "R":
            old, new = fields[1], fields[2]
            if is_test(old) and is_test(new):
                pairs.append((old, new, False))
            elif is_test(old):
                failures[old] = "test file deleted"
            elif is_test(new):
                failures[new] = "test file added"
        elif status == "M" and is_test(fields[1]):
            pairs.append((fields[1], fields[1], False))
        elif status == "A" and is_test(fields[1]):
            added.append(fields[1])
        elif status == "D" and is_test(fields[1]):
            deleted.append(fields[1])
    for path in deleted:
        destination = moves.get(path)
        if destination in added:
            added.remove(destination)
            pairs.append((path, destination, False))
        else:
            failures[path] = "test file deleted"
    for path in added:
        if path in copies:
            pairs.append((copies[path], path, True))
        else:
            failures[path] = "test file added"
    return pairs, failures


def go_parts(text):
    """Split a Go file into (package name, import lines, remaining lines)."""
    package, imports, body, block = "", [], [], False
    for line in text.splitlines():
        stripped = line.strip()
        if block:
            imports.append(stripped)
            block = stripped != ")"
        elif GO_IMPORT_BLOCK.match(stripped):
            imports.append(stripped)
            block = True
        elif GO_IMPORT_LINE.match(stripped):
            imports.append(stripped)
        elif not package and GO_PACKAGE.match(stripped):
            package = GO_PACKAGE.match(stripped)[1]
        else:
            body.append(line)
    return package, imports, body


def ts_parts(text):
    """Split a TypeScript file into (import statement lines, remaining lines)."""
    imports, body, open_import = [], [], False
    for line in text.splitlines():
        stripped = line.strip()
        if open_import:
            imports.append(stripped)
            open_import = not TS_IMPORT_FROM.search(stripped)
        elif stripped.startswith("import "):
            imports.append(stripped)
            open_import = not (TS_IMPORT_FROM.search(stripped) or TS_IMPORT_BARE.match(stripped))
        else:
            body.append(line)
    return imports, body


def mock_args(text):
    """Return the path literals passed to vi.mock, vi.doMock, vi.importActual and import()."""
    return [match[3] for match in MOCK_CALL.finditer(text)]


def mock_normal(text):
    """Replace every mock path literal with "<path>"."""
    return MOCK_CALL.sub(lambda match: match[1] + '"<path>"', text)


def map_identifiers(text, exports):
    """Rewrite head-side names that moved packages back to their base spelling."""
    for old, new, package in exports:
        text = re.sub(rf"\b{re.escape(package)}\.{re.escape(new)}\b", new, text)
        text = re.sub(rf"\b{re.escape(new)}\b", lambda _, old=old: old, text)
    news = {new for _, new, _ in exports}
    for package in {package for _, _, package in exports}:
        text = re.sub(rf"\b{re.escape(package)}\.(\w+)\b",
                      lambda match: match[1] if match[1] in news else match[0], text)
    return text


def envtest_paths(base_lines, head_lines, extra):
    """Apply the OD-9 allowance to filepath.Join lines; return (base, head, changed, reason)."""
    if len(base_lines) != len(head_lines):
        return base_lines, head_lines, False, ""
    base_out, head_out, changed = [], [], False
    for base_line, head_line in zip(base_lines, head_lines):
        if "filepath.Join(" in base_line or "filepath.Join(" in head_line:
            base_dots, head_dots = base_line.count('".."'), head_line.count('".."')
            if base_dots or head_dots:
                if head_dots - base_dots != extra:
                    reason = (f'filepath.Join: head adds {head_dots - base_dots} ".." for '
                              f'{extra} extra folder level(s): {head_line.strip()}')
                    return [], [], False, reason
                changed = changed or head_dots != base_dots
            base_line = JOIN_DOTDOT.sub("", base_line)
            head_line = JOIN_DOTDOT.sub("", head_line)
        base_out.append(base_line)
        head_out.append(head_line)
    return base_out, head_out, changed, ""


def normal(lines):
    """Strip each line, collapse whitespace runs and drop empty lines."""
    result = [re.sub(r"\s+", " ", line).strip() for line in lines]
    return [line for line in result if line]


def compare(base_lines, head_lines):
    """Return (reason, first five differing lines) after the shared normalisation."""
    base_norm, head_norm = normal(base_lines), normal(head_lines)
    if base_norm == head_norm:
        return "", []
    diff = [line for line in difflib.unified_diff(base_norm, head_norm, n=0, lineterm="")
            if line.startswith(("+", "-")) and not line.startswith(("+++", "---"))]
    return "content differs", diff[:5]


def check_go(base_text, head_text, exports, extra):
    """Compare one Go test file pair; return (kinds, reason, diff lines)."""
    base_pkg, base_imports, base_body = go_parts(base_text)
    head_pkg, head_imports, head_body = go_parts(head_text)
    kinds = set()
    if base_pkg != head_pkg:
        kinds.add("package")
    if base_imports != head_imports:
        kinds.add("imports")
    head_joined = "\n".join(head_body)
    mapped = map_identifiers(head_joined, exports)
    if mapped != head_joined:
        kinds.add("qualified-names")
    base_lines, head_lines, changed, reason = envtest_paths(base_body, mapped.split("\n"), extra)
    if reason:
        return kinds, reason, []
    if changed:
        kinds.add("envtest-paths")
    reason, diff = compare(base_lines, head_lines)
    return kinds, reason, diff


def check_ts(base_text, head_text, exports):
    """Compare one TypeScript test file pair; return (kinds, reason, diff lines)."""
    base_imports, base_body = ts_parts(base_text)
    head_imports, head_body = ts_parts(head_text)
    kinds = set()
    if base_imports != head_imports:
        kinds.add("imports")
    if mock_args(base_text) != mock_args(head_text):
        kinds.add("mock-paths")
    head_joined = "\n".join(head_body)
    mapped = map_identifiers(head_joined, exports)
    if mapped != head_joined:
        kinds.add("qualified-names")
    reason, diff = compare(mock_normal("\n".join(base_body)).split("\n"),
                           mock_normal(mapped).split("\n"))
    return kinds, reason, diff


def check_pair(root, base, head, old, new, is_copy, exports):
    """Return (kinds, reason, diff lines) for one changed test file."""
    base_text = read_blobs(root, base, [old])[old]
    head_text = read_blobs(root, head, [new])[new]
    if new.endswith(".go"):
        extra = posixpath.dirname(new).count("/") - posixpath.dirname(old).count("/")
        kinds, reason, diff = check_go(base_text, head_text, exports, extra)
    else:
        kinds, reason, diff = check_ts(base_text, head_text, exports)
    if is_copy:
        kinds.add("new-testmain")
    return kinds, reason, diff


def ordered(kinds):
    """Return the kinds in their canonical order."""
    return [kind for kind in KINDS if kind in kinds]


def cmd_test_edits(root, base, head, brief):
    """Prove each changed test file differs only by OD-6 and OD-9 edits; return the exit code."""
    moves, exports, copies = parse_brief(Path(brief).read_text(encoding="utf-8"))
    pairs, failures = pair_files(root, base, head, moves, copies)
    results = {}
    for old, new, is_copy in pairs:
        results[new] = check_pair(root, base, head, old, new, is_copy, exports)
    for path, reason in failures.items():
        results[path] = (set(), reason, [])
    failed, seen = 0, set()
    for path in sorted(results):
        kinds, reason, diff = results[path]
        if reason:
            failed += 1
            print(f"{path}: FAIL: {reason}")
            for line in diff:
                print(f"    {line}")
        else:
            seen |= kinds
            print(f"{path}: pass ({', '.join(ordered(kinds)) or 'no changes'})")
    if failed:
        print(f"fail, {failed} of {len(results)} test files")
        return 1
    print(f"pass, {len(results)} test files touched, kinds: "
          f"{', '.join(ordered(seen)) or 'none'}")
    return 0


def changed_briefs(root, base, head):
    """Return the move briefs added or modified between base and head."""
    names = git(root, "diff", "--name-only", "--diff-filter=AM", base, head).splitlines()
    return [name for name in names
            if posixpath.dirname(name) == BRIEF_DIR and name.endswith(".md")
            and posixpath.basename(name) != "README.md"]


def cmd_ci(root, base, head):
    """Run names and test-edits for every move brief in the change; return the exit code."""
    briefs = changed_briefs(root, base, head)
    if not briefs:
        print("no spec 028 move brief in this change; skipped")
        return 0
    code = cmd_names(root, base, head)
    for brief in briefs:
        print(f"== {brief}")
        if cmd_test_edits(root, base, head, root / brief):
            code = 1
    return code


def main(argv=None):
    """Parse arguments, run the requested check and return the exit code."""
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", default=str(ROOT))
    commands = parser.add_subparsers(dest="command", required=True)
    names = commands.add_parser("names", help="compare test names between two revisions")
    names.add_argument("base")
    names.add_argument("head")
    edits = commands.add_parser("test-edits", help="check test file edits against a brief")
    edits.add_argument("base")
    edits.add_argument("head")
    edits.add_argument("--brief", required=True)
    ci = commands.add_parser("ci", help="run both checks for every move brief in the change")
    ci.add_argument("--base", required=True)
    ci.add_argument("--head", required=True)
    args = parser.parse_args(argv)
    root = Path(args.root)
    try:
        if args.command == "names":
            return cmd_names(root, args.base, args.head)
        if args.command == "test-edits":
            return cmd_test_edits(root, args.base, args.head, args.brief)
        return cmd_ci(root, args.base, args.head)
    except (OSError, ValueError, KeyError, IndexError, subprocess.CalledProcessError) as exc:
        print(f"::error::move parity: {exc}")
        return 1


if __name__ == "__main__":
    sys.exit(main())
