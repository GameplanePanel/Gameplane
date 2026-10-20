package icu

import (
	"reflect"
	"testing"
)

func TestArguments(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "simple", in: "{a} and {b}", want: []string{"a", "b"}},
		{name: "plural name and nested option args", in: "{n, plural, one {{x}} other {# {y}}}", want: []string{"n", "x", "y"}},
		{name: "select name and tag children", in: "{g, select, other {<b>{a}</b>}}", want: []string{"a", "g"}},
		{name: "deeply nested", in: "{n, plural, other {{g, select, other {{deep}}}}}", want: []string{"deep", "g", "n"}},
		{name: "none", in: "plain", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := SortedKeys(Arguments(m))
			if !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Fatalf("Arguments() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want []string
	}{
		{name: "single", in: "<b>x</b>", want: []string{"b"}},
		{name: "nested", in: "<b><link>{a}</link></b>", want: []string{"b", "link"}},
		{name: "inside plural option", in: "{n, plural, other {<b>#</b>}}", want: []string{"b"}},
		{name: "none", in: "{a}", want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			got := SortedKeys(Tags(m))
			if !reflect.DeepEqual(got, tt.want) && (len(got) != 0 || len(tt.want) != 0) {
				t.Fatalf("Tags() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestSortedKeys(t *testing.T) {
	got := SortedKeys(map[string]struct{}{"c": {}, "a": {}, "b": {}})
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SortedKeys = %v, want %v", got, want)
	}
}
