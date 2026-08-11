package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func gitTags() ([]string, error) {
	out, err := exec.Command("git", "tag", "--list").Output()
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
	hotfix := flag.Bool("hotfix", false, "calculate a hotfix (patch) version instead of an ordinary (minor) train")
	flag.Parse()

	tags, err := gitTags()
	if err != nil {
		fmt.Fprintln(os.Stderr, "calculateversion:", err)
		os.Exit(1)
	}

	fmt.Println(NextVersion(tags, *hotfix))
}
