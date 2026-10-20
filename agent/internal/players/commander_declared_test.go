package players

import (
	"reflect"
	"testing"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

// minecraftActions declares the same behavior the hardcoded minecraft
// commander implements — what modules/minecraft-java/template.yaml ships.
func minecraftActions() *caps.PlayerActions {
	return &caps.PlayerActions{
		Kick:  "kick {{.Player}}{{if .Reason}} {{.Reason}}{{end}}",
		Ban:   "ban {{.Player}}{{if .Reason}} {{.Reason}}{{end}}",
		Unban: "pardon {{.Player}}",
		BanList: &caps.BanList{
			Command:    "banlist players",
			EntryRegex: `^\s*(?P<name>[A-Za-z0-9_]{1,32})\s+was banned by\s+(?P<source>[^:]+?)(?::\s*(?P<reason>.*))?\s*$`,
		},
		Whitelist: &caps.Whitelist{
			List:      "whitelist list",
			Add:       "whitelist add {{.Player}}",
			Remove:    "whitelist remove {{.Player}}",
			ListRegex: `whitelisted player[s()]*:\s*(?P<names>.+)$`,
		},
	}
}

func TestPickCommander_Declared(t *testing.T) {
	// Declared actions are the sole source of moderation support.
	c := pickCommander(minecraftActions())
	if caps := c.Capabilities(); !caps.Kick || !caps.Ban || !caps.Unban {
		t.Fatalf("capabilities = %+v", caps)
	}
	// Nothing declared → unsupported, regardless of game.
	if caps := pickCommander(nil).Capabilities(); caps.Kick || caps.Ban || caps.Unban {
		t.Fatal("undeclared game should be unsupported")
	}
}

func TestTemplateCommander_Minecraft(t *testing.T) {
	c := pickCommander(minecraftActions())

	cases := []struct {
		name, reason, wantKick, wantBan string
	}{
		{"griefer", "", "kick griefer", "ban griefer"},
		{"griefer", "stop that", "kick griefer stop that", "ban griefer stop that"},
	}
	for _, tc := range cases {
		if dk, _ := c.Kick(tc.name, tc.reason); dk != tc.wantKick {
			t.Errorf("Kick(%q,%q) = %q, want %q", tc.name, tc.reason, dk, tc.wantKick)
		}
		if db, _ := c.Ban(tc.name, tc.reason); db != tc.wantBan {
			t.Errorf("Ban(%q,%q) = %q, want %q", tc.name, tc.reason, db, tc.wantBan)
		}
	}
	if du, _ := c.Unban("griefer"); du != "pardon griefer" {
		t.Errorf("Unban = %q", du)
	}
	if dCmd, ok := c.BanList(); !ok || dCmd != "banlist players" {
		t.Errorf("BanList = %q ok=%v", dCmd, ok)
	}

	raw := "griefer1 was banned by Server: Cheating\n" +
		"griefer2 was banned by alice: <no reason given>\n" +
		"griefer3 was banned by Server\n" +
		"not a ban line\n"
	want := []BannedPlayer{
		{Name: "griefer1", Source: "Server", Reason: "Cheating"},
		{Name: "griefer2", Source: "alice"},
		{Name: "griefer3", Source: "Server"},
	}
	if got := c.ParseBanList(raw); !reflect.DeepEqual(got, want) {
		t.Errorf("ParseBanList = %+v, want %+v", got, want)
	}
}

func TestTemplateCommander_PartialDeclarations(t *testing.T) {
	c := pickCommander(&caps.PlayerActions{Kick: "kick {{.Player}}"})
	if caps := c.Capabilities(); !caps.Kick || caps.Ban || caps.Unban {
		t.Fatalf("capabilities = %+v", caps)
	}
	if _, ok := c.Ban("x", ""); ok {
		t.Error("undeclared ban should be unsupported")
	}
	if _, ok := c.BanList(); ok {
		t.Error("undeclared banlist should be unsupported")
	}
	if c.ParseBanList("anything") != nil {
		t.Error("undeclared banlist parse should return nil")
	}
	cmd, ok := c.Kick("griefer", "ignored")
	if !ok || cmd != "kick griefer" {
		t.Errorf("Kick = %q ok=%v", cmd, ok)
	}
}

func TestTemplateCommander_BadDeclarationsDegrade(t *testing.T) {
	t.Run("broken template disables one action", func(t *testing.T) {
		c := pickCommander(&caps.PlayerActions{
			Kick: "kick {{.Player", // unparsable
			Ban:  "ban {{.Player}}",
		})
		if _, ok := c.Kick("x", ""); ok {
			t.Error("broken kick template should be unsupported")
		}
		if cmd, ok := c.Ban("x", ""); !ok || cmd != "ban x" {
			t.Errorf("ban should still work, got %q ok=%v", cmd, ok)
		}
	})

	t.Run("bad banlist regex disables banlist", func(t *testing.T) {
		c := pickCommander(&caps.PlayerActions{
			BanList: &caps.BanList{Command: "banlist", EntryRegex: "(unclosed"},
		})
		if _, ok := c.BanList(); ok {
			t.Error("bad regex should disable banlist")
		}
	})

	t.Run("regex without name group disables banlist", func(t *testing.T) {
		c := pickCommander(&caps.PlayerActions{
			BanList: &caps.BanList{Command: "banlist", EntryRegex: `^(\w+)$`},
		})
		if _, ok := c.BanList(); ok {
			t.Error("regex without name group should disable banlist")
		}
	})

	t.Run("template rendering to empty is unsupported", func(t *testing.T) {
		c := pickCommander(&caps.PlayerActions{Kick: "{{if .Reason}}kick {{.Player}}{{end}}"})
		if _, ok := c.Kick("x", ""); ok {
			t.Error("empty render should be unsupported")
		}
		if cmd, ok := c.Kick("x", "r"); !ok || cmd != "kick x" {
			t.Errorf("got %q ok=%v", cmd, ok)
		}
	})
}
