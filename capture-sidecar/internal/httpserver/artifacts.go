package httpserver

import (
	"encoding/json"
	"io"
	"net/http"

	"github.com/GameplanePanel/gameplane/capture-sidecar/internal/nodecapture"
)

// ArtifactRoutes exposes retained node-worker results without any operation
// that can open a raw socket or start privileged work. The Pod is Restricted.
func (s *Server) ArtifactRoutes(mw func(http.Handler) http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	if mw == nil {
		mw = func(h http.Handler) http.Handler { return h }
	}
	mux.HandleFunc("GET /healthz", HandleHealthz)
	mux.Handle("GET /captures/{id}/status", mw(http.HandlerFunc(s.handleArtifactStatus)))
	mux.Handle("GET /v1/targets/{serverUID}/captures/{id}/uids/{captureUID}/file", mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, ok := s.artifactResult(r.PathValue("id"))
		if !ok || result.Status != "completed" {
			http.NotFound(w, r)
			return
		}
		s.HandleBoundDownload(w, r)
	})))
	mux.Handle("DELETE /v1/targets/{serverUID}/captures/{id}/uids/{captureUID}/file", mw(http.HandlerFunc(s.HandleBoundDelete)))
	return mux
}

func (s *Server) artifactResult(id string) (nodecapture.Result, bool) {
	var result nodecapture.Result
	if !validCaptureID(id) {
		return result, false
	}
	f, err := s.openCaptureRegular("capture-" + id + ".result.json")
	if err != nil {
		return result, false
	}
	defer func() { _ = f.Close() }()
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(raw) > 4096 || json.Unmarshal(raw, &result) != nil || result.CaptureID != id || result.ServerUID != s.serverUID || !captureUIDPattern.MatchString(result.CaptureUID) {
		return result, false
	}
	return result, true
}

func (s *Server) handleArtifactStatus(w http.ResponseWriter, r *http.Request) {
	result, ok := s.artifactResult(r.PathValue("id"))
	if !ok || r.Header.Get("X-Gameplane-Server-UID") != result.ServerUID || r.Header.Get("X-Gameplane-Capture-UID") != result.CaptureUID {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
