// Command messages-export writes the API message registry as JSON to stdout,
// together with the notify.* notification text. The i18n-sync Makefile target
// feeds the output to i18n-check.
package main

import (
	"fmt"
	"os"

	"github.com/GameplanePanel/gameplane/api/internal/messages"
	"github.com/GameplanePanel/gameplane/api/internal/notify"
)

func main() {
	if err := messages.ExportJSON(os.Stdout, notify.Messages()...); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
