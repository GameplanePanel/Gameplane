package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/util/retry"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/api/internal/scope"
)

// modpackAdoptedAnnotation mirrors v1alpha1.ModpackAdoptedAnnotation.
const modpackAdoptedAnnotation = "gameplane.local/modpack-adopted"

// adoptFileCap bounds the manifest read.
const adoptFileCap = 64 << 10

type adoptPlan struct {
	provider    string
	project     string
	versionFile *adoptFileCfg
}

// legacyModpackPlan returns the adoption plan for a legacy server, or false.
func legacyModpackPlan(gs, tmpl *unstructured.Unstructured) (adoptPlan, bool) {
	if tmpl == nil {
		return adoptPlan{}, false
	}
	if _, has, _ := unstructured.NestedMap(gs.Object, "spec", "modpack"); has {
		return adoptPlan{}, false
	}
	if _, has, _ := unstructured.NestedMap(gs.Object, "status", "modpack"); has {
		return adoptPlan{}, false
	}
	env, _, _ := unstructured.NestedSlice(gs.Object, "spec", "env")
	for _, p := range registryProviders(tmpl) {
		if p.modpacks == nil || p.modpacks.refEnv == "" {
			continue
		}
		for _, item := range env {
			m, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if name, _ := m["name"].(string); name != p.modpacks.refEnv {
				continue
			}
			if value, _ := m["value"].(string); modpackProjectRe.MatchString(value) {
				return adoptPlan{provider: p.provider, project: value, versionFile: p.modpacks.adoptVersionFile}, true
			}
		}
	}
	return adoptPlan{}, false
}

// readAdoptedVersion reads the installed pack version the image recorded.
// "" when the server has no pod, the file is unreadable, not JSON, lacks the
// field or holds a value that is not a valid pin.
func readAdoptedVersion(ctx context.Context, files AgentFileReader, cluster string, gs *unstructured.Unstructured, f *adoptFileCfg) string {
	if files == nil || f == nil || !serverHasPod(gs) {
		return ""
	}
	data, err := files.ReadFileForCluster(ctx, cluster, gs.GetName(), gs.GetNamespace(), f.path, adoptFileCap)
	if err != nil {
		return ""
	}
	var doc map[string]any
	if json.Unmarshal(data, &doc) != nil {
		return ""
	}
	v, _ := doc[f.field].(string)
	if v == "" || !modpackVersionRe.MatchString(v) {
		return ""
	}
	return v
}

// adoptLegacyModpack adopts gs when it is a legacy candidate and returns the
// server as stored afterwards (the input unchanged and adopted=false when it
// is not a candidate or someone else adopted it first). The write re-reads the
// server and retries on conflict, so concurrent API replicas are safe.
func adoptLegacyModpack(ctx context.Context, k *kube.Client, cluster string, files AgentFileReader,
	gs, tmpl *unstructured.Unstructured) (*unstructured.Unstructured, bool, error) {
	plan, ok := legacyModpackPlan(gs, tmpl)
	if !ok {
		return gs, false, nil
	}
	version := readAdoptedVersion(ctx, files, cluster, gs, plan.versionFile)
	res := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(gs.GetNamespace())
	out, adopted := gs, false
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		cur, err := res.Get(ctx, gs.GetName(), metav1.GetOptions{})
		if err != nil {
			return err
		}
		if gs.GetUID() != "" && cur.GetUID() != gs.GetUID() {
			return apierrors.NewNotFound(kube.GVRs["servers"].GroupResource(), gs.GetName())
		}
		if _, still := legacyModpackPlan(cur, tmpl); !still {
			out = cur
			return nil
		}
		spec := map[string]any{"provider": plan.provider, "project": plan.project}
		if version != "" {
			spec["version"] = version
		}
		if err := unstructured.SetNestedMap(cur.Object, spec, "spec", "modpack"); err != nil {
			return fmt.Errorf("set spec.modpack: %w", err)
		}
		ann := cur.GetAnnotations()
		if ann == nil {
			ann = map[string]string{}
		}
		ann[modpackAdoptedAnnotation] = "true"
		cur.SetAnnotations(ann)
		updated, err := res.Update(ctx, cur, metav1.UpdateOptions{})
		if err != nil {
			return err
		}
		out, adopted = updated, true
		return nil
	})
	if err != nil {
		return gs, false, fmt.Errorf("adopt modpack of %s/%s: %w", gs.GetNamespace(), gs.GetName(), err)
	}
	return out, adopted, nil
}

// adoptLegacy is the lazy retry on GET /servers/{name}/modpack: adopt the
// target server if it is still unmigrated. Errors are logged; the unmodified
// server is returned so the GET still answers.
func (h *registryHandler) adoptLegacy(ctx context.Context, target *modTarget) *unstructured.Unstructured {
	updated, _, err := adoptLegacyModpack(ctx, target.k, target.cluster, h.files, target.server, target.template)
	if err != nil {
		slog.Warn("legacy modpack adoption failed", "server", target.server.GetName(), "err", err)
		return target.server
	}
	return updated
}

// RunModpackAdoptSweep adopts every legacy server in every registered cluster
// once. Call it from the leader-only worker at API startup; it is idempotent
// (adopted servers are no longer candidates) and returns when done or ctx ends.
func RunModpackAdoptSweep(ctx context.Context, clients *kube.Registry, files AgentFileReader, logger *slog.Logger) {
	for _, cluster := range clients.IDs() {
		k, ok := clients.Get(cluster)
		if !ok || k == nil || k.Dynamic == nil {
			continue
		}
		sweepModpackAdoption(ctx, k, cluster, files, logger)
	}
}

func sweepModpackAdoption(ctx context.Context, k *kube.Client, cluster string, files AgentFileReader, logger *slog.Logger) {
	templates := map[string]*unstructured.Unstructured{}
	adopted, failed := 0, 0
	for _, ns := range scope.AllowedNamespaces {
		list, err := k.Dynamic.Resource(kube.GVRs["servers"]).Namespace(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			logger.Warn("modpack adoption sweep: list servers", "cluster", cluster, "namespace", ns, "err", err)
			continue
		}
		for i := range list.Items {
			if ctx.Err() != nil {
				return
			}
			gs := &list.Items[i]
			name, _, _ := unstructured.NestedString(gs.Object, "spec", "templateRef", "name")
			if name == "" {
				continue
			}
			tmpl, seen := templates[name]
			if !seen {
				tmpl, err = k.Dynamic.Resource(kube.GVRs["templates"]).Get(ctx, name, metav1.GetOptions{})
				if err != nil {
					tmpl = nil
					if !apierrors.IsNotFound(err) {
						logger.Warn("modpack adoption sweep: get template", "cluster", cluster, "template", name, "err", err)
					}
				}
				templates[name] = tmpl
			}
			_, did, err := adoptLegacyModpack(ctx, k, cluster, files, gs, tmpl)
			switch {
			case err != nil:
				failed++
				logger.Warn("modpack adoption sweep", "cluster", cluster, "server", gs.GetName(), "err", err)
			case did:
				adopted++
			}
		}
	}
	if adopted > 0 || failed > 0 {
		logger.Info("modpack adoption sweep done", "cluster", cluster, "adopted", adopted, "failed", failed)
	}
}
