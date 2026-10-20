package telemetry

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/dynamic"

	"github.com/GameplanePanel/gameplane/api/internal/kube"
	"github.com/GameplanePanel/gameplane/telemetryschema"
)

// Labels the operator stamps on a module-managed GameTemplate
// (operator/api/v1alpha1/module_types.go). The API module doesn't import the
// operator module, so the values are repeated here.
const (
	labelManagedBy    = "gameplane.local/managed-by"
	labelModuleName   = "gameplane.local/module-name"
	labelModuleSource = "gameplane.local/module-source"
	managedByModule   = "Module"
)

// listedModules returns the module names the ModuleSource called source
// serves: spec.oci.modules[].name plus the names in status.modules (the
// source's indexed catalog, which is where a git source's names appear). A
// missing source, an empty source name or any read error yields an empty set,
// so every server then counts as custom.
func listedModules(ctx context.Context, dyn dynamic.Interface, source string) map[string]bool {
	out := map[string]bool{}
	if source == "" || dyn == nil {
		return out
	}
	ms, err := dyn.Resource(kube.GVRModuleSource).Get(ctx, source, metav1.GetOptions{})
	if err != nil {
		return out
	}
	if mods, found, _ := unstructured.NestedSlice(ms.Object, "spec", "oci", "modules"); found {
		addModuleNames(out, mods)
	}
	if mods, found, _ := unstructured.NestedSlice(ms.Object, "status", "modules"); found {
		addModuleNames(out, mods)
	}
	return out
}

// addModuleNames adds the "name" of every map entry in mods to set.
func addModuleNames(set map[string]bool, mods []any) {
	for _, m := range mods {
		entry, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := entry["name"].(string); ok && name != "" {
			set[name] = true
		}
	}
}

// officialModule returns the official module name a GameTemplate with these
// labels provides, or "" when it isn't one. It is official only when it is
// managed by a Module, comes from the official ModuleSource (source, from
// --official-module-source), names a module that source lists, and that name
// is in the embedded catalog. Manual templates, uploads, other sources and
// look-alike names all return "" and are counted as custom.
func officialModule(labels map[string]string, source string, listed map[string]bool) string {
	if source == "" || labels[labelManagedBy] != managedByModule || labels[labelModuleSource] != source {
		return ""
	}
	name := labels[labelModuleName]
	if name == "" || !listed[name] || !telemetryschema.IsOfficial(name) {
		return ""
	}
	return name
}
