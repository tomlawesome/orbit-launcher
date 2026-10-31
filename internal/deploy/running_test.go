package deploy

// #316: RunningAppOf reads the deployment's app container from Docker,
// and CompareRunning says whether that container contradicts the record
// (.env-orbit) in the folder the launcher was started in. Written from
// the design note (issue note 46624, sections 2 and 4), before the code.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

const (
	runningProjectLabel = "com.docker.compose.project"
	runningServiceLabel = "com.docker.compose.service"
	runningFolderLabel  = "com.docker.compose.project.working_dir"
	runningVersionLabel = "org.opencontainers.image.version"
)

// containerID is a full, untruncated container id, as `docker ps
// --no-trunc` prints it.
func containerID(c byte) string { return strings.Repeat(string(c), 64) }

// jsonQuoted is a string as Docker's {{json ...}} prints it.
func jsonQuoted(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// psLine is one line of the `docker ps` answer the design asks for:
// id, the working_dir label JSON-quoted, and the state, tab-separated.
func psLine(t *testing.T, id, folder, state string) string {
	t.Helper()
	return id + "\t" + jsonQuoted(t, folder) + "\t" + state + "\n"
}

// inspectLine is the `docker inspect` answer the design asks for: the
// image reference and the version label, each JSON-quoted.
func inspectLine(t *testing.T, image, version string) string {
	t.Helper()
	return jsonQuoted(t, image) + "\t" + jsonQuoted(t, version) + "\n"
}

// fakeRunningDocker is a docker on PATH that records each call's
// arguments (one per line, a "--" line closing each call, as
// fakeDockerArgs does) and answers `ps` and `inspect` from files.
type fakeRunningDocker struct {
	// ps is the answer to `ps`. psRaw, when set, is the answer when the
	// format does not JSON-quote the label (it holds a raw newline), so
	// a launcher that prints the label raw reads what real Docker would
	// print.
	ps, psRaw   string
	psExit      int
	inspect     string
	inspectExit int
	// psHangs and inspectHangs make that call never answer.
	psHangs, inspectHangs bool
}

func (f fakeRunningDocker) install(t *testing.T) (callLog string) {
	t.Helper()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	callLog = filepath.Join(dir, "args.log")
	pids := filepath.Join(dir, "pids")
	psOut, inspectOut := write("ps.out", f.ps), write("inspect.out", f.inspect)
	psRaw := psOut
	if f.psRaw != "" {
		psRaw = write("ps.raw.out", f.psRaw)
	}
	hang := func(on bool) string {
		if on {
			return "echo $$ >> '" + pids + "'; exec sleep 30"
		}
		return ""
	}
	script := fmt.Sprintf(`#!/usr/bin/env bash
for a in "$@"; do printf '%%s\n' "$a" >> '%[1]s'; done
printf -- '--\n' >> '%[1]s'
case "$1" in
ps)
  %[2]s
  if [[ "$*" == *"json (.Label"* ]]; then cat '%[3]s'; else cat '%[4]s'; fi
  exit %[5]d ;;
inspect)
  %[6]s
  cat '%[7]s'
  exit %[8]d ;;
esac
exit 1
`, callLog, hang(f.psHangs), psOut, psRaw, f.psExit, hang(f.inspectHangs), inspectOut, f.inspectExit)
	testutil.FakeExecutable(t, "docker", script)
	killRecordedOnCleanup(t, pids)
	return callLog
}

// calls splits a call log into its docker calls, each as its arguments.
func calls(t *testing.T, callLog string) [][]string {
	t.Helper()
	b, err := os.ReadFile(callLog)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read call log: %v", err)
	}
	var out [][]string
	var cur []string
	for _, line := range strings.Split(strings.TrimSuffix(string(b), "\n"), "\n") {
		if line == "--" {
			out = append(out, cur)
			cur = nil
			continue
		}
		cur = append(cur, line)
	}
	return out
}

// tabbed reads a format argument the way Docker does: a backslash-t is a
// tab, so a launcher may spell the separator either way.
func tabbed(arg string) string { return strings.ReplaceAll(arg, `\t`, "\t") }

func sameArgs(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func runningDeployment() *Deployment {
	return &Deployment{
		TargetDir: "/opt/orbit",
		Project:   "my-orbit",
		Image:     "ghcr.io/example/orbit@sha256:aaa",
		Version:   "v1.2.0",
	}
}

// The first query lists the project's app container by two labels, not
// by the container's name (it is named orbit, but a name is not what
// Compose guarantees), with `-a` so a stopped container is still found.
func TestRunning_PsFiltersByProjectAndServiceLabels(t *testing.T) {
	callLog := fakeRunningDocker{}.install(t)

	if _, err := RunningAppOf(t.Context(), runningDeployment()); err != nil {
		t.Fatalf("RunningAppOf: %v", err)
	}

	all := calls(t, callLog)
	if len(all) == 0 {
		t.Fatal("RunningAppOf never called docker")
	}
	got := append([]string(nil), all[0]...)
	for i, a := range got {
		got[i] = tabbed(a)
	}
	want := []string{
		"ps", "-a", "--no-trunc",
		"--filter", "label=" + runningProjectLabel + "=my-orbit",
		"--filter", "label=" + runningServiceLabel + "=orbit-app",
		"--format", "{{.ID}}\t{{json (.Label \"" + runningFolderLabel + "\")}}\t{{.State}}",
	}
	if !sameArgs(got, want) {
		t.Errorf("first docker call = %q, want %q", got, want)
	}
}

// The second query reads the image reference the container was created
// from and its version label, from the id the first one printed.
func TestRunning_InspectAsksForImageAndVersionLabel(t *testing.T) {
	id := containerID('a')
	callLog := fakeRunningDocker{
		ps:      psLine(t, id, "/srv/orbit", "running"),
		inspect: inspectLine(t, "ghcr.io/example/orbit@sha256:bbb", "v0.3.0"),
	}.install(t)

	got, err := RunningAppOf(t.Context(), runningDeployment())
	if err != nil {
		t.Fatalf("RunningAppOf: %v", err)
	}

	all := calls(t, callLog)
	if len(all) != 2 {
		t.Fatalf("docker was called %d times (%q), want a ps and an inspect", len(all), all)
	}
	args := append([]string(nil), all[1]...)
	for i, a := range args {
		args[i] = tabbed(a)
	}
	want := []string{
		"inspect",
		"--format", "{{json .Config.Image}}\t{{json (index .Config.Labels \"" + runningVersionLabel + "\")}}",
		id,
	}
	if !sameArgs(args, want) {
		t.Errorf("second docker call = %q, want %q", args, want)
	}

	if !got.Found || got.Image != "ghcr.io/example/orbit@sha256:bbb" || got.Version != "v0.3.0" ||
		got.Folder != "/srv/orbit" || got.State != "running" {
		t.Errorf("RunningApp = %+v, want the image, version label, folder and state docker printed", got)
	}
}

// A container with no version label is read as no version, not as an
// error and not as the string "null".
func TestRunning_AbsentVersionLabelIsEmpty(t *testing.T) {
	fakeRunningDocker{
		ps:      psLine(t, containerID('a'), "/srv/orbit", "exited"),
		inspect: inspectLine(t, "ghcr.io/example/orbit:1.2", ""),
	}.install(t)

	got, err := RunningAppOf(t.Context(), runningDeployment())
	if err != nil {
		t.Fatalf("RunningAppOf: %v", err)
	}
	if !got.Found || got.Version != "" || got.Image != "ghcr.io/example/orbit:1.2" || got.State != "exited" {
		t.Errorf("RunningApp = %+v", got)
	}
}

// No container is an answer, not an error: the health probe already says
// the app is not responding.
func TestRunning_NoContainerIsNotFound(t *testing.T) {
	callLog := fakeRunningDocker{}.install(t)

	got, err := RunningAppOf(t.Context(), runningDeployment())
	if err != nil {
		t.Fatalf("no container read as an error: %v", err)
	}
	if got.Found {
		t.Errorf("RunningApp = %+v, want Found false", got)
	}
	for _, c := range calls(t, callLog) {
		if len(c) > 0 && c[0] == "inspect" {
			t.Errorf("inspected %q although no container was listed", c)
		}
	}
}

// More than one app container in the project is not something to guess
// between: it is an error, and the caller shows nothing.
func TestRunning_TwoContainersIsAnError(t *testing.T) {
	fakeRunningDocker{
		ps:      psLine(t, containerID('a'), "/srv/one", "running") + psLine(t, containerID('b'), "/srv/two", "running"),
		inspect: inspectLine(t, "ghcr.io/example/orbit@sha256:bbb", "v0.3.0"),
	}.install(t)

	if _, err := RunningAppOf(t.Context(), runningDeployment()); err == nil {
		t.Error("two app containers read as an answer, want an error")
	}
}

// #277's rule for labels: a label can hold a newline, and printed raw it
// would start a line that reads like another container. The label is
// printed JSON-quoted, so it stays on its container's line.
func TestRunning_LabelWithNewlineStaysOnItsLine(t *testing.T) {
	forged := "/srv/orbit\n" + containerID('f') + "\t\"/srv/forged\"\trunning"
	first, forgedID := containerID('a'), containerID('f')
	callLog := fakeRunningDocker{
		ps:      psLine(t, first, forged, "running"),
		psRaw:   first + "\t" + forged + "\trunning\n",
		inspect: inspectLine(t, "ghcr.io/example/orbit@sha256:bbb", "v0.3.0"),
	}.install(t)

	got, err := RunningAppOf(t.Context(), runningDeployment())
	if err != nil {
		t.Fatalf("a newline in a label made RunningAppOf fail (%v): it forged a second container", err)
	}
	if !got.Found || got.Folder != forged || got.State != "running" {
		t.Errorf("RunningApp = %+v, want the one container with its label whole", got)
	}
	for _, c := range calls(t, callLog) {
		for _, a := range c {
			if a == forgedID {
				t.Errorf("a container the label forged was inspected: %q", c)
			}
		}
	}
}

// With no docker on PATH the question cannot be asked: an error, which
// the UI turns into silence.
func TestRunning_DockerMissingIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := RunningAppOf(t.Context(), runningDeployment()); err == nil {
		t.Error("no docker on PATH read as an answer, want an error")
	}
}

// Docker answering with a failure is an error too, not "no container".
func TestRunning_DockerFailureIsAnError(t *testing.T) {
	fakeRunningDocker{ps: "Cannot connect to the Docker daemon\n", psExit: 1}.install(t)

	if _, err := RunningAppOf(t.Context(), runningDeployment()); err == nil {
		t.Error("a failing docker read as an answer, want an error")
	}
}

// A nil deployment has no project to look under: nothing to ask.
func TestRunning_NilDeploymentIsNotFoundOrAnError(t *testing.T) {
	callLog := fakeRunningDocker{ps: psLine(t, containerID('a'), "/srv/orbit", "running")}.install(t)

	got, err := RunningAppOf(t.Context(), nil)
	if err == nil && got.Found {
		t.Errorf("a nil deployment found a container: %+v", got)
	}
	if n := len(calls(t, callLog)); n != 0 {
		t.Errorf("docker was called %d times for a nil deployment", n)
	}
}

// One 5 s limit covers both queries, owned here and not by the caller
// (#207): with no caller deadline, a docker that never answers still
// ends the read.
func TestRunning_SlowDockerStopsAtTheLimit(t *testing.T) {
	cases := []struct {
		name   string
		docker fakeRunningDocker
	}{
		{"ps never answers", fakeRunningDocker{psHangs: true}},
		{"inspect never answers", fakeRunningDocker{
			ps:           psLine(t, containerID('a'), "/srv/orbit", "running"),
			inspectHangs: true,
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			c.docker.install(t)

			done := make(chan error, 1)
			go func() {
				_, err := RunningAppOf(context.Background(), runningDeployment())
				done <- err
			}()
			select {
			case err := <-done:
				if err == nil {
					t.Error("a docker that never answered read as an answer, want an error")
				}
			case <-time.After(10 * time.Second):
				t.Fatal("RunningAppOf is still waiting after 10s with no caller deadline: the Docker query has no limit of its own")
			}
		})
	}
}

// --- CompareRunning ----------------------------------------------------

// a folder with a real directory in it, so symlinks and EvalSymlinks have
// something to resolve.
func realFolder(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// matching is a running container that agrees with the record in every
// way, started from dir.
func matching(d *Deployment, dir string) RunningApp {
	return RunningApp{Found: true, Image: d.Image, Version: d.Version, Folder: dir, State: "running"}
}

func TestCompare_SameFolderSameImageIsNone(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	if got := CompareRunning(d, dir, matching(d, dir)); got != (Mismatch{}) {
		t.Errorf("CompareRunning = %+v, want no mismatch", got)
	}
}

// The same folder spelled differently (a trailing slash, a dot) is the
// same folder: the cleaned paths are equal.
func TestCompare_UncleanSpellingOfTheSameFolderIsTheSame(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	for _, spelled := range []string{dir + "/", dir + "/.", filepath.Join(dir, "sub", "..") + "/"} {
		r := matching(d, spelled)
		if got := CompareRunning(d, dir, r); got != (Mismatch{}) {
			t.Errorf("label %q against folder %q: CompareRunning = %+v, want no mismatch", spelled, dir, got)
		}
	}
}

// A symlink to the folder is the folder: Docker records the path Compose
// was given, which may be either.
func TestCompare_SymlinkedFolderIsTheSame(t *testing.T) {
	dir := realFolder(t)
	link := filepath.Join(realFolder(t), "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Fatal(err)
	}
	d := runningDeployment()
	d.TargetDir = dir

	t.Run("label names the link, launcher started in the folder", func(t *testing.T) {
		if got := CompareRunning(d, dir, matching(d, link)); got != (Mismatch{}) {
			t.Errorf("CompareRunning = %+v, want no mismatch", got)
		}
	})
	t.Run("label names the folder, launcher started through the link", func(t *testing.T) {
		if got := CompareRunning(d, link, matching(d, dir)); got != (Mismatch{}) {
			t.Errorf("CompareRunning = %+v, want no mismatch", got)
		}
	})
}

// A folder that does not exist cannot be shown to be this one: it is
// another folder.
func TestCompare_MissingOtherFolderIsAnotherFolder(t *testing.T) {
	dir := realFolder(t)
	gone := filepath.Join(realFolder(t), "removed-since")
	d := runningDeployment()
	d.TargetDir = dir

	want := Mismatch{Kind: MismatchFolder, Folder: gone}
	if got := CompareRunning(d, dir, matching(d, gone)); got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}
}

// Another folder's build is not compared with this record: the folder
// wins, and says nothing about versions.
func TestCompare_OtherFolderWins(t *testing.T) {
	dir := realFolder(t)
	other := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	r := RunningApp{Found: true, Image: "ghcr.io/example/orbit@sha256:zzz", Version: "v0.1.0", Folder: other, State: "running"}
	want := Mismatch{Kind: MismatchFolder, Folder: other}
	if got := CompareRunning(d, dir, r); got != want {
		t.Errorf("CompareRunning = %+v, want %+v (the folder only)", got, want)
	}
}

// A container with no working_dir label is judged by its build alone: it
// agrees, or it does not.
func TestCompare_NoFolderLabelFallsToBuild(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	t.Run("agrees", func(t *testing.T) {
		if got := CompareRunning(d, dir, matching(d, "")); got != (Mismatch{}) {
			t.Errorf("CompareRunning = %+v, want no mismatch", got)
		}
	})
	t.Run("differs", func(t *testing.T) {
		r := matching(d, "")
		r.Image = "ghcr.io/example/orbit@sha256:bbb"
		r.Version = "v0.3.0"
		want := Mismatch{Kind: MismatchBuild, Started: "v0.3.0", Recorded: "v1.2.0"}
		if got := CompareRunning(d, dir, r); got != want {
			t.Errorf("CompareRunning = %+v, want %+v", got, want)
		}
	})
}

// The exact comparison the installer's ownership proof makes: the image
// the container was created from against ORBIT_IMAGE. Started is the
// image's version label, Recorded the applied version.
func TestCompare_ImageDiffersIsBuild(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir
	r := matching(d, dir)
	r.Image = "ghcr.io/example/orbit@sha256:bbb"
	r.Version = "v0.3.0"

	want := Mismatch{Kind: MismatchBuild, Started: "v0.3.0", Recorded: "v1.2.0"}
	if got := CompareRunning(d, dir, r); got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}
}

// The same image with a different version label on each side is a
// different build too.
func TestCompare_SameImageOtherVersionLabelIsBuild(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir
	r := matching(d, dir)
	r.Version = "v0.3.0"

	want := Mismatch{Kind: MismatchBuild, Started: "v0.3.0", Recorded: "v1.2.0"}
	if got := CompareRunning(d, dir, r); got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}
}

// A version is compared only when both sides have one: a label the image
// does not carry, or an applied version the record does not hold, is not
// a difference.
func TestCompare_VersionOnOneSideOnlyIsNone(t *testing.T) {
	dir := realFolder(t)

	t.Run("no label on the container", func(t *testing.T) {
		d := runningDeployment()
		d.TargetDir = dir
		r := matching(d, dir)
		r.Version = ""
		if got := CompareRunning(d, dir, r); got != (Mismatch{}) {
			t.Errorf("CompareRunning = %+v, want no mismatch", got)
		}
	})
	t.Run("no applied version in the record", func(t *testing.T) {
		d := runningDeployment()
		d.TargetDir = dir
		d.Version = ""
		r := matching(d, dir)
		r.Version = "v0.3.0"
		if got := CompareRunning(d, dir, r); got != (Mismatch{}) {
			t.Errorf("CompareRunning = %+v, want no mismatch", got)
		}
	})
}

// With no version label to name the started build, the reference it was
// created from does, without the digest pin (as Update's confirm screen
// shows an image). A record with no version leaves Recorded empty.
func TestCompare_NoVersionLabelStartsTheReferenceWithoutDigest(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir
	r := matching(d, dir)
	r.Image = "ghcr.io/example/orbit@sha256:bbb"
	r.Version = ""

	want := Mismatch{Kind: MismatchBuild, Started: "ghcr.io/example/orbit", Recorded: "v1.2.0"}
	if got := CompareRunning(d, dir, r); got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}

	d.Version = ""
	want.Recorded = ""
	if got := CompareRunning(d, dir, r); got != want {
		t.Errorf("with no applied version: CompareRunning = %+v, want %+v", got, want)
	}
}

// A stopped container is still the one the installer would compare, so
// it is compared as a running one is.
func TestCompare_ExitedContainerIsStillCompared(t *testing.T) {
	dir := realFolder(t)
	other := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	t.Run("other folder", func(t *testing.T) {
		r := matching(d, other)
		r.State = "exited"
		want := Mismatch{Kind: MismatchFolder, Folder: other}
		if got := CompareRunning(d, dir, r); got != want {
			t.Errorf("CompareRunning = %+v, want %+v", got, want)
		}
	})
	t.Run("other build", func(t *testing.T) {
		r := matching(d, dir)
		r.State = "exited"
		r.Image = "ghcr.io/example/orbit@sha256:bbb"
		r.Version = "v0.3.0"
		want := Mismatch{Kind: MismatchBuild, Started: "v0.3.0", Recorded: "v1.2.0"}
		if got := CompareRunning(d, dir, r); got != want {
			t.Errorf("CompareRunning = %+v, want %+v", got, want)
		}
	})
	t.Run("agrees", func(t *testing.T) {
		r := matching(d, dir)
		r.State = "exited"
		if got := CompareRunning(d, dir, r); got != (Mismatch{}) {
			t.Errorf("CompareRunning = %+v, want no mismatch", got)
		}
	})
}

// The zero Mismatch is "nothing to say", so a caller that forgets to set
// a kind says nothing.
func TestCompare_NoMismatchIsTheZeroValue(t *testing.T) {
	if MismatchNone != 0 {
		t.Errorf("MismatchNone = %d, want the zero value", MismatchNone)
	}
	if MismatchFolder == MismatchNone || MismatchBuild == MismatchNone || MismatchFolder == MismatchBuild {
		t.Errorf("the three kinds are not distinct: %d %d %d", MismatchNone, MismatchFolder, MismatchBuild)
	}
}

// The switch (9b) has its own name, spelled once in deploy's block: it
// is not the volume check's or the health probe's.
func TestNoRunningCheckEnv_HasItsOwnName(t *testing.T) {
	if !strings.HasPrefix(NoRunningCheckEnv, launcherEnvPrefix) || !strings.HasSuffix(NoRunningCheckEnv, "NO_RUNNING_CHECK") {
		t.Errorf("NoRunningCheckEnv = %q, want a launcher variable ending NO_RUNNING_CHECK", NoRunningCheckEnv)
	}
	for _, other := range []string{NoVolumeCheckEnv, NoHealthProbeEnv, NoUpdateCheckEnv, NoAnimationEnv} {
		if NoRunningCheckEnv == other {
			t.Errorf("NoRunningCheckEnv shares the name %q with another switch", other)
		}
	}
}
