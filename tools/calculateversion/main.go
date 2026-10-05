package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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
// a subprocess. It behaves exactly as the command did when all of this lived
// in main: exit 2 on a bad flag, 0 for -h, 1 when the tags cannot be read.
func run(args []string, tags func() ([]string, error), stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("calculateversion", flag.ContinueOnError)
	fs.SetOutput(stderr)
	hotfix := fs.Bool("hotfix", false, "calculate a hotfix (patch) version instead of an ordinary (minor) train")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}

	list, err := tags()
	if err != nil {
		fmt.Fprintln(stderr, "calculateversion:", err)
		return 1
	}

	fmt.Fprintln(stdout, NextVersion(list, *hotfix))
	return 0
}
