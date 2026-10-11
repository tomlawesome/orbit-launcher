// Command coveragefloor gates CI on orbit-launcher's per-package coverage
// floors: a ratchet, not a target. A floor recorded in
// .github/coverage-floors.txt is never lowered to make a change pass --
// see that file's own header for how to raise one.
//
//	go run ./tools/coveragefloor coverage.out
//	go run ./tools/coveragefloor coverage.out path/to/other-floors.txt
//
// Exit codes are the shared skeleton's (tools/internal/cli): 0 when every
// measured package is at or above its floor and every floor names a
// package the profile still has, and for -h; 1 when the ratchet failed (a
// regression or a stale floor -- see stderr for which) or an input could
// not be read or parsed, so the command fails closed rather than passing
// on bad input; 2 for a bad flag or the wrong number of arguments.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/tomlawesome/orbit-launcher/tools/coveragefloor/floor"
	"github.com/tomlawesome/orbit-launcher/tools/internal/cli"
)

const defaultFloorsPath = ".github/coverage-floors.txt"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is main with its arguments and outputs passed in. The positional
// arguments -- the profile and an optional floors file -- are read after
// the flags are parsed; any other count prints the usage and exits 2,
// which cli.Run reserves for flags, so it is mapped here.
func run(args []string, stdout, stderr io.Writer) int {
	usageError := false
	code := cli.Run("coveragefloor", args, stderr, func(fs *flag.FlagSet) func() error {
		fs.Usage = func() {
			fmt.Fprintln(fs.Output(), "usage: coveragefloor [flags] <coverage.out> [floors-file]")
			fs.PrintDefaults()
		}
		return func() error {
			if fs.NArg() < 1 || fs.NArg() > 2 {
				usageError = true
				fs.Usage()
				return nil
			}
			floorsPath := defaultFloorsPath
			if fs.NArg() == 2 {
				floorsPath = fs.Arg(1)
			}
			return check(fs.Arg(0), floorsPath, stdout)
		}
	})
	if usageError {
		return 2
	}
	return code
}

// check is the ratchet: it reads the profile and the floors, prints each
// package against its floor, and returns an error if either input cannot
// be used or the ratchet failed.
func check(profilePath, floorsPath string, stdout io.Writer) error {
	measured, err := readProfile(profilePath)
	if err != nil {
		return err
	}
	floors, err := readFloors(floorsPath)
	if err != nil {
		return err
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
		msg := "coverage ratchet failed:"
		for _, p := range problems {
			msg += "\n  - " + p
		}
		return errors.New(msg)
	}
	fmt.Fprintf(stdout, "coveragefloor: %d package(s) checked against %s, all at or above their floor\n", len(floors), floorsPath)
	return nil
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
