package registry

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCurseforgeVersionsCarryReleaseTypeAndPublishedAt(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":[
			{"id":1,"displayName":"r","fileName":"r.jar","downloadUrl":"https://edge.forgecdn.net/r.jar","fileLength":1,"gameVersions":["1.21.1"],"releaseType":1,"fileDate":"2026-10-28T14:03:11.52Z"},
			{"id":2,"displayName":"b","fileName":"b.jar","downloadUrl":"https://edge.forgecdn.net/b.jar","fileLength":1,"gameVersions":["1.21.1"],"releaseType":2,"fileDate":"2026-10-28T14:03:11.52Z"},
			{"id":3,"displayName":"a","fileName":"a.jar","downloadUrl":"https://edge.forgecdn.net/a.jar","fileLength":1,"gameVersions":["1.21.1"],"releaseType":3,"fileDate":"2026-10-28T14:03:11.52Z"},
			{"id":4,"displayName":"u","fileName":"u.jar","downloadUrl":"https://edge.forgecdn.net/u.jar","fileLength":1,"gameVersions":["1.21.1"],"releaseType":9,"fileDate":"not-a-date"}
		]}`))
	}))
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200", Filter{})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("len = %d, want 4", len(got))
	}
	wantTypes := []string{"release", "beta", "alpha", ""}
	for i, want := range wantTypes {
		if got[i].ReleaseType != want {
			t.Errorf("version %d ReleaseType = %q, want %q", i, got[i].ReleaseType, want)
		}
	}
	if got[0].PublishedAt != "2026-10-28T14:03:11Z" {
		t.Errorf("PublishedAt = %q, want 2026-10-28T14:03:11Z", got[0].PublishedAt)
	}
	if got[3].PublishedAt != "" {
		t.Errorf("bad fileDate PublishedAt = %q, want empty", got[3].PublishedAt)
	}
}

func TestCfReleaseTypeMapping(t *testing.T) {
	cases := map[int]string{1: "release", 2: "beta", 3: "alpha", 0: "", 9: ""}
	for n, want := range cases {
		if got := cfReleaseType(n); got != want {
			t.Errorf("cfReleaseType(%d) = %q, want %q", n, got, want)
		}
	}
}
