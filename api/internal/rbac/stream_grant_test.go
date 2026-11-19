package rbac

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/auth"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

type streamServerFetcher struct {
	current atomic.Pointer[unstructured.Unstructured]
	reads   atomic.Int32
	checked chan struct{}
}

func (f *streamServerFetcher) GetServer(_ context.Context, cluster, ns, name string) (*unstructured.Unstructured, error) {
	if obj := f.current.Load(); obj != nil && cluster == "remote" && ns == scope.DefaultNamespace && name == obj.GetName() {
		if f.reads.Add(1) == 2 { // admission, then the first live recheck
			close(f.checked)
		}
		return obj, nil
	}
	return nil, errors.New("server unavailable")
}

func (*streamServerFetcher) IDs() []string { return []string{scope.DefaultCluster, "remote"} }

func TestStreamLosesServerGrant(t *testing.T) {
	db := migratedStore(t)
	if _, err := db.DB.ExecContext(t.Context(), `INSERT INTO users(id,username,display_name,email) VALUES (42,'player','','')`); err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewSessionStore(db)
	token, _, err := sessions.Create(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	obj := &unstructured.Unstructured{}
	obj.SetName("game")
	obj.SetNamespace(scope.DefaultNamespace)
	obj.SetUID("original")
	obj.SetAnnotations(map[string]string{"gameplane.local/collaborators": "42"})
	for _, change := range []string{"removed", "replaced", "unavailable"} {
		t.Run(change, func(t *testing.T) {
			fetch := &streamServerFetcher{checked: make(chan struct{})}
			fetch.current.Store(obj)
			stream := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				conn, err := websocket.Accept(w, r, nil)
				if err != nil {
					return
				}
				defer conn.CloseNow()
				<-conn.CloseRead(r.Context()).Done()
			})
			revalidate := sessions.RevalidateStreams(func(*http.Request) bool { return true }, 100*time.Millisecond, StreamGrantValidator(fetch))
			server := httptest.NewServer(sessions.Authenticate(Middleware(fetch)(revalidate(stream))))
			defer server.Close()
			header := http.Header{}
			header.Set("Cookie", (&http.Cookie{Name: "gameplane_session", Value: token}).String())
			conn, resp, err := websocket.Dial(t.Context(), strings.Replace(server.URL, "http://", "ws://", 1)+"/ws/servers/game/console?cluster=remote", &websocket.DialOptions{HTTPHeader: header})
			if resp != nil && resp.Body != nil {
				defer resp.Body.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			defer conn.CloseNow()
			active := conn.CloseRead(t.Context())
			select {
			case <-active.Done():
				t.Fatal("socket closed while the collaborator was authorized")
			case <-fetch.checked:
			case <-time.After(2 * time.Second):
				t.Fatal("established console did not recheck its live server grant")
			}
			ping, stop := context.WithTimeout(t.Context(), time.Second)
			defer stop()
			if err := conn.Ping(ping); err != nil {
				t.Fatalf("socket unusable while the grant was valid: %v", err)
			}
			next := obj.DeepCopy()
			switch change {
			case "removed":
				next.SetAnnotations(nil)
			case "replaced":
				next.SetUID("replacement")
			case "unavailable":
				next = nil
			}
			fetch.current.Store(next)
			select {
			case <-active.Done():
			case <-time.After(2 * time.Second):
				t.Fatal("established console remained open after losing its live server grant")
			}
		})
	}
}
