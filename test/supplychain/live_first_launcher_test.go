// The update leg's first install uses the earlier release's own launcher
// (#307). A real deployment on the earlier preview release was installed
// with the launcher that shipped with it, so the leg installs the earlier
// release with that launcher, then updates and removes with the launcher
// under test. `live-prepare` finds the earlier launcher's archive among
// ai/orbit's `build_launcher` job artifacts with ORBIT_READ_TOKEN, keeps it
// only if its sha256 equals the one the signed earlier manifest records for
// it, fails (never falls back to the current launcher, never skips) when
// none matches, and hands it on under .live/orbit/release/first/. Each leg
// re-checks that sha256, extracts the binary and exports its path as
// ORBIT_LIVE_FIRST_LAUNCHER.
//
// Read as text, for the reason ci_rules_test.go gives.
package supplychain

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"strings"
	"testing"
)

const earlierLauncherArchive = "orbit-launcher_linux_amd64.tar.gz"

// failsAfter reports whether, at or after the first line of job containing
// marker, some line containing every word in words stops the job with
// `exit 1` on that line or the next.
func failsAfter(job []string, marker string, words ...string) bool {
	from := -1
	for i, l := range job {
		if strings.Contains(l, marker) {
			from = i
			break
		}
	}
	if from < 0 {
		return false
	}
	for i := from; i < len(job); i++ {
		l := strings.ToLower(job[i])
		ok := true
		for _, w := range words {
			if !strings.Contains(l, strings.ToLower(w)) {
				ok = false
			}
		}
		if !ok {
			continue
		}
		next := ""
		if i+1 < len(job) {
			next = job[i+1]
		}
		if strings.Contains(job[i], "exit 1") || strings.HasPrefix(next, "exit 1") {
			return true
		}
	}
	return false
}

func TestLivePrepareFindsTheEarlierLauncherInOrbitsBuildLauncherJob(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	text := strings.Join(liveJob(t, ci, "live-prepare"), "\n")
	for _, c := range []struct{ want, why string }{
		{"build_launcher", "look for the archive among ai/orbit's build_launcher job artifacts"},
		{earlierLauncherArchive, "name the archive it looks for"},
		{"ORBIT_READ_TOKEN", "read ai/orbit with the read-only token"},
	} {
		if !strings.Contains(text, c.want) {
			t.Errorf("the `live-prepare` job must contain %q: %s (#307)", c.want, c.why)
		}
	}
}

// buildLauncherRoute finds, in live-prepare, the lines of the build_launcher
// route that decide whether a downloaded archive is kept. Each is found by
// its own content, not by a word that other lines also contain, and each
// problem names the line that is missing:
//   - the manifest entry read into first_launcher_sha, and the hex taken from it;
//   - the loop over build_launcher jobs;
//   - inside it, the comparison of the candidate's sha256 with that hex, as an
//     `if` whose first line moves the candidate into place, so a mismatch is
//     skipped and the loop goes on;
//   - after the loop, the `[ -s ... ]` guard on the kept archive, with the
//     failure message on the next line.
//
// guardMessage is that failure message.
func buildLauncherRoute(job []string) (guardMessage string, problems []string) {
	index := func(from int, match func(string) bool) int {
		for i := from; i < len(job); i++ {
			if match(job[i]) {
				return i
			}
		}
		return -1
	}
	entry := index(0, func(l string) bool {
		return strings.HasPrefix(l, "first_launcher_sha=") && strings.Contains(l, `.files["`+earlierLauncherArchive+`"]`)
	})
	if entry < 0 {
		problems = append(problems, "no `first_launcher_sha=` line reading "+`.files["`+earlierLauncherArchive+`"]`+" from the verified earlier manifest")
	}
	hex := index(entry+1, func(l string) bool { return l == `first_launcher_hex="${first_launcher_sha#sha256:}"` })
	if entry >= 0 && hex < 0 {
		problems = append(problems, "no `first_launcher_hex=\"${first_launcher_sha#sha256:}\"` line after the manifest entry is read")
	}
	loop := index(0, func(l string) bool { return strings.Contains(l, `select(.name == "build_launcher")`) })
	if loop < 0 {
		problems = append(problems, "no loop over ai/orbit's `build_launcher` jobs (`select(.name == \"build_launcher\")`)")
		return "", problems
	}
	cmp := index(loop+1, func(l string) bool {
		return strings.HasPrefix(l, "if ") && strings.Contains(l, "sha256sum") &&
			strings.Contains(l, "candidate.tar.gz") && strings.HasSuffix(l, `)" = "$first_launcher_hex" ]; then`)
	})
	switch {
	case cmp < 0:
		problems = append(problems, "no `if` in the build_launcher loop comparing the candidate's sha256sum with $first_launcher_hex")
	case cmp+1 >= len(job) || !strings.HasPrefix(job[cmp+1], "mv ") || !strings.Contains(job[cmp+1], "candidate.tar.gz") || !strings.Contains(job[cmp+1], `"$first_dir/$first_archive"`):
		problems = append(problems, "the sha256 comparison in the build_launcher loop must be followed by the `mv` of the candidate into $first_dir/$first_archive, so only a match is kept")
	}
	for i := loop + 1; cmp >= 0 && i < cmp; i++ {
		if strings.HasPrefix(job[i], "mv ") && strings.Contains(job[i], `"$first_dir/$first_archive"`) {
			problems = append(problems, "the build_launcher loop moves a candidate into $first_dir/$first_archive before comparing its sha256 (line: "+job[i]+")")
		}
	}
	guard := index(max(cmp, loop)+1, func(l string) bool { return l == `[ -s "$first_dir/$first_archive" ] \` })
	switch {
	case guard < 0:
		problems = append(problems, "no `[ -s \"$first_dir/$first_archive\" ]` guard after the build_launcher loop")
	case guard+1 >= len(job) || !strings.HasPrefix(job[guard+1], "|| {") || !strings.Contains(job[guard+1], "exit 1"):
		problems = append(problems, "the `[ -s ... ]` guard after the build_launcher loop must be followed by `|| { ...; exit 1; }`")
	default:
		guardMessage = job[guard+1]
	}
	return guardMessage, problems
}

func TestLivePrepareKeepsAnEarlierLauncherOnlyIfItsSha256MatchesTheSignedManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	_, problems := buildLauncherRoute(liveJob(t, ci, "live-prepare"))
	for _, p := range problems {
		t.Errorf("the `live-prepare` job keeps the archive only if its sha256 equals the manifest's (#307): %s", p)
	}
}

func TestLivePrepareFailsWhenNoEarlierLauncherMatches(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	message, _ := buildLauncherRoute(liveJob(t, ci, "live-prepare"))
	if message == "" {
		t.Fatal("the `live-prepare` job must exit 1 on the `[ -s ... ]` guard after the build_launcher loop when no archive matches the manifest's sha256 (#307)")
	}
	low := strings.ToLower(message)
	if !strings.Contains(low, "launcher") {
		t.Error("the `live-prepare` job's no-earlier-launcher failure must be about the launcher (#307)")
	}
	if !strings.Contains(low, "commit") {
		t.Error("the `live-prepare` job's no-earlier-launcher failure must name the commit it looked for (#307)")
	}
}

func TestLivePrepareNeverFallsBackFromTheEarlierLauncherOrSkipsTheLeg(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, l := range liveJob(t, ci, "live-prepare") {
		low := strings.ToLower(l)
		if !strings.Contains(low, "launcher") {
			continue
		}
		for _, bad := range []string{"fall back", "falling back", "fallback", "skip", "current launcher instead"} {
			if strings.Contains(low, bad) && !strings.Contains(low, "never") && !strings.Contains(low, "not ") {
				t.Errorf("the `live-prepare` job must never fall back to another launcher or skip the update leg when the earlier one is missing (#307)\nline: %s", l)
			}
		}
	}
}

func TestLivePrepareHandsTheEarlierLauncherOnWithTheFirstReleaseFiles(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	doc := loadCI(t, ci)
	found := false
	for _, l := range liveJob(t, ci, "live-prepare") {
		if strings.Contains(l, earlierLauncherArchive) &&
			(strings.Contains(l, "first_dir") || strings.Contains(l, ".live/orbit/release/first")) {
			found = true
		}
	}
	if !found {
		t.Errorf("the `live-prepare` job must place %s with the other first-release files, under .live/orbit/release/first/ (#307)", earlierLauncherArchive)
	}
	prep, err := doc.effectiveJob("live-prepare")
	if err != nil {
		t.Fatal(err)
	}
	if got := prep.Keys["artifacts"].dump(); !strings.Contains(got, `".live/orbit/release/"`) {
		t.Errorf("the `live-prepare` job's artifacts must include .live/orbit/release/, which carries first/; artifacts are %s (#307)", got)
	}
}

func TestLiveLegsRecheckTheEarlierLauncherAgainstTheManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	manifestEntry := `.files["` + earlierLauncherArchive + `"]`
	for _, name := range []string{"live", "live-fedora"} {
		leg := strings.Join(liveJob(t, ci, name), "\n")
		for _, c := range []struct{ want, why string }{
			{earlierLauncherArchive, "use the handed-over archive"},
			{manifestEntry, "re-check its sha256 against the signed manifest, as it does the other handed-over files"},
			{"sha256sum", "compute the archive's sha256"},
		} {
			if !strings.Contains(leg, c.want) {
				t.Errorf("the `%s` job must contain %q: %s (#307)", name, c.want, c.why)
			}
		}
	}
}

func TestLiveLegsExportTheEarlierLauncherAndSayWhichOne(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, name := range []string{"live", "live-fedora"} {
		leg := strings.Join(liveJob(t, ci, name), "\n")
		for _, c := range []struct{ want, why string }{
			{"export ORBIT_LIVE_FIRST_LAUNCHER=", "hand the test the earlier launcher's binary"},
			{"update leg: earlier launcher", "say which earlier launcher it installed, in a line a watcher can grep"},
			{"orbit-launcher", "extract the orbit-launcher binary from the archive"},
		} {
			if !strings.Contains(leg, c.want) {
				t.Errorf("the `%s` job must contain %q: %s (#307)", name, c.want, c.why)
			}
		}
	}
}

func TestLiveLegsKeepUsingNoTokenToFetchTheEarlierLauncher(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	for _, name := range []string{"live", "live-fedora"} {
		for _, l := range liveJob(t, ci, name) {
			if strings.Contains(l, earlierLauncherArchive) && (strings.Contains(l, "curl ") || strings.Contains(l, "ORBIT_READ_TOKEN")) {
				t.Errorf("the `%s` job must take the earlier launcher from live-prepare's artifacts, not fetch it (#303, #307)\nline: %s", name, l)
			}
		}
	}
}

func TestLiveUpdateTestInstallsEarlierWithTheEarlierLauncherAndFailsWithoutIt(t *testing.T) {
	goLeg := readRepoFile(t, "test", "live", "update_test.go")
	for _, problem := range updateTestFirstLauncherProblems(t, goLeg) {
		t.Error(problem + " (#307)")
	}
}

// updateTestFirstLauncherProblems parses test/live/update_test.go and checks
// the update leg's use of the earlier launcher: the environment variable's
// name is the one the `live` job exports, the value comes from reading it,
// an empty value reaches t.Fatalf (never t.Skip*), and InstallEarlier runs
// the value. It reads the syntax tree, not lines, so a constant or a comment
// that merely names the variable cannot satisfy it.
func updateTestFirstLauncherProblems(t *testing.T, src string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), "update_test.go", src, 0)
	if err != nil {
		t.Fatalf("parse test/live/update_test.go: %v", err)
	}
	const envName = "ORBIT_LIVE_FIRST_LAUNCHER"
	var problems []string

	// The constant naming the variable.
	envConst := ""
	ast.Inspect(file, func(n ast.Node) bool {
		if vs, ok := n.(*ast.ValueSpec); ok {
			for i, id := range vs.Names {
				if i < len(vs.Values) {
					if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Value == strconv.Quote(envName) {
						envConst = id.Name
					}
				}
			}
		}
		return true
	})
	if envConst == "" {
		return []string{"test/live/update_test.go must declare a constant for " + envName + ", the name the `live` job exports"}
	}

	// firstLauncher := os.Getenv(<that constant>)
	value := ""
	ast.Inspect(file, func(n ast.Node) bool {
		as, ok := n.(*ast.AssignStmt)
		if !ok || len(as.Lhs) != 1 || len(as.Rhs) != 1 {
			return true
		}
		call, ok := as.Rhs[0].(*ast.CallExpr)
		if !ok || len(call.Args) != 1 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		arg, argOK := call.Args[0].(*ast.Ident)
		lhs, lhsOK := as.Lhs[0].(*ast.Ident)
		if ok && argOK && lhsOK && sel.Sel.Name == "Getenv" && arg.Name == envConst {
			value = lhs.Name
		}
		return true
	})
	if value == "" {
		return append(problems, "test/live/update_test.go must read "+envName+" with os.Getenv("+envConst+")")
	}

	// if <value> == "" { t.Fatalf(...) } with no t.Skip anywhere in the block.
	guarded := false
	ast.Inspect(file, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		cond, ok := ifs.Cond.(*ast.BinaryExpr)
		if !ok || cond.Op != token.EQL {
			return true
		}
		id, idOK := cond.X.(*ast.Ident)
		lit, litOK := cond.Y.(*ast.BasicLit)
		if !idOK || !litOK || id.Name != value || lit.Value != `""` {
			return true
		}
		guarded = true
		fatal, skip := false, ""
		ast.Inspect(ifs.Body, func(m ast.Node) bool {
			if call, ok := m.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
					switch {
					case sel.Sel.Name == "Fatalf" || sel.Sel.Name == "Fatal":
						fatal = true
					case strings.HasPrefix(sel.Sel.Name, "Skip"):
						skip = sel.Sel.Name
					}
				}
			}
			return true
		})
		if skip != "" {
			problems = append(problems, "test/live/update_test.go must fail, not skip (t."+skip+"), when "+envName+" is missing")
		}
		if !fatal {
			problems = append(problems, "test/live/update_test.go must call t.Fatalf when "+envName+" is missing")
		}
		return true
	})
	if !guarded {
		problems = append(problems, "test/live/update_test.go must check `"+value+` == ""`+"`"+" ("+envName+" missing) and fail")
	}

	// t.Run("InstallEarlier", func(...) { ... <value> ... })
	uses := false
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) != 2 {
			return true
		}
		name, nameOK := call.Args[0].(*ast.BasicLit)
		body, bodyOK := call.Args[1].(*ast.FuncLit)
		if !nameOK || !bodyOK || name.Value != `"InstallEarlier"` {
			return true
		}
		ast.Inspect(body.Body, func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok {
				for _, a := range c.Args {
					if id, ok := a.(*ast.Ident); ok && id.Name == value {
						uses = true
					}
				}
			}
			return true
		})
		return true
	})
	if !uses {
		problems = append(problems, "test/live/update_test.go's InstallEarlier subtest must pass the earlier launcher ("+value+") to the call that starts it")
	}
	return problems
}

func TestUpdateTestFirstLauncherProblemsCatchesEachFault(t *testing.T) {
	const good = `package live
const firstLauncherEnv = "ORBIT_LIVE_FIRST_LAUNCHER"
func f(t T) {
	firstLauncher := os.Getenv(firstLauncherEnv)
	if firstLauncher == "" {
		t.Fatalf("not set")
	}
	t.Run("InstallEarlier", func(t T) { startLiveEnv(t, firstLauncher, dir) })
}`
	for _, tc := range []struct{ name, src, want string }{
		{"good", good, ""},
		{"skip instead of fail", strings.Replace(good, `t.Fatalf("not set")`, `t.Skip("not set")`, 1), "must fail, not skip"},
		{"skip beside fail", strings.Replace(good, `t.Fatalf("not set")`, `t.Fatalf("not set"); t.Skipf("x")`, 1), "must fail, not skip"},
		{"no guard", strings.Replace(good, `if firstLauncher == "" {`, `if false {`, 1), "must check"},
		{"guard without fatal", strings.Replace(good, `t.Fatalf("not set")`, `t.Log("not set")`, 1), "must call t.Fatalf"},
		{"env not read", strings.Replace(good, `os.Getenv(firstLauncherEnv)`, `os.Getenv("OTHER")`, 1), "must read"},
		{"launcher unused", strings.Replace(good, `startLiveEnv(t, firstLauncher, dir)`, `startLiveEnv(t, binPath, dir)`, 1), "InstallEarlier subtest must pass"},
		{"no constant", strings.Replace(good, `"ORBIT_LIVE_FIRST_LAUNCHER"`, `"OTHER"`, 1), "must declare a constant"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.Join(updateTestFirstLauncherProblems(t, tc.src), "\n")
			if tc.want == "" && got != "" || tc.want != "" && !strings.Contains(got, tc.want) {
				t.Errorf("problems = %q, want one containing %q", got, tc.want)
			}
		})
	}
}

// The earlier launcher is read from ai/orbit's previous/ archive first (#313,
// #307). Orbit's record_image job keeps the earlier build's launcher archive
// under .orbit-supply-chain/previous/ beside the manifest, so the common case
// needs no search through Orbit's pipelines; the search stays as the second
// route, and for the hand-started job-ID route.

const previousLauncherPath = ".orbit-supply-chain/previous/" + earlierLauncherArchive

// previousArchiveCurl returns the index of the line in job that reads the
// previous/ archive, or -1.
func previousArchiveCurl(job []string) int {
	for i, l := range job {
		if strings.Contains(l, previousLauncherPath) && strings.Contains(l, "?job=record_image") && strings.Contains(l, "${orbit_branch_path}") {
			return i
		}
	}
	return -1
}

func movesArchive(l string) bool {
	return (strings.Contains(l, "mv ") || strings.Contains(l, "cp ")) &&
		strings.Contains(l, "first_dir") &&
		(strings.Contains(l, earlierLauncherArchive) || strings.Contains(l, "first_archive"))
}

func TestLivePrepareLooksForTheEarlierLauncherInPreviousBeforeOrbitsPipelines(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	curl := previousArchiveCurl(job)
	if curl < 0 {
		t.Fatalf("the `live-prepare` job must read %s?job=record_image by branch (${orbit_branch_path} on the same line, like the previous/ manifest read) (#313)", previousLauncherPath)
	}
	for i, l := range job {
		if strings.Contains(l, "pipelines?sha=") {
			if i > curl {
				return
			}
			t.Errorf("the `live-prepare` job must try the previous/ archive (line %d) before the `pipelines?sha=` lookup (line %d) (#313)", curl, i)
			return
		}
	}
	t.Error("the `live-prepare` job must keep the `pipelines?sha=` lookup as the second route (#313)")
}

func TestLivePrepareKeepsThePreviousArchiveOnlyIfItsSha256MatchesTheSignedManifest(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	curl := previousArchiveCurl(job)
	if curl < 0 {
		t.Fatalf("the `live-prepare` job must read %s?job=record_image by branch (#313)", previousLauncherPath)
	}
	move := -1
	for i := curl + 1; i < len(job); i++ {
		if movesArchive(job[i]) {
			move = i
			break
		}
	}
	if move < 0 {
		t.Fatal("after reading the previous/ archive, the `live-prepare` job must move or copy it into first_dir (#313)")
	}
	for i := curl + 1; i < move; i++ {
		if strings.Contains(job[i], "sha256sum") {
			return
		}
	}
	t.Errorf("the `live-prepare` job must compute the previous/ archive's sha256 (sha256sum) between reading it (line %d) and moving it into first_dir (line %d): it is kept only if it matches the signed manifest (#313)", curl, move)
}

func TestLivePrepareMissingPreviousArchiveDoesNotStopTheJob(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	if previousArchiveCurl(job) < 0 {
		t.Fatalf("the `live-prepare` job must read %s?job=record_image by branch (#313)", previousLauncherPath)
	}
	for i, l := range job {
		if !strings.Contains(l, earlierLauncherArchive) || !strings.Contains(l, "previous/") {
			continue
		}
		next := ""
		if i+1 < len(job) {
			next = job[i+1]
		}
		if strings.Contains(l, "exit 1") || strings.Contains(next, "exit 1") {
			t.Errorf("a missing previous/ archive must fall through to the build_launcher lookup, not stop the job (#313)\nline: %s\nnext: %s", l, next)
		}
	}
}

func TestLivePrepareNoEarlierLauncherFailureNamesTheWayOut(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	if !failsAfter(job, "build_launcher", "previous/", "build_launcher", "preview build", "ORBIT_FIRST_RECORD_IMAGE_JOB") {
		t.Error("the `live-prepare` job's no-earlier-launcher failure must name previous/, build_launcher, a new ai/orbit `preview build` (what fixes it) and ORBIT_FIRST_RECORD_IMAGE_JOB (the hand-started route) (#313)")
	}
}

func TestLivePrepareJobIDRouteStillUsesThePipelineLookup(t *testing.T) {
	ci := readRepoFile(t, ".gitlab-ci.yml")
	job := liveJob(t, ci, "live-prepare")
	closing := -1
	for i, l := range job {
		if strings.Contains(l, `jq -e '.channel == "preview"'`) {
			closing = i
			break
		}
	}
	if closing < 0 {
		t.Fatal(`the live-prepare job must check jq -e '.channel == "preview"' on the earlier manifest (#198)`)
	}
	for i, l := range job {
		if strings.Contains(l, "pipelines?sha=") {
			if i < closing {
				t.Errorf("the `pipelines?sha=` lookup (line %d) must come after the block that reads the earlier manifest (line %d), so it also runs when ORBIT_FIRST_RECORD_IMAGE_JOB is set (#313)", i, closing)
			}
			return
		}
	}
	t.Error("the `live-prepare` job must keep the `pipelines?sha=` lookup, which the job-ID route depends on (#313)")
}
