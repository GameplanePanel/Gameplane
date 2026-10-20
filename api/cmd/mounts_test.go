package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// homeClientMounts lists every router mount in main.go that is handed the
// bare home-cluster client (the k8s variable) rather than the cluster
// registry alone. Each one is also mounted by mountHomeClientRoutes in
// api/internal/handlers/cluster_guard_test.go, where
// TestHomeClientMounts_ServeHomeClusterOnly checks that a grant on a
// registered non-local cluster never reaches the home-cluster client
// through any of its routes.
var homeClientMounts = []string{
	"handlers.MountAuthProviderSecrets",
	"handlers.MountClusterActions",
	"handlers.MountClusters",
	"handlers.MountNotifications",
	"handlers.MountRegistrySecrets",
	"handlers.MountSystemLogs",
}

// TestHomeClientMounts_MatchMain parses main.go and checks that the mounts
// passed the home-cluster client are exactly homeClientMounts, so a new
// mount built on that client is added to the home-cluster-only route test.
func TestHomeClientMounts_MatchMain(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	got := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || !strings.HasPrefix(sel.Sel.Name, "Mount") {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || (pkg.Name != "handlers" && pkg.Name != "ws") {
			return true
		}
		for _, arg := range call.Args {
			if id, ok := arg.(*ast.Ident); ok && id.Name == "k8s" {
				got[pkg.Name+"."+sel.Sel.Name] = true
			}
		}
		return true
	})

	want := map[string]bool{}
	for _, m := range homeClientMounts {
		want[m] = true
	}
	for m := range got {
		if !want[m] {
			t.Errorf("main.go passes the home-cluster client to %s, which is not in homeClientMounts: "+
				"add it here and to mountHomeClientRoutes in api/internal/handlers/cluster_guard_test.go", m)
		}
	}
	for m := range want {
		if !got[m] {
			t.Errorf("homeClientMounts lists %s, but main.go no longer passes it the home-cluster client: "+
				"remove it here and from mountHomeClientRoutes in api/internal/handlers/cluster_guard_test.go", m)
		}
	}
}
