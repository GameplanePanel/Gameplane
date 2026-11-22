package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"
)

// FTB searches Feed The Beast's public modpack API
// (api.feed-the-beast.com), which needs no key. It is modpack-only: the Mods
// tab never browses it (the template marks the provider modpacksOnly) and a
// non-modpack Search returns nothing, like Hangar does for modpacks.
//
// FTB's search answers with pack ids only, so Search asks for up to
// ftbCandidateLimit candidate ids, then resolves pack details in batches of
// ftbBatchSize with at most ftbConcurrency requests in flight, keeping only
// packs that have a version matching the server's Minecraft version and
// loader. Filtering happens before the page limit: it stops when the
// filtered page is full, the candidates run out, or ftbMaxDetails details
// have been resolved, so latency stays bounded. Details are cached for
// ftbDetailTTL (Versions reuses them). Installing is not done here: the
// operator installs the pack through the game image (TYPE=FTBA with the pack
// and version ids), so versions carry no download files.
type FTB struct {
	client    *http.Client
	userAgent string
	baseURL   string           // overridable in tests; default https://api.feed-the-beast.com/v1/modpacks/public/modpack
	now       func() time.Time // test seam for the detail cache

	mu    sync.Mutex
	cache map[int]ftbCacheEntry
}

const (
	ftbCandidateLimit = 100 // candidate ids requested from FTB per search
	ftbBatchSize      = 20  // details resolved per batch
	ftbMaxDetails     = 100 // hard cap on details resolved per search
	ftbConcurrency    = 4   // parallel detail requests
	ftbDetailTTL      = 10 * time.Minute
)

// errFTBNotFound marks an id FTB no longer knows (HTTP 404). Search skips
// such candidates; Versions reports them.
var errFTBNotFound = errors.New("ftb: pack not found")

type ftbCacheEntry struct {
	pack    *ftbPack
	fetched time.Time
}

// ftbPack is the compact, cached form of one pack detail.
type ftbPack struct {
	ID       int
	Slug     string
	Name     string
	Synopsis string
	Author   string
	IconURL  string
	Installs int64
	Versions []ftbVersion
}

type ftbVersion struct {
	ID      int
	Name    string
	Type    string // release, beta, alpha, archived
	Updated int64
	Game    string // Minecraft version
	Loader  string // forge, neoforge, fabric, quilt; "" when none
}

func newFTB(client *http.Client, userAgent string) *FTB {
	return &FTB{
		client:    client,
		userAgent: userAgent,
		baseURL:   "https://api.feed-the-beast.com/v1/modpacks/public/modpack",
		now:       time.Now,
		cache:     map[int]ftbCacheEntry{},
	}
}

// Search lists FTB packs that have a version for q.GameVersion and q.Loader
// (either may be empty: no filter). An empty term uses FTB's popular list.
// Sort and Category are not supported by FTB and are ignored; Offset applies
// to the filtered list.
func (f *FTB) Search(ctx context.Context, q SearchQuery) ([]Project, error) {
	if !q.modpack() {
		return nil, nil
	}
	ids, err := f.candidates(ctx, q.Term)
	if err != nil {
		return nil, err
	}
	if len(ids) > ftbMaxDetails {
		ids = ids[:ftbMaxDetails]
	}
	offset := max(q.Offset, 0)
	need := clampLimit(q.Limit) + offset

	var matched []Project
	for start := 0; start < len(ids) && len(matched) < need; start += ftbBatchSize {
		batch := ids[start:min(start+ftbBatchSize, len(ids))]
		packs, err := f.resolve(ctx, batch)
		if err != nil {
			return nil, err
		}
		for _, p := range packs {
			if p != nil && p.hasMatch(q.Loader, q.GameVersion) {
				matched = append(matched, p.project())
			}
		}
	}
	if offset >= len(matched) {
		return []Project{}, nil
	}
	return matched[offset:min(need, len(matched))], nil
}

// candidates returns the candidate pack ids for a search term, or FTB's
// popular list when the term is empty.
func (f *FTB) candidates(ctx context.Context, term string) ([]int, error) {
	u := f.baseURL + "/popular/installs/" + strconv.Itoa(ftbCandidateLimit)
	if term != "" {
		u = f.baseURL + "/search/" + strconv.Itoa(ftbCandidateLimit) + "?" + url.Values{"term": {term}}.Encode()
	}
	var resp struct {
		Packs []int `json:"packs"`
	}
	if err := f.get(ctx, u, &resp); err != nil {
		return nil, fmt.Errorf("ftb search: %w", err)
	}
	return resp.Packs, nil
}

// resolve fetches the details of one batch with bounded concurrency. The
// result keeps the batch order; a pack FTB no longer knows is nil.
func (f *FTB) resolve(ctx context.Context, ids []int) ([]*ftbPack, error) {
	out := make([]*ftbPack, len(ids))
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(ftbConcurrency)
	for i, id := range ids {
		g.Go(func() error {
			p, err := f.pack(gctx, id)
			if errors.Is(err, errFTBNotFound) {
				return nil
			}
			if err != nil {
				return err
			}
			out[i] = p
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, err
	}
	return out, nil
}

// pack returns one pack's compact detail, served from the cache while fresh.
func (f *FTB) pack(ctx context.Context, id int) (*ftbPack, error) {
	f.mu.Lock()
	e, ok := f.cache[id]
	fresh := ok && f.now().Sub(e.fetched) < ftbDetailTTL
	f.mu.Unlock()
	if fresh {
		return e.pack, nil
	}

	var raw struct {
		ID       int    `json:"id"`
		Slug     string `json:"slug"`
		Name     string `json:"name"`
		Synopsis string `json:"synopsis"`
		Installs int64  `json:"installs"`
		Authors  []struct {
			Name string `json:"name"`
		} `json:"authors"`
		Art []struct {
			Type string `json:"type"`
			URL  string `json:"url"`
		} `json:"art"`
		Versions []struct {
			ID      int    `json:"id"`
			Name    string `json:"name"`
			Type    string `json:"type"`
			Updated int64  `json:"updated"`
			Targets []struct {
				Name    string `json:"name"`
				Type    string `json:"type"`
				Version string `json:"version"`
			} `json:"targets"`
		} `json:"versions"`
	}
	if err := f.get(ctx, f.baseURL+"/"+strconv.Itoa(id), &raw); err != nil {
		return nil, fmt.Errorf("ftb pack %d: %w", id, err)
	}
	p := &ftbPack{
		ID:       raw.ID,
		Slug:     raw.Slug,
		Name:     raw.Name,
		Synopsis: raw.Synopsis,
		Installs: raw.Installs,
	}
	if len(raw.Authors) > 0 {
		p.Author = raw.Authors[0].Name
	}
	for _, a := range raw.Art {
		if a.Type == "square" {
			p.IconURL = a.URL
			break
		}
	}
	for _, v := range raw.Versions {
		fv := ftbVersion{ID: v.ID, Name: v.Name, Type: v.Type, Updated: v.Updated}
		for _, t := range v.Targets {
			switch t.Type {
			case "game":
				fv.Game = t.Version
			case "modloader":
				fv.Loader = t.Name
			}
		}
		p.Versions = append(p.Versions, fv)
	}

	f.mu.Lock()
	f.cache[id] = ftbCacheEntry{pack: p, fetched: f.now()}
	f.mu.Unlock()
	return p, nil
}

// matches reports whether a version is installable for the filter: not
// archived, and (when set) on the given Minecraft version and loader.
func (v ftbVersion) matches(loader, gameVersion string) bool {
	if v.Type == "archived" {
		return false
	}
	if gameVersion != "" && v.Game != gameVersion {
		return false
	}
	return loader == "" || strings.EqualFold(v.Loader, loader)
}

func (p *ftbPack) hasMatch(loader, gameVersion string) bool {
	for _, v := range p.Versions {
		if v.matches(loader, gameVersion) {
			return true
		}
	}
	return false
}

func (p *ftbPack) project() Project {
	return Project{
		ID:          strconv.Itoa(p.ID),
		Slug:        p.Slug,
		Title:       p.Name,
		Description: p.Synopsis,
		Author:      p.Author,
		IconURL:     p.IconURL,
		Downloads:   p.Installs,
		Provider:    "ftb",
	}
}

// Versions lists a pack's installable versions that match the server's
// Minecraft version and loader, newest first. projectID is FTB's numeric pack
// id. Versions carry no files: the operator installs the pack by id through
// the game image.
func (f *FTB) Versions(ctx context.Context, projectID string, flt Filter) ([]Version, error) {
	if !isDigits(projectID) || len(projectID) > 9 {
		return nil, fmt.Errorf("ftb: invalid pack id %q", projectID)
	}
	id, err := strconv.Atoi(projectID)
	if err != nil {
		return nil, fmt.Errorf("ftb: invalid pack id %q: %w", projectID, err)
	}
	p, err := f.pack(ctx, id)
	if err != nil {
		return nil, err
	}
	var match []ftbVersion
	for _, v := range p.Versions {
		if v.matches(flt.Loader, flt.GameVersion) {
			match = append(match, v)
		}
	}
	sort.SliceStable(match, func(i, j int) bool {
		if match[i].Updated != match[j].Updated {
			return match[i].Updated > match[j].Updated
		}
		return match[i].ID > match[j].ID
	})
	out := make([]Version, 0, len(match))
	for _, v := range match {
		ver := Version{
			ID:            strconv.Itoa(v.ID),
			Name:          v.Name,
			VersionNumber: v.Name,
			Files:         []File{},
		}
		switch v.Type {
		case "release", "beta", "alpha":
			ver.ReleaseType = v.Type
		}
		if v.Updated > 0 {
			ver.PublishedAt = time.Unix(v.Updated, 0).UTC().Format(time.RFC3339)
		}
		if v.Game != "" {
			ver.GameVersions = []string{v.Game}
		}
		if v.Loader != "" {
			ver.Loaders = []string{v.Loader}
		}
		out = append(out, ver)
	}
	return out, nil
}

// ModpackDeps is a no-op: FTB packs install through the game image's env
// (TYPE=FTBA with the pack and version ids), not by resolving dependencies.
func (f *FTB) ModpackDeps(_ context.Context, _ string) ([]File, error) {
	return nil, nil
}

// get performs a keyless FTB GET into v, mapping 404 to errFTBNotFound.
func (f *FTB) get(ctx context.Context, rawURL string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("ftb request: %w", err)
	}
	req.Header.Set("User-Agent", f.userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return sanitizeUpstreamErr("ftb GET", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusNotFound {
		return errFTBNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ftb GET: upstream status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, defaultMaxRespBytes)).Decode(v); err != nil {
		return fmt.Errorf("ftb decode: %w", err)
	}
	return nil
}
