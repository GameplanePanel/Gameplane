// Package icu parses and formats the ICU MessageFormat subset used by the
// Gameplane language packs: simple arguments, number/date/time formats,
// plural and select, tags for inline markup, and apostrophe quoting.
package icu

import (
	"fmt"
	"strings"
)

// Kind identifies the type of a parsed Node.
type Kind int

// Node kinds produced by Parse.
const (
	KindLiteral  Kind = iota // plain text
	KindArgument             // {name} or {name, number|date|time[, style]}
	KindPlural               // {name, plural, ...}
	KindSelect               // {name, select, ...}
	KindPound                // # inside a plural option
	KindTag                  // <name>children</name>
)

// Node is one element of a parsed message.
type Node struct {
	Kind     Kind
	Text     string   // literal text (unescaped)
	Name     string   // argument name or tag name
	Format   string   // "", "number", "date", "time" (KindArgument only)
	Style    string   // optional style after second comma, trimmed
	Options  []Option // plural/select branches, in source order
	Children []Node   // tag body
}

// Option is one branch of a plural or select.
type Option struct {
	Key   string // "=3", "one", "other", or a select key
	Value []Node
}

// Message is a parsed ICU message.
type Message []Node

// SyntaxError reports a malformed message at a byte offset.
type SyntaxError struct {
	Offset int
	Msg    string
}

// Error implements error.
func (e *SyntaxError) Error() string {
	return fmt.Sprintf("offset %d: %s", e.Offset, e.Msg)
}

// ctx describes the lexical context in which a sequence is parsed.
type ctx struct {
	plural bool   // '#' is a pound inside the nearest enclosing plural
	option bool   // inside a plural/select option; an unescaped '}' ends it
	tag    string // inside a tag body; a closing tag ends it
}

type parser struct {
	s   string
	pos int
}

// Parse parses an ICU message. Errors are *SyntaxError values carrying the
// byte offset of the problem.
func Parse(s string) (Message, error) {
	p := &parser{s: s}
	msg, err := p.parseSeq(ctx{})
	if err != nil {
		return nil, err
	}
	if p.pos < len(p.s) {
		return nil, p.errAt(p.pos, "unmatched '}'")
	}
	return msg, nil
}

func (p *parser) errAt(off int, msg string) error {
	return &SyntaxError{Offset: off, Msg: msg}
}

func (p *parser) parseSeq(c ctx) (Message, error) {
	var out []Node
	var lit strings.Builder
	flush := func() {
		if lit.Len() > 0 {
			out = append(out, Node{Kind: KindLiteral, Text: lit.String()})
			lit.Reset()
		}
	}
	for p.pos < len(p.s) {
		ch := p.s[p.pos]
		switch {
		case ch == '\'':
			p.apostrophe(c, &lit)
		case ch == '{':
			flush()
			n, err := p.parseArgument(c)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		case ch == '}':
			if c.option {
				flush()
				return out, nil
			}
			return nil, p.errAt(p.pos, "unmatched '}'")
		case ch == '#':
			if c.plural {
				flush()
				out = append(out, Node{Kind: KindPound})
			} else {
				lit.WriteByte('#')
			}
			p.pos++
		case ch == '<' && p.pos+1 < len(p.s) && p.s[p.pos+1] == '/':
			if c.tag != "" {
				flush()
				return out, nil
			}
			return nil, p.errAt(p.pos, "closing tag has no matching opening tag")
		case ch == '<' && p.pos+1 < len(p.s) && isLetter(p.s[p.pos+1]):
			flush()
			n, err := p.parseTag(c)
			if err != nil {
				return nil, err
			}
			out = append(out, n)
		default:
			lit.WriteByte(ch)
			p.pos++
		}
	}
	flush()
	return out, nil
}

// apostrophe handles ICU apostrophe escaping. Two adjacent apostrophes are a
// literal apostrophe. A quote before '{', '}', '<' or '|' (or '#' inside a plural) starts quoted
// text that runs to the next lone apostrophe. Any other apostrophe is literal.
func (p *parser) apostrophe(c ctx, lit *strings.Builder) {
	next := byte(0)
	if p.pos+1 < len(p.s) {
		next = p.s[p.pos+1]
	}
	switch {
	case next == '\'':
		lit.WriteByte('\'')
		p.pos += 2
	case next == '{' || next == '}' || next == '<' || next == '|' || (next == '#' && c.plural):
		p.pos++ // opening quote
		for p.pos < len(p.s) {
			b := p.s[p.pos]
			if b == '\'' {
				if p.pos+1 < len(p.s) && p.s[p.pos+1] == '\'' {
					lit.WriteByte('\'')
					p.pos += 2
					continue
				}
				p.pos++ // closing quote
				return
			}
			lit.WriteByte(b)
			p.pos++
		}
	default:
		lit.WriteByte('\'')
		p.pos++
	}
}

// parseArgument parses a brace expression starting at '{'.
func (p *parser) parseArgument(c ctx) (Node, error) {
	open := p.pos
	p.pos++ // '{'
	p.skipSpace()
	name, ok := p.parseName(false)
	if !ok {
		return Node{}, p.errAt(p.pos, "expected argument name")
	}
	p.skipSpace()
	if p.pos >= len(p.s) {
		return Node{}, p.errAt(open, "unterminated '{'")
	}
	if p.s[p.pos] == '}' {
		p.pos++
		return Node{Kind: KindArgument, Name: name}, nil
	}
	if p.s[p.pos] != ',' {
		return Node{}, p.errAt(p.pos, "expected ',' or '}' after argument name")
	}
	p.pos++
	p.skipSpace()
	typOff := p.pos
	typ := p.parseWord()
	p.skipSpace()
	if p.pos >= len(p.s) {
		return Node{}, p.errAt(open, "unterminated '{'")
	}

	switch typ {
	case "number", "date", "time":
		n := Node{Kind: KindArgument, Name: name, Format: typ}
		if p.s[p.pos] == '}' {
			p.pos++
			return n, nil
		}
		if p.s[p.pos] != ',' {
			return Node{}, p.errAt(p.pos, "expected ',' or '}' after format")
		}
		p.pos++
		end := strings.IndexByte(p.s[p.pos:], '}')
		if end < 0 {
			return Node{}, p.errAt(open, "unterminated '{'")
		}
		n.Style = strings.TrimSpace(p.s[p.pos : p.pos+end])
		p.pos += end + 1
		return n, nil
	case "plural", "select":
		return p.parseChoice(c, name, typ, open)
	case "":
		return Node{}, p.errAt(typOff, "expected argument type")
	default:
		return Node{}, p.errAt(typOff, fmt.Sprintf("unknown argument type %q", typ))
	}
}

// parseChoice parses the option list of a plural or select. p.pos is at the
// comma that follows the type keyword.
func (p *parser) parseChoice(c ctx, name, typ string, open int) (Node, error) {
	if p.s[p.pos] != ',' {
		return Node{}, p.errAt(p.pos, fmt.Sprintf("expected ',' after %s", typ))
	}
	p.pos++
	n := Node{Kind: KindPlural, Name: name}
	if typ == "select" {
		n.Kind = KindSelect
	}
	seen := make(map[string]struct{})
	for {
		p.skipSpace()
		if p.pos >= len(p.s) {
			return Node{}, p.errAt(open, "unterminated '{'")
		}
		if p.s[p.pos] == '}' {
			p.pos++
			break
		}
		keyOff := p.pos
		key, err := p.parseKey(typ)
		if err != nil {
			return Node{}, err
		}
		if _, dup := seen[key]; dup {
			return Node{}, p.errAt(keyOff, fmt.Sprintf("duplicate key %q", key))
		}
		seen[key] = struct{}{}
		p.skipSpace()
		if p.pos >= len(p.s) {
			return Node{}, p.errAt(open, "unterminated '{'")
		}
		if p.s[p.pos] != '{' {
			return Node{}, p.errAt(p.pos, fmt.Sprintf("expected '{' after %q", key))
		}
		p.pos++
		optCtx := ctx{plural: c.plural, option: true}
		if typ == "plural" {
			optCtx.plural = true
		}
		value, err := p.parseSeq(optCtx)
		if err != nil {
			return Node{}, err
		}
		if p.pos >= len(p.s) {
			return Node{}, p.errAt(open, "unterminated '{'")
		}
		p.pos++ // closing '}' of the option
		n.Options = append(n.Options, Option{Key: key, Value: value})
	}
	if _, ok := seen["other"]; !ok {
		return Node{}, p.errAt(open, fmt.Sprintf("%s %q is missing other branch", typ, name))
	}
	return n, nil
}

// parseKey reads one option key: "=N" or a category for plural, a name
// (letters, digits, '_' and '-') for select.
func (p *parser) parseKey(typ string) (string, error) {
	if typ == "select" {
		key, ok := p.parseName(true)
		if !ok {
			return "", p.errAt(p.pos, "expected select key")
		}
		return key, nil
	}
	if p.pos < len(p.s) && p.s[p.pos] == '=' {
		start := p.pos
		p.pos++
		digits := p.pos
		for p.pos < len(p.s) && isDigit(p.s[p.pos]) {
			p.pos++
		}
		if p.pos == digits {
			return "", p.errAt(start, "expected digits after '='")
		}
		return p.s[start:p.pos], nil
	}
	off := p.pos
	word := p.parseWord()
	if word == "offset" {
		return "", p.errAt(off, "offset: is not supported")
	}
	switch word {
	case "zero", "one", "two", "few", "many", "other":
		return word, nil
	case "":
		return "", p.errAt(off, "expected plural key")
	default:
		return "", p.errAt(off, fmt.Sprintf("unknown plural key %q", word))
	}
}

// parseTag parses <name>body</name> starting at '<'.
func (p *parser) parseTag(c ctx) (Node, error) {
	open := p.pos
	p.pos++ // '<'
	name, _ := p.parseName(true)
	if name == "" || !isLetter(name[0]) {
		return Node{}, p.errAt(open, "invalid tag name")
	}
	if p.pos >= len(p.s) || p.s[p.pos] != '>' {
		return Node{}, p.errAt(p.pos, fmt.Sprintf("tag <%s> must not have attributes", name))
	}
	p.pos++ // '>'
	body, err := p.parseSeq(ctx{plural: c.plural, tag: name})
	if err != nil {
		return Node{}, err
	}
	if p.pos >= len(p.s) {
		return Node{}, p.errAt(open, fmt.Sprintf("missing closing tag </%s>", name))
	}
	closeOff := p.pos
	p.pos += 2 // "</"
	closeName, _ := p.parseName(true)
	if p.pos >= len(p.s) || p.s[p.pos] != '>' {
		return Node{}, p.errAt(closeOff, "invalid closing tag")
	}
	p.pos++ // '>'
	if closeName != name {
		return Node{}, p.errAt(closeOff, fmt.Sprintf("closing tag </%s> does not match <%s>", closeName, name))
	}
	return Node{Kind: KindTag, Name: name, Children: body}, nil
}

// parseName reads an identifier: a letter or '_' followed by letters, digits,
// '_' and, when dashes is true, '-'. Returns ok=false when none is present.
func (p *parser) parseName(dashes bool) (string, bool) {
	start := p.pos
	if p.pos >= len(p.s) || (!isLetter(p.s[p.pos]) && p.s[p.pos] != '_') {
		return "", false
	}
	p.pos++
	for p.pos < len(p.s) {
		b := p.s[p.pos]
		if isLetter(b) || isDigit(b) || b == '_' || (dashes && b == '-') {
			p.pos++
			continue
		}
		break
	}
	return p.s[start:p.pos], true
}

// parseWord reads a run of ASCII letters (type keywords and plural categories).
func (p *parser) parseWord() string {
	start := p.pos
	for p.pos < len(p.s) && isLetter(p.s[p.pos]) {
		p.pos++
	}
	return p.s[start:p.pos]
}

func (p *parser) skipSpace() {
	for p.pos < len(p.s) {
		switch p.s[p.pos] {
		case ' ', '\t', '\n', '\r':
			p.pos++
		default:
			return
		}
	}
}

func isLetter(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }

func isDigit(b byte) bool { return b >= '0' && b <= '9' }
