package deploy

// Test helpers shared by this package's tests (#219, EN-17): the fake
// dockers, all put on PATH by testutil.FakeExecutable, and the one
// scripts/ tree builder, testutil.StageTree under this package's name.

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

// fakeDockerPrinting puts a docker on PATH that ignores its arguments and
// prints stdout verbatim, so the parser is exercised against the exact
// shape `docker volume ls --format` really emits.
func fakeDockerPrinting(t *testing.T, stdout string, exitCode int) {
	t.Helper()
	testutil.FakeExecutable(t, "docker", fmt.Sprintf("#!/usr/bin/env bash\nprintf '%%b' %q\nexit %d\n", stdout, exitCode))
}

// fakeDockerLogging puts a docker on PATH that appends each call's
// arguments, one line per call, to the file it returns.
func fakeDockerLogging(t *testing.T) (callLog string) {
	t.Helper()
	callLog = filepath.Join(t.TempDir(), "calls.log")
	testutil.FakeExecutable(t, "docker", "#!/usr/bin/env bash\necho \"$@\" >> "+callLog+"\n")
	return callLog
}

// hangingDockerOnPath puts a docker on PATH that never answers, and
// kills every one it started when the test ends. It returns the file each
// started docker records its pid in.
func hangingDockerOnPath(t *testing.T) string {
	t.Helper()
	pidFile := filepath.Join(t.TempDir(), "pids")
	testutil.FakeExecutable(t, "docker", "#!/usr/bin/env bash\necho $$ >> '"+pidFile+"'\nexec sleep 30\n")
	killRecordedOnCleanup(t, pidFile)
	return pidFile
}

// scriptTree is testutil.StageTree: dir/scripts at scriptsMode, then
// files at their own modes, every mode set exactly so each test states
// the modes it runs against and the umask decides none.
func scriptTree(t *testing.T, dir string, scriptsMode os.FileMode, files ...testutil.File) {
	t.Helper()
	testutil.StageTree(t, dir, scriptsMode, files...)
}
