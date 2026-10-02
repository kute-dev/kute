package actions

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestEveryControllerScreenRendersResults is review 10 H1's guard: a
// screen that embeds an actions.Controller but never renders its Result()
// makes every failed delete/drain/cordon/restart look exactly like a
// success. Every task package with a Controller field must (1) implement
// tui.ActionResulter by returning the controller's Result() — tui.Frame
// renders it on the keybar — and (2) DismissResult on a key press, so the
// line lasts until the user acts. Static on purpose: a new screen fails
// here before anyone has to remember to write its own failed-result test.
func TestEveryControllerScreenRendersResults(t *testing.T) {
	root := filepath.Join("..", "tasks")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkg := parsePackage(t, filepath.Join(root, e.Name()))
		fields := controllerFields(pkg)
		if len(fields) == 0 {
			continue
		}
		checked++
		if !hasActionResult(pkg, fields) {
			t.Errorf("%s embeds actions.Controller (%v) but has no ActionResult() returning its Result() — "+
				"a failed mutation would render like a success", e.Name(), fields)
		}
		if !callsDismiss(pkg, fields) {
			t.Errorf("%s embeds actions.Controller (%v) but never calls DismissResult() — "+
				"the result line would never clear", e.Name(), fields)
		}
	}
	if checked < 10 {
		t.Fatalf("only %d controller screens found — has internal/tui/tasks moved?", checked)
	}
}

// parsePackage parses dir's non-test Go files.
func parsePackage(t *testing.T, dir string) []*ast.File {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		files = append(files, f)
	}
	return files
}

// controllerFields names every struct field typed actions.Controller.
func controllerFields(pkg []*ast.File) map[string]bool {
	out := map[string]bool{}
	for _, f := range pkg {
		ast.Inspect(f, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok {
				return true
			}
			for _, fld := range st.Fields.List {
				sel, ok := fld.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Controller" {
					continue
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "actions" {
					continue
				}
				for _, name := range fld.Names {
					out[name.Name] = true
				}
			}
			return true
		})
	}
	return out
}

// fieldCall reports whether n is a call <recv>.<field>.<method>().
func fieldCall(n ast.Node, fields map[string]bool, method string) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != method {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	return ok && fields[inner.Sel.Name]
}

func hasActionResult(pkg []*ast.File, fields map[string]bool) bool {
	for _, f := range pkg {
		for _, d := range f.Decls {
			fn, ok := d.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "ActionResult" || fn.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				if fieldCall(n, fields, "Result") {
					found = true
				}
				return !found
			})
			if found {
				return true
			}
		}
	}
	return false
}

func callsDismiss(pkg []*ast.File, fields map[string]bool) bool {
	found := false
	for _, f := range pkg {
		ast.Inspect(f, func(n ast.Node) bool {
			if fieldCall(n, fields, "DismissResult") {
				found = true
			}
			return !found
		})
	}
	return found
}
