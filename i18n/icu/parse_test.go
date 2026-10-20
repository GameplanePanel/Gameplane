package icu

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestParseValid(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Message
	}{
		{
			name: "literal",
			in:   "Hello world",
			want: Message{{Kind: KindLiteral, Text: "Hello world"}},
		},
		{
			name: "simple argument",
			in:   "Hi {a}!",
			want: Message{
				{Kind: KindLiteral, Text: "Hi "},
				{Kind: KindArgument, Name: "a"},
				{Kind: KindLiteral, Text: "!"},
			},
		},
		{
			name: "number argument",
			in:   "{n, number}",
			want: Message{{Kind: KindArgument, Name: "n", Format: "number"}},
		},
		{
			name: "date argument with style",
			in:   "{d, date, short}",
			want: Message{{Kind: KindArgument, Name: "d", Format: "date", Style: "short"}},
		},
		{
			name: "plural with exact and category keys and pound",
			in:   "{n, plural, =0 {none} one {# item} other {# items}}",
			want: Message{{
				Kind: KindPlural,
				Name: "n",
				Options: []Option{
					{Key: "=0", Value: Message{{Kind: KindLiteral, Text: "none"}}},
					{Key: "one", Value: Message{{Kind: KindPound}, {Kind: KindLiteral, Text: " item"}}},
					{Key: "other", Value: Message{{Kind: KindPound}, {Kind: KindLiteral, Text: " items"}}},
				},
			}},
		},
		{
			name: "select",
			in:   "{g, select, male {he} other {they}}",
			want: Message{{
				Kind: KindSelect,
				Name: "g",
				Options: []Option{
					{Key: "male", Value: Message{{Kind: KindLiteral, Text: "he"}}},
					{Key: "other", Value: Message{{Kind: KindLiteral, Text: "they"}}},
				},
			}},
		},
		{
			name: "nested select inside plural keeps pound",
			in:   "{n, plural, other {{g, select, other {# x}}}}",
			want: Message{{
				Kind: KindPlural,
				Name: "n",
				Options: []Option{{
					Key: "other",
					Value: Message{{
						Kind: KindSelect,
						Name: "g",
						Options: []Option{{
							Key:   "other",
							Value: Message{{Kind: KindPound}, {Kind: KindLiteral, Text: " x"}},
						}},
					}},
				}},
			}},
		},
		{
			name: "pound outside plural is literal",
			in:   "#1",
			want: Message{{Kind: KindLiteral, Text: "#1"}},
		},
		{
			name: "tag",
			in:   "<b>x</b>",
			want: Message{{Kind: KindTag, Name: "b", Children: Message{{Kind: KindLiteral, Text: "x"}}}},
		},
		{
			name: "tag around argument",
			in:   "<link>{a}</link>",
			want: Message{{Kind: KindTag, Name: "link", Children: Message{{Kind: KindArgument, Name: "a"}}}},
		},
		{
			name: "escaped apostrophe and quoted braces",
			in:   "It''s '{literal}'",
			want: Message{{Kind: KindLiteral, Text: "It's {literal}"}},
		},
		{
			name: "lone apostrophe is literal",
			in:   "don't",
			want: Message{{Kind: KindLiteral, Text: "don't"}},
		},
		{
			name: "unterminated quote runs to end",
			in:   "'{x",
			want: Message{{Kind: KindLiteral, Text: "{x"}},
		},
		{
			name: "less-than not followed by letter is literal",
			in:   "a < b",
			want: Message{{Kind: KindLiteral, Text: "a < b"}},
		},
		{
			name: "quoted tag-like text",
			in:   "'<b>'",
			want: Message{{Kind: KindLiteral, Text: "<b>"}},
		},
		{
			name: "empty message",
			in:   "",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.in)
			if err != nil {
				t.Fatalf("Parse(%q) error: %v", tt.in, err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("Parse(%q)\n got %#v\nwant %#v", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseMalformed(t *testing.T) {
	tests := []struct {
		name string
		in   string
		msg  string
	}{
		{name: "lone open brace", in: "{", msg: "expected argument name"},
		{name: "unterminated argument", in: "{a", msg: "unterminated '{'"},
		{name: "unmatched close brace", in: "a}", msg: "unmatched '}'"},
		{name: "plural without other", in: "{a, plural, one {x}}", msg: "missing other branch"},
		{name: "unknown type", in: "{a, foo}", msg: "unknown argument type"},
		{name: "unclosed tag", in: "<b>x", msg: "missing closing tag </b>"},
		{name: "closing tag without open", in: "x</b>", msg: "closing tag has no matching opening tag"},
		{name: "mismatched closing tag", in: "<b>x</i>", msg: "does not match"},
		{name: "offset not supported", in: "{a, plural, offset:1 other {x}}", msg: "offset: is not supported"},
		{name: "tag with attributes", in: `<b class="x">y</b>`, msg: "must not have attributes"},
		{name: "duplicate key", in: "{a, select, x {1} x {2} other {3}}", msg: "duplicate key"},
		{name: "unknown plural key", in: "{a, plural, mny {x} other {y}}", msg: "unknown plural key"},
		{name: "missing option brace", in: "{a, select, x y other {z}}", msg: "expected '{'"},
		{name: "unterminated style", in: "{d, date, short", msg: "unterminated '{'"},
		{name: "unterminated option", in: "{a, select, other {x", msg: "unterminated '{'"},
		{name: "empty equals key", in: "{a, plural, = {x} other {y}}", msg: "expected digits"},
		{name: "unbalanced brace inside tag", in: "<b>x}</b>", msg: "unmatched '}'"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse(tt.in)
			if err == nil {
				t.Fatalf("Parse(%q) succeeded, want error", tt.in)
			}
			var se *SyntaxError
			if !errors.As(err, &se) {
				t.Fatalf("Parse(%q) error %T is not *SyntaxError", tt.in, err)
			}
			if se.Offset < 0 || se.Offset > len(tt.in) {
				t.Fatalf("Parse(%q) offset %d outside input", tt.in, se.Offset)
			}
			if !strings.Contains(se.Msg, tt.msg) {
				t.Fatalf("Parse(%q) message %q does not contain %q", tt.in, se.Msg, tt.msg)
			}
			if got := se.Error(); got != "offset "+strconv.Itoa(se.Offset)+": "+se.Msg {
				t.Fatalf("Error() = %q", got)
			}
		})
	}
}
