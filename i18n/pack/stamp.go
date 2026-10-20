package pack

// Stamp records the English source hash on every translated entry of tr that
// lacks one, using ref for the English text. It returns the stamped entries
// (tr itself is not modified) and whether anything changed.
func Stamp(ref, tr map[string]Entry) (map[string]Entry, bool) {
	out := make(map[string]Entry, len(tr))
	for id, entry := range tr {
		out[id] = entry
	}
	changed := false
	for id, entry := range tr {
		r, ok := ref[id]
		if !ok || entry.SourceHash != "" {
			continue
		}
		if EntryStatus(r, entry, true) != StatusTranslated {
			continue
		}
		entry.SourceHash = SourceHash(r.DefaultMessage)
		out[id] = entry
		changed = true
	}
	return out, changed
}
