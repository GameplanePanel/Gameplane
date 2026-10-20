package pack

import "testing"

func TestEntryStatus(t *testing.T) {
	ref := Entry{DefaultMessage: "Hi {name}", Description: "greet"}
	tests := []struct {
		name    string
		tr      Entry
		present bool
		want    Status
	}{
		{name: "absent", present: false, want: StatusMissing},
		{name: "empty message", tr: Entry{}, present: true, want: StatusMissing},
		{name: "bad syntax", tr: Entry{DefaultMessage: "Salut {name"}, present: true, want: StatusInvalid},
		{name: "placeholder mismatch", tr: Entry{DefaultMessage: "Salut"}, present: true, want: StatusInvalid},
		{name: "markup problem", tr: Entry{DefaultMessage: "<i>Salut {name}</i>"}, present: true, want: StatusInvalid},
		{name: "stale", tr: Entry{DefaultMessage: "Salut {name}", SourceHash: "000000000000"}, present: true, want: StatusNeedsReview},
		{name: "current hash", tr: Entry{DefaultMessage: "Salut {name}", SourceHash: SourceHash(ref.DefaultMessage)}, present: true, want: StatusTranslated},
		{name: "translated", tr: Entry{DefaultMessage: "Salut {name}"}, present: true, want: StatusTranslated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := EntryStatus(ref, tt.tr, tt.present); got != tt.want {
				t.Fatalf("EntryStatus = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStatusesAndCompleteness(t *testing.T) {
	ref := map[string]Entry{
		"a": {DefaultMessage: "A", Description: "d"},
		"b": {DefaultMessage: "B", Description: "d"},
	}
	if got := Completeness(ref, nil); got != 0 {
		t.Fatalf("Completeness empty translation = %v, want 0", got)
	}
	half := map[string]Entry{"a": {DefaultMessage: "AA"}}
	if got := Completeness(ref, half); got != 50 {
		t.Fatalf("Completeness half = %v, want 50", got)
	}
	stale := map[string]Entry{
		"a": {DefaultMessage: "AA"},
		"b": {DefaultMessage: "BB", SourceHash: "000000000000"},
	}
	if got := Completeness(ref, stale); got != 100 {
		t.Fatalf("Completeness with needs-review = %v, want 100", got)
	}
	st := Statuses(ref, half)
	if st["a"] != StatusTranslated || st["b"] != StatusMissing {
		t.Fatalf("Statuses = %v", st)
	}
	if got := Completeness(map[string]Entry{}, nil); got != 100 {
		t.Fatalf("Completeness empty reference = %v, want 100", got)
	}
}

func TestListed(t *testing.T) {
	ltr := Meta{Direction: "ltr"}
	rtl := Meta{Direction: "rtl"}
	tests := []struct {
		name string
		tag  string
		meta Meta
		pct  float64
		want bool
	}{
		{name: "english always", tag: "en", meta: Meta{}, pct: 0, want: true},
		{name: "just below", tag: "fr", meta: ltr, pct: 79.9, want: false},
		{name: "at threshold", tag: "fr", meta: ltr, pct: 80, want: true},
		{name: "complete", tag: "fr", meta: ltr, pct: 100, want: true},
		{name: "rtl excluded", tag: "ar", meta: rtl, pct: 100, want: false},
		{name: "pseudo locale excluded", tag: "en-XA", meta: ltr, pct: 100, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Listed(tt.tag, tt.meta, tt.pct); got != tt.want {
				t.Fatalf("Listed = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidDropsInvalidEntries(t *testing.T) {
	ref := map[string]Entry{
		"ok":      {DefaultMessage: "Hi {n}", Description: "d"},
		"bad":     {DefaultMessage: "Hi", Description: "d"},
		"stale":   {DefaultMessage: "Yo", Description: "d"},
		"missing": {DefaultMessage: "Miss", Description: "d"},
	}
	tr := map[string]Entry{
		"ok":      {DefaultMessage: "Salut {n}"},
		"bad":     {DefaultMessage: "Salut {n}"},
		"stale":   {DefaultMessage: "Salut", SourceHash: "000000000000"},
		"unknown": {DefaultMessage: "Nope"},
	}
	got := Valid(ref, tr)
	if _, ok := got["ok"]; !ok {
		t.Error("ok dropped")
	}
	if _, ok := got["stale"]; !ok {
		t.Error("needs-review entry dropped")
	}
	if _, ok := got["bad"]; ok {
		t.Error("placeholder-mismatched entry kept")
	}
	if _, ok := got["unknown"]; ok {
		t.Error("id not in reference kept")
	}
	if len(got) != 2 {
		t.Fatalf("Valid returned %d entries, want 2", len(got))
	}
}
