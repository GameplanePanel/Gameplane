package players

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/caps"
)

func TestWhitelistCommander_Minecraft(t *testing.T) {
	c := newTemplateCommander(minecraftActions())

	if !c.Capabilities().Whitelist {
		t.Fatal("whitelist capability should be advertised for minecraft")
	}
	if cmd, ok := c.WhitelistAdd("alice"); !ok || cmd != "whitelist add alice" {
		t.Errorf("WhitelistAdd = %q ok=%v", cmd, ok)
	}
	if cmd, ok := c.WhitelistRemove("alice"); !ok || cmd != "whitelist remove alice" {
		t.Errorf("WhitelistRemove = %q ok=%v", cmd, ok)
	}
	if cmd, ok := c.WhitelistList(); !ok || cmd != "whitelist list" {
		t.Errorf("WhitelistList = %q ok=%v", cmd, ok)
	}

	cases := []struct {
		name string
		raw  string
		want []string
	}{
		{"two players", "There are 2 whitelisted players: alice, bob", []string{"alice", "bob"}},
		{"paren form", "There are 1 whitelisted player(s): alice", []string{"alice"}},
		{"trailing newline", "There are 2 whitelisted players: alice, bob\n", []string{"alice", "bob"}},
		{"none", "There are no whitelisted players", []string{}},
		{"unrecognized", "nonsense output", []string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.ParseWhitelist(tc.raw); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("ParseWhitelist(%q) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWhitelistCommander_PerLineFallback(t *testing.T) {
	// A regex with a "name" group (no "names") falls back to one entry per line.
	c := newTemplateCommander(&caps.PlayerActions{
		Whitelist: &caps.Whitelist{
			List:      "allowlist",
			Add:       "allow {{.Player}}",
			Remove:    "deny {{.Player}}",
			ListRegex: `^-\s*(?P<name>\w+)$`,
		},
	})
	got := c.ParseWhitelist("- alice\n- bob\nignored\n- carol")
	want := []string{"alice", "bob", "carol"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ParseWhitelist = %+v, want %+v", got, want)
	}
}

func TestWhitelistCommander_Degrades(t *testing.T) {
	t.Run("missing list disables capability", func(t *testing.T) {
		c := newTemplateCommander(&caps.PlayerActions{
			Whitelist: &caps.Whitelist{Add: "whitelist add {{.Player}}", Remove: "whitelist remove {{.Player}}"},
		})
		if c.Capabilities().Whitelist {
			t.Error("whitelist without list should not be advertised")
		}
		if _, ok := c.WhitelistList(); ok {
			t.Error("WhitelistList should be unsupported")
		}
	})

	t.Run("bad regex disables list", func(t *testing.T) {
		c := newTemplateCommander(&caps.PlayerActions{
			Whitelist: &caps.Whitelist{List: "wl", Add: "a {{.Player}}", Remove: "r {{.Player}}", ListRegex: "(unclosed"},
		})
		if _, ok := c.WhitelistList(); ok {
			t.Error("bad regex should disable whitelist list")
		}
	})

	t.Run("regex without name/names group disables list", func(t *testing.T) {
		c := newTemplateCommander(&caps.PlayerActions{
			Whitelist: &caps.Whitelist{List: "wl", Add: "a {{.Player}}", Remove: "r {{.Player}}", ListRegex: `^(\w+)$`},
		})
		if _, ok := c.WhitelistList(); ok {
			t.Error("regex without a named group should disable whitelist list")
		}
	})
}

func TestWhitelistUnsupportedCommander(t *testing.T) {
	c := unsupportedCommander{}
	if c.Capabilities().Whitelist {
		t.Error("unsupported commander should not advertise whitelist")
	}
	if _, ok := c.WhitelistAdd("a"); ok {
		t.Error("WhitelistAdd should be unsupported")
	}
	if _, ok := c.WhitelistRemove("a"); ok {
		t.Error("WhitelistRemove should be unsupported")
	}
	if _, ok := c.WhitelistList(); ok {
		t.Error("WhitelistList should be unsupported")
	}
	if c.ParseWhitelist("anything") != nil {
		t.Error("ParseWhitelist should be nil for unsupported")
	}
}

func TestWhitelistHandlers(t *testing.T) {
	t.Run("add happy path", func(t *testing.T) {
		rc := &fakeRcon{respond: func(string) (string, error) { return "Added alice to the whitelist", nil }}
		srv := httptest.NewServer(newTestRouter(t, "minecraft-java", rc))
		defer srv.Close()

		status, body := doJSON(t, srv, "POST", "/players/whitelist/add", modReq{Name: "alice"})
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, body)
		}
		if rc.last != "whitelist add alice" {
			t.Errorf("rcon called with %q", rc.last)
		}
	})

	t.Run("remove happy path", func(t *testing.T) {
		rc := &fakeRcon{respond: func(string) (string, error) { return "Removed alice", nil }}
		srv := httptest.NewServer(newTestRouter(t, "minecraft-java", rc))
		defer srv.Close()

		status, _ := doJSON(t, srv, "POST", "/players/whitelist/remove", modReq{Name: "alice"})
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		if rc.last != "whitelist remove alice" {
			t.Errorf("rcon called with %q", rc.last)
		}
	})

	t.Run("add rejects bad name", func(t *testing.T) {
		rc := &fakeRcon{}
		srv := httptest.NewServer(newTestRouter(t, "minecraft-java", rc))
		defer srv.Close()

		status, _ := doJSON(t, srv, "POST", "/players/whitelist/add", modReq{Name: "alice;op"})
		if status != http.StatusBadRequest {
			t.Errorf("status = %d, want 400", status)
		}
		if rc.last != "" {
			t.Errorf("rcon should not be invoked, got %q", rc.last)
		}
	})

	t.Run("list returns parsed names", func(t *testing.T) {
		rc := &fakeRcon{respond: func(string) (string, error) {
			return "There are 2 whitelisted players: alice, bob", nil
		}}
		srv := httptest.NewServer(newTestRouter(t, "minecraft-java", rc))
		defer srv.Close()

		status, body := doJSON(t, srv, "GET", "/players/whitelist", nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d, body = %s", status, body)
		}
		var got []string
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if !reflect.DeepEqual(got, []string{"alice", "bob"}) {
			t.Errorf("whitelist = %+v", got)
		}
	})

	t.Run("unsupported game returns empty list, no rcon", func(t *testing.T) {
		rc := &fakeRcon{}
		r := chi.NewRouter()
		Mount(r, rc, "valheim", nil)
		srv := httptest.NewServer(r)
		defer srv.Close()

		status, body := doJSON(t, srv, "GET", "/players/whitelist", nil)
		if status != http.StatusOK {
			t.Fatalf("status = %d", status)
		}
		var got []string
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("want empty, got %+v", got)
		}
		if rc.last != "" {
			t.Errorf("rcon should not be invoked, got %q", rc.last)
		}
	})

	t.Run("unsupported game rejects add with 501", func(t *testing.T) {
		rc := &fakeRcon{}
		r := chi.NewRouter()
		Mount(r, rc, "valheim", nil)
		srv := httptest.NewServer(r)
		defer srv.Close()

		status, _ := doJSON(t, srv, "POST", "/players/whitelist/add", modReq{Name: "alice"})
		if status != http.StatusNotImplemented {
			t.Errorf("status = %d, want 501", status)
		}
	})
}
