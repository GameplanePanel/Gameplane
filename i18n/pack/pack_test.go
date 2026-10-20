package pack

import (
	"bytes"
	"errors"
	"testing"

	"github.com/GameplanePanel/gameplane/i18n/icu"
)

func TestParseEntries(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		wantErr bool
		wantLen int
	}{
		{name: "valid", in: `{"a":{"defaultMessage":"Hi","description":"greet"}}`, wantLen: 1},
		{name: "empty object", in: `{}`, wantLen: 0},
		{name: "null top level", in: `null`, wantErr: true},
		{name: "array top level", in: `[]`, wantErr: true},
		{name: "unknown key", in: `{"a":{"defaultMessage":"x","extra":1}}`, wantErr: true},
		{name: "missing defaultMessage", in: `{"a":{"description":"x"}}`, wantErr: true},
		{name: "null entry", in: `{"a":null}`, wantErr: true},
		{name: "trailing data", in: `{} {}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseEntries([]byte(tt.in))
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseEntries err = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && len(got) != tt.wantLen {
				t.Fatalf("len = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestParseMeta(t *testing.T) {
	good := `{"tag":"fr","nativeName":"Français","englishName":"French","direction":"ltr","credits":[{"name":"A"}]}`
	m, err := ParseMeta([]byte(good))
	if err != nil {
		t.Fatalf("ParseMeta: %v", err)
	}
	if m.Tag != "fr" || len(m.Credits) != 1 {
		t.Fatalf("unexpected meta %+v", m)
	}
	for name, in := range map[string]string{
		"unknown key":   `{"tag":"fr","bogus":1}`,
		"trailing data": good + ` {}`,
		"not an object": `"fr"`,
	} {
		if _, err := ParseMeta([]byte(in)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestMarshalRoundTripIsByteExact(t *testing.T) {
	data := []byte("{\n  \"a\": {\n    \"defaultMessage\": \"<b>Hi</b> & more\",\n    \"description\": \"greet\"\n  },\n  \"b\": {\n    \"defaultMessage\": \"Bye\",\n    \"description\": \"farewell\",\n    \"sourceHash\": \"0123456789ab\"\n  }\n}\n")
	entries, err := ParseEntries(data)
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	out, err := Marshal(entries)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !bytes.Equal(out, data) {
		t.Fatalf("round trip differs:\n%s\nwant:\n%s", out, data)
	}
}

func TestMarshalSortsKeysAndEmptyMap(t *testing.T) {
	out, err := Marshal(map[string]Entry{
		"z": {DefaultMessage: "Z"},
		"a": {DefaultMessage: "A"},
	})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if want := "{\n  \"a\": {\n    \"defaultMessage\": \"A\"\n  },\n  \"z\": {\n    \"defaultMessage\": \"Z\"\n  }\n}\n"; string(out) != want {
		t.Fatalf("got %q, want %q", out, want)
	}
	empty, err := Marshal(map[string]Entry{})
	if err != nil {
		t.Fatalf("Marshal empty: %v", err)
	}
	if string(empty) != "{}\n" {
		t.Fatalf("empty = %q, want {}\\n", empty)
	}
}

func TestSourceHash(t *testing.T) {
	h := SourceHash("Hello")
	if len(h) != 12 {
		t.Fatalf("len = %d, want 12", len(h))
	}
	if h != SourceHash("Hello") {
		t.Fatal("SourceHash is not stable")
	}
	if h == SourceHash("Hello!") {
		t.Fatal("SourceHash does not depend on input")
	}
}

func TestValidTag(t *testing.T) {
	for tag, want := range map[string]bool{
		"en": true, "fr": true, "fr-CA": true, "zh-Hant": true, "zh-Hant-TW": true,
		"es-419": true, "en-XA": true,
		"": false, "FR": false, "fr_CA": false, "english": false, "fr-ca": false,
	} {
		if got := ValidTag(tag); got != want {
			t.Errorf("ValidTag(%q) = %v, want %v", tag, got, want)
		}
	}
}

func TestParseErrorsWrapped(t *testing.T) {
	_, err := ParseEntries([]byte(`{"a":{"defaultMessage":"x","nope":1}}`))
	if err == nil {
		t.Fatal("expected error")
	}
	if errors.Unwrap(err) == nil {
		t.Fatalf("error %v does not wrap a cause", err)
	}
}

func mustParse(t *testing.T, s string) icu.Message {
	t.Helper()
	m, err := icu.Parse(s)
	if err != nil {
		t.Fatalf("icu.Parse(%q): %v", s, err)
	}
	return m
}
