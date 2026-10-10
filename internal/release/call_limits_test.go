package release

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// #207 (#199 WI-3: EN-3). The update check owns its time limit (the 3 s
// leaves the splash); the UI only cancels.

// Done-criterion of #207: no http.DefaultClient in release, nor the
// package-level net/http helpers that send through it.
func TestReleaseSource_NeverUsesTheDefaultHTTPClient(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob package files: %v", err)
	}
	implicit := map[string]bool{"DefaultClient": true, "Get": true, "Head": true, "Post": true, "PostForm": true}
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		httpName := ""
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "net/http" {
				httpName = "http"
				if imp.Name != nil {
					httpName = imp.Name.Name
				}
			}
		}
		if httpName == "" {
			continue
		}
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == httpName && implicit[sel.Sel.Name] {
				t.Errorf("%s: http.%s: the update check owns its limit; http.DefaultClient has none", fset.Position(sel.Pos()), sel.Sel.Name)
			}
			return true
		})
	}
}

// EN-3: called with no deadline at all, the update check of a manifest
// server that never answers gives up on its own.
func TestCheckForUpdate_GivesUpOnItsOwnWithoutACallerDeadline(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() {
		close(release)
		srv.Close()
	})

	type result struct {
		hasUpdate bool
		err       error
	}
	done := make(chan result, 1)
	go func() {
		_, hasUpdate, err := checkForUpdate(context.Background(), srv.URL, "0.1.0")
		done <- result{hasUpdate, err}
	}()

	select {
	case got := <-done:
		if got.hasUpdate {
			t.Error("a manifest that never arrived reported an update")
		}
		if got.err == nil {
			t.Error("a manifest server that never answered produced no error")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("the update check is still waiting after 8s with no caller deadline: it has no limit of its own")
	}
}
