package pack

import (
	"strings"
	"testing"
)

func codes(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Code)
	}
	return out
}

func hasCode(fs []Finding, code string) bool {
	for _, f := range fs {
		if f.Code == code {
			return true
		}
	}
	return false
}

func TestFindingString(t *testing.T) {
	f := Finding{Code: CodeUnusedID, Severity: SeverityError, File: "en/app.json", ID: "a.b", Message: "not used"}
	want := "en/app.json: a.b: [unused-id] not used"
	if got := f.String(); got != want {
		t.Fatalf("String() = %q, want %q", got, want)
	}
}

func TestCheckMarkup(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "plain", raw: "Hello", want: ""},
		{name: "allowed tags", raw: "<b>x</b> <link>y</link>", want: ""},
		{name: "other tag", raw: "<i>x</i>", want: "uses tag <i>; only <b> and <link> are allowed"},
		{name: "entity", raw: "a &amp; b", want: "contains an HTML entity"},
		{name: "numeric entity", raw: "&#39;", want: "contains an HTML entity"},
		{name: "javascript link", raw: "<link>JavaScript:alert(1)</link>", want: "contains a javascript: link"},
		{name: "script tag", raw: "x '<script>'", want: "contains a script tag"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := mustParse(t, tt.raw)
			if got := CheckMarkup(tt.raw, m); got != tt.want {
				t.Fatalf("CheckMarkup = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateReferenceCodes(t *testing.T) {
	ref := map[string]Entry{
		"ok":         {DefaultMessage: "Hi <b>there</b>", Description: "greeting"},
		"syntax":     {DefaultMessage: "Broken {", Description: "x"},
		"markup":     {DefaultMessage: "<i>no</i>", Description: "x"},
		"nodesc":     {DefaultMessage: "Plain"},
		"whitespace": {DefaultMessage: "Plain", Description: "   "},
	}
	fs := ValidateReference(ref, "en/app.json")
	for _, want := range []string{CodeICUSyntax, CodeMarkup, CodeMissingDescription} {
		if !hasCode(fs, want) {
			t.Errorf("missing code %s in %v", want, codes(fs))
		}
	}
	for _, f := range fs {
		if f.ID == "ok" {
			t.Errorf("unexpected finding for ok: %v", f)
		}
		if f.Severity != SeverityError || f.File != "en/app.json" {
			t.Errorf("bad finding shape: %+v", f)
		}
	}
}

func TestValidateMetaCodes(t *testing.T) {
	good := `{"tag":"fr","nativeName":"Français","englishName":"French","direction":"ltr"}`
	if fs := ValidateMeta([]byte(good), "fr", "fr/meta.json"); len(fs) != 0 {
		t.Fatalf("valid meta produced findings: %v", fs)
	}
	tests := map[string]string{
		"unknown key":   `{"tag":"fr","nativeName":"a","englishName":"b","direction":"ltr","x":1}`,
		"missing tag":   `{"nativeName":"a","englishName":"b","direction":"ltr"}`,
		"bad tag":       `{"tag":"f","nativeName":"a","englishName":"b","direction":"ltr"}`,
		"dir mismatch":  `{"tag":"de","nativeName":"a","englishName":"b","direction":"ltr"}`,
		"bad direction": `{"tag":"fr","nativeName":"a","englishName":"b","direction":"up"}`,
		"long name":     `{"tag":"fr","nativeName":"` + strings.Repeat("x", 65) + `","englishName":"b","direction":"ltr"}`,
		"no native":     `{"tag":"fr","englishName":"b","direction":"ltr"}`,
	}
	for name, in := range tests {
		fs := ValidateMeta([]byte(in), "fr", "fr/meta.json")
		if len(fs) == 0 {
			t.Errorf("%s: expected findings", name)
			continue
		}
		for _, f := range fs {
			if f.Code != CodeInvalidMeta || f.ID != IDMeta {
				t.Errorf("%s: bad finding %+v", name, f)
			}
		}
	}
}

func TestValidateTranslationCodes(t *testing.T) {
	ref := map[string]Entry{
		"hello":   {DefaultMessage: "Hello {name}", Description: "greet"},
		"files":   {DefaultMessage: "{n, plural, one {# file} other {# files}}", Description: "count"},
		"link":    {DefaultMessage: "See <link>docs</link>", Description: "x"},
		"changed": {DefaultMessage: "New text", Description: "x"},
	}
	tr := map[string]Entry{
		"hello":   {DefaultMessage: "Bonjour"},
		"files":   {DefaultMessage: "{n, plural, one {# fichier} other {# fichiers}}"},
		"gone":    {DefaultMessage: "Old"},
		"link":    {DefaultMessage: "Voir <i>docs</i>"},
		"changed": {DefaultMessage: "Nouveau", SourceHash: "000000000000"},
	}
	// Translation with bad syntax.
	tr["bad"] = Entry{DefaultMessage: "{x"}
	ref["bad"] = Entry{DefaultMessage: "x", Description: "x"}
	// Extra placeholder.
	ref["extra"] = Entry{DefaultMessage: "Plain", Description: "x"}
	tr["extra"] = Entry{DefaultMessage: "Plain {who}"}

	fs := ValidateTranslation(ref, tr, "fr/app.json")
	want := map[string]string{
		"hello":   CodeMissingPlaceholder,
		"extra":   CodeExtraPlaceholder,
		"gone":    CodeUnknownID,
		"bad":     CodeICUSyntax,
		"link":    CodeMarkup,
		"changed": CodeNeedsReview,
	}
	for id, code := range want {
		found := false
		for _, f := range fs {
			if f.ID == id && f.Code == code {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: missing %s in %v", id, code, fs)
		}
	}
	for _, f := range fs {
		if f.ID == "files" {
			t.Errorf("unexpected finding for files: %v", f)
		}
		if f.Code == CodeNeedsReview && f.Severity != SeverityInfo {
			t.Errorf("needs-review must be info, got %s", f.Severity)
		}
	}
}

func TestValidateTranslationNeedsReviewOnlyWhenNoErrors(t *testing.T) {
	ref := map[string]Entry{"a": {DefaultMessage: "A {x}", Description: "d"}}
	tr := map[string]Entry{"a": {DefaultMessage: "B", SourceHash: "000000000000"}}
	fs := ValidateTranslation(ref, tr, "fr/app.json")
	if hasCode(fs, CodeNeedsReview) {
		t.Fatalf("needs-review reported alongside an error: %v", fs)
	}
	if !hasCode(fs, CodeMissingPlaceholder) {
		t.Fatalf("expected missing-placeholder: %v", fs)
	}
}

func TestSortFindings(t *testing.T) {
	fs := []Finding{
		{File: "b", ID: "x", Code: "a"},
		{File: "a", ID: "z", Code: "a"},
		{File: "a", ID: "y", Code: "b"},
		{File: "a", ID: "y", Code: "a"},
	}
	SortFindings(fs)
	want := []string{"a/y/a", "a/y/b", "a/z/a", "b/x/a"}
	for i, f := range fs {
		if got := f.File + "/" + f.ID + "/" + f.Code; got != want[i] {
			t.Fatalf("pos %d = %s, want %s", i, got, want[i])
		}
	}
}
