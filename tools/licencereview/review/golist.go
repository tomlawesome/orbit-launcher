package review

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
)

// Platforms are the GOOS/GOARCH pairs Orbit builds the launcher for
// (scripts/ci/build-launcher.sh in ai/orbit), with CGO_ENABLED=0 as it
// builds them.
var Platforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}}

// Shipped lists the packages ./cmd/orbit-launcher links on every one of
// Platforms, run from the module root.
func Shipped() ([]Package, error) {
	var pkgs []Package
	for _, p := range Platforms {
		got, err := Linked("./cmd/orbit-launcher", p[0], p[1])
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, got...)
	}
	return pkgs, nil
}

// Linked lists the packages pkg links on goos/goarch.
func Linked(pkg, goos, goarch string) ([]Package, error) {
	cmd := exec.Command("go", "list", "-deps", "-json=ImportPath,Dir,GoFiles,SFiles,EmbedFiles,Module", pkg)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list for %s/%s: %w\n%s", goos, goarch, err, stderr.String())
	}
	var pkgs []Package
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var p Package
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list for %s/%s: %w", goos, goarch, err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
