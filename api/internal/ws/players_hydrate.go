package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"time"
)

// PlayerNameResolver maps Steam IDs to Steam display names. Ids that cannot be
// resolved are absent from the returned map; callers keep the raw id for them.
type PlayerNameResolver interface {
	Resolve(ctx context.Context, steamIDs []string) map[string]string
}

// playerNameBudget bounds the name lookup for one player-list request. The
// resolver's own singleflight ignores caller deadlines, so the wait happens
// here and is abandoned on expiry. A var so tests can shorten it.
var playerNameBudget = 1500 * time.Millisecond

// hydratedPlayers proxies agentPath exactly as httpProxy does, then sets
// displayName on structured player entries when a resolver is configured.
// Every other case is the agent's status, headers and bytes verbatim.
func (p *proxy) hydratedPlayers(agentPath string) http.HandlerFunc {
	proxied := p.httpProxy(agentPath)
	return func(w http.ResponseWriter, req *http.Request) {
		if p.playerNames == nil {
			proxied(w, req)
			return
		}
		buf := &bufferedResponse{header: make(http.Header), status: http.StatusOK}
		proxied(buf, req)
		body := buf.body.Bytes()
		if buf.status == http.StatusOK && buf.header.Get("Content-Encoding") == "" {
			if hydrated, ok := hydratePlayers(req.Context(), p.playerNames, body); ok {
				body = hydrated
				buf.header.Del("Content-Length")
			}
		}
		for k, v := range buf.header {
			w.Header()[k] = v
		}
		w.WriteHeader(buf.status)
		_, _ = w.Write(body)
	}
}

// hydratePlayers returns body with displayName set on each structured entry
// whose Steam ID resolved, leaving every other field untouched. It reports
// false, and the caller passes the agent bytes through, when body is not a
// JSON object with a non-empty "entries" array carrying Steam IDs, or when
// no name resolved in time.
func hydratePlayers(parent context.Context, names PlayerNameResolver, body []byte) ([]byte, bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil || top == nil {
		return nil, false
	}
	var entries []map[string]json.RawMessage
	if err := json.Unmarshal(top["entries"], &entries); err != nil || len(entries) == 0 {
		return nil, false
	}
	steamIDs := make([]string, len(entries))
	ids := make([]string, 0, len(entries))
	seen := make(map[string]bool, len(entries))
	for i, entry := range entries {
		id := entrySteamID(entry)
		steamIDs[i] = id
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, false
	}

	ctx, cancel := context.WithTimeout(parent, playerNameBudget)
	defer cancel()
	done := make(chan map[string]string, 1)
	go func() { done <- names.Resolve(ctx, ids) }()
	var resolved map[string]string
	select {
	case resolved = <-done:
	case <-ctx.Done():
		return nil, false
	}

	hydrated := false
	for i, entry := range entries {
		name := resolved[steamIDs[i]]
		if steamIDs[i] == "" || name == "" {
			continue
		}
		raw, err := json.Marshal(name)
		if err != nil {
			continue
		}
		entry["displayName"] = raw
		hydrated = true
	}
	if !hydrated {
		return nil, false
	}
	rawEntries, err := json.Marshal(entries)
	if err != nil {
		return nil, false
	}
	top["entries"] = rawEntries
	out, err := json.Marshal(top)
	if err != nil {
		return nil, false
	}
	return out, true
}

// entrySteamID returns the entry's "steamId" string, or "" when absent or
// not a string.
func entrySteamID(entry map[string]json.RawMessage) string {
	var id string
	if err := json.Unmarshal(entry["steamId"], &id); err != nil {
		return ""
	}
	return id
}

// bufferedResponse captures an upstream response so it can be inspected
// before anything is written to the browser.
type bufferedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (b *bufferedResponse) Header() http.Header { return b.header }

func (b *bufferedResponse) WriteHeader(code int) { b.status = code }

func (b *bufferedResponse) Write(p []byte) (int, error) { return b.body.Write(p) }
