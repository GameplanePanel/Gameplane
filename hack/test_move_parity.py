"""Behavioral coverage for the spec 028 move parity gate (run in CI, tasks T005 and T006)."""

import contextlib
import io
import subprocess
import tempfile
import unittest
from pathlib import Path

from move_parity import main, web_keys


GIT = ["git", "-c", "user.name=test", "-c", "user.email=test@example.com",
       "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
       # background gc could still be writing .git when the temp dir is removed
       "-c", "gc.auto=0", "-c", "maintenance.auto=false"]
GO_TEST = """package x

import "testing"

func TestA(t *testing.T) {
\twant := 1
\tif got := 1; got != want {
\t\tt.Fatal("bad")
\t}
}
"""
WEB_SPEC = """import { render } from "@testing-library/react";
import { X } from "../routes/X";

vi.mock("../routes/X", () => ({ X: () => null }));

describe("suite", () => {
  it("one", () => {
    expect(1).toBe(1);
  });
  it("two", () => {
    expect(2).toBe(2);
  });
});
"""
SUITE = """package controller

import (
\t"path/filepath"
\t"testing"
)

func TestMain(m *testing.M) {
\tcrd := filepath.Join("..", "..", "config", "crd", "bases")
\t_ = crd
}
"""


def brief_text(moves=(), exports=(), copies=()):
    """Return a move brief with the Moves, Exports and New files tables."""
    def table(rows, headers):
        """Return markdown lines for one table."""
        lines = ["| " + " | ".join(headers) + " |", "|" + "---|" * len(headers)]
        return lines + ["| " + " | ".join(row) + " |" for row in rows]
    return "\n".join([
        "# Brief", "", "## 3. Moves", "",
        *table(moves, ["source path", "destination path", "whole file or declarations"]), "",
        "## 4. Exports", "",
        *table(exports, ["old identifier", "new identifier", "package"]), "",
        "## 5. New files", "",
        *table(copies, ["path", "copy of"]), "",
    ])


class MoveParityTests(unittest.TestCase):
    """Run the move parity commands against temporary git repositories."""

    def setUp(self):
        """Create an empty git repository and a directory for briefs."""
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.briefs = tempfile.TemporaryDirectory()
        self.addCleanup(self.briefs.cleanup)
        self.root = Path(self.temp.name)
        self.git("init", "-q")
        self.base = self.head = ""

    def git(self, *args):
        """Run git in the fixture repository and return its trimmed output."""
        return subprocess.run([*GIT, *args], cwd=self.root, check=True, capture_output=True,
                              text=True).stdout.strip()

    def write(self, name, content):
        """Write a fixture file in the repository."""
        path = self.root / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(content, encoding="utf-8")

    def commit(self):
        """Commit every fixture change and return the commit id."""
        self.git("add", "-A")
        self.git("commit", "-q", "--allow-empty", "-m", "change")
        return self.git("rev-parse", "HEAD")

    def brief(self, text):
        """Write a brief outside the repository and return its path."""
        path = Path(self.briefs.name) / "brief.md"
        path.write_text(text, encoding="utf-8")
        return str(path)

    def run_main(self, *args):
        """Run the gate and return (exit code, stdout)."""
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = main(["--root", str(self.root), *args])
        return code, out.getvalue()

    def names(self):
        """Run names between the recorded base and head."""
        return self.run_main("names", self.base, self.head)

    def edits(self, brief):
        """Run test-edits between the recorded base and head with a brief text."""
        return self.run_main("test-edits", self.base, self.head, "--brief", self.brief(brief))

    def test_names_equal_lists_pass(self):
        """Unchanged test names pass even when test bodies change."""
        self.write("api/x/a_test.go", GO_TEST)
        self.write("web/src/s.test.tsx", WEB_SPEC)
        self.base = self.commit()
        self.write("api/x/a_test.go", GO_TEST.replace("want := 1", "want := 1 // note"))
        self.head = self.commit()
        code, out = self.names()
        self.assertEqual(code, 0, out)
        self.assertIn("equal, 4 tests", out)

    def test_removed_go_test_fails(self):
        """A removed Go test function is reported as missing."""
        self.write("api/x/a_test.go", GO_TEST + "\nfunc TestB(t *testing.T) {}\n")
        self.base = self.commit()
        self.write("api/x/a_test.go", GO_TEST)
        self.head = self.commit()
        code, out = self.names()
        self.assertEqual(code, 1)
        self.assertIn("missing: api:TestB (x1)", out)

    def test_removed_it_fails(self):
        """A removed it() inside a describe block is reported with its title chain."""
        self.write("web/src/s.test.tsx", WEB_SPEC)
        self.base = self.commit()
        self.write("web/src/s.test.tsx", WEB_SPEC.replace(
            '  it("two", () => {\n    expect(2).toBe(2);\n  });\n', ""))
        self.head = self.commit()
        code, out = self.names()
        self.assertEqual(code, 1)
        self.assertIn("missing: web:suite > two (x1)", out)

    def test_go_test_moved_to_new_package_passes(self):
        """A test that moves to another package directory of the same module keeps its key."""
        self.write("api/x/a_test.go", GO_TEST)
        self.base = self.commit()
        self.write("api/x/sub/a_test.go", GO_TEST.replace("package x", "package sub"))
        (self.root / "api/x/a_test.go").unlink()
        self.head = self.commit()
        code, out = self.names()
        self.assertEqual(code, 0, out)
        self.assertIn("equal, 1 tests", out)

    def test_nested_describe_chain(self):
        """Nested describe blocks produce the full title chain."""
        text = ('describe("A", () => {\n  describe("b", () => {\n    it("c", () => {});\n'
                '  });\n});\n')
        self.assertEqual(web_keys(text), ["web:A", "web:A > b", "web:A > b > c"])

    def test_renamed_go_file_with_package_change_passes(self):
        """A renamed Go test file whose only change is the package clause passes."""
        self.write("api/x/a_test.go", GO_TEST)
        self.base = self.commit()
        self.git("mv", "api/x/a_test.go", "api/x/b_test.go")
        self.write("api/x/b_test.go", GO_TEST.replace("package x", "package y"))
        self.head = self.commit()
        code, out = self.edits(brief_text())
        self.assertEqual(code, 0, out)
        self.assertIn("api/x/b_test.go: pass (package)", out)

    def test_assertion_change_fails(self):
        """A changed assertion in a moved test file fails the edit check."""
        self.write("api/x/a_test.go", GO_TEST)
        self.base = self.commit()
        self.git("mv", "api/x/a_test.go", "api/x/b_test.go")
        self.write("api/x/b_test.go", GO_TEST.replace("want := 1", "want := 2"))
        self.head = self.commit()
        code, out = self.edits(brief_text())
        self.assertEqual(code, 1)
        self.assertIn("FAIL", out)
        self.assertIn("fail, 1 of 1 test files", out)

    def test_copied_envtest_suite_one_level_deeper(self):
        """A listed copy with exactly one extra ".." per filepath.Join line passes."""
        self.write("operator/internal/controller/suite_envtest_test.go", SUITE)
        self.base = self.commit()
        self.write("operator/internal/controller/sub/suite_envtest_test.go", SUITE.replace(
            "package controller", "package sub").replace(
            '"..", "..", "config"', '"..", "..", "..", "config"'))
        self.head = self.commit()
        brief = brief_text(copies=[("`operator/internal/controller/sub/suite_envtest_test.go`",
                                    "copy of `operator/internal/controller/suite_envtest_test.go`")])
        code, out = self.edits(brief)
        self.assertEqual(code, 0, out)
        self.assertIn("new-testmain", out)
        self.assertIn("envtest-paths", out)

    def test_copied_envtest_suite_two_extra_levels_fail(self):
        """Two extra ".." tokens on one filepath.Join line fail the OD-9 allowance."""
        self.write("operator/internal/controller/suite_envtest_test.go", SUITE)
        self.base = self.commit()
        self.write("operator/internal/controller/sub/suite_envtest_test.go", SUITE.replace(
            "package controller", "package sub").replace(
            '"..", "..", "config"', '"..", "..", "..", "..", "config"'))
        self.head = self.commit()
        brief = brief_text(copies=[("`operator/internal/controller/sub/suite_envtest_test.go`",
                                    "copy of `operator/internal/controller/suite_envtest_test.go`")])
        code, out = self.edits(brief)
        self.assertEqual(code, 1)
        self.assertIn("FAIL", out)

    def test_mock_path_and_import_changes_pass(self):
        """A changed vi.mock path and import line pass with their kinds reported."""
        self.write("web/src/routes/s.test.tsx", WEB_SPEC)
        self.base = self.commit()
        self.write("web/src/routes/s.test.tsx", WEB_SPEC.replace(
            'vi.mock("../routes/X"', 'vi.mock("../pages/X"').replace(
            'import { X } from "../routes/X";', 'import { X } from "../pages/X";'))
        self.head = self.commit()
        code, out = self.edits(brief_text())
        self.assertEqual(code, 0, out)
        self.assertIn("mock-paths", out)
        self.assertIn("imports", out)

    def test_qualified_name_passes_only_with_exports_row(self):
        """foo(x) to httpx.Foo(x) passes with the Exports row and fails without it."""
        base_go = ('package x\n\nimport "testing"\n\nfunc TestUse(t *testing.T) {\n'
                   '\tif foo(1) != 1 {\n\t\tt.Fatal("bad")\n\t}\n}\n')
        head_go = ('package x\n\nimport (\n\t"testing"\n\n\t"example.com/api/httpx"\n)\n\n'
                   'func TestUse(t *testing.T) {\n\tif httpx.Foo(1) != 1 {\n'
                   '\t\tt.Fatal("bad")\n\t}\n}\n')
        self.write("api/x/a_test.go", base_go)
        self.base = self.commit()
        self.write("api/x/a_test.go", head_go)
        self.head = self.commit()
        code, out = self.edits(brief_text(exports=[("`foo`", "`Foo`", "`httpx`")]))
        self.assertEqual(code, 0, out)
        self.assertIn("qualified-names", out)
        code, out = self.edits(brief_text())
        self.assertEqual(code, 1)

    def test_added_test_file_not_in_brief_fails(self):
        """A new test file without a New files row fails."""
        self.write("api/x/a_test.go", GO_TEST)
        self.base = self.commit()
        self.write("api/x/extra_test.go", GO_TEST.replace("TestA", "TestExtra"))
        self.head = self.commit()
        code, out = self.edits(brief_text())
        self.assertEqual(code, 1)
        self.assertIn("test file added", out)

    def test_ci_without_brief_is_skipped(self):
        """ci exits 0 and prints the skip line when the change has no move brief."""
        self.write("api/x/a.go", "package x\n")
        self.base = self.commit()
        self.write("api/x/a.go", "package x\n// note\n")
        self.head = self.commit()
        code, out = self.run_main("ci", "--base", self.base, "--head", self.head)
        self.assertEqual(code, 0)
        self.assertIn("no spec 028 move brief in this change; skipped", out)


if __name__ == "__main__":
    unittest.main()
