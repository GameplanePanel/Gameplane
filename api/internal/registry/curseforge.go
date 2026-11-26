package registry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Curseforge searches the CurseForge v1 API (api.curseforge.com), which
// requires an x-api-key. CurseForge hosts mods for many games, not just
// Minecraft — Search must be faceted to exactly one numeric CurseForge game
// id (see curseforgeGame below — a template picks the game the way
// Thunderstore picks a community or Steam picks an app id). Loader
// filtering maps Gameplane loader ids to CurseForge's numeric
// modLoaderType; the clean game-version token is the gameVersion filter.
type Curseforge struct {
	client    *http.Client
	userAgent string
	apiKey    string
	baseURL   string // overridable in tests; default https://api.curseforge.com/v1
}

func newCurseforge(client *http.Client, userAgent, apiKey string) *Curseforge {
	return &Curseforge{client: client, userAgent: userAgent, apiKey: apiKey, baseURL: "https://api.curseforge.com/v1"}
}

// curseforgeGame binds the CurseForge engine to one template's numeric
// CurseForge game id, mirroring steamApp's per-config wrapper. CurseForge
// has no single "browse everything" search — a template must declare
// curseforgeGameID the same way it declares steamAppID.
type curseforgeGame struct {
	cf     *Curseforge
	gameID int32
}

func (g *curseforgeGame) Search(ctx context.Context, q SearchQuery) ([]Project, error) {
	return g.cf.search(ctx, q, g.gameID)
}

func (g *curseforgeGame) Versions(ctx context.Context, projectID string, f Filter) ([]Version, error) {
	return g.cf.Versions(ctx, projectID, f)
}

// ModpackDeps is a no-op: a CurseForge modpack installs through the game
// image's env (itzg AUTO_CURSEFORGE with the pack slug and file id the
// operator renders from spec.modpack), never by resolving dependency mods.
func (g *curseforgeGame) ModpackDeps(ctx context.Context, projectID string) ([]File, error) {
	return g.cf.ModpackDeps(ctx, projectID)
}

// CurseForge numeric constants (stable, from the official API schema).
// cfGameMinecraft also marks the one game whose classId taxonomy this
// package knows — see classID below.
const (
	cfGameMinecraft = 432
	cfClassMods     = 6
	cfClassModpacks = 4471
)

// cfLoaderType maps a Gameplane loader id to CurseForge's modLoaderType enum.
// 0 (Any) means "no loader filter" — used for plugin loaders CurseForge
// doesn't model as mod loaders.
func cfLoaderType(loader string) int {
	switch loader {
	case "forge":
		return 1
	case "fabric":
		return 4
	case "quilt":
		return 5
	case "neoforge":
		return 6
	default:
		return 0
	}
}

// cfSortField maps a normalized Sort to CurseForge's sortField enum.
func cfSortField(sort string) int {
	switch sort {
	case "downloads":
		return 6 // TotalDownloads
	case "updated":
		return 3 // LastUpdated
	case "newest":
		return 11 // ReleasedDate
	default:
		return 2 // Popularity
	}
}

// classID returns the CurseForge classId to filter results by, or 0 for "no
// class filter". CurseForge's class ids are per-game — 6 (mods) and 4471
// (modpacks) are Minecraft's ids specifically, not a general CurseForge
// concept. Sending them for a different game would filter by classes that
// don't exist there and silently return zero results, so that split is
// kept ONLY for gameId 432 (Minecraft); every other game gets no classId
// filter, i.e. its search isn't narrowed to mods vs. modpacks.
func (c *Curseforge) classID(q SearchQuery, gameID int32) int {
	if gameID != cfGameMinecraft {
		return 0
	}
	if q.modpack() {
		return cfClassModpacks
	}
	return cfClassMods
}

// search runs a CurseForge mod/modpack search scoped to gameID (see
// curseforgeGame — the exported Provider surface is that per-config
// wrapper, not this method).
func (c *Curseforge) search(ctx context.Context, q SearchQuery, gameID int32) ([]Project, error) {
	params := url.Values{}
	params.Set("gameId", strconv.Itoa(int(gameID)))
	if classID := c.classID(q, gameID); classID != 0 {
		params.Set("classId", strconv.Itoa(classID))
	}
	if q.Term != "" {
		params.Set("searchFilter", q.Term)
	}
	if q.GameVersion != "" {
		params.Set("gameVersion", q.GameVersion)
	}
	if lt := cfLoaderType(q.Loader); lt != 0 {
		params.Set("modLoaderType", strconv.Itoa(lt))
	}
	params.Set("sortField", strconv.Itoa(cfSortField(q.Sort)))
	params.Set("sortOrder", "desc")
	params.Set("pageSize", strconv.Itoa(clampLimit(q.Limit)))
	if q.Offset > 0 {
		params.Set("index", strconv.Itoa(q.Offset))
	}

	var resp struct {
		Data []cfMod `json:"data"`
	}
	if err := c.get(ctx, c.baseURL+"/mods/search?"+params.Encode(), &resp); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(resp.Data))
	for _, m := range resp.Data {
		out = append(out, m.project())
	}
	return out, nil
}

type cfMod struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	Slug          string `json:"slug"`
	Summary       string `json:"summary"`
	DownloadCount int64  `json:"downloadCount"`
	Authors       []struct {
		Name string `json:"name"`
	} `json:"authors"`
	Logo struct {
		URL string `json:"url"`
	} `json:"logo"`
	Links struct {
		WebsiteURL string `json:"websiteUrl"`
	} `json:"links"`
}

func (m cfMod) project() Project {
	author := ""
	if len(m.Authors) > 0 {
		author = m.Authors[0].Name
	}
	return Project{
		ID:          strconv.Itoa(m.ID),
		Slug:        m.Slug,
		Title:       m.Name,
		Description: m.Summary,
		Author:      author,
		IconURL:     m.Logo.URL,
		Downloads:   m.DownloadCount,
		PageURL:     m.Links.WebsiteURL,
		Provider:    "curseforge",
	}
}

// Versions lists a mod's (or, with f.ProjectType "modpack", a modpack's)
// files filtered to the active loader + game version, newest first
// (CurseForge returns files newest-first already). Modpacks use the same
// /mods/{id}/files endpoint as mods (class 4471 only matters for search);
// because their pinned file id is what the operator installs, the filter is
// also applied client side for modpacks. Files whose author disabled
// third-party distribution carry no downloadUrl and are skipped — neither
// the dashboard nor the pack installer can fetch them.
func (c *Curseforge) Versions(ctx context.Context, projectID string, f Filter) ([]Version, error) {
	files, err := c.versionFiles(ctx, projectID, f)
	if err != nil {
		return nil, err
	}
	out := make([]Version, 0, len(files))
	for _, fl := range files {
		if fl.DownloadURL == "" {
			continue
		}
		if f.modpack() && !cfFileMatches(fl.GameVersions, f) {
			continue
		}
		published := ""
		if t, err := time.Parse(time.RFC3339Nano, fl.FileDate); err == nil {
			published = t.UTC().Format(time.RFC3339)
		}
		out = append(out, Version{
			ID:            strconv.Itoa(fl.ID),
			Name:          fl.DisplayName,
			VersionNumber: fl.DisplayName,
			GameVersions:  fl.GameVersions,
			Loaders:       cfLoaders(fl.GameVersions),
			ReleaseType:   cfReleaseType(fl.ReleaseType),
			PublishedAt:   published,
			Files: []File{{
				Filename:    fl.FileName,
				DownloadURL: fl.DownloadURL,
				Size:        fl.FileLength,
				Primary:     true,
			}},
		})
	}
	return out, nil
}

// Unfiltered browses (no loader and no game version, the "All loaders" modpack
// list) page through the pack's files: index 0, 50, 100, ... until a page holds
// fewer than cfVersionsPageSize files or cfVersionsMaxPages pages were read.
// That is a hard cap of 250 files per browse. Filtered calls read one page.
const (
	cfVersionsPageSize = 50
	cfVersionsMaxPages = 5
)

// versionFiles fetches the project's raw files for Versions, one page for a
// filtered call and paged files for an unfiltered one (see cfVersionsMaxPages).
func (c *Curseforge) versionFiles(ctx context.Context, projectID string, f Filter) ([]cfFile, error) {
	unfiltered := f.Loader == "" && f.GameVersion == ""
	pages := 1
	if unfiltered {
		pages = cfVersionsMaxPages
	}
	var files []cfFile
	for page := 0; page < pages; page++ {
		params := url.Values{}
		if f.GameVersion != "" {
			params.Set("gameVersion", f.GameVersion)
		}
		if lt := cfLoaderType(f.Loader); lt != 0 {
			params.Set("modLoaderType", strconv.Itoa(lt))
		}
		params.Set("pageSize", strconv.Itoa(cfVersionsPageSize))
		if unfiltered {
			params.Set("index", strconv.Itoa(page*cfVersionsPageSize))
		}
		u := c.baseURL + "/mods/" + url.PathEscape(projectID) + "/files"
		if enc := params.Encode(); enc != "" {
			u += "?" + enc
		}
		var resp struct {
			Data []cfFile `json:"data"`
		}
		if err := c.get(ctx, u, &resp); err != nil {
			return nil, err
		}
		files = append(files, resp.Data...)
		if len(resp.Data) < cfVersionsPageSize {
			break
		}
	}
	return files, nil
}

type cfFile struct {
	ID           int      `json:"id"`
	DisplayName  string   `json:"displayName"`
	FileName     string   `json:"fileName"`
	DownloadURL  string   `json:"downloadUrl"`
	FileLength   int64    `json:"fileLength"`
	GameVersions []string `json:"gameVersions"`
	ReleaseType  int      `json:"releaseType"`
	FileDate     string   `json:"fileDate"`
}

// cfReleaseType maps CurseForge's numeric releaseType (1 release, 2 beta,
// 3 alpha) to the Version.ReleaseType vocabulary; "" for anything else.
func cfReleaseType(n int) string {
	switch n {
	case 1:
		return "release"
	case 2:
		return "beta"
	case 3:
		return "alpha"
	default:
		return ""
	}
}

// cfLoaderTags maps a Gameplane loader id to the loader tag CurseForge puts
// in a file's gameVersions list next to the Minecraft version.
var cfLoaderTags = map[string]string{
	"forge":    "Forge",
	"neoforge": "NeoForge",
	"fabric":   "Fabric",
	"quilt":    "Quilt",
}

// cfLoaders returns the Gameplane loader ids (lower-cased cfLoaderTags keys)
// of the loader tags in a file's gameVersions, in tag order, without repeats.
func cfLoaders(tags []string) []string {
	var out []string
	for _, tag := range tags {
		id := strings.ToLower(tag)
		if _, ok := cfLoaderTags[id]; !ok || slices.Contains(out, id) {
			continue
		}
		out = append(out, id)
	}
	return out
}

// cfFileMatches reports whether a file's gameVersions tags fit the filter.
// The game version must be listed exactly (when set). The loader (when set)
// matches if the file lists it, or if the file lists no loader tag at all
// (nothing to judge by); a file that lists only other loaders is rejected.
func cfFileMatches(tags []string, f Filter) bool {
	if f.GameVersion != "" && !slices.Contains(tags, f.GameVersion) {
		return false
	}
	want, ok := cfLoaderTags[f.Loader]
	if !ok {
		return true
	}
	hasLoader := false
	for _, tag := range tags {
		for _, known := range cfLoaderTags {
			if !strings.EqualFold(tag, known) {
				continue
			}
			if strings.EqualFold(tag, want) {
				return true
			}
			hasLoader = true
		}
	}
	return !hasLoader
}

// ModpackDeps is a no-op: a CurseForge modpack installs through the game
// image's env (see curseforgeGame.ModpackDeps, the Provider surface that
// actually gets called), so there are no dependency files to resolve.
func (c *Curseforge) ModpackDeps(_ context.Context, _ string) ([]File, error) {
	return nil, nil
}

// get performs an authenticated CurseForge GET into v.
func (c *Curseforge) get(ctx context.Context, rawURL string, v any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fmt.Errorf("curseforge request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("curseforge GET: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("curseforge GET: upstream status %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, defaultMaxRespBytes)).Decode(v); err != nil {
		return fmt.Errorf("curseforge decode: %w", err)
	}
	return nil
}
