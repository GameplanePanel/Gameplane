#!/usr/bin/env python3
"""Unit tests for hack/auto_label.py. Run: python3 hack/test_auto_label.py"""

import unittest

from auto_label import area_for_path, parse_title, planned_labels


class ParseTitle(unittest.TestCase):
    def test_prefixes(self):
        cases = {
            "feat: add thing": "type: feature",
            "fix(api): bind identities": "type: fix",
            "refactor(web): split": "type: refactor",
            "test(e2e): cover": "type: test",
            "ci: reduce checks": "type: ci",
            "chore(deps)(deps-dev): Bump x from 1 to 2": "type: chore",
            "docs(specs): record release": "type: docs",
        }
        for title, want in cases.items():
            with self.subTest(title=title):
                self.assertEqual(parse_title(title), (want, False))

    def test_breaking(self):
        self.assertEqual(parse_title("feat(api)!: drop v1"), ("type: feature", True))

    def test_unknown_or_malformed(self):
        for title in ("perf: faster", "Fix the thing", "fix:no space", ""):
            with self.subTest(title=title):
                self.assertEqual(parse_title(title)[0], None)


class AreaForPath(unittest.TestCase):
    def test_mapping(self):
        cases = {
            "operator/internal/controller/x.go": "area: operator",
            "api/cmd/main.go": "area: api",
            "agent/internal/files/a.go": "area: agent",
            "web/src/routes/Share.tsx": "area: web",
            "design.pen": "area: web",
            "assets/design-export/json/a.json": "area: web",
            "modules": "area: modules",
            "docs/module-authoring.md": "area: modules",
            "charts/gameplane/values.yaml": "area: chart",
            "test/e2e/buckets.sh": "area: e2e",
            "specs/018-x/tasks.md": "area: specs",
            "specs/done_014-x/spec.md": "area: specs",
            "mcp-server/internal/kube/log_tail.go": "area: optional-components",
            "telemetry-receiver/main.go": "area: optional-components",
            "netguard/guard.go": "area: shared",
            ".github/workflows/ci.yaml": "area: shared",
            "Makefile": "area: shared",
            "docs/security.md": None,
        }
        for path, want in cases.items():
            with self.subTest(path=path):
                self.assertEqual(area_for_path(path), want)


class PlannedLabels(unittest.TestCase):
    def test_unlabelled(self):
        got = planned_labels(
            "fix(mods): long installs",
            ["agent/a.go", "api/b.go", "web/c.tsx", "agent/d.go"],
            [],
        )
        self.assertEqual(got, ["type: fix", "area: agent", "area: api", "area: web"])

    def test_existing_categories_untouched(self):
        got = planned_labels(
            "fix(api): x", ["operator/a.go"], ["type: security", "area: api"]
        )
        self.assertEqual(got, [])

    def test_only_missing_category(self):
        got = planned_labels("fix(api): x", ["api/a.go"], ["type: security"])
        self.assertEqual(got, ["area: api"])

    def test_breaking_added_once(self):
        self.assertEqual(
            planned_labels("feat!: x", ["api/a.go"], ["type: feature", "area: api"]),
            ["breaking"],
        )
        self.assertEqual(
            planned_labels("feat!: x", [], ["type: feature", "area: api", "breaking"]),
            [],
        )

    def test_docs_only_count_alone(self):
        self.assertEqual(
            planned_labels("docs: x", ["docs/a.md", "api/b.go"], []),
            ["type: docs", "area: api"],
        )
        self.assertEqual(
            planned_labels("docs: x", ["docs/a.md"], []),
            ["type: docs", "area: shared"],
        )

    def test_unknown_prefix_still_gets_area(self):
        self.assertEqual(planned_labels("Update stuff", ["web/a.ts"], []), ["area: web"])


if __name__ == "__main__":
    unittest.main()
