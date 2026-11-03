package deploy

// #321: OtherProjectAppOf asks Docker for Orbit app containers under any
// Compose project but the one Install would use, and OtherFolder says
// where one was started from. Written from the design note (issue note
// 47112, sections 2 and 5) before the code.
//
// The answer is one `docker ps -a` line per container: its id, the
// project label and the working_dir label (both JSON-quoted, #277) and
// its state, tab-separated. Lines under the asked-about project are the
// same-project clash's (#318) and are dropped.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const otherProductionFolder = "/srv/orbit-deployments/production"

// otherPsLine is one line of the `docker ps` answer the design asks for:
// id, project and folder labels JSON-quoted, and the state.
func otherPsLine(t *testing.T, id, project, folder, state string) string {
	t.Helper()
	return id + "\t" + jsonQuoted(t, project) + "\t" + jsonQuoted(t, folder) + "\t" + state + "\n"
}

func TestOtherProject_PsFiltersByServiceLabelAndAnyProject(t *testing.T) {
	callLog := fakeDockerArgs(t)

	if _, err := OtherProjectAppOf(t.Context(), "orbit"); err != nil {
		t.Fatalf("OtherProjectAppOf: %v", err)
	}

	got := recordedArgs(t, callLog) // fails on a second call: one Docker call, no inspect
	for i, a := range got {
		got[i] = tabbed(a)
	}
	want := []string{
		"ps", "-a", "--no-trunc",
		"--filter", "label=" + runningServiceLabel + "=orbit-app",
		"--filter", "label=" + runningProjectLabel,
		"--format", "{{.ID}}\t{{json (.Label \"" + runningProjectLabel + "\")}}\t{{json (.Label \"" + runningFolderLabel + "\")}}\t{{.State}}",
	}
	if !sameArgs(got, want) {
		t.Errorf("docker args = %q, want %q", got, want)
	}
	for _, a := range got {
		if strings.HasPrefix(a, "label="+runningProjectLabel+"=") {
			t.Errorf("the query filters on a project value (%q): Docker has no not-equal filter, so it must ask for every project and drop this one's", a)
		}
		if a == "inspect" {
			t.Errorf("the query inspected a container: the screen shows no image or version")
		}
	}
}

func TestOtherProject_ThisProjectsContainerIsNotOther(t *testing.T) {
	callLog := fakeRunningDocker{
		ps: otherPsLine(t, containerID('a'), "orbit", "/opt/orbit", "running"),
	}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "orbit")

	if err != nil {
		t.Fatalf("the project's own container read as an error: %v", err)
	}
	if got.Found {
		t.Errorf("OtherProjectApp = %+v, want Found false: this project's container is the same-project clash's", got)
	}
	for _, c := range calls(t, callLog) {
		if len(c) > 0 && c[0] == "inspect" {
			t.Errorf("inspected %q", c)
		}
	}
}

func TestOtherProject_OneOtherProjectIsFound(t *testing.T) {
	fakeRunningDocker{
		ps: otherPsLine(t, containerID('a'), "orbit", "/opt/orbit", "running") +
			otherPsLine(t, containerID('b'), "production", otherProductionFolder, "running"),
	}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "orbit")
	if err != nil {
		t.Fatalf("OtherProjectAppOf: %v", err)
	}

	want := OtherProjectApp{Found: true, Project: "production", Folder: otherProductionFolder, State: "running"}
	if got != want {
		t.Errorf("OtherProjectApp = %+v, want %+v", got, want)
	}
}

func TestOtherProject_NoContainerIsNotFound(t *testing.T) {
	fakeRunningDocker{}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "orbit")

	if err != nil {
		t.Fatalf("no container read as an error: %v", err)
	}
	if got.Found {
		t.Errorf("OtherProjectApp = %+v, want Found false", got)
	}
}

// Two Orbit stacks under two other projects cannot both hold the fixed
// container names, so one is not an Orbit as shipped and the launcher
// cannot say which one Compose will trip over: an error, not a guess.
func TestOtherProject_TwoOtherProjectsIsAnError(t *testing.T) {
	for _, c := range []struct{ name, ps string }{
		{"two projects", otherPsLine(t, containerID('a'), "production", "/srv/one", "running") +
			otherPsLine(t, containerID('b'), "staging", "/srv/two", "running")},
		{"one project twice", otherPsLine(t, containerID('a'), "production", "/srv/one", "running") +
			otherPsLine(t, containerID('b'), "production", "/srv/two", "exited")},
		{"two and this project's", otherPsLine(t, containerID('c'), "orbit", "/opt/orbit", "running") +
			otherPsLine(t, containerID('a'), "production", "/srv/one", "running") +
			otherPsLine(t, containerID('b'), "staging", "/srv/two", "running")},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeRunningDocker{ps: c.ps}.install(t)

			got, err := OtherProjectAppOf(t.Context(), "orbit")

			if err == nil {
				t.Errorf("OtherProjectAppOf = %+v, nil, want an error", got)
			}
		})
	}
}

// A stopped container holds its name just the same.
func TestOtherProject_StoppedContainerCounts(t *testing.T) {
	fakeRunningDocker{
		ps: otherPsLine(t, containerID('a'), "production", otherProductionFolder, "exited"),
	}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "orbit")
	if err != nil {
		t.Fatalf("OtherProjectAppOf: %v", err)
	}
	if !got.Found || got.State != "exited" || got.Project != "production" {
		t.Errorf("OtherProjectApp = %+v, want the exited container found", got)
	}
}

// #277's rule for labels: a label can hold a newline, and printed raw it
// would start a line that reads like another container. Both labels are
// printed JSON-quoted, so each stays on its container's line.
func TestOtherProject_LabelWithNewlineStaysOnItsLine(t *testing.T) {
	forgedTail := containerID('f') + "\t\"staging\"\t\"/srv/forged\"\trunning"

	t.Run("folder label", func(t *testing.T) {
		forged := "/srv/orbit\n" + forgedTail
		fakeRunningDocker{
			ps:    otherPsLine(t, containerID('a'), "production", forged, "running"),
			psRaw: containerID('a') + "\tproduction\t" + forged + "\n",
		}.install(t)

		got, err := OtherProjectAppOf(t.Context(), "orbit")

		if err != nil {
			t.Fatalf("a newline in a label made OtherProjectAppOf fail (%v): it forged a second container", err)
		}
		if !got.Found || got.Project != "production" || got.Folder != forged || got.State != "running" {
			t.Errorf("OtherProjectApp = %+v, want the one container with its folder label whole", got)
		}
	})
	t.Run("project label", func(t *testing.T) {
		forged := "prod\n" + forgedTail
		fakeRunningDocker{
			ps:    otherPsLine(t, containerID('a'), forged, otherProductionFolder, "running"),
			psRaw: containerID('a') + "\t" + forged + "\t" + otherProductionFolder + "\trunning\n",
		}.install(t)

		got, err := OtherProjectAppOf(t.Context(), "orbit")

		if err != nil {
			t.Fatalf("a newline in a label made OtherProjectAppOf fail (%v): it forged a second container", err)
		}
		if !got.Found || got.Project != forged || got.Folder != otherProductionFolder {
			t.Errorf("OtherProjectApp = %+v, want the one container with its project label whole", got)
		}
	})
}

// A container with no working_dir label prints it as "" and reads as no
// folder, not as an error.
func TestOtherProject_NoFolderLabelIsEmpty(t *testing.T) {
	fakeRunningDocker{
		ps: otherPsLine(t, containerID('a'), "production", "", "running"),
	}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "orbit")
	if err != nil {
		t.Fatalf("OtherProjectAppOf: %v", err)
	}
	if !got.Found || got.Folder != "" || got.Project != "production" {
		t.Errorf("OtherProjectApp = %+v, want Found with project production and no folder", got)
	}
}

func TestOtherProject_UnreadableLineIsAnError(t *testing.T) {
	good := otherPsLine(t, containerID('a'), "production", otherProductionFolder, "running")
	for _, c := range []struct{ name, ps string }{
		{"not a listing", "Cannot connect to the Docker daemon\n"},
		{"too few fields", containerID('a') + "\t\"production\"\trunning\n"},
		{"too many fields", containerID('a') + "\t\"production\"\t\"/srv/a\"\trunning\textra\n"},
		{"id is not hex", "not-an-id\t\"production\"\t\"/srv/a\"\trunning\n"},
		{"one unreadable line among good ones", good + "garbage\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			fakeRunningDocker{ps: c.ps}.install(t)

			got, err := OtherProjectAppOf(t.Context(), "orbit")

			if err == nil {
				t.Errorf("OtherProjectApp = %+v, nil, want an error", got)
			}
		})
	}
}

// A container with no project label cannot come back from the label
// existence filter, so an empty one is output the launcher cannot trust.
func TestOtherProject_EmptyProjectLabelIsAnError(t *testing.T) {
	fakeRunningDocker{
		ps: otherPsLine(t, containerID('a'), "", otherProductionFolder, "running"),
	}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "orbit")

	if err == nil {
		t.Errorf("OtherProjectApp = %+v, nil, want an error", got)
	}
}

// No project to be other than: nothing to ask.
func TestOtherProject_EmptyProjectArgumentIsAnError(t *testing.T) {
	callLog := fakeRunningDocker{
		ps: otherPsLine(t, containerID('a'), "production", otherProductionFolder, "running"),
	}.install(t)

	got, err := OtherProjectAppOf(t.Context(), "")

	if err == nil {
		t.Errorf("OtherProjectApp = %+v, nil, want an error", got)
	}
	if n := len(calls(t, callLog)); n != 0 {
		t.Errorf("docker was called %d times for an empty project", n)
	}
}

// With no docker on PATH the question cannot be asked: an error, which
// the UI turns into silence.
func TestOtherProject_DockerMissingIsAnError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	if _, err := OtherProjectAppOf(t.Context(), "orbit"); err == nil {
		t.Error("no docker on PATH read as an answer, want an error")
	}
}

// Docker answering with a failure is an error too, not "no container".
func TestOtherProject_DockerFailureIsAnError(t *testing.T) {
	fakeRunningDocker{ps: "Cannot connect to the Docker daemon\n", psExit: 1}.install(t)

	if _, err := OtherProjectAppOf(t.Context(), "orbit"); err == nil {
		t.Error("a failing docker read as an answer, want an error")
	}
}

// The limit is owned here and not by the caller (#207): with no caller
// deadline, a docker that never answers still ends the read.
func TestOtherProject_SlowDockerStopsAtTheLimit(t *testing.T) {
	fakeRunningDocker{psHangs: true}.install(t)

	done := make(chan error, 1)
	go func() {
		_, err := OtherProjectAppOf(context.Background(), "orbit")
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Error("a docker that never answered read as an answer, want an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("OtherProjectAppOf is still waiting after 10s with no caller deadline: the Docker query has no limit of its own")
	}
}

// --- OtherFolder -------------------------------------------------------

func TestOtherProject_OtherFolderIsTheContainersFolder(t *testing.T) {
	dir, other := realFolder(t), realFolder(t)

	for _, c := range []struct{ name, folder string }{
		{"another folder that exists", other},
		{"another folder that is gone", otherProductionFolder},
	} {
		t.Run(c.name, func(t *testing.T) {
			app := OtherProjectApp{Found: true, Project: "production", Folder: c.folder, State: "running"}

			if got := app.OtherFolder(dir); got != c.folder {
				t.Errorf("OtherFolder = %q, want %q", got, c.folder)
			}
		})
	}
}

// Started from this very folder under another project name (a record
// edited or replaced after the stack started): "that folder" would point
// here, so there is no folder to name.
func TestOtherProject_SameFolderGivesNoFolder(t *testing.T) {
	dir := realFolder(t)
	for _, folder := range []string{dir, dir + "/", filepath.Join(dir, "sub", "..")} {
		app := OtherProjectApp{Found: true, Project: "production", Folder: folder, State: "running"}

		if got := app.OtherFolder(dir); got != "" {
			t.Errorf("OtherFolder(%q) with Folder %q = %q, want none: it is this folder", dir, folder, got)
		}
	}
}

// Docker records the path Compose was given, which may be the folder or
// a link to it, whichever the launcher was started through.
func TestOtherProject_SymlinkedFolderGivesNoFolder(t *testing.T) {
	target := realFolder(t)
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	t.Run("the label names the link", func(t *testing.T) {
		app := OtherProjectApp{Found: true, Project: "production", Folder: link}
		if got := app.OtherFolder(target); got != "" {
			t.Errorf("OtherFolder = %q, want none", got)
		}
	})
	t.Run("this folder is the link", func(t *testing.T) {
		app := OtherProjectApp{Found: true, Project: "production", Folder: target}
		if got := app.OtherFolder(link); got != "" {
			t.Errorf("OtherFolder = %q, want none", got)
		}
	})
}

func TestOtherProject_NoLabelGivesNoFolder(t *testing.T) {
	app := OtherProjectApp{Found: true, Project: "production", State: "running"}

	if got := app.OtherFolder(realFolder(t)); got != "" {
		t.Errorf("OtherFolder = %q, want none: the container has no folder label", got)
	}
}
