package ws

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
	"github.com/GameplanePanel/gameplane/gameaction"
)

// actionsBodyLimit caps the /actions/run request body — plenty for an
// action id plus a handful of string params, matching the agent's own cap
// for the same endpoint shape (agent/internal/actions/actions.go).
const actionsBodyLimit = 16 << 10

// errUnknownAction covers both "no such action id" and "this server has no
// template" (so no actions can exist at all) — both mean the same thing to
// the caller: the action can't be run.
var errUnknownAction = errors.New("unknown action")

// stdinWriter is the subset of *kube.Client's behavior the stdin action
// branch needs, extracted so tests can inject a fake instead of attaching
// to a real pod.
type stdinWriter interface {
	WriteStdinLines(ctx context.Context, ns, pod, container string, lines []string) error
}

type actionRunReq struct {
	ID     string            `json:"id"`
	Params map[string]string `json:"params,omitempty"`
}

type actionRunResp struct {
	OK  bool   `json:"ok"`
	Raw string `json:"raw,omitempty"`
}

// runAction handles POST /servers/{name}/actions/run. Actions run over
// either RCON (proxied to the agent, unchanged from before this handler
// existed) or stdin (pod-attach, executed here directly) — see
// resolveTransport. The API is a trust boundary in its own right: stdin
// actions call gameaction.Resolve before rendering, exactly like the
// agent does for RCON actions, so validation is never skipped just
// because a UI layer already checked.
func (p *proxy) runAction(w http.ResponseWriter, req *http.Request) {
	if p.k == nil {
		http.Error(w, "kube client not configured", http.StatusServiceUnavailable)
		return
	}
	name := chi.URLParam(req, "name")
	ns, err := scope.Resolve(req)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(w, req.Body, actionsBodyLimit))
	if err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("body too large"))
		return
	}
	_ = req.Body.Close()

	var body actionRunReq
	if err := json.Unmarshal(raw, &body); err != nil {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("invalid json body"))
		return
	}
	if body.ID == "" {
		httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("id is required"))
		return
	}
	// Restore the body so the rcon branch below can forward the exact same
	// bytes to the agent proxy (httpProxy reads req.Body itself) — reading
	// it above, to find the action and its transport, would otherwise
	// leave req.Body drained for that second read.
	req.Body = io.NopCloser(bytes.NewReader(raw))

	server, tmpl, err := p.k.LoadServerAndTemplate(req.Context(), ns, name)
	if err != nil {
		httperr.Write(w, req, err)
		return
	}
	if p.remoteUID != "" && string(server.GetUID()) != p.remoteUID {
		http.NotFound(w, req)
		return
	}
	if tmpl == nil {
		httperr.WriteCode(w, req, http.StatusNotFound, errUnknownAction)
		return
	}

	action, ok := findAction(tmpl, body.ID)
	if !ok {
		httperr.WriteCode(w, req, http.StatusNotFound, errUnknownAction)
		return
	}

	actionTransport, _ := action["transport"].(string)
	rconProtocol, _, _ := unstructured.NestedString(tmpl.Object, "spec", "rcon", "protocol")
	transport := resolveTransport(actionTransport, rconProtocol)

	if transport == "rcon" {
		// Unchanged proxy path: same mTLS, headers, and agentHost
		// resolution as every other /servers/{name}/... route. Nothing
		// about the request other than its body-read state has changed,
		// and that was restored above.
		p.httpProxy("/actions/run")(w, req)
		return
	}

	p.runStdinAction(w, req, ns, name, action, body.Params)
}

// resolveTransport picks how an action's commands reach the game: the
// action's own Transport if set, else "rcon" when the template declares a
// usable RCON protocol, else "stdin" (pod-attach) — so a pty-console game
// that declares no RCON (e.g. Terraria) can still carry actions.
func resolveTransport(actionTransport, rconProtocol string) string {
	if actionTransport != "" {
		return actionTransport
	}
	if rconProtocol != "" && rconProtocol != "none" {
		return "rcon"
	}
	return "stdin"
}

// findAction looks up one entry of spec.capabilities.actions by id.
func findAction(tmpl *unstructured.Unstructured, id string) (map[string]any, bool) {
	actions, found, err := unstructured.NestedSlice(tmpl.Object, "spec", "capabilities", "actions")
	if !found || err != nil {
		return nil, false
	}
	for _, raw := range actions {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if actionID, _ := m["id"].(string); actionID != "" && actionID == id {
			return m, true
		}
	}
	return nil, false
}

// actionParams reads an action's declared params[] into the shared
// gameaction.Param shape (mirrors agent/internal/actions.go's
// toGameactionParams, adapted for the unstructured CRD read here instead
// of the agent's typed caps.ActionParam).
func actionParams(action map[string]any) []gameaction.Param {
	raw, _ := action["params"].([]any)
	out := make([]gameaction.Param, 0, len(raw))
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		p := gameaction.Param{
			Name:        strField(m, "name"),
			DisplayName: strField(m, "displayName"),
			Description: strField(m, "description"),
			Type:        strField(m, "type"),
			Default:     strField(m, "default"),
			Required:    boolField(m, "required"),
		}
		if enumRaw, ok := m["enum"].([]any); ok {
			for _, e := range enumRaw {
				if s, ok := e.(string); ok {
					p.Enum = append(p.Enum, s)
				}
			}
		}
		out = append(out, p)
	}
	return out
}

func strField(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func boolField(m map[string]any, key string) bool {
	b, _ := m[key].(bool)
	return b
}

// actionCommands returns an action's command sequence: Commands in order
// if declared (non-empty), else the single Command — mirroring the CRD's
// command/commands xor (operator/api/v1alpha1/gametemplate_types.go).
func actionCommands(action map[string]any) []string {
	if rawList, ok := action["commands"].([]any); ok && len(rawList) > 0 {
		out := make([]string, 0, len(rawList))
		for _, r := range rawList {
			if s, ok := r.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	if cmd, ok := action["command"].(string); ok && cmd != "" {
		return []string{cmd}
	}
	return nil
}

// runStdinAction validates params, renders the action's command(s), and
// writes them to the game container's stdin via pods/attach. Fire-and-
// forget: there is no RCON response to echo back, so the dashboard shows
// a "sent" confirmation instead (Task 6) and any output surfaces in the
// Console tab, not in this response.
func (p *proxy) runStdinAction(w http.ResponseWriter, req *http.Request, ns, name string, action map[string]any, gotParams map[string]string) {
	id, _ := action["id"].(string)

	resolved, err := gameaction.Resolve(actionParams(action), gotParams)
	if err != nil {
		// Resolve's error text is safe to surface — it describes which
		// declared param failed and why (matches the agent's own 400
		// behavior for the same validation call). THIS is the
		// console-injection guard: nothing below runs if it errors.
		httperr.WriteCode(w, req, http.StatusBadRequest, err)
		return
	}

	rawCommands := actionCommands(action)
	lines := make([]string, 0, len(rawCommands))
	for i, c := range rawCommands {
		cmd, err := gameaction.Compile(fmt.Sprintf("%s[%d]", id, i), c)
		if err != nil {
			// A bad template is a module/CRD authoring bug, not caller
			// input — httperr.WriteCode scrubs >=500 bodies, so the
			// template source never leaks to the client.
			httperr.WriteCode(w, req, http.StatusInternalServerError,
				fmt.Errorf("compile action %s command: %w", id, err))
			return
		}
		rendered, err := cmd.Render(resolved)
		if err != nil {
			httperr.WriteCode(w, req, http.StatusBadRequest, errors.New("could not render action command"))
			return
		}
		if rendered == "" {
			httperr.WriteCode(w, req, http.StatusUnprocessableEntity, errors.New("action produced an empty command"))
			return
		}
		lines = append(lines, rendered)
	}
	if len(lines) == 0 {
		httperr.WriteCode(w, req, http.StatusUnprocessableEntity, errors.New("action declares no commands"))
		return
	}

	pod := name + "-0"
	if p.remoteUID != "" {
		owned, err := serverPodForUID(req.Context(), p.k, ns, name, p.remoteUID)
		if err != nil {
			httperr.Write(w, req, err)
			return
		}
		pod = owned.Name
	}
	if err := p.stdin.WriteStdinLines(req.Context(), ns, pod, "game", lines); err != nil {
		httperr.WriteCode(w, req, http.StatusBadGateway,
			fmt.Errorf("write stdin to pod %s/%s: %w", ns, pod, err))
		return
	}
	writeJSON(w, http.StatusOK, actionRunResp{OK: true})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
