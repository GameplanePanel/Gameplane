package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func testFTB(baseURL string) *FTB {
	f := newFTB(&http.Client{Timeout: 5 * time.Second}, "gameplane-test")
	f.baseURL = baseURL
	return f
}

// ftbServer fakes api.feed-the-beast.com: search/popular answer with
// `candidates` (or searchBody when set), pack details come from `packs`.
type ftbServer struct {
	*httptest.Server

	candidates []int
	searchBody []byte
	packs      func(id int) ([]byte, int) // body, status (0 means 200)
	delay      time.Duration
	listStatus int

	mu          sync.Mutex
	details     int
	inflight    int
	maxInflight int
	listPath    string
	listTerm    string
}

func newFTBServer(t *testing.T, candidates []int, packs func(id int) ([]byte, int)) *ftbServer {
	t.Helper()
	s := &ftbServer{candidates: candidates, packs: packs}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	t.Cleanup(s.Close)
	return s
}

func (s *ftbServer) serve(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/")
	if strings.HasPrefix(rest, "search/") || strings.HasPrefix(rest, "popular/") {
		s.mu.Lock()
		s.listPath = r.URL.Path
		s.listTerm = r.URL.Query().Get("term")
		s.mu.Unlock()
		if s.listStatus != 0 {
			w.WriteHeader(s.listStatus)
			return
		}
		if s.searchBody != nil {
			_, _ = w.Write(s.searchBody)
			return
		}
		out, _ := json.Marshal(map[string]any{"status": "success", "packs": s.candidates})
		_, _ = w.Write(out)
		return
	}
	id, err := strconv.Atoi(rest)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.mu.Lock()
	s.details++
	s.inflight++
	if s.inflight > s.maxInflight {
		s.maxInflight = s.inflight
	}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inflight--
		s.mu.Unlock()
	}()
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	body, status := s.packs(id)
	if status != 0 && status != http.StatusOK {
		w.WriteHeader(status)
		return
	}
	_, _ = w.Write(body)
}

func (s *ftbServer) detailCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.details
}

func ftbFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/ftb/" + name)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// ftbFixturePacks serves the recorded pack-<id>.json files (ids 130, 100, 4)
// and 404 for anything else.
func ftbFixturePacks() func(id int) ([]byte, int) {
	return func(id int) ([]byte, int) {
		b, err := os.ReadFile(fmt.Sprintf("testdata/ftb/pack-%d.json", id))
		if err != nil {
			return nil, http.StatusNotFound
		}
		return b, 0
	}
}

// ftbSynthPack builds a one-version pack for the batching tests.
func ftbSynthPack(id int, game, loader string) []byte {
	b, _ := json.Marshal(map[string]any{
		"id": id, "name": fmt.Sprintf("Pack %d", id), "slug": fmt.Sprintf("pack-%d", id),
		"synopsis": "synthetic", "installs": id,
		"authors": []map[string]any{{"name": "FTB Team"}},
		"art":     []map[string]any{{"type": "square", "url": "https://cdn.example/x.png"}},
		"versions": []map[string]any{{
			"id": id * 10, "name": "1.0.0", "type": "release", "updated": 1700000000 + id,
			"targets": []map[string]any{
				{"name": "minecraft", "type": "game", "version": game},
				{"name": loader, "type": "modloader", "version": "1.0"},
			},
		}},
	})
	return b
}

func ftbRange(from, to int) []int {
	ids := make([]int, 0, to-from+1)
	for i := from; i <= to; i++ {
		ids = append(ids, i)
	}
	return ids
}

func projectIDs(ps []Project) []string {
	ids := make([]string, 0, len(ps))
	for _, p := range ps {
		ids = append(ids, p.ID)
	}
	return ids
}

func TestFTBSearchFixtures(t *testing.T) {
	srv := newFTBServer(t, nil, ftbFixturePacks())
	srv.searchBody = ftbFixture(t, "search.json")

	cases := []struct {
		name       string
		loader, gv string
		wantIDs    []string
	}{
		{"neoforge 1.21.1", "neoforge", "1.21.1", []string{"130"}},
		{"forge 1.18.2", "forge", "1.18.2", []string{"100"}},
		{"forge 1.12.2", "forge", "1.12.2", []string{"4"}},
		{"no filter keeps FTB order", "", "", []string{"130", "100", "4"}},
		{"fabric 1.21.1 has none", "fabric", "1.21.1", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := testFTB(srv.URL).Search(context.Background(), SearchQuery{
				Term: "stone", Loader: tc.loader, GameVersion: tc.gv, ProjectType: "modpack", Limit: 20,
			})
			if err != nil {
				t.Fatalf("Search: %v", err)
			}
			if ids := projectIDs(got); !reflect.DeepEqual(ids, tc.wantIDs) {
				t.Errorf("ids = %v, want %v", ids, tc.wantIDs)
			}
		})
	}

	srv.mu.Lock()
	gotPath, gotTerm := srv.listPath, srv.listTerm
	srv.mu.Unlock()
	if gotPath != "/search/100" || gotTerm != "stone" {
		t.Errorf("search request = %q term %q, want /search/100 term stone", gotPath, gotTerm)
	}

	got, err := testFTB(srv.URL).Search(context.Background(), SearchQuery{
		Term: "stone", Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack",
	})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search: %v, %v", got, err)
	}
	p := got[0]
	if p.ID != "130" || p.Slug != "ftb-stoneblock-4" || p.Title != "FTB StoneBlock 4" ||
		p.Author != "FTB Team" || p.Downloads != 2219810 || p.Provider != "ftb" ||
		p.IconURL != "https://cdn.feed-the-beast.com/blob/5b/5b10fbf6e78546a5a4be81a2d311718cc24d29e4277e747028d787d6fec0be46.webp" ||
		!strings.HasPrefix(p.Description, "The stone remembers.") {
		t.Errorf("project = %+v", p)
	}
}

func TestFTBSearchPopularWithoutTerm(t *testing.T) {
	srv := newFTBServer(t, []int{130}, ftbFixturePacks())
	got, err := testFTB(srv.URL).Search(context.Background(), SearchQuery{ProjectType: "modpack"})
	if err != nil || len(got) != 1 {
		t.Fatalf("Search: %v, %v", got, err)
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.listPath != "/popular/installs/100" || srv.listTerm != "" {
		t.Errorf("request = %q term %q, want /popular/installs/100", srv.listPath, srv.listTerm)
	}
}

// A match that only appears after the first 20 candidates must still be
// found: filtering happens before the page limit, in batches of 20.
func TestFTBSearchFiltersBeforePageLimit(t *testing.T) {
	srv := newFTBServer(t, ftbRange(1, 45), func(id int) ([]byte, int) {
		if id == 41 {
			return ftbSynthPack(id, "1.21.1", "neoforge"), 0
		}
		return ftbSynthPack(id, "1.20.1", "forge"), 0
	})
	got, err := testFTB(srv.URL).Search(context.Background(), SearchQuery{
		Term: "x", Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack", Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if ids := projectIDs(got); !reflect.DeepEqual(ids, []string{"41"}) {
		t.Errorf("ids = %v, want [41]", ids)
	}
	if n := srv.detailCount(); n != 45 {
		t.Errorf("detail calls = %d, want 45 (batches of 20, 20, 5)", n)
	}
}

// Once the filtered page is full no further batch is resolved.
func TestFTBSearchStopsWhenPageFull(t *testing.T) {
	all := func(id int) ([]byte, int) { return ftbSynthPack(id, "1.21.1", "neoforge"), 0 }
	q := SearchQuery{Term: "x", Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack", Limit: 20}

	srv := newFTBServer(t, ftbRange(1, 100), all)
	got, err := testFTB(srv.URL).Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 20 || srv.detailCount() != 20 {
		t.Errorf("len = %d details = %d, want 20 and 20", len(got), srv.detailCount())
	}

	// Offset 15 + limit 10 needs 25 matches: two batches, ids 16..25 returned.
	srv2 := newFTBServer(t, ftbRange(1, 100), all)
	q.Offset, q.Limit = 15, 10
	got, err = testFTB(srv2.URL).Search(context.Background(), q)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 10 || got[0].ID != "16" || srv2.detailCount() != 40 {
		t.Errorf("len = %d first = %v details = %d, want 10, 16 and 40", len(got), projectIDs(got), srv2.detailCount())
	}
}

// FTB may return more ids than asked for; never resolve more than 100.
func TestFTBSearchStopsAt100Details(t *testing.T) {
	srv := newFTBServer(t, ftbRange(1, 150), func(id int) ([]byte, int) {
		return ftbSynthPack(id, "1.20.1", "forge"), 0
	})
	got, err := testFTB(srv.URL).Search(context.Background(), SearchQuery{
		Term: "x", Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack", Limit: 20,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
	if n := srv.detailCount(); n != 100 {
		t.Errorf("detail calls = %d, want 100", n)
	}
}

func TestFTBDetailCache(t *testing.T) {
	srv := newFTBServer(t, []int{130}, ftbFixturePacks())
	f := testFTB(srv.URL)
	clock := time.Date(2026, 11, 19, 12, 0, 0, 0, time.UTC)
	f.now = func() time.Time { return clock }
	q := SearchQuery{Term: "stone", Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack"}
	ctx := context.Background()

	if _, err := f.Search(ctx, q); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := f.Search(ctx, q); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := f.Versions(ctx, "130", Filter{}); err != nil {
		t.Fatalf("Versions: %v", err)
	}
	if n := srv.detailCount(); n != 1 {
		t.Fatalf("detail calls = %d, want 1 (cached)", n)
	}

	clock = clock.Add(9 * time.Minute)
	if _, err := f.Search(ctx, q); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if n := srv.detailCount(); n != 1 {
		t.Errorf("detail calls after 9m = %d, want 1", n)
	}

	clock = clock.Add(2 * time.Minute) // 11 minutes since the fetch
	if _, err := f.Search(ctx, q); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if n := srv.detailCount(); n != 2 {
		t.Errorf("detail calls after 11m = %d, want 2 (expired)", n)
	}
}

func TestFTBSearchBoundedConcurrency(t *testing.T) {
	srv := newFTBServer(t, ftbRange(1, 40), func(id int) ([]byte, int) {
		return ftbSynthPack(id, "1.21.1", "neoforge"), 0
	})
	srv.delay = 15 * time.Millisecond
	got, err := testFTB(srv.URL).Search(context.Background(), SearchQuery{
		Term: "x", Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack", Limit: 40,
	})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(got) != 40 {
		t.Errorf("len = %d, want 40", len(got))
	}
	srv.mu.Lock()
	defer srv.mu.Unlock()
	if srv.maxInflight > 4 || srv.maxInflight < 2 {
		t.Errorf("max in-flight detail requests = %d, want between 2 and 4", srv.maxInflight)
	}
}

func TestFTBUpstreamErrors(t *testing.T) {
	ctx := context.Background()
	q := SearchQuery{Term: "x", ProjectType: "modpack"}

	t.Run("search failure", func(t *testing.T) {
		srv := newFTBServer(t, nil, ftbFixturePacks())
		srv.listStatus = http.StatusInternalServerError
		if _, err := testFTB(srv.URL).Search(ctx, q); err == nil {
			t.Error("Search succeeded, want upstream error")
		}
	})
	t.Run("detail failure fails the search", func(t *testing.T) {
		srv := newFTBServer(t, []int{130}, func(int) ([]byte, int) { return nil, http.StatusBadGateway })
		if _, err := testFTB(srv.URL).Search(ctx, q); err == nil {
			t.Error("Search succeeded, want upstream error")
		}
	})
	t.Run("unknown pack is skipped", func(t *testing.T) {
		srv := newFTBServer(t, []int{999, 130}, ftbFixturePacks())
		got, err := testFTB(srv.URL).Search(ctx, q)
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if ids := projectIDs(got); !reflect.DeepEqual(ids, []string{"130"}) {
			t.Errorf("ids = %v, want [130]", ids)
		}
	})
	t.Run("versions of unknown pack", func(t *testing.T) {
		srv := newFTBServer(t, nil, ftbFixturePacks())
		if _, err := testFTB(srv.URL).Versions(ctx, "999", Filter{}); err == nil {
			t.Error("Versions succeeded, want error")
		}
	})
	t.Run("invalid pack id", func(t *testing.T) {
		srv := newFTBServer(t, nil, ftbFixturePacks())
		if _, err := testFTB(srv.URL).Versions(ctx, "../130", Filter{}); err == nil {
			t.Error("Versions succeeded, want error")
		}
		if n := srv.detailCount(); n != 0 {
			t.Errorf("detail calls = %d, want 0", n)
		}
	})
}

func TestFTBModsSearchReturnsNothing(t *testing.T) {
	got, err := testFTB("http://unused").Search(context.Background(), SearchQuery{Term: "x"})
	if err != nil || got != nil {
		t.Errorf("mod search = %v, %v, want nil, nil", got, err)
	}
}

func TestFTBVersions(t *testing.T) {
	srv := newFTBServer(t, nil, ftbFixturePacks())
	ctx := context.Background()

	got, err := testFTB(srv.URL).Versions(ctx, "130", Filter{Loader: "neoforge", GameVersion: "1.21.1", ProjectType: "modpack"})
	if err != nil {
		t.Fatalf("Versions: %v", err)
	}
	// 56 versions in the fixture, 5 archived.
	if len(got) != 51 {
		t.Fatalf("len = %d, want 51", len(got))
	}
	if got[0].ID != "100574" || got[0].Name != "1.24.1" || got[len(got)-1].ID != "100140" {
		t.Errorf("order: first %+v last %+v, want newest 100574 first and 100140 last", got[0], got[len(got)-1])
	}
	for _, v := range got {
		if v.ID == "100142" {
			t.Error("archived version 100142 listed")
		}
	}
	if !reflect.DeepEqual(got[0].GameVersions, []string{"1.21.1"}) || !reflect.DeepEqual(got[0].Loaders, []string{"neoforge"}) ||
		got[0].Files == nil || len(got[0].Files) != 0 {
		t.Errorf("fields = %+v", got[0])
	}

	// Wrong loader or game version: nothing, as an empty (non-nil) list.
	none, err := testFTB(srv.URL).Versions(ctx, "130", Filter{Loader: "forge", GameVersion: "1.21.1"})
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("forge 1.21.1 = %v, %v, want empty non-nil", none, err)
	}

	// Pack 100: 20 release + 6 beta + 1 alpha on Forge 1.18.2, 1 archived.
	st3, err := testFTB(srv.URL).Versions(ctx, "100", Filter{Loader: "forge", GameVersion: "1.18.2"})
	if err != nil || len(st3) != 27 {
		t.Errorf("pack 100 forge 1.18.2 = %d versions, %v, want 27", len(st3), err)
	}
}

func TestSetForFTB(t *testing.T) {
	s := NewSet("test", StaticKeys(map[string]string{}))
	p, ok := s.For(context.Background(), Config{Provider: "ftb"})
	if !ok {
		t.Fatal("ftb should be selectable without a key")
	}
	if _, isFTB := p.(*FTB); !isFTB {
		t.Errorf("provider = %T, want *FTB", p)
	}
	if !s.Available(context.Background(), "ftb") {
		t.Error("ftb should be available without a key")
	}
}
