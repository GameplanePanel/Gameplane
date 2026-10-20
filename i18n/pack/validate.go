package pack

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/GameplanePanel/gameplane/i18n/icu"
)

// Severity classifies a Finding. Error findings fail the check; info findings
// are advisory.
type Severity string

// Finding severities.
const (
	SeverityError Severity = "error"
	SeverityInfo  Severity = "info"
)

// Finding codes reported by the validators.
const (
	CodeMalformedPack      = "malformed-pack"
	CodeInvalidMeta        = "invalid-meta"
	CodeICUSyntax          = "icu-syntax"
	CodeMissingPlaceholder = "missing-placeholder"
	CodeExtraPlaceholder   = "extra-placeholder"
	CodeMarkup             = "markup"
	CodeUnknownID          = "unknown-id"
	CodeMissingDescription = "missing-description"
	CodeExtractDrift       = "extract-drift"
	CodeServerDrift        = "server-drift"
	CodeUnusedID           = "unused-id"
	CodeNeedsReview        = "needs-review"
)

// Pseudo-identifiers used in findings that are not about a message id.
const (
	IDPack      = "<pack>"
	IDMeta      = "<meta>"
	IDReference = "<reference>"
	IDRegistry  = "<registry>"
)

// Finding is one problem reported by a validator.
type Finding struct {
	Code     string   `json:"code"`
	Severity Severity `json:"severity"`
	File     string   `json:"file"`
	ID       string   `json:"id"`
	Message  string   `json:"message"`
}

// String renders the finding as "<file>: <id>: [<code>] <message>".
func (f Finding) String() string {
	return fmt.Sprintf("%s: %s: [%s] %s", f.File, f.ID, f.Code, f.Message)
}

var (
	entityRE = regexp.MustCompile(`&[A-Za-z#][A-Za-z0-9]*;`)
)

// CheckMarkup reports the first markup problem in a message, or "" when the
// message only uses the allowed inline tags and no HTML entities or scripts.
// raw is the message text as stored; m is its parsed form.
func CheckMarkup(raw string, m icu.Message) string {
	tags := icu.SortedKeys(icu.Tags(m))
	for _, name := range tags {
		if name != "b" && name != "link" {
			return fmt.Sprintf("uses tag <%s>; only <b> and <link> are allowed", name)
		}
	}
	if entityRE.MatchString(raw) {
		return "contains an HTML entity"
	}
	lower := strings.ToLower(raw)
	if strings.Contains(lower, "javascript:") {
		return "contains a javascript: link"
	}
	if strings.Contains(lower, "<script") {
		return "contains a script tag"
	}
	return ""
}

// ValidateReference checks every entry of the English reference: the message
// must parse, use only allowed markup, and carry a translator description.
func ValidateReference(ref map[string]Entry, file string) []Finding {
	var out []Finding
	for _, id := range sortedEntryIDs(ref) {
		e := ref[id]
		m, err := icu.Parse(e.DefaultMessage)
		if err != nil {
			out = append(out, errorFinding(CodeICUSyntax, file, id, fmt.Sprintf("does not parse: %v", err)))
		} else if msg := CheckMarkup(e.DefaultMessage, m); msg != "" {
			out = append(out, errorFinding(CodeMarkup, file, id, msg))
		}
		if strings.TrimSpace(e.Description) == "" {
			out = append(out, errorFinding(CodeMissingDescription, file, id, "has no translator note (description)"))
		}
	}
	return out
}

// ValidateMeta checks a meta.json file. dirTag is the directory name the file
// lives in; the tag must match it.
func ValidateMeta(data []byte, dirTag, file string) []Finding {
	meta, err := ParseMeta(data)
	if err != nil {
		return []Finding{errorFinding(CodeInvalidMeta, file, IDMeta, fmt.Sprintf("meta.json is not valid: %v", err))}
	}
	var out []Finding
	bad := func(msg string) {
		out = append(out, errorFinding(CodeInvalidMeta, file, IDMeta, msg))
	}
	if meta.Tag == "" {
		bad(`missing required field "tag"`)
	} else {
		if !ValidTag(meta.Tag) && meta.Tag != "en-XA" {
			bad(fmt.Sprintf("tag %q is not a valid language tag", meta.Tag))
		}
		if dirTag != "" && meta.Tag != dirTag {
			bad(fmt.Sprintf("tag %q does not match directory %q", meta.Tag, dirTag))
		}
	}
	if meta.NativeName == "" {
		bad(`missing required field "nativeName"`)
	} else if utf8.RuneCountInString(meta.NativeName) > 64 {
		bad("nativeName is longer than 64 characters")
	}
	if meta.EnglishName == "" {
		bad(`missing required field "englishName"`)
	} else if utf8.RuneCountInString(meta.EnglishName) > 64 {
		bad("englishName is longer than 64 characters")
	}
	switch meta.Direction {
	case "":
		bad(`missing required field "direction"`)
	case "ltr", "rtl":
	default:
		bad(fmt.Sprintf("direction %q must be \"ltr\" or \"rtl\"", meta.Direction))
	}
	return out
}

// ValidateTranslation checks a translated pack against the English reference.
// Placeholder and markup problems are errors; a translation whose English
// source has changed since it was made is reported as needs-review (info).
func ValidateTranslation(ref, tr map[string]Entry, file string) []Finding {
	var out []Finding
	ids := sortedEntryIDs(tr)
	for _, id := range ids {
		e := tr[id]
		refE, ok := ref[id]
		if !ok {
			out = append(out, errorFinding(CodeUnknownID, file, id, "translation for an id the English reference no longer has"))
			continue
		}
		m, err := icu.Parse(e.DefaultMessage)
		if err != nil {
			out = append(out, errorFinding(CodeICUSyntax, file, id, fmt.Sprintf("does not parse: %v", err)))
			continue
		}
		hasError := false
		if refM, rerr := icu.Parse(refE.DefaultMessage); rerr == nil {
			refArgs := icu.Arguments(refM)
			trArgs := icu.Arguments(m)
			if missing := setDifference(refArgs, trArgs); len(missing) > 0 {
				out = append(out, errorFinding(CodeMissingPlaceholder, file, id, "missing placeholder(s): "+braceList(missing)))
				hasError = true
			}
			if extra := setDifference(trArgs, refArgs); len(extra) > 0 {
				out = append(out, errorFinding(CodeExtraPlaceholder, file, id, "placeholder(s) not in English: "+braceList(extra)))
				hasError = true
			}
		}
		if msg := CheckMarkup(e.DefaultMessage, m); msg != "" {
			out = append(out, errorFinding(CodeMarkup, file, id, msg))
			hasError = true
		}
		if !hasError && e.SourceHash != "" && e.SourceHash != SourceHash(refE.DefaultMessage) {
			out = append(out, Finding{
				Code:     CodeNeedsReview,
				Severity: SeverityInfo,
				File:     file,
				ID:       id,
				Message:  "English text changed since this was translated; please review",
			})
		}
	}
	return out
}

// SortFindings orders findings by file, then id, then code.
func SortFindings(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		if fs[i].File != fs[j].File {
			return fs[i].File < fs[j].File
		}
		if fs[i].ID != fs[j].ID {
			return fs[i].ID < fs[j].ID
		}
		return fs[i].Code < fs[j].Code
	})
}

func errorFinding(code, file, id, msg string) Finding {
	return Finding{Code: code, Severity: SeverityError, File: file, ID: id, Message: msg}
}

func sortedEntryIDs(m map[string]Entry) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// setDifference returns the sorted members of a that are not in b.
func setDifference(a, b map[string]struct{}) []string {
	var out []string
	for k := range a {
		if _, ok := b[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

func braceList(names []string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = "{" + n + "}"
	}
	return strings.Join(parts, ", ")
}
