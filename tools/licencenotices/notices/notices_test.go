package notices

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/tools/licencereview/review"
)

// module writes a fake module root holding files and returns a linked
// package in it.
func module(t *testing.T, path, version string, files map[string]string) review.Package {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return review.Package{ImportPath: path, Dir: dir, Module: &review.Module{Path: path, Version: version, Dir: dir}}
}

func TestBuild_CarriesEveryNotice(t *testing.T) {
	pkgs := []review.Package{
		module(t, "example.com/b", "v1.0.0", map[string]string{"LICENSE": "B licence text", "README.md": "not a licence"}),
		module(t, "example.com/a", "v2.0.0", map[string]string{"LICENSE.txt": "A licence text", "NOTICE": "A notice text"}),
	}
	main := module(t, "example.com/launcher", "", map[string]string{"LICENSE": "AGPL text"})
	main.Module.Main = true
	pkgs = append(pkgs, main)
	extras := []Extra{{Name: "x.txt", Modules: []string{"example.com/a"}, About: "a table from elsewhere", Text: "Elsewhere's notice"}}

	got, err := Build(pkgs, "Go licence text", extras)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	for _, want := range []string{
		"==== Go standard library and runtime (LICENSE) ====\n\nGo licence text",
		"==== example.com/a v2.0.0 (LICENSE.txt) ====\n\nA licence text",
		"==== example.com/a v2.0.0 (NOTICE) ====\n\nA notice text",
		"==== example.com/b v1.0.0 (LICENSE) ====\n\nB licence text",
		"==== Carried by example.com/a: a table from elsewhere ====\n\nElsewhere's notice",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("notices lack %q", want)
		}
	}
	for _, unwanted := range []string{"not a licence", "AGPL text"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("notices carry %q, which is not a dependency's licence", unwanted)
		}
	}
}

// A module whose notice cannot be found must stop the build, not ship
// without it.
// The same modules must always produce the same file, or -check would
// fail at random in CI.
func TestBuild_IsStableAndInPathOrder(t *testing.T) {
	var pkgs []review.Package
	for _, name := range []string{"e", "c", "a", "d", "b", "g", "f"} {
		pkgs = append(pkgs, module(t, "example.com/"+name, "v1.0.0", map[string]string{"LICENSE": name}))
	}
	first, err := Build(pkgs, "Go", nil)
	if err != nil {
		t.Fatal(err)
	}
	last := -1
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g"} {
		i := strings.Index(first, "==== example.com/"+name+" ")
		if i < last {
			t.Fatalf("example.com/%s is out of path order", name)
		}
		last = i
	}
	for range 20 {
		again, err := Build(pkgs, "Go", nil)
		if err != nil {
			t.Fatal(err)
		}
		if again != first {
			t.Fatal("two builds of the same modules differ")
		}
	}
}

func TestBuild_FailsOnAModuleWithoutALicenceFile(t *testing.T) {
	pkgs := []review.Package{module(t, "example.com/bare", "v0.1.0", map[string]string{"README.md": "hi"})}
	_, err := Build(pkgs, "Go", nil)
	if err == nil || !strings.Contains(err.Error(), "example.com/bare v0.1.0 has no licence file") {
		t.Fatalf("err = %v, want it to name the module without a licence", err)
	}
}

func TestBuild_FailsOnAnExtraForAModuleNoLongerLinked(t *testing.T) {
	pkgs := []review.Package{module(t, "example.com/a", "v1.0.0", map[string]string{"LICENSE": "A"})}
	extras := []Extra{{Name: "gone.txt", Modules: []string{"example.com/gone"}, About: "x", Text: "y"}}
	_, err := Build(pkgs, "Go", extras)
	if err == nil || !strings.Contains(err.Error(), "gone.txt names example.com/gone") {
		t.Fatalf("err = %v, want the stale extra named", err)
	}
}

func TestParseExtra(t *testing.T) {
	e, err := ParseExtra("u.txt", "Module: example.com/a\nModule: example.com/b\nAbout: tables\n\nNOTICE\nline two\n")
	if err != nil {
		t.Fatalf("ParseExtra: %v", err)
	}
	if len(e.Modules) != 2 || e.About != "tables" || e.Text != "NOTICE\nline two" {
		t.Fatalf("got %+v", e)
	}
	for _, bad := range []string{
		"Module: example.com/a\nAbout: tables\nNOTICE", // no blank line
		"About: tables\n\nNOTICE",                      // no module
		"Module: example.com/a\n\nNOTICE",              // no about
		"Module: example.com/a\nAbout: tables\n\n",     // no notice
		"Modules: example.com/a\nAbout: t\n\nN",        // unknown header
	} {
		if _, err := ParseExtra("bad.txt", bad); err == nil {
			t.Errorf("%q parsed, want an error", bad)
		}
	}
}

// The committed extras must parse: CI's -check reads them, but a broken
// one should fail here too, next to the code that reads it.
func TestCommittedExtrasParse(t *testing.T) {
	names, err := filepath.Glob("../../../internal/notices/extra/*.txt")
	if err != nil || len(names) == 0 {
		t.Fatalf("no extras found (%v)", err)
	}
	for _, name := range names {
		content, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ParseExtra(name, string(content)); err != nil {
			t.Error(err)
		}
	}
}
