// Command licencereview checks what the launcher's dependencies carry
// beyond their own code against .github/licence-review.txt (#176).
//
//	go run ./tools/licencereview            # check, as CI's deps job runs it
//	go run ./tools/licencereview -list      # print every finding and exit 0
//
// go-licenses, which runs first in the same job, checks each package's
// declared licence. This checks what that licence does not cover: files a
// package embeds, code under its own third_party or vendor directory or
// its own licence file, generated tables, and comments saying code came
// from elsewhere. See package review for the rules.
//
// It reads the packages linked into ./cmd/orbit-launcher for each
// platform Orbit builds the launcher for (scripts/ci/build-launcher.sh in
// ai/orbit), so only what really ships is reviewed.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/tomlawesome/orbit-launcher/tools/licencereview/review"
)

// platforms are the GOOS/GOARCH pairs Orbit builds the launcher for, with
// CGO_ENABLED=0 as it builds them.
var platforms = [][2]string{{"linux", "amd64"}, {"linux", "arm64"}}

func main() {
	reviewPath := flag.String("review", ".github/licence-review.txt", "the recorded reviews")
	list := flag.Bool("list", false, "print every finding, reviewed or not, and exit 0")
	flag.Parse()

	if err := run(*reviewPath, *list, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "licencereview:", err)
		os.Exit(1)
	}
}

func run(reviewPath string, list bool, out io.Writer) error {
	var pkgs []review.Package
	for _, p := range platforms {
		got, err := linked("./cmd/orbit-launcher", p[0], p[1])
		if err != nil {
			return err
		}
		pkgs = append(pkgs, got...)
	}
	findings, err := review.Scan(pkgs)
	if err != nil {
		return err
	}
	if list {
		for _, f := range findings {
			fmt.Fprintln(out, f)
		}
		return nil
	}

	text, err := os.ReadFile(reviewPath)
	if err != nil {
		return err
	}
	reviews, err := review.ParseReviews(string(text))
	if err != nil {
		return fmt.Errorf("%s: %w", reviewPath, err)
	}
	unreviewed, stale := review.Check(findings, reviews)
	for _, f := range unreviewed {
		fmt.Fprintf(out, "unreviewed: %s\n", f)
	}
	for _, r := range stale {
		fmt.Fprintf(out, "stale: %s:%d %s %s %s matches nothing linked -- delete it, or re-review it for the new version\n", reviewPath, r.Line, r.Module, r.Path, r.Kind)
	}
	if len(unreviewed) > 0 || len(stale) > 0 {
		return fmt.Errorf("%d unreviewed, %d stale; read each file, then record its licence in %s (a licence decision is the owner's)", len(unreviewed), len(stale), reviewPath)
	}
	fmt.Fprintf(out, "%d findings, all reviewed\n", len(findings))
	return nil
}

// linked lists the packages pkg links on goos/goarch.
func linked(pkg, goos, goarch string) ([]review.Package, error) {
	cmd := exec.Command("go", "list", "-deps", "-json=ImportPath,Dir,GoFiles,SFiles,EmbedFiles,Module", pkg)
	cmd.Env = append(os.Environ(), "GOOS="+goos, "GOARCH="+goarch, "CGO_ENABLED=0")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go list for %s/%s: %w\n%s", goos, goarch, err, stderr.String())
	}
	var pkgs []review.Package
	dec := json.NewDecoder(bytes.NewReader(stdout))
	for {
		var p review.Package
		if err := dec.Decode(&p); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("go list for %s/%s: %w", goos, goarch, err)
		}
		pkgs = append(pkgs, p)
	}
	return pkgs, nil
}
