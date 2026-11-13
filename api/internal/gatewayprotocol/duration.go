package gatewayprotocol

import "net/http"

// LongRunningOperation identifies streams and transfers whose response headers
// may arrive only after lengthy work. Caller cancellation and gateway lifetime
// limits still apply; ordinary operations keep their short timeouts.
func LongRunningOperation(method, path string) bool {
	if path == "/files/archive" {
		return method == http.MethodGet || method == http.MethodPost
	}
	if method == http.MethodGet {
		return Streaming(path) || path == "/files/download" || path == "/logs/download"
	}
	return method == http.MethodPost && (path == "/files/upload" || path == "/mods/upload" || path == "/mods/install")
}
