// Command releasereadiness checks a promotion before its merge request is
// opened (#359, after ai/orbit#1860):
//
//	go run ./tools/releasereadiness -target preview|main [-remote <name>]
//
// It runs on the source branch's checkout (dev for -target preview, preview
// for -target main), with the target fetched as <remote>/<target>. It prints
// ok or FAIL per check and exits 0 when ready, 1 when not, 2 on a usage
// error. The checks are in readiness; docs/releasing.md lists them.
//
// Environment (all optional; no value is ever printed):
//
//	RELEASE_READINESS_TOKEN  a GitLab token that can read this project's
//	                         issues, for the audit check's issue-closed half.
//	                         Without it that half fails and says to check by hand.
//	CI_API_V4_URL, CI_PROJECT_ID
//	                         the GitLab API and project (set in CI).
//	CI_MERGE_REQUEST_SOURCE_BRANCH_SHA
//	                         the source commit (set in CI); default HEAD.
//	ORBIT_GITHUB_REPO        the public Orbit mirror, default tomlawesome/orbit.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"

	"github.com/tomlawesome/orbit-launcher/tools/internal/cli"
	"github.com/tomlawesome/orbit-launcher/tools/releasereadiness/readiness"
)

func main() {
	os.Exit(run(os.Args[1:], realCheck, os.Stdout, os.Stderr))
}

var githubRepo = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*/[A-Za-z0-9][A-Za-z0-9._-]*$`)

// realCheck runs the checks on the repository in the current directory.
func realCheck(target, remote string) readiness.Report {
	cfg := lookupConfig{
		Dir:    ".",
		Remote: remote,
		Target: target,
		Source: os.Getenv("CI_MERGE_REQUEST_SOURCE_BRANCH_SHA"),
	}
	token := os.Getenv("RELEASE_READINESS_TOKEN")
	api, project := os.Getenv("CI_API_V4_URL"), os.Getenv("CI_PROJECT_ID")
	switch {
	case token == "":
		cfg.NoIssuesWhy = "no token to read issues"
	case api == "" || project == "":
		cfg.NoIssuesWhy = "no GitLab API address to read issues (CI_API_V4_URL and CI_PROJECT_ID are unset)"
	default:
		cfg.GitLabAPI = api + "/projects/" + project
		cfg.GitLabToken = token
	}
	repo := os.Getenv("ORBIT_GITHUB_REPO")
	if repo == "" {
		repo = "tomlawesome/orbit"
	}
	if githubRepo.MatchString(repo) {
		cfg.OrbitRepoURL = "https://github.com/" + repo + ".git"
	}
	return readiness.Run(target, ".", newLookups(cfg))
}

// run is main with its inputs passed in: the arguments, the function that
// runs the checks for a target and remote, and the two output streams.
func run(args []string, check func(target, remote string) readiness.Report, stdout, stderr io.Writer) int {
	usage, notReady := false, false
	code := cli.Run("releasereadiness", args, stderr, func(fs *flag.FlagSet) func() error {
		target := fs.String("target", "", "the branch the promotion merges into: preview or main (required)")
		remote := fs.String("remote", "origin", "the git remote that holds the target and the tags")
		return func() error {
			switch {
			case fs.NArg() > 0:
				usage = true
				fmt.Fprintf(stderr, "releasereadiness: unexpected argument %q\n", fs.Arg(0))
			case *target != "preview" && *target != "main":
				usage = true
				fmt.Fprintf(stderr, "releasereadiness: -target must be preview or main, got %q\nusage: go run ./tools/releasereadiness -target preview|main [-remote <name>]\n", *target)
			case *remote == "":
				usage = true
				fmt.Fprintln(stderr, "releasereadiness: -remote must name a git remote")
			default:
				rep := check(*target, *remote)
				fmt.Fprint(stdout, rep.Render())
				notReady = !rep.Ready()
			}
			return nil
		}
	})
	switch {
	case code != 0:
		return code
	case usage:
		return 2
	case notReady:
		return 1
	}
	return 0
}
