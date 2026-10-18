package release

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Pins #212 (#199 WI-8, EN-13): release decides what a version string
// looks like for display. The function does not exist yet, so the tests
// reach it without naming it at compile time: a source scan for its
// declaration, and a throwaway program, compiled through go's overlay
// so nothing is written into the repository, that calls it.

// wi8DisplayVersionDecls returns every top-level declaration named
// DisplayVersion in the package's non-test sources.
func wi8DisplayVersionDecls(t *testing.T) []*ast.FuncDecl {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	var found []*ast.FuncDecl
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "DisplayVersion" {
				found = append(found, fn)
			}
		}
	}
	return found
}

func TestDisplayVersion_IsDeclaredOnceInRelease(t *testing.T) {
	decls := wi8DisplayVersionDecls(t)
	if len(decls) != 1 {
		t.Fatalf("release declares DisplayVersion %d times, want exactly 1", len(decls))
	}
	fn := decls[0]
	if fn.Type.Params.NumFields() != 1 || fn.Type.Results.NumFields() != 1 {
		t.Fatalf("DisplayVersion must be func(string) string")
	}
	for _, field := range []*ast.Field{fn.Type.Params.List[0], fn.Type.Results.List[0]} {
		if id, ok := field.Type.(*ast.Ident); !ok || id.Name != "string" {
			t.Errorf("DisplayVersion must be func(string) string")
		}
	}
}

// wi8CallDisplayVersion runs release.DisplayVersion on each input in a
// separate process and returns the outputs in order.
func wi8CallDisplayVersion(t *testing.T, inputs []string) []string {
	t.Helper()
	if len(wi8DisplayVersionDecls(t)) != 1 {
		t.Fatal("DisplayVersion is not declared in release yet")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	argsJSON, err := json.Marshal(inputs)
	if err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(t.TempDir(), "main.go")
	src := `package main

import (
	"encoding/json"
	"os"

	"github.com/tomlawesome/orbit-launcher/internal/release"
)

func main() {
	var in, out []string
	if err := json.Unmarshal([]byte(os.Args[1]), &in); err != nil {
		panic(err)
	}
	for _, v := range in {
		out = append(out, release.DisplayVersion(v))
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
`
	if err := os.WriteFile(probe, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	virtual := filepath.Join(root, "internal", "release", "zz_displayversion_probe", "main.go")
	overlay := filepath.Join(t.TempDir(), "overlay.json")
	ov, _ := json.Marshal(map[string]map[string]string{"Replace": {virtual: probe}})
	if err := os.WriteFile(overlay, ov, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "run", "-overlay", overlay, "./internal/release/zz_displayversion_probe", string(argsJSON))
	cmd.Dir = root
	out, err := cmd.Output()
	if err != nil {
		stderr := ""
		if ee, ok := err.(*exec.ExitError); ok {
			stderr = string(ee.Stderr)
		}
		t.Fatalf("running the probe: %v\n%s", err, stderr)
	}
	var got []string
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("probe output %q: %v", out, err)
	}
	if len(got) != len(inputs) {
		t.Fatalf("probe returned %d results for %d inputs", len(got), len(inputs))
	}
	return got
}

// The rules are main.go's displayVersion rules, moved: a bare version
// gets its "v", an already-prefixed one is untouched, and an empty or
// "dev" build reads "dev". The cases include both prefixed and bare
// forms, so a function that returns its input unchanged cannot pass.
func TestDisplayVersion_ShapesAVersionForDisplay(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "dev"},
		{"dev", "dev"},
		{"1.2.3", "v1.2.3"},
		{"v1.2.3", "v1.2.3"},
	}
	inputs := make([]string, len(cases))
	for i, c := range cases {
		inputs[i] = c.in
	}
	got := wi8CallDisplayVersion(t, inputs)
	for i, c := range cases {
		if got[i] != c.want {
			t.Errorf("DisplayVersion(%q) = %q, want %q", c.in, got[i], c.want)
		}
	}
}
