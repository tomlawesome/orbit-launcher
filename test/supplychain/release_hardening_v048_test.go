package supplychain

// v0.4.8 release hardening, from the specifications on #365 (the readiness
// token must not reach branch code), #364 part 2 (the release job's
// CHANGELOG check must match tools/releasereadiness CheckChangelog) and #368
// (a release button refuses when CHANGELOG.md lacks the version it would
// cut). The job scripts are run for real, in a temporary directory, with
// stand-ins for go (calculateversion), git and release-tag-check.sh, so the
// CHANGELOG checks are tested on their behaviour, not their wording alone.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const rrToken = "RELEASE_READINESS_TOKEN"

// collectStrings gathers every scalar below n.
func collectStrings(n *yamlNode, out *[]string) {
	if n == nil {
		return
	}
	switch n.kind {
	case 's':
		*out = append(*out, n.str)
	case 'q':
		for _, it := range n.items {
			collectStrings(it, out)
		}
	case 'm':
		for _, k := range n.keys {
			collectStrings(n.vals[k], out)
		}
	}
}

func isUnsetLine(l string) bool { return strings.HasPrefix(strings.TrimSpace(l), "unset ") }

func TestV048_365_DropJobCredentialsUnsetsTheReleaseReadinessToken(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	drop, ok := doc.root.vals[".drop_job_credentials"]
	if !ok {
		t.Fatal("no .drop_job_credentials in .gitlab-ci.yml")
	}
	var strs []string
	collectStrings(drop, &strs)
	for _, s := range strs {
		for _, l := range strings.Split(s, "\n") {
			if isUnsetLine(l) && strings.Contains(l, rrToken) {
				return
			}
		}
	}
	t.Errorf(".drop_job_credentials must unset %s so branch code never sees it (#365); it holds:\n%s", rrToken, strings.Join(strs, "\n"))
}

func TestV048_365_OnlyReleaseReadinessPassesTheTokenAndOnlyToTheToolCommand(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))

	// No other job, template or global key references the token, apart from
	// the unset line the drop anchor expands to.
	for _, name := range doc.root.keys {
		if name == "release_readiness" || name == ".drop_job_credentials" {
			continue
		}
		var strs []string
		collectStrings(doc.root.vals[name], &strs)
		for _, s := range strs {
			for _, l := range strings.Split(s, "\n") {
				if strings.Contains(l, rrToken) && !isUnsetLine(l) {
					t.Errorf("%s references %s in %q: only release_readiness may (#365)", name, rrToken, l)
				}
			}
		}
	}

	eff, err := doc.effectiveJob("release_readiness")
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range eff.Keys {
		var strs []string
		collectStrings(v, &strs)
		for _, s := range strs {
			if strings.Contains(s, rrToken) {
				t.Errorf("release_readiness key %q passes %s on (#365): %q", k, rrToken, s)
			}
		}
	}
	lines := strings.Split(eff.Script, "\n")
	drop, tool := -1, -1
	for i, l := range lines {
		tl := strings.TrimSpace(l)
		if isUnsetLine(tl) && drop < 0 {
			drop = i
		}
		if strings.Contains(tl, "go run ./tools/releasereadiness") {
			tool = i
		}
	}
	if drop < 0 || tool < 0 || drop > tool {
		t.Fatalf("want the credential drop before the tool command; got lines %d and %d in:\n%s", drop, tool, eff.Script)
	}
	if !strings.Contains(lines[drop], rrToken) {
		t.Errorf("release_readiness's drop line must unset %s: %q", rrToken, lines[drop])
	}
	copied := false
	for i, l := range lines {
		if !strings.Contains(l, rrToken) || isUnsetLine(l) {
			continue
		}
		switch {
		case i < drop:
			if strings.Contains(l, "export") || strings.Contains(l, "git ") {
				t.Errorf("the copy before the drop must only hold the value in a shell variable, not export it or hand it to git: %q", l)
			}
			copied = true
		case i == tool:
			if !strings.Contains(l, rrToken+"=") || strings.Contains(l, "export") {
				t.Errorf("the tool command must set %s for its own process only (%s=... go run ...): %q", rrToken, rrToken, l)
			}
		default:
			t.Errorf("after the drop only the tool command may mention %s, line %d: %q", rrToken, i, l)
		}
	}
	if !copied {
		t.Errorf("release_readiness must copy %s before the credential drop; no such line in:\n%s", rrToken, eff.Script)
	}
	if !strings.Contains(lines[tool], rrToken) {
		t.Errorf("the tool command must receive %s: %q", rrToken, lines[tool])
	}
}

// ---- running a job's script against fixture CHANGELOGs ----

const (
	changelogHeadPatch = "0.4.9"
	changelogHeadMinor = "0.5.0"
)

// runJobScript runs the job's effective script in a temporary directory that
// holds CHANGELOG.md (none when changelog is nil), with stand-ins on PATH:
// go prints the patch version for `-hotfix` and the minor one otherwise, git
// does nothing, and scripts/ci/release-tag-check.sh prints RELEASE_TAG=v<arg1>.
// It returns the combined output and whether the script exited 0.
func runJobScript(t *testing.T, job string, changelog *string, extraEnv ...string) (string, bool) {
	t.Helper()
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	eff, err := doc.effectiveJob(job)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(p, body string, mode os.FileMode) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(bin, "go"), "#!/bin/sh\ncase \"$*\" in\n*-hotfix*) echo "+changelogHeadPatch+" ;;\n*) echo "+changelogHeadMinor+" ;;\nesac\n", 0o755)
	write(filepath.Join(bin, "git"), "#!/bin/sh\nexit 0\n", 0o755)
	write(filepath.Join(dir, "scripts", "ci", "release-tag-check.sh"), "#!/bin/sh\necho \"RELEASE_TAG=v$1\"\n", 0o755)
	if changelog != nil {
		write(filepath.Join(dir, "CHANGELOG.md"), *changelog, 0o644)
	}
	env := []string{"PATH=" + bin + ":" + os.Getenv("PATH"), "HOME=" + dir, "CI_COMMIT_SHA=0123456789abcdef"}
	if v, ok := eff.Keys["variables"]; ok && v.kind == 'm' {
		for _, k := range v.keys {
			env = append(env, k+"="+v.vals[k].str)
		}
	}
	env = append(env, extraEnv...)
	cmd := exec.Command("sh", "-c", eff.Script)
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

func clog(sections ...string) *string {
	s := "# Changelog\n\n## [Unreleased]\n\n" + strings.Join(sections, "\n")
	return &s
}

const (
	secGood      = "## [0.4.9] - 2026-11-21\n\n### Fixed\n\n- Something was fixed.\n"
	secNoDate    = "## [0.4.9] - TBD\n\n### Fixed\n\n- Something was fixed.\n"
	secBare      = "## [0.4.9]\n\n### Fixed\n\n- Something was fixed.\n"
	secProseOnly = "## [0.4.9] - 2026-11-21\n\nA few words about the release, but no list entry.\n"
	secOther     = "## [0.5.0] - 2026-11-21\n\n### Added\n\n- Something was added.\n"
)

// ---- #364 part 2: .release_gitlab ----

func TestV048_364_ReleaseGitlabChangelogCheck(t *testing.T) {
	cases := []struct {
		name      string
		changelog *string
		wantPass  bool
		wantInMsg string // lower-case, required in the refusal text
	}{
		{"a dated section with a list entry passes", clog(secGood), true, ""},
		{"a heading with no date is refused", clog(secNoDate), false, "date"},
		{"a heading with no date and no dash is refused", clog(secBare), false, "date"},
		{"a dated section with prose and no list entry is refused", clog(secProseOnly), false, "list"},
		{"a missing section is refused", clog(secOther), false, "0.4.9"},
		{"a missing CHANGELOG.md is refused", nil, false, "changelog.md"},
	}
	for _, job := range []string{"release:gitlab:patch", "release:gitlab:minor"} {
		for _, c := range cases {
			t.Run(job+"/"+c.name, func(t *testing.T) {
				out, ok := runJobScript(t, job, c.changelog, "RELEASE_TAG=v"+changelogHeadPatch)
				if ok != c.wantPass {
					t.Fatalf("exit ok=%v, want %v; output:\n%s", ok, c.wantPass, out)
				}
				if !c.wantPass && !strings.Contains(strings.ToLower(out), c.wantInMsg) {
					t.Errorf("the refusal must say what is checked (mention %q); output:\n%s", c.wantInMsg, out)
				}
			})
		}
	}
}

// ---- #368: release:patch / release:minor check CHANGELOG.md first ----

func TestV048_368_ReleaseButtonsRefuseAVersionWithoutAChangelogSection(t *testing.T) {
	buttons := []struct {
		job, other string
		mine, no   string // section for this button's version, and for the other's
	}{
		{"release:patch", "release:minor", secGood, secOther},
		{"release:minor", "release:patch", secOther, secGood},
	}
	for _, b := range buttons {
		t.Run(b.job+" passes with its own version", func(t *testing.T) {
			out, ok := runJobScript(t, b.job, clog(b.mine))
			if !ok {
				t.Fatalf("the button must pass when CHANGELOG.md has its version; output:\n%s", out)
			}
		})
		t.Run(b.job+" refuses a CHANGELOG with only the other button's version", func(t *testing.T) {
			out, ok := runJobScript(t, b.job, clog(b.no))
			if ok {
				t.Fatalf("the button must refuse; output:\n%s", out)
			}
			if !strings.Contains(out, b.other) {
				t.Errorf("the refusal must name the other button %q; output:\n%s", b.other, out)
			}
		})
		t.Run(b.job+" refuses its version with no date", func(t *testing.T) {
			s := secNoDate
			if b.job == "release:minor" {
				s = strings.ReplaceAll(secNoDate, "0.4.9", changelogHeadMinor)
			}
			out, ok := runJobScript(t, b.job, clog(s))
			if ok {
				t.Fatalf("the section must be dated (## [X.Y.Z] - YYYY-MM-DD); output:\n%s", out)
			}
		})
		t.Run(b.job+" refuses a missing CHANGELOG.md", func(t *testing.T) {
			if out, ok := runJobScript(t, b.job, nil); ok {
				t.Fatalf("the button must refuse with no CHANGELOG.md; output:\n%s", out)
			}
		})
	}
}

// Structure: the check sits before the tag check and the release.env write,
// so a refused button creates nothing, and it names the other button.
func TestV048_368_ChangelogCheckRunsBeforeTheTagCheckAndNamesTheOtherButton(t *testing.T) {
	doc := loadCI(t, readRepoFile(t, ".gitlab-ci.yml"))
	for job, other := range map[string]string{"release:patch": "release:minor", "release:minor": "release:patch"} {
		eff, err := doc.effectiveJob(job)
		if err != nil {
			t.Fatal(err)
		}
		lines := strings.Split(eff.Script, "\n")
		check, tagCheck := -1, -1
		for i, l := range lines {
			if check < 0 && strings.Contains(l, "CHANGELOG.md") {
				check = i
			}
			if tagCheck < 0 && strings.Contains(l, "release-tag-check.sh") {
				tagCheck = i
			}
		}
		if check < 0 || tagCheck < 0 || check > tagCheck {
			t.Errorf("%s: want a CHANGELOG.md check before release-tag-check.sh; got lines %d and %d in:\n%s", job, check, tagCheck, eff.Script)
		}
		if !strings.Contains(eff.Script, other) {
			t.Errorf("%s: the refusal text must name the other button %q", job, other)
		}
	}
}
