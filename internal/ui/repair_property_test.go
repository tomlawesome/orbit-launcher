package ui

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// repairFiles are the files that draw and drive the Repair screen. They
// are the UI layer of Repair: whatever touches the deployment or starts a
// process does it behind a seam in deploy or engine, never here.
var repairFiles = []string{"repair.go", "repair_services.go", "repair_foreign.go"}

// repairForbiddenImports are the packages whose every use is a way to
// touch the filesystem, the network or a process directly. Reaching them
// by any name — a call, a function value, a renamed import — counts.
var repairForbiddenImports = map[string]bool{
	"os":        true,
	"os/exec":   true,
	"syscall":   true,
	"io/ioutil": true,
	"io/fs":     true,
	"net":       true,
	"net/http":  true,
}

// repairAllowedUses are the only selections from a forbidden package the
// Repair files may make: standard output as the writer the copy sequence
// goes to (repair_foreign.go), which is a value, not an action.
var repairAllowedUses = map[string]bool{"os.Stdout": true}

// repairAllowedCalls are the only functions of deploy and engine the
// Repair files may call. Each one is the seam where Repair reaches
// outside the process (RepairCommand builds repair.sh's command and the
// two Start functions run it with repair.sh's own read-only contract;
// ServiceStates asks Docker for container states; Detect reads
// .env-orbit), builds text (ChownCommandWords) or checks the folder's
// name and nothing else (CheckFolderName). A new entry point
// gets in only by being added here, after someone has asked what it does.
var repairAllowedCalls = map[string]bool{
	"deploy.RepairCommand":     true,
	"deploy.ServiceStates":     true,
	"deploy.Detect":            true,
	"deploy.ChownCommandWords": true,
	"deploy.CheckFolderName":   true, // the path string only (#277 W3)
	"engine.Start":             true,
	"engine.StartInteractive":  true,
}

// TestRepairModel_NeverMutatesAnything statically scans the Repair
// screen's files and fails if one reaches the filesystem, the network or
// a process directly, or calls a deploy or engine function that is not on
// the list of known seams. The diagnosis flow's only writes and spawns
// live behind those seams, where they carry repair.sh's own contract —
// the UI layer itself must stay incapable of acting on a deployment, so a
// future screen change can't quietly grow a side effect.
func TestRepairModel_NeverMutatesAnything(t *testing.T) {
	for _, name := range repairFiles {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		// Local name -> import path, so a renamed import is still seen.
		local := map[string]string{}
		for _, im := range file.Imports {
			path, err := strconv.Unquote(im.Path.Value)
			if err != nil {
				t.Fatalf("%s: import %s: %v", name, im.Path.Value, err)
			}
			base := path[strings.LastIndex(path, "/")+1:]
			if im.Name != nil {
				base = im.Name.Name
			}
			if base == "." {
				t.Errorf("%s: dot-imports %s, which hides what the file calls", name, path)
			}
			local[base] = path
		}

		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			path, imported := local[id.Name]
			if !imported {
				return true
			}
			use := path[strings.LastIndex(path, "/")+1:] + "." + sel.Sel.Name
			if repairForbiddenImports[path] && !repairAllowedUses[use] {
				t.Errorf("%s: %s uses %s — Repair must not be able to touch the filesystem, the network or a process", fset.Position(sel.Pos()), name, use)
			}
			return true
		})

		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			path, imported := local[id.Name]
			if !imported || !strings.HasSuffix(path, "/internal/deploy") && !strings.HasSuffix(path, "/internal/engine") {
				return true
			}
			use := path[strings.LastIndex(path, "/")+1:] + "." + sel.Sel.Name
			// engine's parsers read one event line and return a value.
			if strings.HasPrefix(use, "engine.Parse") || repairAllowedCalls[use] {
				return true
			}
			t.Errorf("%s: %s calls %s, which is not a known Repair seam (repairAllowedCalls)", fset.Position(call.Pos()), name, use)
			return true
		})
	}
}

// TestRepairFiles_ListsEveryRepairFile keeps repairFiles complete: a new
// repair*.go file is scanned too, or this fails.
func TestRepairFiles_ListsEveryRepairFile(t *testing.T) {
	found, err := filepath.Glob("repair*.go")
	if err != nil {
		t.Fatal(err)
	}
	listed := map[string]bool{}
	for _, f := range repairFiles {
		listed[f] = true
	}
	for _, f := range found {
		if !strings.HasSuffix(f, "_test.go") && !listed[f] {
			t.Errorf("%s is not in repairFiles, so the Repair scans never read it", f)
		}
	}
}
