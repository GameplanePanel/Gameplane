package handlers

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/ws"
)

// AgentFileReader reads one small file from a server's data volume through its
// agent sidecar; satisfied by *ws.AgentClient. A missing file must be reported
// as an error wrapping ws.ErrAgentFileNotFound. At most limit bytes are returned.
type AgentFileReader interface {
	ReadFileForCluster(ctx context.Context, cluster, name, namespace, path string, limit int64) ([]byte, error)
}

const (
	// missingFilesReadCap bounds the bytes read and parsed.
	missingFilesReadCap = 256 << 10
	// missingFilesMaxRows bounds the rows returned.
	missingFilesMaxRows = 200
	// missingFieldMaxRunes bounds each returned field.
	missingFieldMaxRunes = 256
	// curseForgePageHost is the only host a download page may point at.
	curseForgePageHost = "www.curseforge.com"
	// defaultDownloadsPath is the data-relative folder the module's
	// CF_DOWNLOADS_REPO=/data/downloads points the image at.
	defaultDownloadsPath = "downloads/"
)

var missingColumnSplit = regexp.MustCompile(`\s{2,}|\t+`)

// parseModsNeedDownload parses MODS_NEED_DOWNLOAD.txt. Blank lines, the header
// row, rows with fewer than 3 columns and rows whose last column is not an
// https://www.curseforge.com/... URL are dropped. Input beyond
// missingFilesReadCap and rows beyond missingFilesMaxRows are ignored. 4 columns
// are Mod, Version Name, Filename, Download page; 3 columns are Mod, Filename,
// Download page; with more than 4, the leading columns join into Mod.
func parseModsNeedDownload(data []byte) []modpackMissingFile {
	out := []modpackMissingFile{}
	if len(data) > missingFilesReadCap {
		data = data[:missingFilesReadCap]
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 4096), missingFilesReadCap+1)
	for sc.Scan() && len(out) < missingFilesMaxRows {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		cols := missingColumnSplit.Split(line, -1)
		n := len(cols)
		if n < 3 {
			continue
		}
		page := strings.TrimSpace(cols[n-1])
		if !validCurseForgePage(page) {
			continue
		}
		row := modpackMissingFile{DownloadPage: page, Filename: cleanMissingField(cols[n-2])}
		if n == 3 {
			row.Mod = cleanMissingField(cols[0])
		} else {
			row.VersionName = cleanMissingField(cols[n-3])
			row.Mod = cleanMissingField(strings.Join(cols[:n-3], " "))
		}
		out = append(out, row)
	}
	return out
}

// validCurseForgePage accepts only https://www.curseforge.com/... without
// userinfo, so a hostile file cannot make the dashboard link elsewhere.
func validCurseForgePage(raw string) bool {
	if len(raw) > 512 || !strings.HasPrefix(raw, "https://"+curseForgePageHost+"/") {
		return false
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == curseForgePageHost && u.User == nil
}

// cleanMissingField drops control characters and bounds the length.
func cleanMissingField(s string) string {
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	return truncate(s, missingFieldMaxRunes)
}

// downloadsPathFor derives the data-relative downloads folder from the
// provider's fixed CF_DOWNLOADS_REPO env, defaulting to "downloads/".
func downloadsPathFor(mp *modpackCfg) string {
	for _, e := range mp.env {
		if e.Name != "CF_DOWNLOADS_REPO" {
			continue
		}
		if rel := strings.Trim(strings.TrimPrefix(e.Value, "/data"), "/"); rel != "" {
			return rel + "/"
		}
	}
	return defaultDownloadsPath
}

// serverHasPod reports whether a pod (and so the agent) can exist: not
// suspended and in a phase that has a pod. Stopped, Suspended (also idle sleep)
// and Pending servers have none.
func serverHasPod(gs *unstructured.Unstructured) bool {
	if suspended, _, _ := unstructured.NestedBool(gs.Object, "spec", "suspend"); suspended {
		return false
	}
	phase, _, _ := unstructured.NestedString(gs.Object, "status", "phase")
	switch phase {
	case "Starting", "Running", "Stopping", "Failed":
		return true
	}
	return false
}

// fillMissingFiles sets view.MissingFiles / MissingFilesUnknown / DownloadsPath
// from the active pack's provider (missingFilesPath). No-op without an active
// pack or when the provider declares no missingFilesPath.
func (h *registryHandler) fillMissingFiles(req *http.Request, target *modTarget, gs *unstructured.Unstructured, view *modpackView) {
	if view.Active == nil {
		return
	}
	prov, ok := pickProvider(target.template, view.Active.Provider)
	if !ok || prov.modpacks == nil || prov.modpacks.missingFilesPath == "" {
		return
	}
	view.DownloadsPath = downloadsPathFor(prov.modpacks)
	if h.files == nil || !serverHasPod(gs) {
		view.MissingFilesUnknown = true
		return
	}
	data, err := h.files.ReadFileForCluster(req.Context(), target.cluster, gs.GetName(), target.namespace,
		prov.modpacks.missingFilesPath, missingFilesReadCap)
	switch {
	case err == nil:
		view.MissingFiles = parseModsNeedDownload(data)
	case errors.Is(err, ws.ErrAgentFileNotFound):
		// The image deletes the file after a good install: nothing to download.
	default:
		slog.Debug("read missing-files list failed", "server", gs.GetName(), "err", err)
		view.MissingFilesUnknown = true
	}
}
