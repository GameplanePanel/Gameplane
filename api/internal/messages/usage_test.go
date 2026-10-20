package messages

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestEveryMessagesNewLiteralIsRegistered walks the API's internal packages
// and checks that every messages.New call with a literal id names a
// registered server.* entry, so a migrated handler cannot ship an id the
// registry does not know (it would render as "internal error").
func TestEveryMessagesNewLiteralIsRegistered(t *testing.T) {
	fset := token.NewFileSet()
	sites := 0
	err := filepath.WalkDir(filepath.Join(".."), func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if filepath.Clean(path) == filepath.Join("..", "messages") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			return perr
		}
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "New" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "messages" {
				return true
			}
			lit := idLiteral(call.Args[0])
			if lit == "" {
				return true
			}
			sites++
			pos := fset.Position(call.Pos())
			if !strings.HasPrefix(lit, "server.") {
				t.Errorf("%s: messages.New(%q) must use a server.* id", pos, lit)
			}
			if _, ok := Lookup(ID(lit)); !ok {
				t.Errorf("%s: messages.New(%q) is not registered", pos, lit)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if sites < 100 {
		t.Fatalf("found %d messages.New call sites, want at least 100 (walker broken?)", sites)
	}
}

// idLiteral returns the string value of a literal id argument, accepting
// a bare string literal or a conversion ID("...") / messages.ID("...").
// Non-literal arguments return "".
func idLiteral(e ast.Expr) string {
	if conv, ok := e.(*ast.CallExpr); ok && len(conv.Args) == 1 {
		switch fn := conv.Fun.(type) {
		case *ast.Ident:
			if fn.Name == "ID" {
				e = conv.Args[0]
			}
		case *ast.SelectorExpr:
			if id, ok := fn.X.(*ast.Ident); ok && id.Name == "messages" && fn.Sel.Name == "ID" {
				e = conv.Args[0]
			}
		}
	}
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return s
}
