// Heuristic advice text for the propose_fix tool. This is deliberately not
// an LLM call: it's a small, deterministic, keyword-matched table so the
// tool's output is reproducible and auditable. It only ever returns text —
// see tools.go's proposeFixHandler and the package doc comment in main.go
// for why that's a hard invariant.
package main

import (
	"context"
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/GameplanePanel/gameplane/mcp-server/internal/kube"
)

// fixRule matches a propose_fix symptom against a set of keywords and
// contributes a block of suggested diagnostics/remediation text when any
// keyword is found (case-insensitively) in the symptom.
type fixRule struct {
	keywords []string
	advice   string
}

var fixRules = []fixRule{
	{
		keywords: []string{"crashloop", "crash loop", "restarting"},
		advice: "CrashLoopBackOff / repeated restarts usually means the container's " +
			"process exits immediately. Check the previous instance's logs (this " +
			"server's get_pod_logs with previous=true), and compare requests/limits " +
			"against what the game process actually needs — an OOM-killed container " +
			"restarts the same way. Suggested commands:\n" +
			"  kubectl describe pod <pod> -n <namespace>\n" +
			"  kubectl logs <pod> -n <namespace> --previous --all-containers",
	},
	{
		keywords: []string{"imagepullbackoff", "errimagepull", "image pull", "pull image"},
		advice: "Image pull failures are almost always a wrong tag/digest, a private " +
			"registry needing imagePullSecrets, or a registry the cluster's nodes " +
			"can't reach (see netguard's dial-guard docs if the registry is behind " +
			"an SSRF allowlist). Suggested YAML check:\n" +
			"  kubectl get pod <pod> -n <namespace> -o jsonpath='{.spec.containers[*].image}'\n" +
			"  kubectl get pod <pod> -n <namespace> -o jsonpath='{.spec.imagePullSecrets}'",
	},
	{
		keywords: []string{"pending", "unschedulable", "not scheduled"},
		advice: "A Pod/GameServer stuck Pending is usually a scheduling constraint: " +
			"insufficient CPU/memory on any node, a PVC that can't bind (missing " +
			"StorageClass or no capacity), or a taint/toleration mismatch. Suggested " +
			"commands:\n" +
			"  kubectl describe pod <pod> -n <namespace>   # see the Events: block\n" +
			"  kubectl get pvc -n <namespace>\n" +
			"  kubectl get nodes -o wide",
	},
	{
		keywords: []string{"oom", "outofmemory", "out of memory", "oomkilled"},
		advice: "OOMKilled means the container exceeded its memory limit. Either the " +
			"limit is too low for the game/mods in use, or there's a genuine leak. " +
			"Suggested YAML patch (apply by hand after review):\n" +
			"  spec:\n" +
			"    resources:\n" +
			"      limits:\n" +
			"        memory: <raise this>\n" +
			"Check GameServer/GameTemplate resource overrides before raising the " +
			"cluster-wide default.",
	},
	{
		keywords: []string{"backup", "restic"},
		advice: "For a failed Backup: check the backup Job's pod logs (restic errors " +
			"surface there, not on the Backup CR itself), and confirm the restic " +
			"repository secret/PVC referenced by the BackupSchedule still exists. " +
			"Suggested commands (the backup Job is named after the Backup, not " +
			"labeled):\n" +
			"  kubectl get job <backup-name> -n <namespace>\n" +
			"  kubectl logs -n <namespace> job/<backup-name>",
	},
	{
		keywords: []string{"restore"},
		advice: "For a failed Restore: confirm the source Backup completed " +
			"successfully and its restic snapshot still exists in the repository " +
			"(a pruned/expired snapshot is a common cause). Suggested commands:\n" +
			"  kubectl get backup <source-backup> -n <namespace> -o jsonpath='{.status}'\n" +
			"  kubectl logs -n <namespace> job/<restore-job-name>",
	},
	{
		keywords: []string{"connection refused", "cannot connect", "unreachable", "timeout", "timed out"},
		advice: "Connectivity failures between components are usually a Service/" +
			"NetworkPolicy mismatch or an mTLS certificate problem (agent <-> " +
			"operator/API). Suggested commands:\n" +
			"  kubectl get svc,endpoints -n <namespace>\n" +
			"  kubectl get networkpolicy -n <namespace> -o yaml\n" +
			"  kubectl logs -n <namespace> <pod> -c agent",
	},
	{
		keywords: []string{"permission denied", "forbidden", "rbac", "unauthorized"},
		advice: "A 403/Forbidden from the Kubernetes API usually points at the " +
			"ServiceAccount's RBAC bindings drifting from what the image expects " +
			"(common after an image upgrade that added a new watched resource, " +
			"before a matching Role/ClusterRole is applied). Suggested commands:\n" +
			"  kubectl auth can-i list gameservers --as=system:serviceaccount:<namespace>:<serviceaccount>\n" +
			"  kubectl get role,rolebinding,clusterrole,clusterrolebinding -n <namespace>",
	},
}

// buildFixSuggestion assembles the propose_fix response text: an echo of
// the request, a best-effort read of current state, matched heuristic
// advice, and a generic diagnostics footer. It never mutates anything —
// the resource read below is a plain Get, same as get_gameplane_resource/
// get_pod.
func buildFixSuggestion(ctx context.Context, c *kube.Client, in proposeFixInput) string {
	var b strings.Builder

	fmt.Fprintf(&b, "Symptom: %s\n", in.Symptom)
	if in.Kind != "" || in.Name != "" {
		fmt.Fprintf(&b, "Resource: kind=%s namespace=%s name=%s\n", in.Kind, in.Namespace, in.Name)
	}
	b.WriteString("\nObserved state (read-only best effort):\n")
	b.WriteString(observeState(ctx, c, in))

	matched := matchFixRules(in.Symptom)
	b.WriteString("\nSuggested next steps:\n")
	if len(matched) == 0 {
		b.WriteString(genericAdvice)
	} else {
		for i, rule := range matched {
			fmt.Fprintf(&b, "%d. %s\n", i+1, rule.advice)
		}
	}

	b.WriteString("\nGeneral diagnostics:\n")
	b.WriteString(genericDiagnostics(in))

	b.WriteString("\nNote: this tool is strictly read-only. It did not change anything " +
		"in the cluster. Review any YAML or kubectl command above before an operator " +
		"with write access runs it.\n")

	return b.String()
}

const genericAdvice = "No specific heuristic matched this symptom text. Start from the " +
	"resource's status.conditions and the namespace's recent Events (list_events), " +
	"then narrow down with pod logs (get_pod_logs).\n"

func matchFixRules(symptom string) []fixRule {
	lower := strings.ToLower(symptom)
	var out []fixRule
	for _, rule := range fixRules {
		for _, kw := range rule.keywords {
			if strings.Contains(lower, kw) {
				out = append(out, rule)
				break
			}
		}
	}
	return out
}

// observeState does a best-effort, read-only lookup of the referenced
// resource so the suggestion can be grounded in its current status. Any
// error (not found, unknown kind, no reference given) becomes a plain
// sentence in the output rather than a tool error — propose_fix should
// still return useful generic advice even without a valid resource ref.
func observeState(ctx context.Context, c *kube.Client, in proposeFixInput) string {
	switch {
	case in.Kind == "" || in.Name == "":
		return "  (no resource reference given — advice below is generic)\n"
	case in.Kind == "Pod":
		if in.Namespace == "" {
			return "  Pod is namespaced: a namespace is required to read it.\n"
		}
		pod, err := c.GetPod(ctx, in.Namespace, in.Name)
		if err != nil {
			return fmt.Sprintf("  could not read pod %s/%s: %v\n", in.Namespace, in.Name, err)
		}
		text, err := marshalIndent(pod.Status)
		if err != nil {
			return fmt.Sprintf("  read pod %s/%s but failed to render its status: %v\n", in.Namespace, in.Name, err)
		}
		return "  pod.status:\n" + indent(text) + "\n"
	default:
		obj, err := c.GetCRD(ctx, in.Kind, in.Namespace, in.Name)
		if err != nil {
			return fmt.Sprintf("  could not read %s %s/%s: %v\n", in.Kind, in.Namespace, in.Name, err)
		}
		status, _, _ := unstructured.NestedFieldNoCopy(obj.Object, "status")
		text, err := marshalIndent(status)
		if err != nil {
			return fmt.Sprintf("  read %s %s/%s but failed to render its status: %v\n", in.Kind, in.Namespace, in.Name, err)
		}
		return fmt.Sprintf("  %s.status:\n", in.Kind) + indent(text) + "\n"
	}
}

func indent(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}

func genericDiagnostics(in proposeFixInput) string {
	ns := in.Namespace
	if ns == "" {
		ns = "<namespace>"
	}
	name := in.Name
	if name == "" {
		name = "<name>"
	}
	kind := in.Kind
	if kind == "" {
		kind = "<kind>"
	}
	return fmt.Sprintf(
		"  kubectl describe %s %s -n %s\n"+
			"  kubectl get events -n %s --field-selector involvedObject.name=%s\n",
		strings.ToLower(kind), name, ns, ns, name,
	)
}
