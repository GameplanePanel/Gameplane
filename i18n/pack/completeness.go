package pack

import "github.com/GameplanePanel/gameplane/i18n/icu"

// Status is the state of one translated message.
type Status string

// Message statuses.
const (
	StatusTranslated  Status = "translated"
	StatusNeedsReview Status = "needs-review"
	StatusInvalid     Status = "invalid"
	StatusMissing     Status = "missing"
)

// EntryStatus classifies a translation of ref. present reports whether the
// translation pack has an entry for the id at all.
func EntryStatus(ref Entry, tr Entry, present bool) Status {
	if !present || tr.DefaultMessage == "" {
		return StatusMissing
	}
	trM, err := icu.Parse(tr.DefaultMessage)
	if err != nil {
		return StatusInvalid
	}
	if refM, rerr := icu.Parse(ref.DefaultMessage); rerr == nil {
		if !sameSet(icu.Arguments(refM), icu.Arguments(trM)) {
			return StatusInvalid
		}
	}
	if CheckMarkup(tr.DefaultMessage, trM) != "" {
		return StatusInvalid
	}
	if tr.SourceHash != "" && tr.SourceHash != SourceHash(ref.DefaultMessage) {
		return StatusNeedsReview
	}
	return StatusTranslated
}

// Statuses returns the status of every reference id in tr.
func Statuses(ref, tr map[string]Entry) map[string]Status {
	out := make(map[string]Status, len(ref))
	for id, refE := range ref {
		e, ok := tr[id]
		out[id] = EntryStatus(refE, e, ok)
	}
	return out
}

// Completeness returns the percentage of reference ids that have a usable
// translation (translated or needs-review). An empty reference is 100.
func Completeness(ref, tr map[string]Entry) float64 {
	if len(ref) == 0 {
		return 100
	}
	usable := 0
	for _, st := range Statuses(ref, tr) {
		if st == StatusTranslated || st == StatusNeedsReview {
			usable++
		}
	}
	return float64(usable) * 100 / float64(len(ref))
}

// Listed reports whether a language is offered to users. English is always
// listed; others need enough completeness, left-to-right text, and must not be
// the pseudo-locale.
func Listed(tag string, meta Meta, completeness float64) bool {
	if tag == "en" {
		return true
	}
	return completeness >= ListingThreshold && meta.Direction == "ltr" && tag != "en-XA"
}

// Valid returns the entries of tr that may be shown to users: ids the
// reference still has, with status translated or needs-review.
func Valid(ref, tr map[string]Entry) map[string]Entry {
	out := make(map[string]Entry, len(tr))
	for id, e := range tr {
		refE, ok := ref[id]
		if !ok {
			continue
		}
		switch EntryStatus(refE, e, true) {
		case StatusTranslated, StatusNeedsReview:
			out[id] = e
		}
	}
	return out
}

func sameSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for k := range a {
		if _, ok := b[k]; !ok {
			return false
		}
	}
	return true
}
