package quiesce

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/GameplanePanel/gameplane/agent/internal/rcon"
)

func TestMinecraftPositiveCommandResponses(t *testing.T) {
	for _, tc := range []struct {
		cmd, reply string
		ok         bool
	}{
		{"save-off", "Automatic saving is now disabled", true},
		{"save-off", "Saving is already turned off", true},
		{"save-on", "Automatic saving is now enabled", true},
		{"save-on", "Saving is already turned on", true},
		{"save-all flush", "Saving the game (this may take a moment!)Saved the game", true},
		{"save-all flush", "Saving the game (this may take a moment!)\nSaved the game", true},
		{"save-all flush", "Saved the game", true},
		{"save-all flush", "Saving the game (this may take a moment!)", false},
		{"save-all flush", "Unable to save the game (is there enough disk space?)", false},
		{"save-on", "Automatic saving is now disabled", false},
		{"save-on", "", false},
		{"save-on", "Unknown or incomplete command", false},
		{"save-on", "Error: Automatic saving is now enabled", false},
	} {
		t.Run(tc.cmd+"/"+tc.reply, func(t *testing.T) {
			m := minecraftReplies{&fakeRcon{respond: func(string) (string, error) { return tc.reply, nil }}}
			if _, err := m.Exec(tc.cmd); (err == nil) != tc.ok {
				t.Fatalf("reply %q: %v", tc.reply, err)
			}
		})
	}
}

func TestVerifiedSequenceBlocksCleanupUntilRollbackFinishes(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	rc := &fakeRcon{respond: func(cmd string) (string, error) {
		switch cmd {
		case "save-off":
			return "Automatic saving is now disabled", nil
		case "save-all flush":
			once.Do(func() { close(reached) })
			<-release
			return "", context.DeadlineExceeded
		case "save-on":
			return "Saving is already turned on", nil
		}
		return "", errors.New("unexpected command")
	}}
	commands := rcon.NewCommands(rc)
	router := chi.NewRouter()
	MountVerified(router, commands, "minecraft-java", minecraftQuiesceSpec())
	pause := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/quiesce", nil))
		pause <- w.Code
	}()
	<-reached
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequestWithContext(ctx, http.MethodPost, "/quiesce", nil))
	if w.Code != http.StatusBadGateway {
		t.Errorf("canceled pause: %d", w.Code)
	}
	resumed := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/unquiesce", nil))
		resumed <- w
	}()
	close(release)
	if status := <-pause; status != http.StatusBadGateway {
		t.Errorf("timed-out pause: %d", status)
	}
	w = <-resumed
	if w.Code != http.StatusOK || decodeResponse(t, w.Body.Bytes()).Outcome != "resumed" {
		t.Fatalf("cleanup response: %d %s", w.Code, w.Body)
	}
	want := []string{"save-off", "save-all flush", "save-on", "save-on"}
	if got := rc.calls(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands = %v, want %v", got, want)
	}
}

func TestVerifiedRejectsUnqualifiedDeclarations(t *testing.T) {
	rc := &fakeRcon{}
	r := chi.NewRouter()
	MountVerified(r, rcon.NewCommands(rc), "other-game", minecraftQuiesceSpec())
	for _, path := range []string{"/quiesce", "/unquiesce"} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequestWithContext(t.Context(), http.MethodPost, path, nil))
		if got := decodeResponse(t, w.Body.Bytes()); got.Outcome != "unsupported" || got.Quiesced || len(rc.calls()) != 0 {
			t.Fatalf("%s = %+v, calls %v", path, got, rc.calls())
		}
	}
}
