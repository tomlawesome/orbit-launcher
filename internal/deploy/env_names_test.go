package deploy

// EN-14 (#199, done under #219): the launcher's ORBIT_LAUNCHER_* names are
// one constant block in deploy, the one that holds ConfigTreeEnv, and
// every reader imports it. Two places may still type a name: the
// black-box suites under test/, whose names are the contract they drive
// the binary through, and internal/testutil, which cannot import deploy
// (deploy's own tests import it); fetch_test.go's
// TestCountingScriptSource_IsWhereFetchInstallScriptFetches guards that
// one copy.

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

const launcherEnvPrefix = "ORBIT_LAUNCHER_"

// launcherEnvNames is every name the launcher reads.
var launcherEnvNames = []string{
	"ORBIT_LAUNCHER_CONFIG_TREE",
	"ORBIT_LAUNCHER_INSTALL_SCRIPT_URL",
	"ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH",
	"ORBIT_LAUNCHER_NO_ANIMATION",
	"ORBIT_LAUNCHER_NO_UPDATE_CHECK",
	"ORBIT_LAUNCHER_NO_HEALTH_PROBE",
	"ORBIT_LAUNCHER_NO_VOLUME_CHECK",
	"ORBIT_LAUNCHER_REQUIRE_IN_CONSOLE_CONFIG",
}

// launcherEnvBlock is the literals of deploy's const block that defines
// ConfigTreeEnv, by value; nil when there is no such block.
func launcherEnvBlock(sources []parsedSource) map[*ast.BasicLit]string {
	for _, src := range sources {
		if filepath.Dir(src.rel) != "internal/deploy" {
			continue
		}
		for _, d := range src.file.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			holds := false
			for _, spec := range gd.Specs {
				for _, name := range spec.(*ast.ValueSpec).Names {
					holds = holds || name.Name == "ConfigTreeEnv"
				}
			}
			if !holds {
				continue
			}
			lits := map[*ast.BasicLit]string{}
			ast.Inspect(gd, func(n ast.Node) bool {
				if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					lits[lit] = v
				}
				return true
			})
			return lits
		}
	}
	return nil
}

func TestEnvNames_OneBlockInDeployHoldsEveryName(t *testing.T) {
	block := launcherEnvBlock(moduleSources(t, token.NewFileSet()))
	if block == nil {
		t.Fatal("deploy has no const block defining ConfigTreeEnv")
	}
	held := map[string]bool{}
	for _, v := range block {
		held[v] = true
	}
	for _, name := range launcherEnvNames {
		if !held[name] {
			t.Errorf("the launcher's environment block does not hold %s", name)
		}
	}
}

// Non-test code outside test/ and internal/testutil names the variables
// only through the block.
func TestEnvNames_NonTestCodeSpellsNoneOutsideTheBlock(t *testing.T) {
	fset := token.NewFileSet()
	sources := moduleSources(t, fset)
	block := launcherEnvBlock(sources)
	for _, src := range sources {
		if strings.HasPrefix(src.rel, "test/") || strings.HasPrefix(src.rel, "internal/testutil/") {
			continue
		}
		ast.Inspect(src.file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if _, inBlock := block[lit]; inBlock {
				return true
			}
			if v, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(v, launcherEnvPrefix) {
				t.Errorf("%s: %s typed outside deploy's environment block", at(fset, src, lit.Pos()), lit.Value)
			}
			return true
		})
	}
}

// The product packages' own tests import deploy already, so they use the
// constants too.
func TestEnvNames_ProductTestsUseTheConstants(t *testing.T) {
	root := moduleRoot(t)
	fset := token.NewFileSet()
	for _, top := range []string{"cmd", "internal"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && d.Name() == "testutil" {
				return filepath.SkipDir
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "env_names_test.go" {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, path)
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if v, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(v, launcherEnvPrefix) {
					t.Errorf("%s:%d: %s typed; use deploy's constant", rel, fset.Position(lit.Pos()).Line, lit.Value)
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
