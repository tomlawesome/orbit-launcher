package ui

import (
	"bytes"
	"context"
	"io"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/exp/teatest"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
)

// machineInitScript speaks the machine prompt protocol for --init the
// way orbit's configure.sh does (verified against the real script):
// APP_URL validated with one honest rejection class, the other fields
// accepted, three attempts then abort.
const machineInitScript = `
attempt=1
while :; do
  echo "prompt field=APP_URL kind=url required=true attempt=$attempt"
  read -r a || { echo "prompt-abort field=APP_URL"; exit 1; }
  case "$a" in
    https://*) echo "prompt-accept field=APP_URL"; break ;;
    *) echo "prompt-reject field=APP_URL reason=not-https"; attempt=$((attempt+1)) ;;
  esac
  if [ "$attempt" -gt 3 ]; then echo "prompt-abort field=APP_URL"; exit 1; fi
done
for f in OIDC_ISSUER:url OIDC_CLIENT_ID:text; do
  field=${f%%:*}; kind=${f##*:}
  echo "prompt field=$field kind=$kind required=true attempt=1"
  read -r a || { echo "prompt-abort field=$field"; exit 1; }
  echo "prompt-accept field=$field"
done
exit 0`

const machineSecretScript = `
echo "prompt field=OIDC_CLIENT_SECRET kind=secret required=true attempt=1"
read -r a || { echo "prompt-abort field=OIDC_CLIENT_SECRET"; exit 1; }
echo "prompt-accept field=OIDC_CLIENT_SECRET"
exit 0`

// legacyConfigureScript is orbit main's behaviour under machine mode:
// no protocol line, a refusal on stderr, exit 1 — the capability
// signal for the handoff fallback.
const legacyConfigureScript = `
echo "Orbit configuration: Guided configuration needs a controlling terminal." >&2
exit 1`

// scriptedConfig returns a startConfig seam running real subprocesses.
func scriptedConfig(initScript, secretScript string) startConfigFunc {
	return func(_ string, step deploy.ConfigStep) (*engine.Stream, io.WriteCloser, error) {
		script := initScript
		if step == deploy.ConfigStepSecret {
			script = secretScript
		}
		return engine.StartInteractive(exec.Command("bash", "-c", script))
	}
}

func planned(needInit, needSecret bool) prepareConfigFunc {
	return func(context.Context, string) configPlanMsg {
		return configPlanMsg{plan: configPlan{
			treeDir:    "/nonexistent-tree-for-tests",
			cleanup:    func() {},
			needInit:   needInit,
			needSecret: needSecret,
		}}
	}
}

// engineTwice serves the refusal on the first run and success on the
// retry — the full in-console configuration journey's engine side.
func engineTwice() prepareEngineFunc {
	var mu sync.Mutex
	calls := 0
	return func(_ context.Context, _ string, _ string) (*engine.Stream, func() error, error) {
		mu.Lock()
		calls++
		n := calls
		mu.Unlock()
		msgs := configRefusalStream()
		if n > 1 {
			msgs = successStream()
		}
		ch := make(chan any, len(msgs))
		for _, m := range msgs {
			ch <- m
		}
		close(ch)
		return &engine.Stream{C: ch}, func() error { return nil }, nil
	}
}

func startConfigJourney(t *testing.T, seams engineRunSeams) *teatest.TestModel {
	t.Helper()
	m := NewAppModel()
	m.targetDir = t.TempDir()
	m = m.WithVersion("v9.9.9")
	m.flowCheckVolumes = noStaleVolumes
	m.flowSeams = seams
	tm := teatest.NewTestModel(t, m, teatest.WithInitialTermSize(80, 26))
	skipArrival(tm)

	wait := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
			return bytes.Contains(out, []byte(want))
		}, teatest.WithDuration(10*time.Second))
	}

	wait("Install")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("Choose a deployment profile")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("Ready to install")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("Orbit needs your configuration")
	return tm
}

func TestAppModel_InConsoleConfigCollectThenRetrySucceeds(t *testing.T) {
	adopted := make(chan struct{}, 1)
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: planned(true, true),
		startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
		adoptConfig: func(_, _ string) error {
			adopted <- struct{}{}
			return nil
		},
		detect: fakeDetect("https://orbit.example.test"),
	})

	// Each wait can list several strings expected on the same frame —
	// one WaitFor per screen transition, because a WaitFor consumes the
	// output stream up to its match and a second look at the same frame
	// would starve.
	wait := func(wants ...string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
			for _, want := range wants {
				if !bytes.Contains(out, []byte(want)) {
					return false
				}
			}
			return true
		}, teatest.WithDuration(10*time.Second))
	}

	// Continue — guided configuration (in-console).
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("Public Orbit origin")

	// A bad answer is rejected with the engine's reason, re-prompted.
	tm.Type("orbit.example.test")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("must start with https://", "attempt 2 of 3")

	tm.Type("https://orbit.example.test")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("OIDC issuer URL")
	tm.Type("https://accounts.example.test")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("OIDC client ID")
	tm.Type("orbit-client")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	// The secret step: hidden input, then adoption and the retry.
	wait("OIDC client secret")
	tm.Type("s3cret-value")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	select {
	case <-adopted:
	case <-time.After(10 * time.Second):
		t.Fatal("configuration was never adopted into the target")
	}

	// The retry engine run succeeds and lands on the success screen.
	wait("Get into Orbit", "orbit.example.test")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_SecretInputIsNeverEchoed(t *testing.T) {
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: planned(false, true), // only the secret owed
		startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
		adoptConfig:   func(_, _ string) error { return nil },
		detect:        fakeDetect("https://orbit.example.test"),
	})

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("OIDC client secret"))
	}, teatest.WithDuration(10*time.Second))

	tm.Type("hunter2-super-secret")
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
		return bytes.Contains(out, []byte("Get into Orbit"))
	}, teatest.WithDuration(10*time.Second))

	// The final output must never have contained the typed secret.
	out, err := io.ReadAll(tm.Output())
	if err == nil && bytes.Contains(out, []byte("hunter2")) {
		t.Fatal("the secret was echoed to the screen")
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_LegacyConfigureFallsBackToHandoff(t *testing.T) {
	handoffRan := make(chan struct{}, 1)
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: planned(true, false),
		startConfig:   scriptedConfig(legacyConfigureScript, legacyConfigureScript),
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { return nil }, nil
		},
		runHandoff: func(*exec.Cmd) tea.Cmd {
			return func() tea.Msg {
				handoffRan <- struct{}{}
				return installFinishedMsg{}
			}
		},
		detect: fakeDetect("https://orbit.example.test"),
	})

	// Continue: the legacy script exits with no protocol line, and the
	// flow falls back to the terminal handoff automatically.
	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case <-handoffRan:
	case <-time.After(10 * time.Second):
		t.Fatal("legacy configure never fell back to the handoff")
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_ConfigAbortReturnsToRefusalMenu(t *testing.T) {
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: planned(true, false),
		startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
		detect:        fakeDetect("https://orbit.example.test"),
	})

	wait := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
			return bytes.Contains(out, []byte(want))
		}, teatest.WithDuration(10*time.Second))
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("Public Orbit origin")

	// Three rejected answers exhaust the engine's patience: abort, and
	// the refusal menu returns rather than pretending anything worked.
	for i := 0; i < 3; i++ {
		tm.Type("nope")
		tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
		time.Sleep(100 * time.Millisecond)
	}
	wait("Continue — guided configuration")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_EscCancelsConfigCollectToRefusalMenu(t *testing.T) {
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: planned(true, false),
		startConfig:   scriptedConfig(machineInitScript, machineSecretScript),
		detect:        fakeDetect("https://orbit.example.test"),
	})

	wait := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool {
			return bytes.Contains(out, []byte(want))
		}, teatest.WithDuration(10*time.Second))
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	wait("Public Orbit origin")
	tm.Send(tea.KeyMsg{Type: tea.KeyEsc})
	wait("Continue — guided configuration")

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

func TestAppModel_UnfixableFieldsFallBackToHandoff(t *testing.T) {
	handoffRan := make(chan struct{}, 1)
	tm := startConfigJourney(t, engineRunSeams{
		prepareEngine: engineTwice(),
		prepareConfig: func(context.Context, string) configPlanMsg {
			return configPlanMsg{plan: configPlan{
				treeDir:   "/nonexistent",
				cleanup:   func() {},
				needInit:  true,
				unfixable: []string{"SOME_FUTURE_FIELD"},
			}}
		},
		prepareInstall: func(context.Context, string) (*exec.Cmd, func() error, error) {
			return exec.Command("true"), func() error { return nil }, nil
		},
		runHandoff: func(*exec.Cmd) tea.Cmd {
			return func() tea.Msg {
				handoffRan <- struct{}{}
				return installFinishedMsg{}
			}
		},
		detect: fakeDetect("https://orbit.example.test"),
	})

	tm.Send(tea.KeyMsg{Type: tea.KeyEnter})
	select {
	case <-handoffRan:
	case <-time.After(10 * time.Second):
		t.Fatal("unfixable fields never fell back to the handoff")
	}

	tm.Send(tea.KeyMsg{Type: tea.KeyCtrlC})
	if err := tm.Quit(); err != nil {
		t.Fatalf("model did not quit cleanly: %v", err)
	}
}

// Naming a field is not the same as telling someone where to find its
// value. These are the notes that turn "your identity provider's issuer"
// into something an administrator can actually answer.
func TestPromptWords_CarriesTheGuidanceEachFieldNeeds(t *testing.T) {
	t.Run("the issuer shows both self-hosted shapes", func(t *testing.T) {
		_, _, notes := promptWords("OIDC_ISSUER", "")
		joined := strings.Join(notes, "\n")
		// Authentik's trailing slash is load-bearing: its discovery
		// document is only served from the slashed form, and it is the
		// character most easily dropped when copying by eye.
		if !strings.Contains(joined, "/application/o/orbit/") {
			t.Errorf("the Authentik example lost its trailing slash: %q", joined)
		}
		if !strings.Contains(joined, "/realms/orbit") {
			t.Errorf("no Keycloak example: %q", joined)
		}
	})

	t.Run("an unknown field is never guessed at", func(t *testing.T) {
		label, hint, notes := promptWords("SOME_FUTURE_FIELD", "https://orbit.example.com")
		if label != "SOME_FUTURE_FIELD" || hint != "" || notes != nil {
			t.Errorf("invented words for an unknown field: %q / %q / %v", label, hint, notes)
		}
	})
}

// The redirect URI is the value an identity provider's registration needs
// and the one most often got wrong, so the client-ID note names this
// deployment's real one whenever APP_URL has already been answered.
func TestCallbackNote_NamesThisDeploymentsRedirectURI(t *testing.T) {
	const generic = "register <your Orbit URL>/api/auth/callback"

	cases := []struct {
		name   string
		origin string
		want   string
	}{
		{"no APP_URL answered yet", "", generic},
		{"a real origin", "https://orbit.example.com", "register https://orbit.example.com/api/auth/callback"},
		{"a trailing slash is not doubled", "https://orbit.example.com/", "register https://orbit.example.com/api/auth/callback"},
		{"surrounding whitespace", "  https://orbit.example.com  ", "register https://orbit.example.com/api/auth/callback"},
		{"plain http is not an Orbit origin", "http://orbit.example.com", generic},
		{"too long for the bar", "https://" + strings.Repeat("a", 70) + ".example.com", generic},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := callbackNote(tc.origin)
			if len(got) == 0 || got[0] != tc.want {
				t.Errorf("callbackNote(%q) first line = %q, want %q", tc.origin, got, tc.want)
			}
			for _, line := range got {
				if len(line) > 76 {
					t.Errorf("note line would break the 80-cell bar: %q", line)
				}
			}
		})
	}
}

// nopWriteCloser stands in for the engine's stdin pipe, which the
// session closes; a buffer has nothing to close.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// origin is only ever remembered from a non-secret field: a secret must
// not survive one keystroke longer than the write to the engine's stdin.
func TestConfigCollect_OnlyAPPURLIsRemembered(t *testing.T) {
	var stdin bytes.Buffer
	r := engineRun{cfg: configCollect{stdin: nopWriteCloser{&stdin}}}

	r.cfg.prompt = &engine.Prompt{Field: "OIDC_CLIENT_SECRET", Kind: "secret"}
	r.cfg.input = []rune("hunter2")
	r, _ = r.handleConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if r.cfg.origin != "" {
		t.Errorf("a secret was remembered as the origin: %q", r.cfg.origin)
	}

	r.cfg.prompt = &engine.Prompt{Field: "APP_URL", Kind: "url"}
	r.cfg.input = []rune("https://orbit.example.com")
	r, _ = r.handleConfigKey(tea.KeyMsg{Type: tea.KeyEnter})
	if r.cfg.origin != "https://orbit.example.com" {
		t.Errorf("APP_URL was not remembered: %q", r.cfg.origin)
	}
}
