package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/tomlawesome/orbit-launcher/tools/internal/cli"
)

// gitTags lists the tags of the repository at dir; an empty dir means the
// current directory, which is what the command uses. The parameter exists so
// tests can point it at a throwaway repository instead of this one.
func gitTags(dir string) ([]string, error) {
	cmd := exec.Command("git", "tag", "--list")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git tag --list: %w", err)
	}
	var tags []string
	for _, line := range strings.Split(string(out), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			tags = append(tags, trimmed)
		}
	}
	return tags, nil
}

func main() {
	os.Exit(run(os.Args[1:], func() ([]string, error) { return gitTags("") }, os.Stdout, os.Stderr))
}

// run is main with its inputs and outputs passed in, so the flag handling,
// the output and the exit codes can be tested without a real repository or
// a subprocess. Exit codes are cli.Run's: 2 on a bad flag or any
// positional argument, 0 for -h, 1 when the tags cannot be read.
func run(args []string, tags func() ([]string, error), stdout, stderr io.Writer) int {
	usageError := false
	code := cli.Run("calculateversion", args, stderr, func(fs *flag.FlagSet) func() error {
		hotfix := fs.Bool("hotfix", false, "calculate a hotfix (patch) version instead of an ordinary (minor) train")
		return func() error {
			// The command takes no positional arguments. `calculateversion
			// hotfix` (the flag without its dash) would otherwise print the
			// next minor version and a release would be cut at the wrong
			// number. cli.Run reserves 2 for flags, so it is mapped here.
			if fs.NArg() > 0 {
				usageError = true
				fmt.Fprintf(stderr, "calculateversion: unexpected argument %q; the only flag is -hotfix\n", fs.Arg(0))
				return nil
			}
			list, err := tags()
			if err != nil {
				return err
			}
			fmt.Fprintln(stdout, NextVersion(list, *hotfix))
			return nil
		}
	})
	if usageError {
		return 2
	}
	return code
}
