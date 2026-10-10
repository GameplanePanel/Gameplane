#!/usr/bin/env python3
"""auto_label.py: add missing `type:` and `area:` labels to open pull requests.

Run by .github/workflows/auto-label.yaml on a schedule. For every open PR:

  - no `type:` label  -> derive one from the conventional-commit title prefix
                         (`fix(api): ...` -> `type: fix`); unknown prefixes add
                         nothing.
  - no `area:` label  -> derive one per top-level directory the PR changes
                         (docs/contributing.md "PR labels" taxonomy). docs/
                         pages only count when nothing else changed.
  - `!` before the colon in the title (`feat(api)!: ...`) -> `breaking`.

A category that already has a label is left alone, so a maintainer's manual
choice is never overridden (removing the last label of a category lets the
next run derive one again). Labels are only added,
never removed. `type: security` cannot be derived and stays manual.

The script reads PR titles and file lists through the REST API only; it never
checks out or executes pull request code.

Environment:
  GITHUB_TOKEN       token with pull-requests: write
  GITHUB_REPOSITORY  owner/repo
Flags:
  --dry-run          print the planned labels without applying them
"""

import json
import os
import re
import sys
import urllib.request

API = "https://api.github.com"

TYPE_LABELS = {
    "feat": "type: feature",
    "fix": "type: fix",
    "refactor": "type: refactor",
    "test": "type: test",
    "ci": "type: ci",
    "chore": "type: chore",
    "docs": "type: docs",
}

# `type(scope)(scope2)!: subject` -- Dependabot titles carry two scopes.
TITLE_RE = re.compile(r"^(?P<type>[a-z]+)(?:\([^)]*\))*(?P<bang>!)?:\s")

AREA_BY_DIR = {
    "operator": "area: operator",
    "api": "area: api",
    "agent": "area: agent",
    "web": "area: web",
    "modules": "area: modules",
    "charts": "area: chart",
    "specs": "area: specs",
    "sentinel": "area: optional-components",
    "capture-sidecar": "area: optional-components",
    "mcp-server": "area: optional-components",
    "tunnel": "area: optional-components",
    "audit-syslog-bridge": "area: optional-components",
    "telemetry-receiver": "area: optional-components",
    "telemetryschema": "area: shared",
}

AREA_BY_FILE = {
    "design.pen": "area: web",
    "docs/module-authoring.md": "area: modules",
}


def parse_title(title):
    """Return (type label or None, breaking flag) for a PR title."""
    m = TITLE_RE.match(title.strip())
    if not m:
        return None, False
    return TYPE_LABELS.get(m.group("type")), m.group("bang") is not None


def area_for_path(path):
    """Map one changed file path to its `area:` label (fallback: shared).

    Returns None for docs/ pages, which accompany most code changes and only
    mean `area: shared` when nothing else changed.
    """
    if path in AREA_BY_FILE:
        return AREA_BY_FILE[path]
    if path.startswith("assets/design-export/"):
        return "area: web"
    if path.startswith("test/e2e/"):
        return "area: e2e"
    if path.startswith("docs/"):
        return None
    return AREA_BY_DIR.get(path.split("/", 1)[0], "area: shared")


def planned_labels(title, files, existing):
    """Labels to add to a PR, given its title, changed files and labels."""
    existing = set(existing)
    add = []
    type_label, breaking = parse_title(title)
    if type_label and not any(name.startswith("type: ") for name in existing):
        add.append(type_label)
    if files and not any(name.startswith("area: ") for name in existing):
        areas = {area_for_path(f) for f in files} - {None}
        add.extend(sorted(areas) or ["area: shared"])
    if breaking and "breaking" not in existing:
        add.append("breaking")
    return add


def api(method, path, token, body=None):
    data = None if body is None else json.dumps(body).encode()
    headers = {
        "Accept": "application/vnd.github+json",
        "Authorization": "Bearer " + token,
        "X-GitHub-Api-Version": "2022-11-28",
    }
    if data is not None:
        # urllib defaults bodies to application/x-www-form-urlencoded.
        headers["Content-Type"] = "application/json"
    req = urllib.request.Request(API + path, data=data, method=method, headers=headers)
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.load(resp)


def paginate(path, token, max_pages=30):
    sep = "&" if "?" in path else "?"
    items = []
    for page in range(1, max_pages + 1):
        batch = api("GET", f"{path}{sep}per_page=100&page={page}", token)
        items.extend(batch)
        if len(batch) < 100:
            return items
    print(f"warning: {path} truncated at {len(items)} items", file=sys.stderr)
    return items


def main(argv):
    dry_run = "--dry-run" in argv
    token = os.environ.get("GITHUB_TOKEN", "")
    repo = os.environ.get("GITHUB_REPOSITORY", "")
    if not token or not repo:
        print("GITHUB_TOKEN and GITHUB_REPOSITORY must be set", file=sys.stderr)
        return 1

    try:
        pulls = paginate(f"/repos/{repo}/pulls?state=open", token)
    except (OSError, ValueError) as err:
        print(f"listing open pull requests: {err}", file=sys.stderr)
        return 1

    failed = False
    for pr in pulls:
        number = pr["number"]
        existing = [label["name"] for label in pr.get("labels", [])]
        try:
            files = []
            if not any(name.startswith("area: ") for name in existing):
                files = [
                    f["filename"]
                    for f in paginate(f"/repos/{repo}/pulls/{number}/files", token)
                ]
            add = planned_labels(pr["title"], files, existing)
            if not add:
                continue
            print(f"#{number}: add {', '.join(add)}")
            if not dry_run:
                api("POST", f"/repos/{repo}/issues/{number}/labels", token,
                    {"labels": add})
        # HTTPError/URLError and timeouts are OSError; bad JSON is ValueError.
        except (OSError, ValueError, KeyError) as err:
            print(f"#{number}: {err}", file=sys.stderr)
            failed = True
    return 1 if failed else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
