package telemetry

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestOfficialModule_Table(t *testing.T) {
	listed := map[string]bool{
		"minecraft-java":       true,
		"terraria":             true,
		"minecraft-java-evil":  true, // listed by the source, but not in the embedded catalog
		"private-game-by-user": true,
	}
	good := map[string]string{
		labelManagedBy:    managedByModule,
		labelModuleSource: "default",
		labelModuleName:   "minecraft-java",
	}
	with := func(key, val string) map[string]string {
		out := map[string]string{}
		for k, v := range good {
			out[k] = v
		}
		if val == "" {
			delete(out, key)
		} else {
			out[key] = val
		}
		return out
	}
	cases := []struct {
		name   string
		labels map[string]string
		source string
		want   string
	}{
		{"official module", good, "default", "minecraft-java"},
		{"another official module", with(labelModuleName, "terraria"), "default", "terraria"},
		{"nil labels (manual template)", nil, "default", ""},
		{"not managed by a module", with(labelManagedBy, ""), "default", ""},
		{"managed by something else", with(labelManagedBy, "Helm"), "default", ""},
		{"upload source", with(labelModuleSource, "uploads"), "default", ""},
		{"source label missing", with(labelModuleSource, ""), "default", ""},
		{"no official source configured", good, "", ""},
		{"module name missing", with(labelModuleName, ""), "default", ""},
		{"module not listed by the source", with(labelModuleName, "valheim"), "default", ""},
		{"look-alike name outside the catalog", with(labelModuleName, "minecraft-java-evil"), "default", ""},
		{"listed but not in the catalog", with(labelModuleName, "private-game-by-user"), "default", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := officialModule(tc.labels, tc.source, listed); got != tc.want {
				t.Fatalf("officialModule = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestListedModules_UnionOfSpecAndStatus(t *testing.T) {
	src := moduleSourceObj("default", []string{"minecraft-java", "terraria"}, []string{"terraria", "valheim"})
	k := collectKubeClients(t, collectOpts{dynamic: []runtime.Object{src}})
	got := listedModules(context.Background(), k.Dynamic, "default")
	for _, name := range []string{"minecraft-java", "terraria", "valheim"} {
		if !got[name] {
			t.Errorf("%s missing from %v", name, got)
		}
	}
	if len(got) != 3 {
		t.Errorf("got %v, want exactly three names", got)
	}
}

func TestListedModules_EmptyOnProblems(t *testing.T) {
	ctx := context.Background()
	src := moduleSourceObj("default", []string{"minecraft-java"}, nil)
	k := collectKubeClients(t, collectOpts{dynamic: []runtime.Object{src}})

	if got := listedModules(ctx, k.Dynamic, ""); len(got) != 0 {
		t.Errorf("empty source name: got %v", got)
	}
	if got := listedModules(ctx, nil, "default"); len(got) != 0 {
		t.Errorf("nil client: got %v", got)
	}
	if got := listedModules(ctx, k.Dynamic, "absent"); len(got) != 0 {
		t.Errorf("unknown source: got %v", got)
	}

	failing := collectKubeClients(t, collectOpts{
		dynamic:     []runtime.Object{src},
		dynamicFail: map[string]bool{"get": true},
	})
	if got := listedModules(ctx, failing.Dynamic, "default"); len(got) != 0 {
		t.Errorf("a failing Get must yield an empty set, got %v", got)
	}
}

func TestAddModuleNames_SkipsMalformedEntries(t *testing.T) {
	set := map[string]bool{}
	addModuleNames(set, []any{
		map[string]any{"name": "terraria"},
		map[string]any{"name": ""},
		map[string]any{"name": 7},
		map[string]any{"other": "x"},
		"not-a-map",
		nil,
	})
	if len(set) != 1 || !set["terraria"] {
		t.Fatalf("got %v, want only terraria", set)
	}
}
