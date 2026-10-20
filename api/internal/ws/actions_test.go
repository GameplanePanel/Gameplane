package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// --- fixtures -------------------------------------------------------------

// newActionsFakeClient builds a *kube.Client backed by a fake dynamic
// client preloaded with a GameServer "alpha" in gameplane-games (the
// scope package's default namespace) that references a GameTemplate
// "tmplx" carrying tmplSpec.
func newActionsFakeClient(t *testing.T, tmplSpec map[string]any) *kube.Client {
	t.Helper()
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec":       map[string]any{"templateRef": map[string]any{"name": "tmplx"}},
	}}
	tmpl := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameTemplate",
		"metadata":   map[string]any{"name": "tmplx"},
		"spec":       tmplSpec,
	}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			kube.GVRs["servers"]:   "GameServerList",
			kube.GVRs["templates"]: "GameTemplateList",
		}, gs, tmpl)
	return &kube.Client{Dynamic: dyn}
}

// newActionsFakeClientNoTemplate builds a GameServer with no templateRef —
// the "this server has no template, so no action can exist" case.
func newActionsFakeClientNoTemplate(t *testing.T) *kube.Client {
	t.Helper()
	gs := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "gameplane.local/v1alpha1",
		"kind":       "GameServer",
		"metadata":   map[string]any{"name": "alpha", "namespace": "gameplane-games"},
		"spec":       map[string]any{},
	}}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			kube.GVRs["servers"]:   "GameServerList",
			kube.GVRs["templates"]: "GameTemplateList",
		}, gs)
	return &kube.Client{Dynamic: dyn}
}

// fakeStdinWriter records what runStdinAction would have sent to a real
// pod, so tests can assert on rendered command lines without a cluster.
type fakeStdinWriter struct {
	called             bool
	ns, pod, container string
	lines              []string
	err                error
}

func (f *fakeStdinWriter) WriteStdinLines(_ context.Context, ns, pod, container string, lines []string) error {
	f.called = true
	f.ns, f.pod, f.container = ns, pod, container
	f.lines = lines
	return f.err
}

// panicStdinWriter fails the test the instant it's invoked — used to prove
// the rcon branch never reaches the stdin writer.
type panicStdinWriter struct{ t *testing.T }

func (p panicStdinWriter) WriteStdinLines(context.Context, string, string, string, []string) error {
	p.t.Fatal("stdin writer was called for a request that should have taken the rcon branch")
	return nil
}

// mountRunAction wires p.runAction behind the real route pattern so
// chi.URLParam(req, "name") resolves the same way it does in production.
func mountRunAction(p *proxy) *chi.Mux {
	r := chi.NewRouter()
	r.Post("/servers/{name}/actions/run", p.runAction)
	return r
}

func postAction(t *testing.T, r *chi.Mux, payload map[string]any) *httptest.ResponseRecorder {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/alpha/actions/run", bytes.NewReader(raw))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}

// --- resolveTransport (pure function) -------------------------------------

func TestResolveTransport(t *testing.T) {
	cases := []struct {
		name            string
		actionTransport string
		rconProtocol    string
		want            string
	}{
		{"explicit stdin wins over a usable rcon protocol", "stdin", "source", "stdin"},
		{"explicit rcon wins with no protocol declared", "rcon", "", "rcon"},
		{"empty + protocol set resolves to rcon", "", "source", "rcon"},
		{"empty + protocol none resolves to stdin", "", "none", "stdin"},
		{"empty + no rcon block resolves to stdin", "", "", "stdin"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := resolveTransport(tc.actionTransport, tc.rconProtocol)
			if got != tc.want {
				t.Fatalf("resolveTransport(%q, %q) = %q, want %q",
					tc.actionTransport, tc.rconProtocol, got, tc.want)
			}
		})
	}
}

// --- stdin branch ----------------------------------------------------------

func stdinActionTemplate() map[string]any {
	return map[string]any{
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{
					"id":        "announce",
					"command":   "say {{.Params.message}}",
					"transport": "stdin",
					"params": []any{
						map[string]any{"name": "message", "type": "string", "required": true},
					},
				},
			},
		},
	}
}

func TestRunAction_StdinHappyPath(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	fw := &fakeStdinWriter{}
	r := mountRunAction(&proxy{k: k, stdin: fw})

	rr := postAction(t, r, map[string]any{
		"id":     "announce",
		"params": map[string]string{"message": "hello world"},
	})

	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rr.Code, rr.Body)
	}
	if !fw.called {
		t.Fatal("stdin writer was never called")
	}
	if fw.ns != "gameplane-games" || fw.pod != "alpha-0" || fw.container != "game" {
		t.Fatalf("writer target = ns:%q pod:%q container:%q", fw.ns, fw.pod, fw.container)
	}
	if len(fw.lines) != 1 || fw.lines[0] != "say hello world" {
		t.Fatalf("lines = %+v, want [%q]", fw.lines, "say hello world")
	}

	var resp actionRunResp
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !resp.OK {
		t.Fatal("response.ok = false, want true")
	}
}

// TestRunAction_StdinInjectionRejected is the security regression guard:
// a param value carrying a newline must never reach the game's stdin —
// gameaction.Resolve has to reject it before anything is rendered or sent.
func TestRunAction_StdinInjectionRejected(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	fw := &fakeStdinWriter{}
	r := mountRunAction(&proxy{k: k, stdin: fw})

	rr := postAction(t, r, map[string]any{
		"id":     "announce",
		"params": map[string]string{"message": "hi\nstop"},
	})

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s, want 400", rr.Code, rr.Body)
	}
	if fw.called {
		t.Fatal("SECURITY REGRESSION: stdin writer was called despite a control-char injection attempt")
	}
}

func TestRunAction_MultipleCommandsInOrder(t *testing.T) {
	tmplSpec := map[string]any{
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{
					"id":       "maint",
					"commands": []any{"save-off", "save-all"},
				},
			},
		},
	}
	k := newActionsFakeClient(t, tmplSpec)
	fw := &fakeStdinWriter{}
	r := mountRunAction(&proxy{k: k, stdin: fw})

	rr := postAction(t, r, map[string]any{"id": "maint"})
	if rr.Code != http.StatusOK {
		t.Fatalf("code = %d, body = %s", rr.Code, rr.Body)
	}
	if want := []string{"save-off", "save-all"}; len(fw.lines) != 2 || fw.lines[0] != want[0] || fw.lines[1] != want[1] {
		t.Fatalf("lines = %+v, want %+v", fw.lines, want)
	}
}

func TestRunAction_EnumParamValidation(t *testing.T) {
	tmplSpec := map[string]any{
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{
					"id":        "difficulty",
					"command":   "difficulty {{.Params.level}}",
					"transport": "stdin",
					"params": []any{
						map[string]any{
							"name":     "level",
							"type":     "enum",
							"enum":     []any{"easy", "hard"},
							"required": true,
						},
					},
				},
			},
		},
	}
	k := newActionsFakeClient(t, tmplSpec)

	t.Run("value outside the enum is rejected", func(t *testing.T) {
		fw := &fakeStdinWriter{}
		r := mountRunAction(&proxy{k: k, stdin: fw})
		rr := postAction(t, r, map[string]any{"id": "difficulty", "params": map[string]string{"level": "medium"}})
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("code = %d, body = %s, want 400", rr.Code, rr.Body)
		}
		if fw.called {
			t.Fatal("stdin writer was called for a rejected enum value")
		}
	})

	t.Run("declared enum value is accepted and rendered", func(t *testing.T) {
		fw := &fakeStdinWriter{}
		r := mountRunAction(&proxy{k: k, stdin: fw})
		rr := postAction(t, r, map[string]any{"id": "difficulty", "params": map[string]string{"level": "hard"}})
		if rr.Code != http.StatusOK {
			t.Fatalf("code = %d, body = %s", rr.Code, rr.Body)
		}
		if len(fw.lines) != 1 || fw.lines[0] != "difficulty hard" {
			t.Fatalf("lines = %+v, want [%q]", fw.lines, "difficulty hard")
		}
	})
}

func TestRunAction_RenderMissingKeyRejected(t *testing.T) {
	tmplSpec := map[string]any{
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{
					"id":        "broken",
					"command":   "say {{.Params.undeclared}}",
					"transport": "stdin",
				},
			},
		},
	}
	k := newActionsFakeClient(t, tmplSpec)
	fw := &fakeStdinWriter{}
	r := mountRunAction(&proxy{k: k, stdin: fw})

	rr := postAction(t, r, map[string]any{"id": "broken"})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, body = %s, want 400", rr.Code, rr.Body)
	}
	if fw.called {
		t.Fatal("stdin writer was called for an action whose template failed to render")
	}
}

func TestRunAction_EmptyRenderedCommandRejected(t *testing.T) {
	tmplSpec := map[string]any{
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{"id": "blank", "command": "   ", "transport": "stdin"},
			},
		},
	}
	k := newActionsFakeClient(t, tmplSpec)
	fw := &fakeStdinWriter{}
	r := mountRunAction(&proxy{k: k, stdin: fw})

	rr := postAction(t, r, map[string]any{"id": "blank"})
	if rr.Code != http.StatusUnprocessableEntity {
		t.Fatalf("code = %d, body = %s, want 422", rr.Code, rr.Body)
	}
}

func TestRunAction_WriteStdinLinesErrorIsGatewayAndNonLeaking(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	fw := &fakeStdinWriter{err: errors.New("dial tcp 10.0.0.5:10250: connect: connection refused")}
	r := mountRunAction(&proxy{k: k, stdin: fw})

	rr := postAction(t, r, map[string]any{
		"id":     "announce",
		"params": map[string]string{"message": "hi"},
	})

	if rr.Code != http.StatusBadGateway {
		t.Fatalf("code = %d, body = %s, want 502", rr.Code, rr.Body)
	}
	if strings.Contains(rr.Body.String(), "10.0.0.5") {
		t.Fatalf("body leaked the upstream error detail: %q", rr.Body.String())
	}
}

// --- rcon branch -------------------------------------------------------

// TestRunAction_RconDelegatesToProxy proves an rcon-resolved action takes
// the existing agent-proxy path (byte-identical to before this handler
// existed): with p.transport left nil, httpProxy's own mTLS guard fires 503 —
// and the panicking stdin writer proves the stdin branch was never
// reached, which is the regression this test exists to catch.
func TestRunAction_RconDelegatesToProxy(t *testing.T) {
	tmplSpec := map[string]any{
		"rcon": map[string]any{"protocol": "source"},
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{"id": "say", "command": "say hi"},
			},
		},
	}
	k := newActionsFakeClient(t, tmplSpec)
	r := mountRunAction(&proxy{k: k, stdin: panicStdinWriter{t: t}})

	rr := postAction(t, r, map[string]any{"id": "say"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, body = %s, want 503 (httpProxy's own mTLS guard)", rr.Code, rr.Body)
	}
}

func TestRunAction_ExplicitRconTransportDelegatesToProxy(t *testing.T) {
	tmplSpec := map[string]any{
		"capabilities": map[string]any{
			"actions": []any{
				map[string]any{"id": "say", "command": "say hi", "transport": "rcon"},
			},
		},
	}
	k := newActionsFakeClient(t, tmplSpec)
	r := mountRunAction(&proxy{k: k, stdin: panicStdinWriter{t: t}})

	rr := postAction(t, r, map[string]any{"id": "say"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, body = %s, want 503 (httpProxy's own mTLS guard)", rr.Code, rr.Body)
	}
}

// --- error paths shared by both branches --------------------------------

func TestRunAction_NilKubeClient503(t *testing.T) {
	r := mountRunAction(&proxy{})
	rr := postAction(t, r, map[string]any{"id": "say"})
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("code = %d, want 503", rr.Code)
	}
}

func TestRunAction_UnknownActionID404(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	r := mountRunAction(&proxy{k: k, stdin: &fakeStdinWriter{}})
	rr := postAction(t, r, map[string]any{"id": "ghost"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

func TestRunAction_NoTemplate404(t *testing.T) {
	k := newActionsFakeClientNoTemplate(t)
	r := mountRunAction(&proxy{k: k, stdin: &fakeStdinWriter{}})
	rr := postAction(t, r, map[string]any{"id": "anything"})
	if rr.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rr.Code)
	}
}

func TestRunAction_MissingIDIs400(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	r := mountRunAction(&proxy{k: k, stdin: &fakeStdinWriter{}})
	rr := postAction(t, r, map[string]any{"params": map[string]string{}})
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
}

func TestRunAction_BadJSONBodyIs400(t *testing.T) {
	k := newActionsFakeClient(t, stdinActionTemplate())
	r := mountRunAction(&proxy{k: k, stdin: &fakeStdinWriter{}})

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/servers/alpha/actions/run", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rr.Code)
	}
}
