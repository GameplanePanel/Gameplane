package quiesce

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// minecraftQuiesceSpec declares what the hardcoded minecraftQuiescer
// does — the same block modules/minecraft-java/template.yaml ships.
func minecraftQuiesceSpec() *caps.Quiesce {
	return &caps.Quiesce{
		Quiesce:        []string{"save-off", "save-all flush"},
		Unquiesce:      []string{"save-on"},
		FailurePattern: "saving failed",
	}
}

func TestPick_HalfDeclaredIgnored(t *testing.T) {
	// A half-declared spec (missing unquiesce) is ignored — never pause
	// a game we can't resume.
	if Pick(&caps.Quiesce{Quiesce: []string{"pause"}}).Supported() {
		t.Fatal("spec without unquiesce should be unsupported")
	}
	if Pick(&caps.Quiesce{Unquiesce: []string{"resume"}}).Supported() {
		t.Fatal("spec without quiesce should be unsupported")
	}
}

func TestDeclaredQuiescer_HappyPath(t *testing.T) {
	rc := &fakeRcon{}
	q := Pick(minecraftQuiesceSpec())
	if err := q.Quiesce(rc); err != nil {
		t.Fatalf("Quiesce: %v", err)
	}
	if err := q.Unquiesce(rc); err != nil {
		t.Fatalf("Unquiesce: %v", err)
	}
	want := []string{"save-off", "save-all flush", "save-on"}
	if got := rc.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestDeclaredQuiescer_CommandErrorRollsBack(t *testing.T) {
	rc := &fakeRcon{failNext: map[string]error{"save-all flush": errors.New("connection reset")}}
	q := Pick(minecraftQuiesceSpec())
	if err := q.Quiesce(rc); err == nil {
		t.Fatal("expected error")
	}
	// save-off ran, save-all flush failed → save-on must roll back.
	want := []string{"save-off", "save-all flush", "save-on"}
	if got := rc.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestDeclaredQuiescer_FirstCommandErrorSkipsRollback(t *testing.T) {
	rc := &fakeRcon{failNext: map[string]error{"save-off": errors.New("boom")}}
	q := Pick(minecraftQuiesceSpec())
	if err := q.Quiesce(rc); err == nil {
		t.Fatal("expected error")
	}
	// Nothing was paused yet, so no unquiesce.
	if got := rc.calls(); !reflect.DeepEqual(got, []string{"save-off"}) {
		t.Errorf("calls = %v", got)
	}
}

func TestDeclaredQuiescer_FailurePattern(t *testing.T) {
	rc := &fakeRcon{respond: func(cmd string) (string, error) {
		if cmd == "save-all flush" {
			return "ERROR: Saving FAILED for region r.0.0", nil // case-insensitive match
		}
		return "ok", nil
	}}
	q := Pick(minecraftQuiesceSpec())
	err := q.Quiesce(rc)
	if err == nil || !strings.Contains(err.Error(), "reported failure") {
		t.Fatalf("err = %v", err)
	}
	want := []string{"save-off", "save-all flush", "save-on"}
	if got := rc.calls(); !reflect.DeepEqual(got, want) {
		t.Errorf("calls = %v, want %v", got, want)
	}
}

func TestDeclaredQuiescer_InvalidFailurePatternIgnored(t *testing.T) {
	rc := &fakeRcon{respond: func(string) (string, error) { return "saving failed", nil }}
	spec := minecraftQuiesceSpec()
	spec.FailurePattern = "(unclosed"
	q := Pick(spec)
	if !q.Supported() {
		t.Fatal("bad pattern must not disable quiesce entirely")
	}
	// With the pattern broken, output checking is off — the sequence
	// still runs and succeeds.
	if err := q.Quiesce(rc); err != nil {
		t.Fatalf("Quiesce: %v", err)
	}
}

func TestDeclaredQuiescer_UnquiesceReportsFirstError(t *testing.T) {
	rc := &fakeRcon{failNext: map[string]error{"resume-a": errors.New("boom")}}
	q := Pick(&caps.Quiesce{
		Quiesce:   []string{"pause"},
		Unquiesce: []string{"resume-a", "resume-b"},
	})
	if err := q.Unquiesce(rc); err == nil {
		t.Fatal("expected error")
	}
	// Both resume commands still run (best effort).
	if got := rc.calls(); !reflect.DeepEqual(got, []string{"resume-a", "resume-b"}) {
		t.Errorf("calls = %v", got)
	}
}
