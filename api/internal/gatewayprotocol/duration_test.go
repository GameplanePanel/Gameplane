package gatewayprotocol

import (
	"net/http"
	"testing"
)

func TestLongRunningOperation(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         bool
	}{
		{http.MethodGet, "/console", true},
		{http.MethodGet, "/logs/tail", true},
		{http.MethodGet, "/files/download", true},
		{http.MethodGet, "/logs/download", true},
		{http.MethodPost, "/files/upload", true},
		{http.MethodPost, "/mods/upload", true},
		{http.MethodPost, "/mods/install", true},
		{http.MethodPut, "/files/stream", true},
		{http.MethodGet, "/files/stat", false},
		{http.MethodPost, "/files/rename", false},
		{http.MethodGet, "/mods/install", false},
		{http.MethodPost, "/mods", false},
		{http.MethodPost, "/mods/install/extra", false},
		{http.MethodGet, "/files/download/extra", false},
		{http.MethodGet, "/status", false},
		{http.MethodPost, "/console", false},
		{http.MethodDelete, "/mods", false},
	} {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			if got := LongRunningOperation(tc.method, tc.path); got != tc.want {
				t.Fatalf("long running = %v, want %v", got, tc.want)
			}
		})
	}
}
