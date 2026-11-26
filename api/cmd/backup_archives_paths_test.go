package main

import (
	"net/http"
	"testing"
)

func TestIsLargeTransferPathBackupDownloads(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string
		want   bool
	}{
		{"download tar.gz route", http.MethodGet, "/backups/nightly-1:download", true},
		{"download with a long name", http.MethodGet, "/backups/my-server-20260101-0000-abc:download", true},
		{"backup list is not a transfer", http.MethodGet, "/backups", false},
		{"backup read is not a transfer", http.MethodGet, "/backups/nightly-1", false},
		{"download suffix on another verb", http.MethodPost, "/backups/nightly-1:download", false},
		{"download suffix without backups prefix", http.MethodGet, "/servers/alpha:download", false},
		// Changed in spec 041 upload work: POST /restores:upload streams the whole archive, so it is a large transfer.
		{"restore upload streams its archive", http.MethodPost, "/restores:upload", true},
		{"restore confirm is not a transfer", http.MethodPost, "/restores/nightly-1:confirm", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isLargeTransferPath(tc.method, tc.path); got != tc.want {
				t.Fatalf("isLargeTransferPath(%s, %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}
