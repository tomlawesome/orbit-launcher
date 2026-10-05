package review

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// tinyModule writes a module that needs nothing from the network -- a
// main package under cmd/orbit-launcher, and a dependency held in a
// local directory through a replace directive -- and makes it the
// current directory, as Shipped and Linked expect to be run from a
// module root. go list is then run for real.
func tinyModule(t *testing.T) string {
	t.Helper()
	root := fakeModule(t, map[string]string{
		"go.mod":                     "module example.com/tiny\n\ngo 1.22\n\nrequire example.com/dep v1.0.0\n\nreplace example.com/dep => ./dep\n",
		"cmd/orbit-launcher/main.go": "package main\n\nimport \"example.com/dep\"\n\nfunc main() { dep.F() }\n",
		"dep/go.mod":                 "module example.com/dep\n\ngo 1.22\n",
		"dep/dep.go":                 "package dep\n\nimport _ \"embed\"\n\n//go:embed logo.txt\nvar logo string\n\nfunc F() { _ = logo }\n",
		"dep/logo.txt":               "logo",
		"dep/dep_arm64.go":           "package dep\n",
		"dep/dep_arm64.s":            "TEXT ·g(SB),0,$0\n\tRET\n",
	})
	t.Chdir(root)
	t.Setenv("GOFLAGS", "-mod=mod")
	t.Setenv("GOPROXY", "off")
	t.Setenv("GOWORK", "off")
	t.Setenv("GOTOOLCHAIN", "local")
	return root
}

func find(pkgs []Package, importPath string) *Package {
	for i := range pkgs {
		if pkgs[i].ImportPath == importPath {
			return &pkgs[i]
		}
	}
	return nil
}

// Linked reports what the main package links on the platform asked for:
// a dependency's files for that GOARCH only, what it embeds and which
// module it is in, with the main module marked so Scan can skip it.
func TestLinked_ListsWhatThePlatformLinks(t *testing.T) {
	root := tinyModule(t)

	arm, err := Linked("./cmd/orbit-launcher", "linux", "arm64")
	if err != nil {
		t.Fatalf("Linked arm64: %v", err)
	}
	dep := find(arm, "example.com/dep")
	if dep == nil {
		t.Fatalf("arm64 lists no example.com/dep: %+v", arm)
	}
	if !slices.Equal(dep.GoFiles, []string{"dep.go", "dep_arm64.go"}) || !slices.Equal(dep.SFiles, []string{"dep_arm64.s"}) {
		t.Errorf("arm64 dep files = %v + %v, want the arm64 Go and assembly files too", dep.GoFiles, dep.SFiles)
	}
	if !slices.Equal(dep.EmbedFiles, []string{"logo.txt"}) {
		t.Errorf("dep EmbedFiles = %v, want logo.txt", dep.EmbedFiles)
	}
	if dep.Module == nil || dep.Module.Path != "example.com/dep" || dep.Module.Version != "v1.0.0" || dep.Module.Main || !strings.HasPrefix(dep.Module.Dir, root) {
		t.Errorf("dep module = %+v, want example.com/dep v1.0.0 in the local directory", dep.Module)
	}
	if m := find(arm, "example.com/tiny/cmd/orbit-launcher"); m == nil || m.Module == nil || !m.Module.Main {
		t.Errorf("main package missing or not marked main: %+v", m)
	}
	if std := find(arm, "runtime"); std == nil || std.Module != nil {
		t.Errorf("runtime missing or given a module: %+v", std)
	}

	amd, err := Linked("./cmd/orbit-launcher", "linux", "amd64")
	if err != nil {
		t.Fatalf("Linked amd64: %v", err)
	}
	if dep := find(amd, "example.com/dep"); dep == nil || !slices.Equal(dep.GoFiles, []string{"dep.go"}) || len(dep.SFiles) != 0 {
		t.Errorf("amd64 dep = %+v, want only dep.go", dep)
	}
}

// Shipped covers every platform Orbit builds, so a file linked on only
// one of them is still reviewed.
func TestShipped_ListsEveryPlatform(t *testing.T) {
	tinyModule(t)
	pkgs, err := Shipped()
	if err != nil {
		t.Fatalf("Shipped: %v", err)
	}
	var sawArm64File bool
	deps := 0
	for _, p := range pkgs {
		if p.ImportPath == "example.com/dep" {
			deps++
			sawArm64File = sawArm64File || slices.Contains(p.GoFiles, "dep_arm64.go")
		}
	}
	if deps != len(Platforms) || !sawArm64File {
		t.Fatalf("example.com/dep listed %d times (want %d), arm64 file seen = %v", deps, len(Platforms), sawArm64File)
	}
}

// A package go list cannot load fails, naming the platform, rather than
// reviewing an empty list and passing.
func TestLinked_FailsOnAPackageGoListCannotLoad(t *testing.T) {
	tinyModule(t)
	_, err := Linked("./cmd/missing", "linux", "amd64")
	if err == nil || !strings.Contains(err.Error(), "linux/amd64") {
		t.Fatalf("err = %v, want a go list failure naming linux/amd64", err)
	}
}

func TestShipped_FailsWithoutTheLauncherPackage(t *testing.T) {
	root := tinyModule(t)
	if err := os.RemoveAll(filepath.Join(root, "cmd")); err != nil {
		t.Fatal(err)
	}
	if pkgs, err := Shipped(); err == nil {
		t.Fatalf("Shipped = %d packages, nil; want an error", len(pkgs))
	}
}
