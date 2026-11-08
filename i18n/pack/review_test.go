package pack

import (
	"reflect"
	"strings"
	"testing"
)

// TestParseReviewCases checks ParseReview across valid and invalid app review inputs.
func TestParseReviewCases(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    map[string]string
		wantErr string
	}{
		{name: "valid", data: `{"a":"000000000000","b":"111111111111"}`, want: map[string]string{"a": "000000000000", "b": "111111111111"}},
		{name: "empty object", data: `{}`, want: map[string]string{}},
		{name: "null", data: `null`, wantErr: "top level must be a JSON object"},
		{name: "array", data: `["a"]`, wantErr: "pack: parse review:"},
		{name: "non-string value", data: `{"a":3}`, wantErr: "pack: parse review:"},
		{name: "empty hash", data: `{"a":""}`, wantErr: `id "a" has an empty hash`},
		{name: "null value", data: `{"a":null}`, wantErr: `id "a" has an empty hash`},
		{name: "trailing data", data: `{"a":"x"} {}`, wantErr: "unexpected data after JSON value"},
		{name: "syntax error", data: `[`, wantErr: "pack: parse review:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseReview([]byte(tc.data))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v; want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v; want %v", got, tc.want)
			}
		})
	}
}

// TestMarshalReviewRoundTripAndBytes checks that MarshalReview round-trips through ParseReview and produces the expected bytes.
func TestMarshalReviewRoundTripAndBytes(t *testing.T) {
	data, err := MarshalReview(map[string]string{"b": "y", "a": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"a\": \"x\",\n  \"b\": \"y\"\n}\n"; string(data) != want {
		t.Errorf("bytes = %q; want %q", data, want)
	}
	back, err := ParseReview(data)
	if err != nil {
		t.Fatal(err)
	}
	if back["a"] != "x" || back["b"] != "y" {
		t.Errorf("round trip = %v", back)
	}
}

// TestMarshalReviewNilEncodesEmptyObject checks that a nil review map marshals to an empty JSON object.
func TestMarshalReviewNilEncodesEmptyObject(t *testing.T) {
	data, err := MarshalReview(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{}\n" {
		t.Errorf("nil = %q; want %q", data, "{}\n")
	}
}

// TestParseModuleReviewCases checks ParseModuleReview across valid and invalid module review inputs.
func TestParseModuleReviewCases(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    map[string]map[string]string
		wantErr string
	}{
		{name: "valid", data: `{"fr":{"x":"000000000000"},"de":{}}`, want: map[string]map[string]string{"fr": {"x": "000000000000"}, "de": {}}},
		{name: "null", data: `null`, wantErr: "top level must be a JSON object"},
		{name: "array", data: `[]`, wantErr: "pack: parse module review:"},
		{name: "inner not object", data: `{"fr":"x"}`, wantErr: "pack: parse module review:"},
		{name: "inner null", data: `{"fr":null}`, wantErr: `tag "fr" must map to an object`},
		{name: "invalid tag", data: `{"not a tag":{}}`, wantErr: `"not a tag" is not a valid language tag`},
		{name: "empty hash", data: `{"fr":{"x":""}}`, wantErr: `tag "fr" id "x" has an empty hash`},
		{name: "non-string hash", data: `{"fr":{"x":1}}`, wantErr: "pack: parse module review:"},
		{name: "trailing data", data: `{} []`, wantErr: "unexpected data after JSON value"},
		{name: "syntax error", data: `{`, wantErr: "pack: parse module review:"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseModuleReview([]byte(tc.data))
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v; want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v; want %v", got, tc.want)
			}
		})
	}
}

// TestMarshalModuleReviewRoundTrip checks that MarshalModuleReview round-trips through ParseModuleReview.
func TestMarshalModuleReviewRoundTrip(t *testing.T) {
	data, err := MarshalModuleReview(map[string]map[string]string{
		"fr":  {"x": "111111111111"},
		"nil": nil,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "{\n  \"fr\": {\n    \"x\": \"111111111111\"\n  },\n  \"nil\": {}\n}\n"
	if string(data) != want {
		t.Errorf("bytes = %q; want %q", data, want)
	}
	back, err := ParseModuleReview(data)
	if err != nil {
		t.Fatal(err)
	}
	if back["fr"]["x"] != "111111111111" {
		t.Errorf("round trip = %v", back)
	}
}

// TestMarshalModuleReviewInvalidTagRejectedOnParse checks that an invalid language tag in a module review is rejected on parse.
func TestMarshalModuleReviewInvalidTagRejectedOnParse(t *testing.T) {
	data, err := MarshalModuleReview(map[string]map[string]string{"BAD TAG": {"x": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseModuleReview(data); err == nil {
		t.Error("parse accepted an invalid tag")
	}
}

// TestApplyReview checks that ApplyReview applies review hashes to translated entries.
func TestApplyReview(t *testing.T) {
	entries := map[string]Entry{
		"a": {DefaultMessage: "A", SourceHash: "inline00000"},
		"b": {DefaultMessage: "B"},
	}
	hashes := map[string]string{"a": "review000000", "b": "bbbbbbbbbbbb", "zzz": "cccccccccccc"}

	out := ApplyReview(entries, hashes)
	if out["a"].SourceHash != "review000000" {
		t.Errorf("a hash = %q; want review hash to override inline", out["a"].SourceHash)
	}
	if out["b"].SourceHash != "bbbbbbbbbbbb" {
		t.Errorf("b hash = %q", out["b"].SourceHash)
	}
	if _, ok := out["zzz"]; ok {
		t.Error("id only in review was added")
	}
	if entries["a"].SourceHash != "inline00000" || entries["b"].SourceHash != "" {
		t.Error("input entries were modified")
	}
}

// TestSplitReview checks that SplitReview separates review hashes from inline translation content.
func TestSplitReview(t *testing.T) {
	entries := map[string]Entry{
		"a": {DefaultMessage: "A", Description: "d", SourceHash: "aaaaaaaaaaaa"},
		"b": {DefaultMessage: "B"},
	}
	stripped, hashes := SplitReview(entries)
	if stripped["a"].SourceHash != "" || stripped["a"].DefaultMessage != "A" || stripped["a"].Description != "d" {
		t.Errorf("stripped a = %+v", stripped["a"])
	}
	if want := map[string]string{"a": "aaaaaaaaaaaa"}; !reflect.DeepEqual(hashes, want) {
		t.Errorf("hashes = %v; want %v", hashes, want)
	}
	if entries["a"].SourceHash != "aaaaaaaaaaaa" {
		t.Error("input entries were modified")
	}

	none, empty := SplitReview(map[string]Entry{"b": {DefaultMessage: "B"}})
	if empty == nil || len(empty) != 0 {
		t.Errorf("hashes = %v; want empty non-nil map", empty)
	}
	if len(none) != 1 {
		t.Errorf("stripped = %v", none)
	}
}

// TestHasInlineHashes checks that HasInlineHashes detects inline source hashes in translation content.
func TestHasInlineHashes(t *testing.T) {
	if !HasInlineHashes(map[string]Entry{"a": {DefaultMessage: "A", SourceHash: "x"}}) {
		t.Error("HasInlineHashes = false; want true")
	}
	if HasInlineHashes(map[string]Entry{"a": {DefaultMessage: "A"}}) {
		t.Error("HasInlineHashes = true; want false")
	}
	if HasInlineHashes(nil) {
		t.Error("HasInlineHashes(nil) = true; want false")
	}
}
