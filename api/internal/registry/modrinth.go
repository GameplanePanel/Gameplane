package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"sort"
	"strconv"
)

// Modrinth searches the Modrinth v2 API (keyless). It suits Minecraft —
// the loader id (paper/spigot/bukkit/purpur/fabric/forge/quilt/neoforge)
// is used verbatim as a Modrinth "categories" facet, which disambiguates
// plugins from mods, and the clean game-version token as a "versions"
// facet.
type Modrinth struct {
	client    *http.Client
	userAgent string
	baseURL   string // overridable in tests; default https://api.modrinth.com/v2
}

func newModrinth(client *http.Client, userAgent string) *Modrinth {
	return &Modrinth{client: client, userAgent: userAgent, baseURL: "https://api.modrinth.com/v2"}
}

// Search runs a faceted project search. An empty loader/game-version omits
// that facet (broader results) rather than sending a wrong one.
func (m *Modrinth) Search(ctx context.Context, q SearchQuery) ([]Project, error) {
	var facets [][]string
	if q.Loader != "" {
		facets = append(facets, []string{"categories:" + q.Loader})
	}
	if q.Category != "" {
		facets = append(facets, []string{"categories:" + q.Category})
	}
	if q.GameVersion != "" {
		facets = append(facets, []string{"versions:" + q.GameVersion})
	}
	// The modpacks browser pins project_type:modpack. The mod browser leaves
	// it open (the loader facet already scopes to mods/plugins) — Modrinth v2
	// can't express "not modpack", but modpacks rarely surface under a loader
	// facet, so this is good enough.
	if q.modpack() {
		facets = append(facets, []string{"project_type:modpack"})
	}

	params := url.Values{}
	params.Set("query", q.Term)
	params.Set("limit", strconv.Itoa(clampLimit(q.Limit)))
	if q.Offset > 0 {
		params.Set("offset", strconv.Itoa(q.Offset))
	}
	if idx := modrinthIndex(q.Sort); idx != "" {
		params.Set("index", idx)
	}
	if len(facets) > 0 {
		b, err := json.Marshal(facets)
		if err != nil {
			return nil, err
		}
		params.Set("facets", string(b))
	}

	var resp struct {
		Hits []struct {
			ProjectID   string `json:"project_id"`
			Slug        string `json:"slug"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Author      string `json:"author"`
			IconURL     string `json:"icon_url"`
			Downloads   int64  `json:"downloads"`
		} `json:"hits"`
	}
	if err := httpGetJSON(ctx, m.client, m.userAgent, m.baseURL+"/search?"+params.Encode(), &resp, defaultMaxRespBytes); err != nil {
		return nil, err
	}

	out := make([]Project, 0, len(resp.Hits))
	for _, h := range resp.Hits {
		slug := h.Slug
		if slug == "" {
			slug = h.ProjectID
		}
		out = append(out, Project{
			ID:          h.ProjectID,
			Slug:        h.Slug,
			Title:       h.Title,
			Description: h.Description,
			Author:      h.Author,
			IconURL:     h.IconURL,
			Downloads:   h.Downloads,
			PageURL:     "https://modrinth.com/project/" + slug,
			Provider:    "modrinth",
		})
	}
	return out, nil
}

// Versions lists a project's versions, filtered to the active loader +
// game version, newest first.
func (m *Modrinth) Versions(ctx context.Context, projectID string, f Filter) ([]Version, error) {
	params := url.Values{}
	if f.Loader != "" {
		b, err := json.Marshal([]string{f.Loader})
		if err != nil {
			return nil, err
		}
		params.Set("loaders", string(b))
	}
	if f.GameVersion != "" {
		b, err := json.Marshal([]string{f.GameVersion})
		if err != nil {
			return nil, err
		}
		params.Set("game_versions", string(b))
	}
	u := m.baseURL + "/project/" + url.PathEscape(projectID) + "/version"
	if enc := params.Encode(); enc != "" {
		u += "?" + enc
	}

	var raw []struct {
		ID            string   `json:"id"`
		Name          string   `json:"name"`
		VersionNumber string   `json:"version_number"`
		GameVersions  []string `json:"game_versions"`
		Loaders       []string `json:"loaders"`
		DatePublished string   `json:"date_published"`
		VersionType   string   `json:"version_type"`
		Files         []struct {
			URL      string `json:"url"`
			Filename string `json:"filename"`
			Primary  bool   `json:"primary"`
			Size     int64  `json:"size"`
		} `json:"files"`
	}
	if err := httpGetJSON(ctx, m.client, m.userAgent, u, &raw, defaultMaxRespBytes); err != nil {
		return nil, err
	}

	// Modrinth doesn't guarantee ordering; sort newest-first by publish date.
	sort.SliceStable(raw, func(i, j int) bool { return raw[i].DatePublished > raw[j].DatePublished })

	out := make([]Version, 0, len(raw))
	for _, v := range raw {
		files := make([]File, 0, len(v.Files))
		for _, fl := range v.Files {
			files = append(files, File{
				Filename:    fl.Filename,
				DownloadURL: fl.URL,
				Size:        fl.Size,
				Primary:     fl.Primary,
			})
		}
		out = append(out, Version{
			ID:            v.ID,
			Name:          v.Name,
			VersionNumber: v.VersionNumber,
			GameVersions:  v.GameVersions,
			Loaders:       v.Loaders,
			ReleaseType:   v.VersionType,
			PublishedAt:   v.DatePublished,
			Files:         files,
		})
	}
	return out, nil
}

// ModpackDeps is a no-op for Modrinth: on the supported game (Minecraft via
// itzg) a modpack installs through the MODRINTH_MODPACK env, not by
// installing dependency files.
func (m *Modrinth) ModpackDeps(_ context.Context, _ string) ([]File, error) {
	return nil, nil
}

// clampLimit bounds a caller-supplied result limit to a sane range.
func clampLimit(n int) int {
	if n <= 0 || n > 100 {
		return 20
	}
	return n
}

// modrinthIndex maps a normalized Sort to Modrinth's "index" param. Empty
// (the default) lets Modrinth use relevance.
func modrinthIndex(sort string) string {
	switch sort {
	case "downloads", "follows", "newest", "updated", "relevance":
		return sort
	default:
		return ""
	}
}
