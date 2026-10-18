package ui

// Pins #232 (consistency review C10-C11) for internal/ui: the stderr
// diagnostics for a closed stream say one thing, and the engine action is
// the typed deploy.Action rather than a bare string compared in the UI.
// Scans of every non-test Go file under internal/ui.

import (
	"go/ast"
	"go/token"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func c232UISource(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range uiSourceFiles(t) {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[p] = string(b)
	}
	return out
}

// C10 diagnostics: `<area> stream: closed before the <run|step> finished`.
func TestC10_StreamDiagnosticsUseOneShape(t *testing.T) {
	oneShape := regexp.MustCompile(`"[a-z]+ stream: closed before the (run|step) finished"`)
	var shaped int
	for p, src := range c232UISource(t) {
		if strings.Contains(src, "closed while the") {
			t.Errorf("%s still has a \"closed while the ...\" diagnostic, want \"<area> stream: closed before the <run|step> finished\"", p)
		}
		shaped += len(oneShape.FindAllString(src, -1))
	}
	if shaped != 3 {
		t.Errorf("found %d diagnostics of the one shape, want 3 (engine and repair streams: run; configuration stream: step)", shaped)
	}
	for _, want := range []string{
		`"engine stream: closed before the run finished"`,
		`"repair stream: closed before the run finished"`,
		`"configuration stream: closed before the step finished"`,
	} {
		var seen bool
		for _, src := range c232UISource(t) {
			seen = seen || strings.Contains(src, want)
		}
		if !seen {
			t.Errorf("missing diagnostic %s", want)
		}
	}
}

// C11: the UI holds the engine action as deploy.Action: newEngineRun takes
// one, engineRun stores one, and nothing compares it with a bare string.
func TestC11_TheUIHoldsTheActionAsADeployAction(t *testing.T) {
	_, files := wi9Parse(t)
	var sawNew, sawField bool
	for p, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.FuncDecl:
				if x.Name.Name == "newEngineRun" {
					sawNew = true
					first := x.Type.Params.List[0]
					if exprString(first.Type) != "deploy.Action" {
						t.Errorf("%s: newEngineRun's action parameter is %s, want deploy.Action", p, exprString(first.Type))
					}
				}
			case *ast.TypeSpec:
				if x.Name.Name != "engineRun" {
					return true
				}
				st, ok := x.Type.(*ast.StructType)
				if !ok {
					return true
				}
				for _, fld := range st.Fields.List {
					for _, name := range fld.Names {
						if name.Name == "action" {
							sawField = true
							if exprString(fld.Type) != "deploy.Action" {
								t.Errorf("%s: engineRun.action is %s, want deploy.Action", p, exprString(fld.Type))
							}
						}
					}
				}
			case *ast.BinaryExpr:
				if x.Op == token.EQL || x.Op == token.NEQ {
					for _, side := range []ast.Expr{x.X, x.Y} {
						if bl, ok := side.(*ast.BasicLit); ok && bl.Kind == token.STRING {
							if s, _ := strconv.Unquote(bl.Value); s == "install" || s == "update" || s == "repair" {
								t.Errorf("%s: compares against bare %s, want deploy.Action%s", p, bl.Value, strings.ToUpper(s[:1])+s[1:])
							}
						}
					}
				}
			case *ast.CallExpr:
				if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "newEngineRun" && len(x.Args) > 0 {
					if bl, ok := x.Args[0].(*ast.BasicLit); ok && bl.Kind == token.STRING {
						t.Errorf("%s: newEngineRun called with bare %s, want a deploy.Action constant", p, bl.Value)
					}
				}
			}
			return true
		})
	}
	if !sawNew {
		t.Error("newEngineRun not found")
	}
	if !sawField {
		t.Error("engineRun.action field not found")
	}
}

// C11: no bare identifier-switch on the action either (case "update":).
func TestC11_NoBareActionCasesInTheUI(t *testing.T) {
	_, files := wi9Parse(t)
	for p, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			cc, ok := n.(*ast.CaseClause)
			if !ok {
				return true
			}
			for _, e := range cc.List {
				if bl, ok := e.(*ast.BasicLit); ok && bl.Kind == token.STRING {
					if s, _ := strconv.Unquote(bl.Value); s == "install" || s == "update" || s == "repair" {
						t.Errorf("%s: case %s on a bare string, want the deploy.Action constant", p, bl.Value)
					}
				}
			}
			return true
		})
	}
}
