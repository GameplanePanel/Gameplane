package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/api/internal/audit"
	"github.com/GameplanePanel/gameplane/api/internal/httperr"
	"github.com/GameplanePanel/gameplane/api/internal/kube"
)

// securityAuditSettings are the spec.security members a change row can name,
// in the order the reason lists them (contracts/audit-events.md).
var securityAuditSettings = []string{"runAsUser", "runAsGroup", "fsGroup", "volumeOwnershipCheck"}

// firstAuditor returns the optional auditor of a variadic parameter, or nil.
// A nil auditor means the handler writes no security rows (test routers).
func firstAuditor(auditors []*audit.Auditor) *audit.Auditor {
	if len(auditors) == 0 {
		return nil
	}
	return auditors[0]
}

// securityRefusalReason maps a security refusal to its audit reason and the
// status it is returned with. ok is false for any other error. The reason
// carries only the setting name, never the message text.
func securityRefusalReason(err error) (reason string, status int, ok bool) {
	var m *httperr.Message
	if !errors.As(err, &m) {
		return "", 0, false
	}
	setting, _ := m.Args["setting"].(string)
	switch m.ID {
	case "server.security.forbidden":
		return "security_forbidden", m.Status, true
	case "server.security.root_disabled":
		return "security_root_disabled:" + setting, m.Status, true
	case "server.security.invalid":
		return "security_invalid:" + setting, m.Status, true
	}
	return "", 0, false
}

// writeSecurityRefusal records a security refusal as the request's own row. A
// refusal that is not a security one writes nothing. If the row cannot be
// written, MarkRecorded stays unset, so the middleware row stands in; the
// caller still returns the refusal.
func writeSecurityRefusal(req *http.Request, auditor *audit.Auditor, target string, err error) {
	if auditor == nil {
		return
	}
	reason, status, ok := securityRefusalReason(err)
	if !ok {
		return
	}
	if werr := auditor.WriteSync(req.Context(), req.Method, req.URL.Path, target, reason, status); werr != nil {
		slog.Warn("security refusal audit row not written", "target", target, "reason", reason, "err", werr)
		return
	}
	audit.MarkRecorded(req.Context())
}

// writeSecurityChange writes the change row before the Kubernetes write. An
// empty reason means spec.security does not change, so no row is written. It
// returns false, after writing a 500 that saves nothing, when the row cannot
// be written.
func writeSecurityChange(w http.ResponseWriter, req *http.Request, auditor *audit.Auditor, target, reason string, status int) bool {
	if auditor == nil || reason == "" {
		return true
	}
	if err := auditor.WriteSync(req.Context(), req.Method, req.URL.Path, target, reason, status); err != nil {
		httperr.WriteCode(w, req, http.StatusInternalServerError, errors.New("audit write failed"))
		return false
	}
	audit.MarkRecorded(req.Context())
	return true
}

// writeSecuritySaveFailed records, after a change row was written, that the
// change was attempted and not stored. Its status is the error status of the
// failed write. A failed row write is only logged: the first row stands.
func writeSecuritySaveFailed(req *http.Request, auditor *audit.Auditor, target string, err error) {
	if auditor == nil {
		return
	}
	status := http.StatusInternalServerError
	var apiErr apierrors.APIStatus
	if errors.As(err, &apiErr) {
		if code := int(apiErr.Status().Code); code >= 400 {
			status = code
		}
	}
	if werr := auditor.WriteSync(req.Context(), req.Method, req.URL.Path, target, "security_save_failed", status); werr != nil {
		slog.Warn("security_save_failed audit row not written", "target", target, "err", werr)
	}
}

// securityBlock returns spec.security of obj as a map, or nil when absent.
func securityBlock(obj *unstructured.Unstructured) map[string]any {
	val, ok := securityOverrideValue(obj)
	if !ok {
		return nil
	}
	block, _ := val.(map[string]any)
	return block
}

// securityAuditValue renders one setting in the notation of
// contracts/audit-events.md: not set is null, a number is the number, the
// image default is "image", and volumeOwnershipCheck is its string.
func securityAuditValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case map[string]any:
		if val, ok := t["value"]; ok && val != nil {
			return val
		}
		if def, _ := t["useImageDefault"].(bool); def {
			return "image"
		}
		return nil
	default:
		return v
	}
}

// buildSecurityReason builds the reason of a change row:
// security:{"before":{...},"after":{...}[,"risky":[...]]}. Only the changed
// settings appear. On a create (create true) before is empty. risky names the
// changed settings the pinned template marks risky; the key is omitted when
// none is. It returns "" when no setting changed.
func buildSecurityReason(before, after map[string]any, create bool, risky map[string]bool) string {
	beforeOut := map[string]any{}
	afterOut := map[string]any{}
	riskyOut := []string{}
	changed := false
	for _, name := range securityAuditSettings {
		b, a := before[name], after[name]
		if securityValuesEqual(b, a) {
			continue
		}
		changed = true
		if !create {
			beforeOut[name] = securityAuditValue(b)
		}
		afterOut[name] = securityAuditValue(a)
		if risky[name] {
			riskyOut = append(riskyOut, name)
		}
	}
	if !changed {
		return ""
	}
	out := map[string]any{"before": beforeOut, "after": afterOut}
	if len(riskyOut) > 0 {
		out["risky"] = riskyOut
	}
	raw, err := json.Marshal(out)
	if err != nil {
		// Every value here is JSON-native; a failure cannot be produced by the input.
		return ""
	}
	return "security:" + string(raw)
}

// securityRiskyNames reads the template named by templateName and returns the
// security settings it marks risky (spec.security.risky.<setting> present). A
// template that cannot be read marks nothing; the row is still written.
func securityRiskyNames(ctx context.Context, k *kube.Client, templateName string) map[string]bool {
	out := map[string]bool{}
	if k == nil || k.Dynamic == nil || templateName == "" {
		return out
	}
	tmpl, err := k.Dynamic.Resource(kube.GVRs["templates"]).Get(ctx, templateName, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			slog.Warn("security audit: read template for risky marks", "template", templateName, "err", err)
		}
		return out
	}
	risky, found, _ := unstructured.NestedMap(tmpl.Object, "spec", "security", "risky")
	if !found {
		return out
	}
	for _, name := range securityAuditSettings {
		if v, ok := risky[name]; ok && v != nil {
			out[name] = true
		}
	}
	return out
}

// securityChangeReason returns the change-row reason for saving desired over
// live (live is nil on create and clone), or "" when spec.security does not
// change. The pinned template is read only when a setting changed.
func securityChangeReason(ctx context.Context, k *kube.Client, desired, live *unstructured.Unstructured) string {
	before := securityBlock(live)
	after := securityBlock(desired)
	changed := false
	for _, name := range securityAuditSettings {
		if !securityValuesEqual(before[name], after[name]) {
			changed = true
			break
		}
	}
	if !changed {
		return ""
	}
	templateName, _, _ := unstructured.NestedString(desired.Object, "spec", "templateRef", "name")
	if templateName == "" && live != nil {
		templateName, _, _ = unstructured.NestedString(live.Object, "spec", "templateRef", "name")
	}
	return buildSecurityReason(before, after, live == nil, securityRiskyNames(ctx, k, templateName))
}
