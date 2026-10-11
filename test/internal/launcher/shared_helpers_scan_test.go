package launcher

// #219 (WI-15), SU-20: helpers that two or more packages' tests repeat move
// to internal/testutil, and the pty fake engines share their text. These
// tests read source because neither exists yet. Counts are of places that
// define a helper, so a caller using the shared one is never penalised.

import (
	"go/ast"
	"strings"
	"testing"
)

const testutilDir = "internal/testutil"

func testutilSources(t *testing.T) []goFile { return loadDir(t, testutilDir, false) }

// SU-20: the package exists, offers a helper per repeated chore, and can be
// imported by deploy's and ui's own (in-package) tests.
func TestTestutil_PackageExistsAndCanBeImportedByProductTests(t *testing.T) {
	files := testutilSources(t)
	if len(files) == 0 {
		t.Fatalf("%s has no non-test Go files: the shared test helpers do not exist", testutilDir)
	}
	for _, gf := range files {
		if name := gf.file.Name.Name; name != "testutil" {
			t.Errorf("%s declares package %q, want testutil", gf.rel, name)
		}
	}
	// One exported helper for each chore SU-20 lists: a fake executable on
	// PATH, a counting install.sh server, the .env-orbit fixture and the
	// configure-tree stager.
	if got := exportedFuncs(files); len(got) < 4 {
		t.Errorf("%s exports %d function(s) %v, want at least 4 (fake executable, counting server, env fixture, tree stager)",
			testutilDir, len(got), got)
	}
	// deploy's tests are package deploy; a helper package that imported
	// deploy (or ui, which imports it) would make them an import cycle.
	for _, path := range importsOf(files) {
		for _, banned := range []string{"/internal/deploy", "/internal/ui"} {
			if strings.HasSuffix(path, banned) {
				t.Errorf("%s imports %s: deploy's and ui's own tests could not import testutil without a cycle", testutilDir, path)
			}
		}
	}
}

// SU-20 and EN-17: one fake-executable-on-PATH implementation. deploy may
// keep one thin writer of its own (EN-17 names deploy/helpers_test.go),
// testutil may hold the shared one; ui and every other deploy test use them.
func TestFakeExecutable_OnePathPrependNotSixCopies(t *testing.T) {
	deploy := loadDir(t, "internal/deploy", true)
	ui := loadDir(t, "internal/ui", true)
	util := testutilSources(t)

	nDeploy, nUI, nUtil := countCalls(deploy, isPathPrepend), countCalls(ui, isPathPrepend), countCalls(util, isPathPrepend)
	if nDeploy > 1 {
		t.Errorf("internal/deploy tests prepend to PATH in %d places, want at most 1", nDeploy)
	}
	if nUI != 0 {
		t.Errorf("internal/ui tests prepend to PATH in %d place(s): ui's fake docker should come from testutil", nUI)
	}
	if nUtil > 1 {
		t.Errorf("%s prepends to PATH in %d places, want at most 1", testutilDir, nUtil)
	}
	if nDeploy+nUI+nUtil == 0 {
		t.Error("no helper puts a fake executable on PATH at all")
	}
	if nDeploy+nUI+nUtil > 2 {
		t.Errorf("%d PATH-prepending helpers across deploy, ui and testutil, want at most 2", nDeploy+nUI+nUtil)
	}
}

// The same for the writer of the fake docker file itself.
func TestFakeExecutable_OneFakeDockerWriter(t *testing.T) {
	deploy := funcDecls(loadDir(t, "internal/deploy", true), writesFakeDocker)
	ui := funcDecls(loadDir(t, "internal/ui", true), writesFakeDocker)
	if len(deploy) > 1 {
		t.Errorf("internal/deploy tests write a fake docker in %d places %v, want at most 1", len(deploy), deploy)
	}
	if len(ui) != 0 {
		t.Errorf("internal/ui tests write their own fake docker: %v", ui)
	}
}

// SU-20: the install.sh server that counts requests was typed in deploy
// (noScriptSource) and in ui (countingScriptSource) with slightly different
// bodies; it now lives once, in testutil.
func TestCountingScriptServer_LivesOnlyInTestutil(t *testing.T) {
	if got := funcDecls(loadDir(t, "internal/deploy", true), runsCountingScriptServer); len(got) != 0 {
		t.Errorf("deploy tests still define a counting script server: %v", got)
	}
	if got := funcDecls(loadDir(t, "internal/ui", true), runsCountingScriptServer); len(got) != 0 {
		t.Errorf("ui tests still define a counting script server: %v", got)
	}
	if got := funcDecls(testutilSources(t), runsCountingServer); len(got) != 1 {
		t.Errorf("testutil defines %d counting script servers %v, want exactly 1", len(got), got)
	}
}

func isEnvFixture(s string) bool {
	return strings.Contains(s, "APP_URL=https://mail.example.com") && strings.Contains(s, "ORBIT_IMAGE=ghcr.io/tomlawesome/orbit@sha256:") &&
		!strings.Contains(s, "#!/") // a fake engine script writes it in shell; that is the engine's output
}

// SU-20: the .env-orbit fixture was five hand-typed copies; it is one value
// in testutil.
func TestEnvOrbitFixture_IsTypedOnceInTestutil(t *testing.T) {
	var consumers []goFile
	consumers = append(consumers, loadDir(t, "internal/deploy", true)...)
	consumers = append(consumers, loadDir(t, "internal/ui", true)...)
	consumers = append(consumers, loadDir(t, "test/pty", true)...)
	if got := literalHits(consumers, isEnvFixture); len(got) != 0 {
		t.Errorf("the .env-orbit fixture is still typed out in %v: take it from testutil", got)
	}
	if got := literalHits(testutilSources(t), isEnvFixture); len(got) != 1 {
		t.Errorf("testutil spells the fixture out %d times, want exactly once", len(got))
	}
}

// SU-20: staging a scripts/ tree (configure tree, repair.sh placement) was
// repeated in ui and pty. Outside deploy (EN-17 keeps one builder there,
// tested separately) it is testutil's.
func TestScriptTreeStager_OutsideDeployItIsTestutils(t *testing.T) {
	if got := funcDecls(loadDir(t, "internal/ui", true), makesScriptsDir); len(got) != 0 {
		t.Errorf("ui tests still stage a scripts/ tree themselves: %v", got)
	}
	if got := funcDecls(loadDir(t, "test/pty", true), makesScriptsDir); len(got) != 0 {
		t.Errorf("pty tests still stage a scripts/ tree themselves: %v", got)
	}
	if got := funcDecls(testutilSources(t), makesScriptsDir); len(got) == 0 {
		t.Error("testutil has no scripts/ tree stager")
	}
}

// --- pty fake engines ---

// SU-20 / Done "the pty fake engines share one prologue": the "refuse until
// .env-orbit exists" opening was copied into three engines.
func TestPtyFakeEngines_ShareOneRefusalPrologue(t *testing.T) {
	const prologue = "[[ ! -f .env-orbit ]]"
	var scope []goFile
	scope = append(scope, loadAll(t, "test/pty")...)
	scope = append(scope, loadAll(t, "test/internal")...) // vtscreen aside, the harness lives here
	scope = append(scope, testutilSources(t)...)
	hits := literalHits(scope, func(s string) bool { return strings.Contains(s, prologue) })
	if len(hits) != 1 {
		t.Errorf("the refuse-until-configured prologue is spelled out %d times %v, want exactly once", len(hits), hits)
	}
}

// The two machine-configure fakes share --init and --set-oidc-secret and
// differ only in --check and the auth-mode write.
func TestPtyFakeMachineConfigure_ShareInitAndSecretSteps(t *testing.T) {
	pty := loadAll(t, "test/pty")
	for _, step := range []string{
		"prompt field=APP_URL kind=url required=true attempt=1",
		"prompt field=OIDC_CLIENT_SECRET kind=secret required=true attempt=1",
	} {
		hits := literalHits(pty, func(s string) bool { return strings.Contains(s, step) })
		if len(hits) != 1 {
			t.Errorf("the fake configure step %q is spelled out %d times %v, want exactly once", step, len(hits), hits)
		}
	}
}

// The scans above rely on how the engines are written today; this keeps
// them honest: the pieces they look for are still there, once, in the tests.
// Passed already: guards that sharing did not delete the fake engines.
func TestPtyFakeEngines_StillExist(t *testing.T) {
	pty := loadAll(t, "test/pty")
	for _, want := range []string{"fakeConfigAwareEngine", "fakeLongLineRetryEngine", "fakeDatabasePhaseEngine", "fakeMachineConfigure", "fakeMachineConfigureModeAware"} {
		found := false
		for _, gf := range pty {
			ast.Inspect(gf.file, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok && id.Name == want && id.Obj != nil {
					found = true
				}
				return true
			})
		}
		if !found {
			t.Errorf("test/pty no longer defines %s: sharing a prologue must not remove an engine", want)
		}
	}
}
