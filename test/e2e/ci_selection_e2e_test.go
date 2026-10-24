//go:build e2e

package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The TestCISelection_* tests prove that `hack/ci_scope.py e2e` turns a
// one-commit change into the expected e2e selection (spec 023 OD-4, research
// R13). Each one copies this checkout with `git archive HEAD`, commits a base
// and a head, and runs the selector on them. They need git and python3 only,
// create no cluster resources and never log in, so they live in the operator
// bucket.

// ciSelectionBuckets mirrors E2E_BUCKETS in hack/ci_scope.py (the e2e-go matrix).
var ciSelectionBuckets = []string{
	"operator", "api-auth", "api-roles", "api-rbac", "api-agent", "api-mods",
	"telemetry", "optional-components", "management-placement",
}

// ciSelection is the decoded output of one `ci_scope.py e2e` run.
type ciSelection struct {
	Reason string
	Run    map[string]string
	Bots   []string
	Flags  map[string]bool
}

// ciGit runs git in dir with a fixed identity and returns trimmed stdout.
// Background gc and maintenance are off: committing a whole checkout leaves
// enough loose objects to start them, and they would still be writing into
// .git when t.TempDir removes it.
func ciGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	base := []string{"-C", dir, "-c", "user.name=ci-selection",
		"-c", "user.email=ci-selection@example.invalid", "-c", "commit.gpgsign=false",
		"-c", "gc.auto=0", "-c", "maintenance.auto=false"}
	out, err := exec.CommandContext(t.Context(), "git", append(base, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// ciSelectionCopy exports HEAD into a fresh repository and commits it as the
// base. Large paths no selection rule reads are left out to keep it fast.
func ciSelectionCopy(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	dir := t.TempDir()
	script := `set -o pipefail; git -C "$1" archive --format=tar HEAD -- . ` +
		`':(exclude)design.pen' ':(exclude)assets' ':(exclude)docs' ':(exclude)specs' ` +
		`':(exclude)web' | tar -x -C "$2"`
	if out, err := exec.CommandContext(t.Context(), "bash", "-c", script, "bash", root, dir).CombinedOutput(); err != nil {
		t.Fatalf("git archive HEAD: %v\n%s", err, out)
	}
	ciGit(t, dir, "init", "-q")
	ciGit(t, dir, "add", "-A")
	ciGit(t, dir, "commit", "-q", "-m", "base")
	return dir
}

// ciSelectionAppend appends text to a file in the copy, creating it if needed.
func ciSelectionAppend(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(text); err != nil {
		t.Fatalf("append to %s: %v", path, err)
	}
}

// ciSelectionRun commits edit as the head and returns the selector's output.
func ciSelectionRun(t *testing.T, dir string, fullRunLabel bool, edit func(dir string)) ciSelection {
	t.Helper()
	base := ciGit(t, dir, "rev-parse", "HEAD")
	if edit != nil {
		edit(dir)
	}
	ciGit(t, dir, "add", "-A")
	ciGit(t, dir, "commit", "-q", "--allow-empty", "-m", "head")
	head := ciGit(t, dir, "rev-parse", "HEAD")
	output := filepath.Join(t.TempDir(), "github_output")
	cmd := exec.CommandContext(t.Context(), "python3", "hack/ci_scope.py", "e2e", "--base", base, "--head", head)
	cmd.Dir = dir
	// A clean environment: the e2e job's own GITHUB_EVENT_NAME and GITHUB_REF
	// would turn a master push into a full-scope answer here.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"),
		"GITHUB_OUTPUT=" + output}
	if fullRunLabel {
		cmd.Env = append(cmd.Env, "CI_FULL_RUN=true")
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ci_scope.py e2e: %v\n%s", err, out)
	}
	return ciSelectionParse(t, output)
}

// ciSelectionParse decodes the key=<json> lines ci_scope.py writes to GITHUB_OUTPUT.
func ciSelectionParse(t *testing.T, path string) ciSelection {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	raw := map[string]json.RawMessage{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			t.Fatalf("malformed output line %q", line)
		}
		raw[key] = json.RawMessage(value)
	}
	decode := func(key string, into any) {
		if err := json.Unmarshal(raw[key], into); err != nil {
			t.Fatalf("decode %s=%s: %v", key, raw[key], err)
		}
	}
	sel := ciSelection{Flags: map[string]bool{}}
	decode("reason", &sel.Reason)
	decode("e2e-run", &sel.Run)
	var bots struct {
		Test []string `json:"test"`
	}
	decode("bot-matrix", &bots)
	sel.Bots = bots.Test
	for _, key := range []string{"all", "e2e-multicluster", "e2e-upgrade", "e2e-web-live",
		"e2e-capture-overhead", "bots-any"} {
		var value bool
		decode(key, &value)
		sel.Flags[key] = value
	}
	return sel
}

// ciSelectionBucket runs buckets.sh in this package directory.
func ciSelectionBucket(t *testing.T, args ...string) []string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "bash", append([]string{"buckets.sh"}, args...)...).Output()
	if err != nil {
		t.Fatalf("buckets.sh %s: %v", strings.Join(args, " "), err)
	}
	return strings.Fields(string(out))
}

// ciSelectionSuiteTests lists the Test functions compiled into this package.
func ciSelectionSuiteTests(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*_test.go")
	if err != nil {
		t.Fatalf("glob test files: %v", err)
	}
	pattern := regexp.MustCompile(`(?m)^func (Test[A-Za-z0-9_]+)\(`)
	var names []string
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		for _, match := range pattern.FindAllStringSubmatch(string(data), -1) {
			if match[1] != "TestMain" {
				names = append(names, match[1])
			}
		}
	}
	return names
}

// ciSelectionCheckRegexes asserts every emitted -run regex runs at least one
// test of this package and nothing outside its own bucket.
func ciSelectionCheckRegexes(t *testing.T, sel ciSelection) {
	t.Helper()
	suite := ciSelectionSuiteTests(t)
	for bucket, expr := range sel.Run {
		re, err := regexp.Compile(expr)
		if err != nil {
			t.Errorf("bucket %s: bad regex %q: %v", bucket, expr, err)
			continue
		}
		members := map[string]bool{}
		for _, name := range ciSelectionBucket(t, "list", bucket) {
			members[name] = true
		}
		matched := 0
		for _, name := range suite {
			if !re.MatchString(name) {
				continue
			}
			matched++
			if !members[name] {
				t.Errorf("bucket %s regex %q matches %s, which is not in that bucket", bucket, expr, name)
			}
		}
		if matched == 0 {
			t.Errorf("bucket %s regex %q matches no test in this package", bucket, expr)
		}
	}
}

// ciSelectionRequireAll asserts every e2e-go bucket runs its whole buckets.sh regex.
func ciSelectionRequireAll(t *testing.T, sel ciSelection) {
	t.Helper()
	for _, bucket := range ciSelectionBuckets {
		want := strings.Join(ciSelectionBucket(t, "regex", bucket), "")
		if got := sel.Run[bucket]; got != want {
			t.Errorf("bucket %s regex = %q, want the full bucket %q", bucket, got, want)
		}
	}
	for _, key := range []string{"e2e-multicluster", "e2e-upgrade", "e2e-web-live", "bots-any"} {
		if !sel.Flags[key] {
			t.Errorf("%s = false, want true when every suite runs", key)
		}
	}
}

func TestCISelection_AgentOnlyChange(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "agent/internal/console/console.go"), "\n// ci selection probe\n")
	})
	if sel.Reason != "" {
		t.Fatalf("reason = %q, want a partial selection", sel.Reason)
	}
	if !strings.Contains(sel.Run["api-agent"], "TestAPI_ConsolePTYRoundTrip") {
		t.Errorf("api-agent regex %q lacks TestAPI_ConsolePTYRoundTrip", sel.Run["api-agent"])
	}
	for _, bucket := range []string{"api-auth", "ratelimit", "telemetry"} {
		if expr, ok := sel.Run[bucket]; ok {
			t.Errorf("%s selected for an agent-only change: %s", bucket, expr)
		}
	}
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_SentinelOnlyChange(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "sentinel/main.go"), "\n// ci selection probe\n")
	})
	if sel.Reason != "" {
		t.Fatalf("reason = %q, want a partial selection", sel.Reason)
	}
	for _, bucket := range []string{"api-auth", "api-roles", "api-rbac", "api-agent", "telemetry"} {
		if expr, ok := sel.Run[bucket]; ok {
			t.Errorf("%s selected for a sentinel-only change: %s", bucket, expr)
		}
	}
	wake := false
	for _, name := range sel.Bots {
		if name == "TestGameServer_WakeOnConnect_LoginWakes" {
			wake = true
		}
	}
	if !wake {
		t.Errorf("bot matrix %v lacks TestGameServer_WakeOnConnect_LoginWakes", sel.Bots)
	}
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_ChartChangeRunsEverySuite(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "charts/gameplane/values.yaml"), "\n# ci selection probe\n")
	})
	if sel.Reason != "" {
		t.Fatalf("reason = %q, want a partial scope that still selects every suite", sel.Reason)
	}
	ciSelectionRequireAll(t, sel)
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_E2ETestFileSelectsItsTests(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "test/e2e/telemetry_e2e_test.go"), "\n// ci selection probe\n")
	})
	if got, want := sel.Run["telemetry"], "^(TestTelemetryLifecycle)$"; got != want {
		t.Errorf("telemetry regex = %q, want %q", got, want)
	}
	if expr, ok := sel.Run["api-rbac"]; ok {
		t.Errorf("api-rbac selected for a telemetry test edit: %s", expr)
	}
	ciSelectionCheckRegexes(t, sel)
}

func TestCISelection_UnmappedPathRunsEverything(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, false, func(dir string) {
		ciSelectionAppend(t, filepath.Join(dir, "tools/x.sh"), "#!/bin/sh\n")
	})
	if want := "unmapped path tools/x.sh"; sel.Reason != want {
		t.Fatalf("reason = %q, want %q", sel.Reason, want)
	}
	if !sel.Flags["all"] {
		t.Errorf("all = false, want true for an unmapped path")
	}
	ciSelectionRequireAll(t, sel)
}

func TestCISelection_FullRunLabel(t *testing.T) {
	t.Parallel()
	dir := ciSelectionCopy(t)
	sel := ciSelectionRun(t, dir, true, nil)
	if want := "label ci: full-run"; sel.Reason != want {
		t.Fatalf("reason = %q, want %q", sel.Reason, want)
	}
	ciSelectionRequireAll(t, sel)
}
