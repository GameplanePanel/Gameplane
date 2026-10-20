package pack

import "testing"

func TestStampRecordsHashOnTranslatedEntries(t *testing.T) {
	t.Parallel()
	ref := map[string]Entry{
		"a": {DefaultMessage: "Hello"},
		"b": {DefaultMessage: "World"},
		"c": {DefaultMessage: "Kept"},
	}
	tr := map[string]Entry{
		"a": {DefaultMessage: "Bonjour"},
		"b": {DefaultMessage: "{broken"}, // invalid ICU: not translated
		"c": {DefaultMessage: "Garde", SourceHash: "abc"},
		"x": {DefaultMessage: "Unknown id"},
	}
	out, changed := Stamp(ref, tr)
	if !changed {
		t.Fatal("expected a change")
	}
	if got := out["a"].SourceHash; got != SourceHash("Hello") {
		t.Errorf("a hash = %q", got)
	}
	if out["b"].SourceHash != "" || out["x"].SourceHash != "" {
		t.Errorf("invalid or unknown entries must not be stamped: %+v", out)
	}
	if out["c"].SourceHash != "abc" {
		t.Errorf("existing hash replaced: %q", out["c"].SourceHash)
	}
	if tr["a"].SourceHash != "" {
		t.Error("input map was modified")
	}
	if _, again := Stamp(ref, out); again {
		t.Error("second stamp must be a no-op")
	}
}
