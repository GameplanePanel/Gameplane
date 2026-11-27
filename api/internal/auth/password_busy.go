package auth

import (
	"net/http"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/messages"
)

// writePasswordBusy answers the generic 503 with Retry-After when the argon2
// queue is full. The response is identical whatever the username, so it leaks
// nothing on the unauthenticated login path.
func writePasswordBusy(w http.ResponseWriter, req *http.Request) {
	w.Header().Set("Retry-After", "5")
	httperr.WriteMessage(w, req, messages.New("server.generic.503", nil))
}
