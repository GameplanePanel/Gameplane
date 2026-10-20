package messages

import (
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

// ExportJSON writes the registry as a JSON array sorted by id, with
// two-space indentation and a trailing newline. The extra entries (for
// example the notify.* text owned by another package) are written alongside
// the registry's own. The i18n tooling reads this output to sync the server.*
// and notify.* entries of the English reference pack.
func ExportJSON(w io.Writer, extra ...Entry) error {
	all := append(All(), extra...)
	slices.SortFunc(all, func(a, b Entry) int {
		return strings.Compare(string(a.ID), string(b.ID))
	})
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(all); err != nil {
		return fmt.Errorf("messages: export json: %w", err)
	}
	return nil
}
