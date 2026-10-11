package notices_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/licencenotices/notices"
	"github.com/tomlawesome/orbit-launcher/tools/licencereview/review"
)

// dualLicensed writes a module root the way a dual-licensed Rust-style
// dependency lays it out: no plain LICENSE, one file per licence. A second
// directory below the root carries someone else's code with its own
// licence file, which is what the review scan reads.
func dualLicensed(t *testing.T) (root string, pkgs []review.Package) {
	t.Helper()
	root = t.TempDir()
	files := map[string]string{
		"LICENSE-MIT":              "MIT licence text",
		"LICENSE-APACHE":           "Apache licence text",
		"README.md":                "not a licence",
		"p.go":                     "package p\n\nfunc F() {}\n",
		"internal/xxh/LICENSE-MIT": "xxh's licence",
		"internal/xxh/sub/x.go":    "package sub\n\nfunc G() {}\n",
		"internal/own/o.go":        "package own\n\nfunc H() {}\n",
	}
	for name, body := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mod := &review.Module{Path: "example.com/dual", Version: "v1.0.0", Dir: root}
	pkgs = []review.Package{
		{ImportPath: "example.com/dual", Dir: root, GoFiles: []string{"p.go"}, Module: mod},
		{ImportPath: "example.com/dual/internal/xxh/sub", Dir: filepath.Join(root, "internal/xxh/sub"), GoFiles: []string{"x.go"}, Module: mod},
		{ImportPath: "example.com/dual/internal/own", Dir: filepath.Join(root, "internal/own"), GoFiles: []string{"o.go"}, Module: mod},
	}
	return root, pkgs
}

// SU-1: a LICENSE-MIT file is a licence file to both tools. The review
// scan already treats a nested one as marking vendored code (the case
// that proves the review side can see it); the notices builder must carry
// the module's own LICENSE-MIT and LICENSE-APACHE text instead of
// stopping with "no licence file".
func TestLicenceMITIsCountedByBothTools(t *testing.T) {
	_, pkgs := dualLicensed(t)

	findings, err := review.Scan(pkgs)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	var vendored []string
	for _, f := range findings {
		if f.Kind == review.Vendored {
			vendored = append(vendored, f.Path)
		}
	}
	if len(vendored) != 1 || vendored[0] != "internal/xxh/sub/x.go" {
		t.Errorf("review: vendored findings = %v, want only internal/xxh/sub/x.go (below the nested LICENSE-MIT)", vendored)
	}

	got, err := notices.Build(pkgs[:1], "Go licence text", nil)
	if err != nil {
		t.Fatalf("notices: Build rejected a module whose licences are LICENSE-MIT and LICENSE-APACHE: %v", err)
	}
	for _, want := range []string{
		"==== example.com/dual v1.0.0 (LICENSE-MIT) ====\n\nMIT licence text",
		"==== example.com/dual v1.0.0 (LICENSE-APACHE) ====\n\nApache licence text",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notices lack %q", want)
		}
	}
	if strings.Contains(got, "not a licence") {
		t.Error("notices carry the README, which is not a licence file")
	}
}

// The narrower rule must keep holding after the merge: a module with no
// licence-named file still stops the build. Pairs with the test above so
// "accepts LICENSE-MIT" cannot pass by accepting everything.
func TestBuildStillRefusesAModuleWithNoLicenceNamedFile(t *testing.T) {
	root := t.TempDir()
	for name, body := range map[string]string{"README.md": "hi", "MIT.txt": "stray"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mod := &review.Module{Path: "example.com/bare", Version: "v0.1.0", Dir: root}
	_, err := notices.Build([]review.Package{{ImportPath: "example.com/bare", Dir: root, Module: mod}}, "Go", nil)
	if err == nil || !strings.Contains(err.Error(), "example.com/bare v0.1.0 has no licence file") {
		t.Fatalf("err = %v, want the module without a licence file named", err)
	}
}

// parseDir parses every non-test Go file directly in dir.
func parseDir(t *testing.T, dir string) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, name), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files[name] = f
	}
	if len(files) == 0 {
		t.Fatalf("no Go source in %s", dir)
	}
	return files
}

// licenceNameLiterals lists, per file, the string literals that spell out
// the licence-file names ("unlicense" is the least common of them).
func licenceNameLiterals(files map[string]*ast.File) map[string]int {
	found := map[string]int{}
	for name, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err == nil && strings.Contains(strings.ToLower(s), "unlicense") {
				found[name]++
			}
			return true
		})
	}
	return found
}

// SU-1: the rule is typed once, exported from review as LicenceFile, and
// notices calls it. The source is scanned because the name does not exist
// yet; the behaviour above is the real proof.
func TestLicenceFileRuleHasOneHome(t *testing.T) {
	reviewFiles := parseDir(t, "../../licencereview/review")
	var decls int
	for _, f := range reviewFiles {
		for _, d := range f.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == "LicenceFile" {
				decls++
				if fn.Type.Results == nil || len(fn.Type.Results.List) != 1 {
					t.Errorf("review.LicenceFile must return one value (the verdict)")
				}
			}
		}
	}
	if decls != 1 {
		t.Errorf("review declares LicenceFile %d times, want exactly once", decls)
	}

	reviewLits := licenceNameLiterals(reviewFiles)
	total := 0
	for _, n := range reviewLits {
		total += n
	}
	if total != 1 {
		t.Errorf("review spells the licence names in %d literals (%v), want one", total, reviewLits)
	}

	noticeFiles := parseDir(t, ".")
	if lits := licenceNameLiterals(noticeFiles); len(lits) != 0 {
		t.Errorf("notices still spells its own licence-name rule: %v", lits)
	}

	used := false
	for _, f := range noticeFiles {
		ast.Inspect(f, func(n ast.Node) bool {
			if sel, ok := n.(*ast.SelectorExpr); ok && sel.Sel.Name == "LicenceFile" {
				if id, ok := sel.X.(*ast.Ident); ok && id.Name == "review" {
					used = true
				}
			}
			return true
		})
	}
	if !used {
		t.Error("notices does not call review.LicenceFile")
	}
}

// SU-17: one platform list. notices prints "linux/amd64, linux/arm64" in
// its header; it takes that from review.Platforms instead of retyping it.
// The first half proves the header does name every platform review scans;
// the second that no second typing of one remains.
func TestPlatformListHasOneHome(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "LICENSE"), []byte("plain"), 0o644); err != nil {
		t.Fatal(err)
	}
	mod := &review.Module{Path: "example.com/plain", Version: "v1.0.0", Dir: root}
	got, err := notices.Build([]review.Package{{ImportPath: "example.com/plain", Dir: root, Module: mod}}, "Go", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, p := range review.Platforms {
		if !strings.Contains(got, p[0]+"/"+p[1]) {
			t.Errorf("notices header does not name %s/%s, which review scans", p[0], p[1])
		}
	}
	for name, f := range parseDir(t, ".") {
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && strings.Contains(lit.Value, "linux/amd64") {
				t.Errorf("%s retypes the platform list in a string literal; build it from review.Platforms", name)
			}
			return true
		})
	}
}
