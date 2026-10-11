package notices_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/licencenotices/notices"
	"github.com/tomlawesome/orbit-launcher/tools/licencereview/review"
)

// The notices builder copies every licence file at a module's root into
// THIRD_PARTY_NOTICES.txt verbatim, so the rule must not mistake source
// code named like a licence for licence text: license.go, notice.go,
// LICENSE_test.go and COPYING.c are code, not notices.
func TestBuildShipsOnlyLicenceTextNotSource(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"LICENSE-MIT":     "MIT licence text",
		"LICENSE.APACHE":  "Apache licence text",
		"NOTICE.md":       "notice text",
		"license.go":      "package x // license.go",
		"notice.go":       "package x // notice.go",
		"LICENSE_test.go": "package x // LICENSE_test.go",
		"COPYING.c":       "int copying_c;",
	}
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mod := &review.Module{Path: "example.com/src", Version: "v1.0.0", Dir: root}
	got, err := notices.Build([]review.Package{{ImportPath: "example.com/src", Dir: root, Module: mod}}, "Go", nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, name := range []string{"LICENSE-MIT", "LICENSE.APACHE", "NOTICE.md"} {
		if want := "==== example.com/src v1.0.0 (" + name + ") ====\n\n" + files[name]; !strings.Contains(got, want) {
			t.Errorf("notices lack %s's text", name)
		}
	}
	for _, name := range []string{"license.go", "notice.go", "LICENSE_test.go", "COPYING.c"} {
		if strings.Contains(got, "("+name+")") || strings.Contains(got, files[name]) {
			t.Errorf("notices ship %s, which is source code, not licence text", name)
		}
	}
}
