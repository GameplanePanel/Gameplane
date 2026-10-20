package icu

import (
	"fmt"
	"math"
	"testing"
	"time"

	"golang.org/x/text/language"
)

func TestFormatNumericTypes(t *testing.T) {
	plural, err := Parse("{n, plural, one {# item} other {# items}}")
	if err != nil {
		t.Fatal(err)
	}
	number, err := Parse("{n, number}")
	if err != nil {
		t.Fatal(err)
	}
	values := []any{
		int(3), int8(3), int16(3), int32(3), int64(3),
		uint(3), uint8(3), uint16(3), uint32(3), uint64(3),
		float32(3),
	}
	for _, v := range values {
		name := fmt.Sprintf("%T", v)
		if got := Format(plural, language.English, map[string]any{"n": v}); got != "3 items" {
			t.Errorf("plural with %s = %q; want %q", name, got, "3 items")
		}
		if got := Format(number, language.English, map[string]any{"n": v}); got != "3" {
			t.Errorf("number with %s = %q; want %q", name, got, "3")
		}
	}
	// NaN and +Inf have no meaningful count; they must still render without panicking.
	for _, v := range []float64{math.NaN(), math.Inf(1)} {
		if got := Format(plural, language.English, map[string]any{"n": v}); got == "" {
			t.Errorf("plural with %v rendered empty", v)
		}
		if got := Format(number, language.English, map[string]any{"n": v}); got == "" {
			t.Errorf("number with %v rendered empty", v)
		}
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		name string
		tag  language.Tag
		in   string
		args map[string]any
		want string
	}{
		{
			name: "french plural zero",
			tag:  language.French,
			in:   "{n, plural, one {# fichier} other {# fichiers}}",
			args: map[string]any{"n": 0},
			want: "0 fichier",
		},
		{
			name: "french plural one",
			tag:  language.French,
			in:   "{n, plural, one {# fichier} other {# fichiers}}",
			args: map[string]any{"n": 1},
			want: "1 fichier",
		},
		{
			name: "french plural other",
			tag:  language.French,
			in:   "{n, plural, one {# fichier} other {# fichiers}}",
			args: map[string]any{"n": 2},
			want: "2 fichiers",
		},
		{
			name: "polish one",
			tag:  language.Polish,
			in:   "{n, plural, one {# plik} few {# pliki} many {# plików} other {# pliku}}",
			args: map[string]any{"n": 1},
			want: "1 plik",
		},
		{
			name: "polish few",
			tag:  language.Polish,
			in:   "{n, plural, one {# plik} few {# pliki} many {# plików} other {# pliku}}",
			args: map[string]any{"n": 2},
			want: "2 pliki",
		},
		{
			name: "polish many",
			tag:  language.Polish,
			in:   "{n, plural, one {# plik} few {# pliki} many {# plików} other {# pliku}}",
			args: map[string]any{"n": 5},
			want: "5 plików",
		},
		{
			name: "polish few again at 22",
			tag:  language.Polish,
			in:   "{n, plural, one {# plik} few {# pliki} many {# plików} other {# pliku}}",
			args: map[string]any{"n": 22},
			want: "22 pliki",
		},
		{
			name: "arabic zero",
			tag:  language.Arabic,
			in:   "{n, plural, zero {z} one {o} two {t} few {f} many {m} other {x}}",
			args: map[string]any{"n": 0},
			want: "z",
		},
		{
			name: "arabic one",
			tag:  language.Arabic,
			in:   "{n, plural, zero {z} one {o} two {t} few {f} many {m} other {x}}",
			args: map[string]any{"n": 1},
			want: "o",
		},
		{
			name: "arabic two",
			tag:  language.Arabic,
			in:   "{n, plural, zero {z} one {o} two {t} few {f} many {m} other {x}}",
			args: map[string]any{"n": 2},
			want: "t",
		},
		{
			name: "arabic few",
			tag:  language.Arabic,
			in:   "{n, plural, zero {z} one {o} two {t} few {f} many {m} other {x}}",
			args: map[string]any{"n": 3},
			want: "f",
		},
		{
			name: "arabic many",
			tag:  language.Arabic,
			in:   "{n, plural, zero {z} one {o} two {t} few {f} many {m} other {x}}",
			args: map[string]any{"n": 11},
			want: "m",
		},
		{
			name: "arabic other",
			tag:  language.Arabic,
			in:   "{n, plural, zero {z} one {o} two {t} few {f} many {m} other {x}}",
			args: map[string]any{"n": 100},
			want: "x",
		},
		{
			name: "exact match wins over category",
			tag:  language.English,
			in:   "{n, plural, =0 {none} one {# item} other {# items}}",
			args: map[string]any{"n": 0},
			want: "none",
		},
		{
			name: "english one with pound",
			tag:  language.English,
			in:   "{n, plural, =0 {none} one {# item} other {# items}}",
			args: map[string]any{"n": 1},
			want: "1 item",
		},
		{
			name: "float plural uses visible fraction digits",
			tag:  language.English,
			in:   "{n, plural, one {# day} other {# days}}",
			args: map[string]any{"n": 1.5},
			want: "1.5 days",
		},
		{
			name: "string numeric value",
			tag:  language.English,
			in:   "{n, plural, one {# day} other {# days}}",
			args: map[string]any{"n": "1"},
			want: "1 day",
		},
		{
			name: "non numeric plural uses other",
			tag:  language.English,
			in:   "{n, plural, one {x} other {# y}}",
			args: map[string]any{"n": "abc"},
			want: "abc y",
		},
		{
			name: "missing plural argument",
			tag:  language.English,
			in:   "{n, plural, one {x} other {y}}",
			args: nil,
			want: "{n}",
		},
		{
			name: "reordering",
			tag:  language.French,
			in:   "{b} avant {a}",
			args: map[string]any{"a": "x", "b": "y"},
			want: "y avant x",
		},
		{
			name: "missing argument renders placeholder",
			tag:  language.English,
			in:   "Hello {a}",
			args: nil,
			want: "Hello {a}",
		},
		{
			name: "select match",
			tag:  language.English,
			in:   "{g, select, female {she} other {they}}",
			args: map[string]any{"g": "female"},
			want: "she",
		},
		{
			name: "select fallback",
			tag:  language.English,
			in:   "{g, select, female {she} other {they}}",
			args: map[string]any{"g": "x"},
			want: "they",
		},
		{
			name: "select missing argument uses other",
			tag:  language.English,
			in:   "{g, select, female {she} other {they}}",
			args: nil,
			want: "they",
		},
		{
			name: "tags kept",
			tag:  language.English,
			in:   "<b>{a}</b> and <link>x</link>",
			args: map[string]any{"a": "y"},
			want: "<b>y</b> and <link>x</link>",
		},
		{
			name: "english number grouping",
			tag:  language.English,
			in:   "{n, number}",
			args: map[string]any{"n": 1234567},
			want: "1,234,567",
		},
		{
			name: "date format",
			tag:  language.English,
			in:   "{d, date}",
			args: map[string]any{"d": time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)},
			want: "2026-10-11",
		},
		{
			name: "time format",
			tag:  language.English,
			in:   "{d, time}",
			args: map[string]any{"d": time.Date(2026, 10, 11, 9, 30, 0, 0, time.UTC)},
			want: "09:30",
		},
		{
			name: "plain argument uses fmt",
			tag:  language.English,
			in:   "{a}",
			args: map[string]any{"a": 42},
			want: "42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.in, err)
			}
			if got := Format(m, tt.tag, tt.args); got != tt.want {
				t.Fatalf("Format(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
