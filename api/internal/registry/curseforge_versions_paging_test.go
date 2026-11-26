package registry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// cfFilePage returns a CurseForge files response with n downloadable files
// whose ids start at firstID.
func cfFilePage(firstID, n int) string {
	var b strings.Builder
	b.WriteString(`{"data":[`)
	for i := 0; i < n; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		id := firstID + i
		fmt.Fprintf(&b, `{"id":%d,"displayName":"f%d","fileName":"f%d.zip","downloadUrl":"https://edge.forgecdn.net/f%d.zip","fileLength":1,"gameVersions":["1.21.1","NeoForge"],"releaseType":1,"fileDate":"2026-10-28T14:03:11Z"}`, id, id, id, id)
	}
	b.WriteString(`]}`)
	return b.String()
}

// indexRecorder serves files pages by the request's index parameter and
// records each index it was asked for.
type indexRecorder struct {
	mu      sync.Mutex
	indexes []string
	serve   func(index int) string
}

func (r *indexRecorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	idx := req.URL.Query().Get("index")
	r.mu.Lock()
	r.indexes = append(r.indexes, idx)
	r.mu.Unlock()
	n, _ := strconv.Atoi(idx)
	_, _ = w.Write([]byte(r.serve(n)))
}

func TestCurseforgeVersionsUnfilteredPagesThroughFiles(t *testing.T) {
	rec := &indexRecorder{serve: func(index int) string {
		if index == 0 {
			return cfFilePage(1, 50)
		}
		return cfFilePage(51, 3)
	}}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200", Filter{ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 53 {
		t.Fatalf("len = %d, want 53 (50 + 3 files over two pages)", len(got))
	}
	if got[52].ID != "53" {
		t.Errorf("last ID = %s, want 53", got[52].ID)
	}
	if want := []string{"0", "50"}; strings.Join(rec.indexes, ",") != strings.Join(want, ",") {
		t.Errorf("requested indexes = %v, want %v", rec.indexes, want)
	}
}

func TestCurseforgeVersionsUnfilteredHardCapsAtFivePages(t *testing.T) {
	rec := &indexRecorder{serve: func(index int) string {
		return cfFilePage(index+1, 50)
	}}
	srv := httptest.NewServer(rec)
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200", Filter{ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 250 {
		t.Fatalf("len = %d, want the 250-file hard cap", len(got))
	}
	if len(rec.indexes) != 5 {
		t.Errorf("requests = %d, want 5", len(rec.indexes))
	}
}

func TestCurseforgeVersionsFilteredPagesToo(t *testing.T) {
	var mu sync.Mutex
	var queries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		q := req.URL.Query()
		mu.Lock()
		queries = append(queries, q)
		mu.Unlock()
		if q.Get("index") == "0" {
			_, _ = w.Write([]byte(cfFilePage(1, 50)))
			return
		}
		_, _ = w.Write([]byte(cfFilePage(51, 2)))
	}))
	defer srv.Close()

	got, err := testCurseforgeGame(srv.URL, cfGameMinecraft).Versions(context.Background(), "925200", Filter{Loader: "neoforge", ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if len(got) != 52 {
		t.Fatalf("len = %d, want 52 (50 + 2 files over two pages)", len(got))
	}
	if len(queries) != 2 {
		t.Fatalf("filtered requests = %d, want 2", len(queries))
	}
	wantLoader := strconv.Itoa(cfLoaderType("neoforge"))
	for i, q := range queries {
		if v := q.Get("modLoaderType"); v != wantLoader {
			t.Errorf("request %d modLoaderType = %q, want %q", i, v, wantLoader)
		}
		if v := q.Get("pageSize"); v != strconv.Itoa(cfVersionsPageSize) {
			t.Errorf("request %d pageSize = %q, want %d", i, v, cfVersionsPageSize)
		}
	}
	if got := queries[1].Get("index"); got != strconv.Itoa(cfVersionsPageSize) {
		t.Errorf("second request index = %q, want %d", got, cfVersionsPageSize)
	}
}
