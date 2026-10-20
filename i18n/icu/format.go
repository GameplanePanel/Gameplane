package icu

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/feature/plural"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
	"golang.org/x/text/number"
)

// state carries the locale, the arguments and the current plural value (for #)
// through a recursive render.
type state struct {
	tag      language.Tag
	args     map[string]any
	pound    string
	hasPound bool
}

// Format renders m for tag using args. A missing argument renders as {name}.
// Format never panics on a malformed message; unknown shapes render as text.
func Format(m Message, tag language.Tag, args map[string]any) string {
	var b strings.Builder
	writeNodes(&b, m, state{tag: tag, args: args})
	return b.String()
}

func writeNodes(b *strings.Builder, nodes []Node, st state) {
	for _, n := range nodes {
		switch n.Kind {
		case KindLiteral:
			b.WriteString(n.Text)
		case KindPound:
			if st.hasPound {
				b.WriteString(st.pound)
			} else {
				b.WriteByte('#')
			}
		case KindTag:
			b.WriteString("<" + n.Name + ">")
			writeNodes(b, n.Children, st)
			b.WriteString("</" + n.Name + ">")
		case KindArgument:
			v, ok := st.args[n.Name]
			if !ok {
				b.WriteString("{" + n.Name + "}")
				continue
			}
			b.WriteString(formatArgument(v, n, st.tag))
		case KindPlural:
			v, ok := st.args[n.Name]
			if !ok {
				b.WriteString("{" + n.Name + "}")
				continue
			}
			writePlural(b, n, v, st)
		case KindSelect:
			v, ok := st.args[n.Name]
			if !ok {
				writeOption(b, findOption(n.Options, "other"), st, "", false)
				continue
			}
			opt := findOption(n.Options, fmt.Sprint(v))
			if opt == nil {
				opt = findOption(n.Options, "other")
			}
			writeOption(b, opt, st, "", false)
		}
	}
}

// writePlural renders the branch selected by an exact =N match, else by the
// CLDR plural category of the value, else the other branch.
func writePlural(b *strings.Builder, n Node, v any, st state) {
	val, text, ok := toNumber(v)
	if !ok {
		writeOption(b, findOption(n.Options, "other"), st, fmt.Sprint(v), true)
		return
	}
	x, _ := strconv.ParseFloat(text, 64)
	opt := exactOption(n.Options, x)
	if opt == nil {
		opt = findOption(n.Options, pluralKey(st.tag, text))
	}
	if opt == nil {
		opt = findOption(n.Options, "other")
	}
	pound := message.NewPrinter(st.tag).Sprint(number.Decimal(val))
	writeOption(b, opt, st, pound, true)
}

func writeOption(b *strings.Builder, opt *Option, st state, pound string, hasPound bool) {
	if opt == nil {
		return
	}
	inner := st
	if hasPound {
		inner.pound = pound
		inner.hasPound = true
	}
	writeNodes(b, opt.Value, inner)
}

func findOption(opts []Option, key string) *Option {
	for i := range opts {
		if opts[i].Key == key {
			return &opts[i]
		}
	}
	return nil
}

func exactOption(opts []Option, x float64) *Option {
	for i := range opts {
		k := opts[i].Key
		if !strings.HasPrefix(k, "=") {
			continue
		}
		n, err := strconv.Atoi(k[1:])
		if err == nil && x == float64(n) {
			return &opts[i]
		}
	}
	return nil
}

// pluralKey maps the CLDR category of a decimal string to its option key.
func pluralKey(tag language.Tag, text string) string {
	i, v, w, f, t := operands(text)
	switch plural.Cardinal.MatchPlural(tag, i, v, w, f, t) {
	case plural.Zero:
		return "zero"
	case plural.One:
		return "one"
	case plural.Two:
		return "two"
	case plural.Few:
		return "few"
	case plural.Many:
		return "many"
	default:
		return "other"
	}
}

// operands derives the CLDR plural operands i, v, w, f and t from a plain
// decimal string such as "-12.50". Operands too large for an int keep their
// last seven digits, as the CLDR rules permit.
func operands(text string) (i, v, w, f, t int) {
	text = strings.TrimPrefix(text, "-")
	intPart, frac, _ := strings.Cut(text, ".")
	fracTrim := strings.TrimRight(frac, "0")
	i = tailInt(intPart)
	v = len(frac)
	w = len(fracTrim)
	f = tailInt(frac)
	t = tailInt(fracTrim)
	return i, v, w, f, t
}

// tailInt parses the last seven digits of s; empty input yields 0.
func tailInt(s string) int {
	const keep = 7
	if len(s) > keep {
		s = s[len(s)-keep:]
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// toNumber reports whether v is numeric (a Go numeric type or a numeric
// string). It returns the value for number.Decimal and its plain decimal text.
func toNumber(v any) (val any, text string, ok bool) {
	switch t := v.(type) {
	case int:
		return t, strconv.FormatInt(int64(t), 10), true
	case int8:
		return t, strconv.FormatInt(int64(t), 10), true
	case int16:
		return t, strconv.FormatInt(int64(t), 10), true
	case int32:
		return t, strconv.FormatInt(int64(t), 10), true
	case int64:
		return t, strconv.FormatInt(t, 10), true
	case uint:
		return t, strconv.FormatUint(uint64(t), 10), true
	case uint8:
		return t, strconv.FormatUint(uint64(t), 10), true
	case uint16:
		return t, strconv.FormatUint(uint64(t), 10), true
	case uint32:
		return t, strconv.FormatUint(uint64(t), 10), true
	case uint64:
		return t, strconv.FormatUint(t, 10), true
	case float32:
		f := float64(t)
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, "", false
		}
		return f, strconv.FormatFloat(f, 'f', -1, 32), true
	case float64:
		if math.IsNaN(t) || math.IsInf(t, 0) {
			return nil, "", false
		}
		return t, strconv.FormatFloat(t, 'f', -1, 64), true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, "", false
		}
		return f, strconv.FormatFloat(f, 'f', -1, 64), true
	}
	return nil, "", false
}

func formatArgument(v any, n Node, tag language.Tag) string {
	switch n.Format {
	case "number":
		if val, _, ok := toNumber(v); ok {
			return message.NewPrinter(tag).Sprint(number.Decimal(val))
		}
	case "date":
		if t, ok := v.(time.Time); ok {
			return t.Format("2006-01-02")
		}
	case "time":
		if t, ok := v.(time.Time); ok {
			return t.Format("15:04")
		}
	}
	return fmt.Sprint(v)
}
