package players

import (
	"log/slog"
	"regexp"
	"strings"
	"text/template"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// commander formats per-game RCON commands for moderation actions.
// Methods return (cmd, ok=false) when the game doesn't support that
// action — the handler turns ok=false into a 501 response.
type commander interface {
	Capabilities() Capabilities
	Kick(name, reason string) (string, bool)
	Ban(name, reason string) (string, bool)
	Unban(name string) (string, bool)
	BanList() (string, bool)
	ParseBanList(raw string) []BannedPlayer
	WhitelistAdd(name string) (string, bool)
	WhitelistRemove(name string) (string, bool)
	WhitelistList() (string, bool)
	ParseWhitelist(raw string) []string
}

// pickCommander builds the moderation commander entirely from the
// module's declared commands (spec.capabilities.players). A template
// that declares nothing reports every action unsupported — moderation
// is module-driven, with no per-game special-casing in the agent.
func pickCommander(actions *caps.PlayerActions) commander {
	if actions == nil {
		return unsupportedCommander{}
	}
	return newTemplateCommander(actions)
}

// --- Declared (template-driven) commanders --------------------------------

// modVars is the render context for action command templates.
type modVars struct {
	Player string
	Reason string
}

type templateCommander struct {
	kick, ban, unban           *template.Template
	banListCmd                 string
	banListRE                  *regexp.Regexp
	whitelistAdd, whitelistRem *template.Template
	whitelistListCmd           string
	whitelistRE                *regexp.Regexp
}

// newTemplateCommander compiles the declared action templates. A
// malformed template or regex disables that single action (logged) —
// one bad declaration must not take down the whole moderation surface.
func newTemplateCommander(actions *caps.PlayerActions) commander {
	parse := func(action, text string) *template.Template {
		if text == "" {
			return nil
		}
		t, err := template.New(action).Parse(text)
		if err != nil {
			slog.Warn("invalid capability command template; action disabled",
				"action", action, "err", err)
			return nil
		}
		return t
	}
	c := templateCommander{
		kick:  parse("kick", actions.Kick),
		ban:   parse("ban", actions.Ban),
		unban: parse("unban", actions.Unban),
	}
	if bl := actions.BanList; bl != nil && bl.Command != "" {
		re, err := regexp.Compile(bl.EntryRegex)
		switch {
		case err != nil:
			slog.Warn("invalid banList entryRegex; ban list disabled", "err", err)
		case re.SubexpIndex("name") < 0:
			slog.Warn("banList entryRegex has no (?P<name>…) group; ban list disabled")
		default:
			c.banListCmd, c.banListRE = bl.Command, re
		}
	}
	if wl := actions.Whitelist; wl != nil {
		c.whitelistAdd = parse("whitelistAdd", wl.Add)
		c.whitelistRem = parse("whitelistRemove", wl.Remove)
		if wl.List != "" && wl.ListRegex != "" {
			re, err := regexp.Compile(wl.ListRegex)
			switch {
			case err != nil:
				slog.Warn("invalid whitelist listRegex; whitelist list disabled", "err", err)
			case re.SubexpIndex("names") < 0 && re.SubexpIndex("name") < 0:
				slog.Warn("whitelist listRegex has no (?P<names>…) or (?P<name>…) group; whitelist list disabled")
			default:
				c.whitelistListCmd, c.whitelistRE = wl.List, re
			}
		}
	}
	return c
}

func (c templateCommander) Capabilities() Capabilities {
	return Capabilities{
		Kick:  c.kick != nil,
		Ban:   c.ban != nil,
		Unban: c.unban != nil,
		// Advertise whitelist only when the full surface works (list+add+remove),
		// so the UI doesn't offer a half-functional tab.
		Whitelist: c.whitelistAdd != nil && c.whitelistRem != nil && c.whitelistListCmd != "",
	}
}

func (c templateCommander) render(t *template.Template, name, reason string) (string, bool) {
	if t == nil {
		return "", false
	}
	var sb strings.Builder
	if err := t.Execute(&sb, modVars{Player: name, Reason: reason}); err != nil {
		slog.Warn("render capability command", "template", t.Name(), "err", err)
		return "", false
	}
	cmd := strings.TrimSpace(sb.String())
	if cmd == "" {
		return "", false
	}
	return cmd, true
}

func (c templateCommander) Kick(name, reason string) (string, bool) {
	return c.render(c.kick, name, reason)
}

func (c templateCommander) Ban(name, reason string) (string, bool) {
	return c.render(c.ban, name, reason)
}

func (c templateCommander) Unban(name string) (string, bool) {
	return c.render(c.unban, name, "")
}

func (c templateCommander) BanList() (string, bool) {
	return c.banListCmd, c.banListCmd != ""
}

func (c templateCommander) ParseBanList(raw string) []BannedPlayer {
	if c.banListRE == nil {
		return nil
	}
	nameIdx := c.banListRE.SubexpIndex("name")
	sourceIdx := c.banListRE.SubexpIndex("source")
	reasonIdx := c.banListRE.SubexpIndex("reason")
	out := []BannedPlayer{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		m := c.banListRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		entry := BannedPlayer{Name: m[nameIdx]}
		if sourceIdx >= 0 {
			entry.Source = strings.TrimSpace(m[sourceIdx])
		}
		if reasonIdx >= 0 {
			if r := strings.TrimSpace(m[reasonIdx]); r != "" && r != "<no reason given>" {
				entry.Reason = r
			}
		}
		out = append(out, entry)
	}
	return out
}

func (c templateCommander) WhitelistAdd(name string) (string, bool) {
	return c.render(c.whitelistAdd, name, "")
}

func (c templateCommander) WhitelistRemove(name string) (string, bool) {
	return c.render(c.whitelistRem, name, "")
}

func (c templateCommander) WhitelistList() (string, bool) {
	return c.whitelistListCmd, c.whitelistListCmd != ""
}

func (c templateCommander) ParseWhitelist(raw string) []string {
	if c.whitelistRE == nil {
		return nil
	}
	namesIdx := c.whitelistRE.SubexpIndex("names")
	nameIdx := c.whitelistRE.SubexpIndex("name")
	out := []string{}
	// Match per line so $ anchors don't trip on a trailing newline. A
	// "names" group holds a comma-separated list (Minecraft: "There are 2
	// whitelisted players: alice, bob"); otherwise each line's "name" group
	// is one entry (banlist-style).
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r", ""), "\n") {
		m := c.whitelistRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		switch {
		case namesIdx >= 0:
			for _, n := range strings.Split(m[namesIdx], ",") {
				if s := strings.TrimSpace(n); s != "" {
					out = append(out, s)
				}
			}
		case nameIdx >= 0:
			if s := strings.TrimSpace(m[nameIdx]); s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}

// --- Default (RCON-less or undeclared games) -----------------------------

type unsupportedCommander struct{}

func (unsupportedCommander) Capabilities() Capabilities         { return Capabilities{} }
func (unsupportedCommander) Kick(string, string) (string, bool) { return "", false }
func (unsupportedCommander) Ban(string, string) (string, bool)  { return "", false }
func (unsupportedCommander) Unban(string) (string, bool)        { return "", false }
func (unsupportedCommander) BanList() (string, bool)            { return "", false }
func (unsupportedCommander) ParseBanList(string) []BannedPlayer { return nil }
func (unsupportedCommander) WhitelistAdd(string) (string, bool) { return "", false }
func (unsupportedCommander) WhitelistRemove(string) (string, bool) {
	return "", false
}
func (unsupportedCommander) WhitelistList() (string, bool)  { return "", false }
func (unsupportedCommander) ParseWhitelist(string) []string { return nil }
