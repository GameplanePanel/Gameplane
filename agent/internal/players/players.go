// Package players exposes online-player and moderation endpoints
// backed by RCON.
//
// Many game protocols are different, but "list online players via RCON
// list" works for all Minecraft variants (vanilla/paper/spigot/forge)
// and Source-engine servers. Moderation commands (kick/ban/unban/
// whitelist) are dispatched through a single template-driven commander
// that renders each command from the module's declared
// capabilities.players templates; a template declaring no capabilities
// yields a commander that reports everything unsupported.
package players

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
	"github.com/GameplanePanel/gameplane/agent/internal/httpjson"
	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
	"github.com/GameplanePanel/gameplane/gameaction"
)

// Rcon is the interface to the game's remote console.
type Rcon interface {
	Exec(cmd string) (string, error)
}

type handler struct {
	rcon Rcon
	cmdr commander
	game string

	listCmd string         // RCON command to fetch online players
	listRE  *regexp.Regexp // optional regex to parse entry output

	mu         sync.Mutex
	lastFetch  time.Time
	lastResult Snapshot
	lastErr    error
}

// Capabilities indicates which moderation commands are supported.
type Capabilities struct {
	Kick      bool `json:"kick"`
	Ban       bool `json:"ban"`
	Unban     bool `json:"unban"`
	Whitelist bool `json:"whitelist"`
}

// Entry is one structured player-list record for games whose list command
// returns JSON (Nuclear Option's get-player-list). It carries the game's
// own identifier and faction; the agent never adds a display name.
type Entry struct {
	SteamID string `json:"steamId"`
	Faction string `json:"faction,omitempty"`
}

// Snapshot is a player list query result.
type Snapshot struct {
	Online       int          `json:"online"`
	Max          int          `json:"max"`
	Players      []string     `json:"players"`
	Entries      []Entry      `json:"entries,omitempty"`
	AsOf         string       `json:"asOf"`
	Capabilities Capabilities `json:"capabilities"`
}

// BannedPlayer is a banned player entry.
type BannedPlayer struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
	Source string `json:"source,omitempty"`
}

// Mount wires the player endpoints. actions carries the module's
// declared moderation commands (nil when the template declares none, in
// which case moderation is reported unsupported). game is retained only
// for log/error context.
func Mount(r chi.Router, rc Rcon, game string, actions *caps.PlayerActions) {
	h := &handler{rcon: rc, cmdr: pickCommander(actions), game: game}

	// Configure the list command and optional regex.
	h.listCmd = "list"
	if actions != nil && actions.List != nil {
		if actions.List.Command != "" {
			h.listCmd = actions.List.Command
		}
		if actions.List.EntryRegex != "" {
			re, err := CompileEntryRegex(actions.List.EntryRegex)
			switch {
			case err != nil:
				slog.Warn("invalid player list entryRegex; using built-in parser", "err", err)
			default:
				h.listRE = re
			}
		}
	}

	r.Get("/players", h.serve)
	r.Get("/players/banned", h.banned)
	r.Post("/players/kick", h.kick)
	r.Post("/players/ban", h.ban)
	r.Post("/players/unban", h.unban)
	r.Get("/players/whitelist", h.whitelistList)
	r.Post("/players/whitelist/add", h.whitelistAdd)
	r.Post("/players/whitelist/remove", h.whitelistRemove)
}

const cacheTTL = 5 * time.Second

func (h *handler) serve(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	fresh := time.Since(h.lastFetch) < cacheTTL
	result, err := h.lastResult, h.lastErr
	h.mu.Unlock()

	if !fresh {
		result, err = h.fetch()
	}
	if errors.Is(err, rcon.ErrDisabled) {
		// The game has no RCON: player counts are simply unknown. The
		// dashboard contract is a valid snapshot with online=-1, not an
		// error — there is no upstream to be unavailable.
		httpjson.Write(w, http.StatusOK, Snapshot{
			Online:  -1,
			Max:     -1,
			Players: []string{},
			AsOf:    time.Now().UTC().Format(time.RFC3339),
		})
		return
	}
	if err != nil {
		// err can contain RCON protocol detail (addresses, passwords from
		// poorly-written server mods, etc.). Never reflect it.
		slog.Warn("players rcon", "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	result.Capabilities = h.cmdr.Capabilities()
	httpjson.Write(w, http.StatusOK, result)
}

func (h *handler) fetch() (Snapshot, error) {
	raw, err := h.rcon.Exec(h.listCmd)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.lastFetch = time.Now()
	if err != nil {
		h.lastErr = err
		return Snapshot{}, err
	}
	var snap Snapshot
	if structured, ok := parseStructuredList(raw); ok {
		snap = structured
	} else if h.listRE != nil {
		snap = h.parseListWithRegex(raw)
	} else {
		snap = parseList(raw)
	}
	snap.AsOf = h.lastFetch.UTC().Format(time.RFC3339)
	h.lastResult = snap
	h.lastErr = nil
	return snap, nil
}

func (h *handler) bustCache() {
	h.mu.Lock()
	h.lastFetch = time.Time{}
	h.mu.Unlock()
}

type modReq struct {
	Name   string `json:"name"`
	Reason string `json:"reason,omitempty"`
}

type modResp struct {
	OK  bool   `json:"ok"`
	Raw string `json:"raw,omitempty"`
}

func (h *handler) kick(w http.ResponseWriter, req *http.Request) {
	h.runMod(w, req, h.cmdr.Kick, true)
}

func (h *handler) ban(w http.ResponseWriter, req *http.Request) {
	h.runMod(w, req, h.cmdr.Ban, true)
}

func (h *handler) unban(w http.ResponseWriter, req *http.Request) {
	// Unban takes a name only; reason is ignored even if supplied.
	h.runMod(w, req, func(name, _ string) (string, bool) {
		return h.cmdr.Unban(name)
	}, false)
}

func (h *handler) runMod(
	w http.ResponseWriter, req *http.Request,
	build func(name, reason string) (string, bool),
	wantReason bool,
) {
	var body modReq
	if err := json.NewDecoder(http.MaxBytesReader(w, req.Body, 4<<10)).Decode(&body); err != nil {
		httpjson.Error(w, http.StatusBadRequest, "invalid json body")
		return
	}
	if err := validateName(body.Name); err != nil {
		httpjson.Error(w, http.StatusBadRequest, err.Error())
		return
	}
	if wantReason {
		clean, err := sanitizeReason(body.Reason)
		if err != nil {
			httpjson.Error(w, http.StatusBadRequest, err.Error())
			return
		}
		body.Reason = clean
	}
	cmd, ok := build(body.Name, body.Reason)
	if !ok {
		httpjson.Error(w, http.StatusNotImplemented, fmt.Sprintf("not supported by %s", h.game))
		return
	}
	raw, err := h.rcon.Exec(cmd)
	if errors.Is(err, rcon.ErrDisabled) {
		httpjson.Error(w, http.StatusNotImplemented, fmt.Sprintf("not supported by %s (no RCON)", h.game))
		return
	}
	if err != nil {
		slog.Warn("players moderation rcon", "cmd", cmd, "err", err)
		httpjson.Error(w, http.StatusBadGateway, "upstream unavailable")
		return
	}
	h.bustCache()
	httpjson.Write(w, http.StatusOK, modResp{OK: true, Raw: strings.TrimSpace(raw)})
}

func (h *handler) whitelistAdd(w http.ResponseWriter, req *http.Request) {
	// Whitelist add/remove take a name only; reason is ignored.
	h.runMod(w, req, func(name, _ string) (string, bool) {
		return h.cmdr.WhitelistAdd(name)
	}, false)
}

func (h *handler) whitelistRemove(w http.ResponseWriter, req *http.Request) {
	h.runMod(w, req, func(name, _ string) (string, bool) {
		return h.cmdr.WhitelistRemove(name)
	}, false)
}

func (h *handler) whitelistList(w http.ResponseWriter, _ *http.Request) {
	cmd, ok := h.cmdr.WhitelistList()
	if !ok {
		// Empty list rather than 501 so a Whitelist tab renders uniformly;
		// capabilities advertise whether management is actually available.
		httpjson.Write(w, http.StatusOK, []string{})
		return
	}
	raw, err := h.rcon.Exec(cmd)
	if errors.Is(err, rcon.ErrDisabled) {
		httpjson.Write(w, http.StatusOK, []string{})
		return
	}
	if err != nil {
		slog.Warn("whitelist rcon", "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	out := h.cmdr.ParseWhitelist(raw)
	if out == nil {
		out = []string{}
	}
	httpjson.Write(w, http.StatusOK, out)
}

func (h *handler) banned(w http.ResponseWriter, _ *http.Request) {
	cmd, ok := h.cmdr.BanList()
	if !ok {
		// Empty list rather than 501: callers can render a Banned tab
		// uniformly for every game; capabilities advertise reality.
		httpjson.Write(w, http.StatusOK, []BannedPlayer{})
		return
	}
	raw, err := h.rcon.Exec(cmd)
	if errors.Is(err, rcon.ErrDisabled) {
		httpjson.Write(w, http.StatusOK, []BannedPlayer{})
		return
	}
	if err != nil {
		slog.Warn("banlist rcon", "err", err)
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
		return
	}
	out := h.cmdr.ParseBanList(raw)
	if out == nil {
		out = []BannedPlayer{}
	}
	httpjson.Write(w, http.StatusOK, out)
}

// validateName rejects anything that isn't a printable username so that
// the RCON command we build can never be smuggled with extra arguments
// or newlines that would chain a second command.
var nameRE = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)

func validateName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if !nameRE.MatchString(name) {
		return errors.New("name must be 1-32 chars [A-Za-z0-9_]")
	}
	return nil
}

// sanitizeReason validates a ban/kick reason before it is folded into an RCON
// command, applying the same input-character policy as the shared
// console-injection guard (gameaction.CheckText, used by the actions
// handler): ASCII control characters and the shell/RCON metacharacters
// ; & | $ ` \ " ' are rejected. Checking only CR/LF is not enough: a NUL byte,
// for instance, truncates the null-terminated command a Source RCON server
// parses server-side, silently dropping everything after it, and a quote or
// separator can break out of a quoted template argument. The whole input is
// validated first, then truncated to 256 bytes.
func sanitizeReason(reason string) (string, error) {
	switch err := gameaction.CheckText(reason); {
	case errors.Is(err, gameaction.ErrControlChar):
		return "", errors.New("reason must not contain control characters")
	case err != nil:
		return "", errors.New("reason must not contain command separators, quotes or backslashes")
	}
	if len(reason) > 256 {
		reason = reason[:256]
	}
	return reason, nil
}

// Minecraft "list" responses look like one of:
//
//	There are 0 of a max of 20 players online:
//	There are 2 of a max of 20 players online: alice, bob
//
// Older server versions also say "There are 2/20 players online:".
var (
	reMaxOf = regexp.MustCompile(`There are (\d+) of a max of (\d+) players online:?\s*(.*)`)
	reSlash = regexp.MustCompile(`There are (\d+)/(\d+) players online:?\s*(.*)`)
)

// matchList runs the known "list" response formats against raw and returns
// the matching regexp submatches, or nil when none match.
func matchList(raw string) []string {
	line := strings.TrimSpace(strings.ReplaceAll(raw, "\r", ""))
	for _, re := range []*regexp.Regexp{reMaxOf, reSlash} {
		if m := re.FindStringSubmatch(line); m != nil {
			return m
		}
	}
	return nil
}

// ParseCounts extracts the online and max player counts from a raw RCON
// "list" response. ok is false when the response matches no known format, in
// which case the caller should treat both counts as unknown. It exists so the
// heartbeat can reuse this parser instead of duplicating the formats.
func ParseCounts(raw string) (online, maxPlayers int, ok bool) {
	m := matchList(raw)
	if m == nil {
		return 0, 0, false
	}
	online, _ = strconv.Atoi(m[1])
	maxPlayers, _ = strconv.Atoi(m[2])
	return online, maxPlayers, true
}

func parseList(raw string) Snapshot {
	m := matchList(raw)
	if m == nil {
		// No known player-list response format matched — e.g. a game with
		// RCON enabled but no capabilities.players declared. This is
		// "unknown", not "zero players": a zero-value Online/Max here is
		// indistinguishable from a real empty server. Use the same -1/-1
		// sentinel already returned when RCON is disabled entirely (see
		// h.serve's rcon.ErrDisabled branch above) so both unknown cases
		// share one representation (spec 018 F-106).
		return Snapshot{Online: -1, Max: -1, Players: []string{}}
	}
	online, _ := strconv.Atoi(m[1])
	maxN, _ := strconv.Atoi(m[2])
	names := []string{}
	if len(m) > 3 && strings.TrimSpace(m[3]) != "" {
		for _, n := range strings.Split(m[3], ",") {
			if s := strings.TrimSpace(n); s != "" {
				names = append(names, s)
			}
		}
	}
	return Snapshot{Online: online, Max: maxN, Players: names}
}

// parseStructuredList decodes a JSON player-list body of the form
// {"Players":[{"steamId":"...","faction":"..."}]}. It reports ok=false for
// any output that is not that shape (including a missing or null Players
// key), so the caller falls back to the regex/line parsers unchanged.
// Entries with an empty steamId are skipped. Max stays -1, as it does for
// the regex path, because the body carries no server maximum.
func parseStructuredList(raw string) (Snapshot, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return Snapshot{}, false
	}
	var body struct {
		Players *[]struct {
			SteamID string `json:"steamId"`
			Faction string `json:"faction"`
		} `json:"Players"`
	}
	if err := json.Unmarshal([]byte(trimmed), &body); err != nil || body.Players == nil {
		return Snapshot{}, false
	}
	entries := []Entry{}
	ids := []string{}
	for _, p := range *body.Players {
		if p.SteamID == "" {
			continue
		}
		entries = append(entries, Entry{SteamID: p.SteamID, Faction: p.Faction})
		ids = append(ids, p.SteamID)
	}
	snap := Snapshot{Online: len(ids), Max: -1, Players: ids}
	if len(entries) > 0 {
		snap.Entries = entries
	}
	return snap, true
}

// parseListWithRegex uses a custom regex to extract player names from the
// output. It extracts the first capture group if present, otherwise the
// whole match. Each match is one player.
func (h *handler) parseListWithRegex(raw string) Snapshot {
	if h.listRE == nil {
		return Snapshot{Max: -1, Players: []string{}}
	}
	matches := h.listRE.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return Snapshot{Max: -1, Players: []string{}}
	}
	names := []string{}
	for _, m := range matches {
		var name string
		if len(m) > 1 {
			// Use first capture group if present.
			// A participating-but-empty capture group (empty string) is dropped.
			name = strings.TrimSpace(m[1])
		} else if len(m) > 0 {
			// Use whole match if no capture group.
			name = strings.TrimSpace(m[0])
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return Snapshot{Online: len(names), Max: -1, Players: names}
}

// CompileEntryRegex compiles a module-declared entryRegex pattern. The
// pattern is compiled in multiline mode — ^ and $ anchor per line — because
// list-command output is line-oriented and template authors write
// line-anchored patterns. A leading (?m) is prepended; it is harmless when
// the pattern already supplies one.
func CompileEntryRegex(pattern string) (*regexp.Regexp, error) {
	return regexp.Compile("(?m)" + pattern)
}

// CountWithRegex counts the number of matches in raw using the provided
// regex. It extracts the first capture group if present (when non-empty),
// otherwise counts the whole match. Used by heartbeat to derive player counts
// from custom list commands.
func CountWithRegex(raw string, re *regexp.Regexp) int {
	matches := re.FindAllStringSubmatch(raw, -1)
	if len(matches) == 0 {
		return 0
	}
	count := 0
	for _, m := range matches {
		var name string
		if len(m) > 1 {
			// Use first capture group if present.
			// A participating-but-empty capture group is dropped.
			name = strings.TrimSpace(m[1])
		} else if len(m) > 0 {
			// Use whole match if no capture group.
			name = strings.TrimSpace(m[0])
		}
		if name != "" {
			count++
		}
	}
	return count
}
