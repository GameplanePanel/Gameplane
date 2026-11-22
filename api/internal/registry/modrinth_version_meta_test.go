package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModrinthVersionsCarryReleaseTypeAndPublishedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[
			{"id":"b1","name":"v2","version_number":"2.0","game_versions":["1.21.4"],"loaders":["fabric"],"version_type":"beta","date_published":"2025-06-01T00:00:00Z","files":[]}
		]`))
	}))
	defer srv.Close()

	got, err := testModrinth(srv.URL).Versions(context.Background(), "sodium", Filter{})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	if got[0].ReleaseType != "beta" {
		t.Errorf("ReleaseType = %q, want beta", got[0].ReleaseType)
	}
	if got[0].PublishedAt != "2025-06-01T00:00:00Z" {
		t.Errorf("PublishedAt = %q, want 2025-06-01T00:00:00Z", got[0].PublishedAt)
	}
}
