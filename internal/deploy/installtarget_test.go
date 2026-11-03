package deploy

// #318: the project Install would use, read before anything is written.
// Written from the design note (issue note 46957, sections 2 and 5)
// before the code.
//
// InstallTarget mirrors the order Orbit's installer derives a fresh
// install's Compose project name in: COMPOSE_PROJECT_NAME in an existing
// .env-orbit, else the same name in the shell's environment (when it is
// one the installer accepts), else the compose file's own name, "orbit".

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// envProject sets the shell's COMPOSE_PROJECT_NAME for the test; an
// empty name is the variable not being set to anything usable.
func envProject(t *testing.T, name string) {
	t.Helper()
	t.Setenv("COMPOSE_PROJECT_NAME", name)
}

func TestInstallTarget_DormantFolderUsesTheDefaultProject(t *testing.T) {
	envProject(t, "")
	dir := t.TempDir()

	d, err := InstallTarget(dir)
	if err != nil {
		t.Fatalf("InstallTarget: %v", err)
	}
	if d == nil {
		t.Fatal("InstallTarget = nil, want a deployment naming the project an install would use")
	}
	if d.Project != "orbit" || d.TargetDir != dir {
		t.Errorf("Project, TargetDir = %q, %q, want %q, %q", d.Project, d.TargetDir, "orbit", dir)
	}
	if d.Image != "" || d.Version != "" {
		t.Errorf("a dormant folder has no build: Image = %q, Version = %q, want neither", d.Image, d.Version)
	}
}

func TestInstallTarget_RecordNamesTheProject(t *testing.T) {
	envProject(t, "")
	for _, withCompose := range []bool{false, true} {
		name := "pre-provisioned"
		if withCompose {
			name = "installed"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile), "COMPOSE_PROJECT_NAME=production")
			if withCompose {
				testutil.WriteComposeFile(t, filepath.Join(dir, ComposeFile))
			}

			d, err := InstallTarget(dir)
			if err != nil {
				t.Fatalf("InstallTarget: %v", err)
			}
			if d == nil || d.Project != "production" || d.TargetDir != dir {
				t.Errorf("InstallTarget = %+v, want Project production in %s", d, dir)
			}
		})
	}
}

// With no project name in the record, the shell's stands in, as the
// installer forwards it to the engine; a name in the record still wins.
func TestInstallTarget_ShellProjectNameStandsInWhenTheRecordHasNone(t *testing.T) {
	envProject(t, "mail")

	t.Run("dormant folder", func(t *testing.T) {
		d, err := InstallTarget(t.TempDir())
		if err != nil || d == nil || d.Project != "mail" {
			t.Errorf("InstallTarget = %+v, %v, want Project mail", d, err)
		}
	})
	t.Run("record without a project name", func(t *testing.T) {
		dir := t.TempDir()
		testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile))
		d, err := InstallTarget(dir)
		if err != nil || d == nil || d.Project != "mail" {
			t.Errorf("InstallTarget = %+v, %v, want Project mail", d, err)
		}
		if d != nil && d.Image != "ghcr.io/tomlawesome/orbit@sha256:abc" {
			t.Errorf("Image = %q, want the record's: an existing record is read as Detect reads it", d.Image)
		}
	})
	t.Run("record naming another project", func(t *testing.T) {
		dir := t.TempDir()
		testutil.WriteEnvOrbit(t, filepath.Join(dir, EnvFile), "COMPOSE_PROJECT_NAME=production")
		d, err := InstallTarget(dir)
		if err != nil || d == nil || d.Project != "production" {
			t.Errorf("InstallTarget = %+v, %v, want Project production", d, err)
		}
	})
}

// A name the installer would refuse is ignored: the installer says so in
// its own words.
func TestInstallTarget_UnsafeShellProjectNameIsIgnored(t *testing.T) {
	for _, bad := range []string{"Bad Name!", "UPPER", "-leading-dash", "_leading", "has.dot", "a/b"} {
		t.Run(bad, func(t *testing.T) {
			envProject(t, bad)
			d, err := InstallTarget(t.TempDir())
			if err != nil || d == nil || d.Project != "orbit" {
				t.Errorf("InstallTarget = %+v, %v, want the default project orbit", d, err)
			}
		})
	}
}

// A record that cannot be read is an error, not a guess: the check does
// not run.
func TestInstallTarget_UnreadableRecordIsAnError(t *testing.T) {
	envProject(t, "")
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, EnvFile), 0o700); err != nil {
		t.Fatal(err)
	}

	d, err := InstallTarget(dir)
	if err == nil {
		t.Fatalf("InstallTarget = %+v, nil, want an error for a record that is a directory", d)
	}
	if d != nil {
		t.Errorf("InstallTarget returned %+v beside its error, want nil", d)
	}
}

// --- CompareRunning for a target that carries only a project -----------

// A dormant or pre-provisioned target has a project and nothing else. A
// container started from another folder is still another folder.
func TestCompare_ProjectOnlyRecordFindsAnotherFolder(t *testing.T) {
	dir, other := realFolder(t), realFolder(t)
	target := &Deployment{TargetDir: dir, Project: "orbit"}
	app := RunningApp{Found: true, Image: "ghcr.io/example/orbit@sha256:bbb", Version: "v9.9.9", Folder: other, State: "running"}

	want := Mismatch{Kind: MismatchFolder, Folder: other}
	if got := CompareRunning(target, dir, app); got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}
}

// Without an image or version in the record there is nothing for a build
// to differ from, so a project-only target never finds one.
func TestCompare_ProjectOnlyRecordNeverFindsABuild(t *testing.T) {
	dir := realFolder(t)
	target := &Deployment{TargetDir: dir, Project: "orbit"}
	for _, app := range []RunningApp{
		{Found: true, Image: "ghcr.io/example/orbit@sha256:bbb", Version: "v9.9.9", Folder: dir, State: "running"},
		{Found: true, Image: "other/image:1", Folder: dir, State: "exited"},
		{Found: true, Version: "v0.0.1", Folder: dir, State: "running"},
	} {
		if got := CompareRunning(target, dir, app); got.Kind != MismatchNone {
			t.Errorf("CompareRunning(%+v) = %+v, want no mismatch", app, got)
		}
	}
}
