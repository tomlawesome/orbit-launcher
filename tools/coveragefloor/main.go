// Command coveragefloor gates CI on orbit-launcher's per-package coverage
// floors: a ratchet, not a target. A floor recorded in
// .github/coverage-floors.txt is never lowered to make a change pass --
// see that file's own header for how to raise one.
//
//	go run ./tools/coveragefloor coverage.out
//	go run ./tools/coveragefloor coverage.out path/to/other-floors.txt
//
// Exit code 0: every measured package is at or above its floor, and every
// floor names a package the profile still has. Exit code 1: the ratchet
// failed (a regression or a stale floor) -- see stderr for which. Exit
// code 2: the profile or the floors file could not be read or parsed --
// this command fails closed rather than passing on bad input.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/tomlawesome/orbit-launcher/tools/coveragefloor/floor"
)

const defaultFloorsPath = ".github/coverage-floors.txt"

func main() {
	if len(os.Args) < 2 || len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: coveragefloor <coverage.out> [floors-file]")
		os.Exit(2)
	}
	floorsPath := defaultFloorsPath
	if len(os.Args) == 3 {
		floorsPath = os.Args[2]
	}
	os.Exit(run(os.Args[1], floorsPath, os.Stdout, os.Stderr))
}

// run is the whole command, with its inputs and outputs as parameters so
// it can be exercised in tests without a subprocess.
func run(profilePath, floorsPath string, stdout, stderr io.Writer) int {
	measured, err := readProfile(profilePath)
	if err != nil {
		fmt.Fprintln(stderr, "coveragefloor:", err)
		return 2
	}
	floors, err := readFloors(floorsPath)
	if err != nil {
		fmt.Fprintln(stderr, "coveragefloor:", err)
		return 2
	}

	results, problems := floor.Check(measured, floors)
	for _, r := range results {
		if r.HasFloor {
			fmt.Fprintf(stdout, "%-65s got %5.1f%%  floor %5.1f%%\n", r.Package, r.Got, r.Floor)
		} else {
			fmt.Fprintf(stdout, "%-65s got %5.1f%%  (no floor set)\n", r.Package, r.Got)
		}
	}

	if len(problems) > 0 {
		fmt.Fprintln(stderr, "coveragefloor: coverage ratchet failed:")
		for _, p := range problems {
			fmt.Fprintln(stderr, "  -", p)
		}
		return 1
	}
	fmt.Fprintf(stdout, "coveragefloor: %d package(s) checked against %s, all at or above their floor\n", len(floors), floorsPath)
	return 0
}

func readProfile(path string) (map[string]floor.Coverage, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening coverage profile: %w", err)
	}
	defer f.Close()
	return floor.ParseProfile(f)
}

func readFloors(path string) (map[string]floor.Floor, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("opening floors file: %w", err)
	}
	defer f.Close()
	return floor.ParseFloors(f)
}
