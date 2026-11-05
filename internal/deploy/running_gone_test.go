package deploy

// #334: when the folder an Orbit was started from no longer exists,
// Mismatch says so (FolderGone), and says whether the build in this
// folder's record is the running one (BuildDiffers), so the launcher can
// offer to carry on from this folder. Written from the design note (issue
// #334, "Gone versus elsewhere" and "Build brief") before the code. The
// owner decided the gone test is strict (2026-11-03): a folder that
// exists is never gone, whatever it holds.

import (
	"os"
	"path/filepath"
	"testing"
)

const otherBuildImage = "ghcr.io/example/orbit@sha256:bbb"

// A recorded folder that is not there, with the same build running, is
// gone and the build does not differ.
func TestCompare_MissingOtherFolderIsGone(t *testing.T) {
	dir := realFolder(t)
	gone := filepath.Join(realFolder(t), "removed-since")
	d := runningDeployment()
	d.TargetDir = dir

	got := CompareRunning(d, dir, matching(d, gone))

	want := Mismatch{Kind: MismatchFolder, Folder: gone, FolderGone: true}
	if got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}
	if got.BuildDiffers {
		t.Error("BuildDiffers is true for the same build")
	}
}

// Gone with another build: the folder still wins Kind, and the build
// comparison's Started and Recorded are filled with BuildDiffers.
func TestCompare_GoneFolderWithAnotherBuildFillsTheBuild(t *testing.T) {
	dir := realFolder(t)
	gone := filepath.Join(realFolder(t), "removed-since")
	d := runningDeployment()
	d.TargetDir = dir
	r := matching(d, gone)
	r.Image = otherBuildImage
	r.Version = "v0.3.0"

	want := Mismatch{Kind: MismatchFolder, Folder: gone, FolderGone: true, BuildDiffers: true, Started: "v0.3.0", Recorded: "v1.2.0"}
	if got := CompareRunning(d, dir, r); got != want {
		t.Errorf("CompareRunning = %+v, want %+v", got, want)
	}
}

// Same image, another version label, is a different build too.
func TestCompare_GoneFolderWithAnotherVersionLabelDiffers(t *testing.T) {
	dir := realFolder(t)
	gone := filepath.Join(realFolder(t), "removed-since")
	d := runningDeployment()
	d.TargetDir = dir
	r := matching(d, gone)
	r.Version = "v0.3.0"

	got := CompareRunning(d, dir, r)
	if got.Kind != MismatchFolder || !got.FolderGone || !got.BuildDiffers {
		t.Errorf("CompareRunning = %+v, want a gone folder whose build differs", got)
	}
}

// A folder that exists is elsewhere, not gone, even when it holds no
// .env-orbit (owner, 2026-11-03: strict gone test).
func TestCompare_ExistingOtherFolderIsNotGone(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	t.Run("empty folder, no .env-orbit", func(t *testing.T) {
		other := realFolder(t)
		want := Mismatch{Kind: MismatchFolder, Folder: other}
		if got := CompareRunning(d, dir, matching(d, other)); got != want {
			t.Errorf("CompareRunning = %+v, want %+v", got, want)
		}
	})
	t.Run("folder with other files, no .env-orbit", func(t *testing.T) {
		other := realFolder(t)
		if err := os.WriteFile(filepath.Join(other, "docker-compose.yml"), []byte("name: orbit\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := CompareRunning(d, dir, matching(d, other)); got.FolderGone {
			t.Errorf("CompareRunning = %+v, want the folder not gone", got)
		}
	})
	t.Run("folder with .env-orbit", func(t *testing.T) {
		other := realFolder(t)
		if err := os.WriteFile(filepath.Join(other, EnvFile), []byte("X=1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := CompareRunning(d, dir, matching(d, other)); got.FolderGone {
			t.Errorf("CompareRunning = %+v, want the folder not gone", got)
		}
	})
}

// A relative label cannot be shown to be gone: fail closed.
func TestCompare_RelativeLabelIsNotGone(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir

	got := CompareRunning(d, dir, matching(d, "no-such-relative-folder-334"))

	if got.Kind != MismatchFolder || got.FolderGone {
		t.Errorf("CompareRunning = %+v, want another folder, not gone", got)
	}
}

// A file at the recorded path is not gone.
func TestCompare_FileAtTheRecordedPathIsNotGone(t *testing.T) {
	dir := realFolder(t)
	file := filepath.Join(realFolder(t), "orbit")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	d := runningDeployment()
	d.TargetDir = dir

	got := CompareRunning(d, dir, matching(d, file))

	if got.Kind != MismatchFolder || got.FolderGone {
		t.Errorf("CompareRunning = %+v, want another folder, not gone", got)
	}
}

// A path below a file does not exist (ENOTDIR): gone. A dangling symlink
// is gone too, since Stat follows it.
func TestCompare_NotADirectoryAndDanglingSymlinkAreGone(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir
	base := realFolder(t)
	file := filepath.Join(base, "afile")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	dangling := filepath.Join(base, "dangling")
	if err := os.Symlink(filepath.Join(base, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}

	for name, label := range map[string]string{"below a file": filepath.Join(file, "orbit"), "dangling symlink": dangling} {
		t.Run(name, func(t *testing.T) {
			got := CompareRunning(d, dir, matching(d, label))
			if got.Kind != MismatchFolder || !got.FolderGone {
				t.Errorf("CompareRunning = %+v, want a gone folder", got)
			}
		})
	}
}

// A Stat that fails any other way (permission) is not gone.
func TestCompare_UnreadableParentIsNotGone(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read any parent")
	}
	dir := realFolder(t)
	parent := realFolder(t)
	if err := os.Mkdir(filepath.Join(parent, "inner"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(parent, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })
	d := runningDeployment()
	d.TargetDir = dir

	got := CompareRunning(d, dir, matching(d, filepath.Join(parent, "inner")))

	if got.Kind != MismatchFolder || got.FolderGone {
		t.Errorf("CompareRunning = %+v, want another folder, not gone", got)
	}
}

// The same folder, or no mismatch at all, is never gone.
func TestCompare_NoMismatchIsNotGone(t *testing.T) {
	dir := realFolder(t)
	d := runningDeployment()
	d.TargetDir = dir
	if got := CompareRunning(d, dir, matching(d, dir)); got != (Mismatch{}) {
		t.Errorf("CompareRunning = %+v, want no mismatch", got)
	}
}
