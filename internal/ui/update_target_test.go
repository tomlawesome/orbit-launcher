package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/release"
)

// #336. These need symbols the base lacks: release.Target, targetMsg,
// UpdateModel.lookupTarget, updateStateNewer, engineRun.channel.
// Case D (what the confirm offers when installed equals target) and the
// console title naming the version are open for the owner and not
// covered; the console-title tests below follow the note's proposed
// wording and may need to follow the owner's answer.

var errTargetUnreadable = errors.New("manifest unreachable")

func versionedDeployment(version string) *deploy.Deployment {
	d := testDeployment()
	d.Version = version
	return d
}

// answeredModel is an Update confirm model at width whose target lookup has
// answeredModel with target/err and whose install date has arrived.
func answeredModel(t *testing.T, d *deploy.Deployment, width int, seams engineRunSeams, target release.Target, err error) UpdateModel {
	t.Helper()
	m, _ := newTestUpdateModel(d, seams)
	m.lookupTarget = func(context.Context) (release.Target, error) { return target, err }
	u, _ := m.Update(tea.WindowSizeMsg{Width: width, Height: 26})
	u, _ = u.Update(installedAtMsg{at: time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)})
	u, _ = u.Update(targetMsg{target: target, err: err})
	return u.(UpdateModel)
}

func targetRowsOf(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if r := strings.TrimSpace(strings.Trim(strings.TrimSpace(l), "⟡✦●")); r != "" {
			out = append(out, r)
		}
	}
	return out
}

func hasTargetRow(s, want string) bool {
	trim := func(x string) string { return strings.Trim(x, " ▸.·✦●⟡") }
	for _, l := range strings.Split(s, "\n") {
		if trim(l) == trim(want) {
			return true
		}
	}
	return false
}

func targetRowContaining(s, sub string) (string, bool) {
	for _, l := range strings.Split(s, "\n") {
		if strings.Contains(l, sub) {
			return strings.TrimSpace(l), true
		}
	}
	return "", false
}

func refusingSeams(called *bool) engineRunSeams {
	return engineRunSeams{prepareEngine: func(context.Context, string, string) (*engine.Stream, func() error, error) {
		*called = true
		return nil, nil, errors.New("not wanted")
	}}
}

// 5
func TestUpdateInit_CallsTheTargetSeamOnce(t *testing.T) {
	calls := 0
	m := NewUpdateModel(testDeployment(), "/opt/orbit", "v9.9.9")
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	m.lookupTarget = func(context.Context) (release.Target, error) {
		calls++
		return release.Target{Version: "v0.3.6", Pin: true}, nil
	}
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned no command")
	}
	var msgs []tea.Msg
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch r := c().(type) {
		case tea.BatchMsg:
			for _, sub := range r {
				run(sub)
			}
		default:
			msgs = append(msgs, r)
		}
	}
	run(cmd)
	if calls != 1 {
		t.Errorf("the seam was called %d times, want 1", calls)
	}
	var got *targetMsg
	for _, x := range msgs {
		if tm, ok := x.(targetMsg); ok {
			got = &tm
		}
	}
	if got == nil || got.target.Version != "v0.3.6" || !got.target.Pin || got.err != nil {
		t.Errorf("Init did not answer with the seam's target: %+v", msgs)
	}
}

func TestUpdateInit_WithNoSeamAsksNothingBeyondTheDate(t *testing.T) {
	m := NewUpdateModel(testDeployment(), "/opt/orbit", "v9.9.9")
	m.lookupInstalledAt = func(context.Context, *deploy.Deployment) time.Time { return time.Time{} }
	cmd := m.Init()
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		switch r := c().(type) {
		case tea.BatchMsg:
			for _, sub := range r {
				run(sub)
			}
		case targetMsg:
			t.Errorf("Init asked for a target with no seam set: %+v", r)
		}
	}
	run(cmd)
}

// 7
func TestUpdateConfirm_CaseA_TitleNamesBothVersions(t *testing.T) {
	m := answeredModel(t, versionedDeployment("v0.3.5"), 80, engineRunSeams{}, release.Target{Version: "v0.3.6", Pin: true}, nil)
	s := screen(m)
	if !hasTargetRow(s, "Update Orbit from v0.3.5 to v0.3.6") {
		t.Errorf("no exact title row:\n%s", s)
	}
	if strings.Contains(s, "current release") {
		t.Errorf("case A has no release row:\n%s", s)
	}
	if !strings.Contains(s, "ghcr.io/tomlawesome/orbit") {
		t.Errorf("image row missing:\n%s", s)
	}
}

// 8
func TestUpdateConfirm_CaseB_InstalledUnknown(t *testing.T) {
	s := screen(answeredModel(t, versionedDeployment(""), 80, engineRunSeams{}, release.Target{Version: "v0.3.6", Pin: true}, nil))
	if !hasTargetRow(s, "Update Orbit to v0.3.6") {
		t.Errorf("no title row:\n%s", s)
	}
	if !hasTargetRow(s, "Version not recorded") {
		t.Errorf("no 'Version not recorded' row:\n%s", s)
	}
}

// 9
func TestUpdateConfirm_CaseC_TargetUnknown(t *testing.T) {
	s := screen(answeredModel(t, versionedDeployment("v0.3.5"), 80, engineRunSeams{}, release.Target{}, errTargetUnreadable))
	if !hasTargetRow(s, "Pull the latest Orbit and update this deployment") {
		t.Errorf("title changed:\n%s", s)
	}
	if !hasTargetRow(s, "On v0.3.5 · current release couldn't be read") {
		t.Errorf("no release row:\n%s", s)
	}
}

// 10
func TestUpdateConfirm_CaseC2_BothUnknownWrapsAtTheSeparator(t *testing.T) {
	const row = "Version not recorded · current release couldn't be read"
	s80 := screen(answeredModel(t, versionedDeployment(""), 80, engineRunSeams{}, release.Target{}, errTargetUnreadable))
	if !hasTargetRow(s80, row) {
		t.Errorf("80 columns: want one row %q:\n%s", row, s80)
	}
	s50 := screen(answeredModel(t, versionedDeployment(""), 50, engineRunSeams{}, release.Target{}, errTargetUnreadable))
	first, ok1 := targetRowContaining(s50, "Version not recorded")
	second, ok2 := targetRowContaining(s50, "be read")
	if !ok1 || !ok2 || first == second {
		t.Fatalf("50 columns: want two rows:\n%s", s50)
	}
	for _, r := range []string{first, second} {
		if w := lipgloss.Width(r); w > 46 {
			t.Errorf("row %q is %d cells, want at most 46", r, w)
		}
	}
}

// 12
func TestUpdateConfirm_CaseE_InstalledNewerIsADeadEnd(t *testing.T) {
	called := false
	m := answeredModel(t, versionedDeployment("v0.3.7"), 80, refusingSeams(&called), release.Target{Version: "v0.3.6", Pin: true}, nil)
	if m.state != updateStateNewer {
		t.Fatalf("state = %v, want updateStateNewer", m.state)
	}
	s := screen(m)
	for _, want := range []string{
		"This Orbit is newer than the current release",
		"v0.3.7 is installed; the current stable release is v0.3.6.",
		"Orbit's installer doesn't downgrade.",
		"nothing has been changed, and nothing will be by this screen",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q:\n%s", want, s)
		}
	}
	if got := screenLabels(s); !slices.Equal(got, []string{"Menu"}) {
		t.Errorf("rows = %q, want just Menu", got)
	}

	for name, k := range map[string]tea.KeyPressMsg{"Enter": key(tea.KeyEnter), "Esc": key(tea.KeyEsc)} {
		u, cmd := m.Update(k)
		if cmd != nil {
			cmd()
		}
		if o := u.(UpdateModel).Outcome(); !leftForMenu(o) {
			t.Errorf("%s: outcome = %+v, want back to the menu", name, o)
		}
	}
	if called {
		t.Error("the engine started from the dead end")
	}
}

func TestUpdateConfirm_CaseE_At50ColumnsNothingIsWiderThanTheScreen(t *testing.T) {
	s := screen(answeredModel(t, versionedDeployment("v0.3.7"), 50, engineRunSeams{}, release.Target{Version: "v0.3.6", Pin: true}, nil))
	for _, r := range targetRowsOf(s) {
		if w := lipgloss.Width(r); w > 46 {
			t.Errorf("row %q is %d cells, want at most 46", r, w)
		}
	}
}

// 13
func TestUpdateConfirm_NonSemverInstalledGetsCaseAWordingNotADeadEnd(t *testing.T) {
	m := answeredModel(t, versionedDeployment("v0.3.6-preview.abc1234"), 80, engineRunSeams{}, release.Target{Version: "v0.3.6", Pin: true}, nil)
	if m.state != updateStateConfirm {
		t.Fatalf("state = %v, want the confirm screen", m.state)
	}
	s := screen(m)
	title, ok := targetRowContaining(s, "Update Orbit from v0.3.6-")
	if !ok || !strings.HasSuffix(textOf(title), "to v0.3.6") {
		t.Errorf("want case A wording with the installed string shown:\n%s", s)
	}
	if !slices.Contains(screenLabels(s), "Update Orbit") {
		t.Errorf("Update Orbit not offered:\n%s", s)
	}
}

func TestUpdateConfirm_HostileInstalledVersionIsEscapedAndCutTo10Cells(t *testing.T) {
	m := answeredModel(t, versionedDeployment("v1.2.3\u202e\a-very-long-suffix"), 80, engineRunSeams{}, release.Target{Version: "v1.2.4", Pin: true}, nil)
	s := screen(m)
	if strings.ContainsAny(s, "\u202e\a") {
		t.Errorf("raw control or bidi characters reached the screen: %q", s)
	}
	if strings.Contains(s, "very-long-suffix") || !strings.Contains(s, "…") {
		t.Errorf("version not cut to 10 cells with an ellipsis:\n%s", s)
	}
}

func TestUpdateConfirm_TenCellVersionsKeepTitlesAndRowsInside(t *testing.T) {
	cases := []struct {
		name         string
		installed    string
		target       release.Target
		err          error
		titleSnippet string
	}{
		{"A", "v10.20.300", release.Target{Version: "v10.20.301", Pin: true}, nil, "Update Orbit from"},
		{"B", "", release.Target{Version: "v10.20.301", Pin: true}, nil, "Update Orbit to"},
		{"C", "v10.20.300", release.Target{}, errTargetUnreadable, "Pull the latest Orbit"},
	}
	for _, c := range cases {
		for _, width := range []int{50, 80, 120} {
			s := screen(answeredModel(t, versionedDeployment(c.installed), width, engineRunSeams{}, c.target, c.err))
			title, ok := targetRowContaining(s, c.titleSnippet)
			if !ok {
				t.Errorf("%s@%d: no title row:\n%s", c.name, width, s)
				continue
			}
			if w := lipgloss.Width(textOf(title)); w > 48 {
				t.Errorf("%s@%d: title is %d cells, want at most 48", c.name, width, w)
			}
			for i, l := range strings.Split(s, "\n") {
				if w := lipgloss.Width(l); w > width {
					t.Errorf("%s@%d: row %d is %d cells, wider than the screen: %q", c.name, width, i, w, l)
				}
			}
		}
	}
}

// 14
func TestUpdateConfirm_PinSetsTheChannelOnlyForAPinnedTarget(t *testing.T) {
	cases := []struct {
		name   string
		target release.Target
		err    error
		want   string
	}{
		{"pin", release.Target{Version: "v0.3.6", Pin: true}, nil, "v0.3.6"},
		{"manifest file or pinned channel", release.Target{Version: "v0.3.6", Pin: false}, nil, ""},
		{"target unknown", release.Target{}, errTargetUnreadable, ""},
	}
	for _, c := range cases {
		m := answeredModel(t, versionedDeployment("v0.3.5"), 80, engineRunSeams{}, c.target, c.err)
		u, _ := m.Update(key(tea.KeyEnter))
		got := u.(UpdateModel)
		if got.state != updateStateRunning {
			t.Fatalf("%s: state = %v, want running", c.name, got.state)
		}
		if got.run.channel != c.want {
			t.Errorf("%s: run channel = %q, want %q", c.name, got.run.channel, c.want)
		}
	}
}

// 15
func pendingTargetModel(t *testing.T, called *bool) UpdateModel {
	t.Helper()
	m, _ := newTestUpdateModel(versionedDeployment("v0.3.5"), refusingSeams(called))
	m.lookupTarget = func(context.Context) (release.Target, error) { return release.Target{}, nil }
	return m
}

func TestUpdateConfirm_EnterWhilePendingWaitsAndShowsTheWorkingLine(t *testing.T) {
	called := false
	m := pendingTargetModel(t, &called)
	u, _ := m.Update(key(tea.KeyEnter))
	got := u.(UpdateModel)
	if got.state != updateStateConfirm || called {
		t.Fatalf("Enter while pending started the run (state %v)", got.state)
	}
	if s := screen(got); !strings.Contains(s, "checking which Orbit release is current") {
		t.Errorf("no working line:\n%s", s)
	}
}

func TestUpdateConfirm_AnswerWhileAwaitingStartsTheRunPinnedAndNamed(t *testing.T) {
	called := false
	u, _ := pendingTargetModel(t, &called).Update(key(tea.KeyEnter))
	u, _ = u.Update(targetMsg{target: release.Target{Version: "v0.3.6", Pin: true}})
	got := u.(UpdateModel)
	if got.state != updateStateRunning {
		t.Fatalf("state = %v, want running", got.state)
	}
	if got.run.channel != "v0.3.6" {
		t.Errorf("channel = %q, want v0.3.6", got.run.channel)
	}
	if got.run.title != "Update to v0.3.6 — mail.example.com" {
		t.Errorf("console title = %q", got.run.title)
	}
}

func TestUpdateConfirm_UnknownAnswerWhileAwaitingStartsNothingAndSecondEnterRunsUnpinned(t *testing.T) {
	called := false
	u, _ := pendingTargetModel(t, &called).Update(key(tea.KeyEnter))
	u, _ = u.Update(targetMsg{err: errTargetUnreadable})
	got := u.(UpdateModel)
	if got.state != updateStateConfirm {
		t.Fatalf("state = %v, want the confirm screen: an unknown answer starts nothing", got.state)
	}
	s := screen(got)
	if !hasTargetRow(s, "On v0.3.5 · current release couldn't be read") {
		t.Errorf("no case C row:\n%s", s)
	}
	if strings.Contains(s, "checking which Orbit release") {
		t.Errorf("working line still showing:\n%s", s)
	}
	if caretLabel(s) != "Update Orbit" {
		t.Errorf("caret on %q, want Update Orbit", caretLabel(s))
	}
	u, _ = got.Update(key(tea.KeyEnter))
	got = u.(UpdateModel)
	if got.state != updateStateRunning || got.run.channel != "" {
		t.Errorf("second Enter: state %v channel %q, want running unpinned", got.state, got.run.channel)
	}
	if got.run.title != "Update — mail.example.com" {
		t.Errorf("console title = %q, want the plain one", got.run.title)
	}
}

func TestUpdateConfirm_NewerAnswerWhileAwaitingShowsTheDeadEndAndStartsNothing(t *testing.T) {
	called := false
	m := pendingTargetModel(t, &called)
	m.deployment.Version = "v0.3.7"
	u, _ := m.Update(key(tea.KeyEnter))
	u, _ = u.Update(targetMsg{target: release.Target{Version: "v0.3.6", Pin: true}})
	got := u.(UpdateModel)
	if got.state != updateStateNewer || called {
		t.Errorf("state = %v, started = %v; want the dead end and no run", got.state, called)
	}
}

func TestUpdateConfirm_EscAndCancelWhileAwaitingLeaveToTheMenu(t *testing.T) {
	for name, keys := range map[string][]tea.KeyPressMsg{
		"Esc":    {key(tea.KeyEnter), key(tea.KeyEsc)},
		"Cancel": {key(tea.KeyEnter), key(tea.KeyDown), key(tea.KeyEnter)},
	} {
		called := false
		var m tea.Model = pendingTargetModel(t, &called)
		for _, k := range keys {
			m, _ = m.Update(k)
		}
		if o := m.(UpdateModel).Outcome(); !leftForMenu(o) || called {
			t.Errorf("%s: outcome %+v, started %v; want the menu and no run", name, o, called)
		}
		// A late answer after leaving starts nothing.
		m, _ = m.Update(targetMsg{target: release.Target{Version: "v0.3.6", Pin: true}})
		if m.(UpdateModel).state == updateStateRunning || called {
			t.Errorf("%s: a late answer started the run", name)
		}
	}
}

// 16
func TestUpdateRun_ALateTargetAnswerChangesNeitherChannelNorTitle(t *testing.T) {
	m, _ := newTestUpdateModel(versionedDeployment("v0.3.5"), engineRunSeams{})
	u, _ := m.Update(key(tea.KeyEnter))
	started := u.(UpdateModel)
	if started.state != updateStateRunning {
		t.Fatalf("state = %v, want running", started.state)
	}
	u, _ = started.Update(targetMsg{target: release.Target{Version: "v0.3.6", Pin: true}})
	got := u.(UpdateModel)
	if got.run.channel != "" || got.run.title != "Update — mail.example.com" {
		t.Errorf("late answer changed the run: channel %q title %q", got.run.channel, got.run.title)
	}
}

// 17
func TestUpdateRun_ConsoleTitleWithNoAddressAndWithUnknownTarget(t *testing.T) {
	d := versionedDeployment("v0.3.5")
	d.AppURL = ""
	m := answeredModel(t, d, 80, engineRunSeams{}, release.Target{Version: "v0.3.6", Pin: true}, nil)
	u, _ := m.Update(key(tea.KeyEnter))
	if got := u.(UpdateModel).run.title; got != "Update to v0.3.6" {
		t.Errorf("title = %q, want %q", got, "Update to v0.3.6")
	}

	m = answeredModel(t, versionedDeployment("v0.3.5"), 80, engineRunSeams{}, release.Target{}, errTargetUnreadable)
	u, _ = m.Update(key(tea.KeyEnter))
	if got := u.(UpdateModel).run.title; got != "Update — mail.example.com" {
		t.Errorf("title = %q, want the plain one", got)
	}
}

// 18
func TestUpdateConfirm_InstallDateStillShowsInCasesABCAndC2(t *testing.T) {
	cases := map[string]struct {
		installed string
		target    release.Target
		err       error
	}{
		"A":  {"v0.3.5", release.Target{Version: "v0.3.6", Pin: true}, nil},
		"B":  {"", release.Target{Version: "v0.3.6", Pin: true}, nil},
		"C":  {"v0.3.5", release.Target{}, errTargetUnreadable},
		"C2": {"", release.Target{}, errTargetUnreadable},
	}
	at := installedAtMsg{at: time.Date(2026, 6, 14, 9, 0, 0, 0, time.UTC)}
	for name, c := range cases {
		// The date arrives before the target in answeredModel(); try the other order too.
		first := screen(answeredModel(t, versionedDeployment(c.installed), 80, engineRunSeams{}, c.target, c.err))
		m, _ := newTestUpdateModel(versionedDeployment(c.installed), engineRunSeams{})
		u, _ := m.Update(targetMsg{target: c.target, err: c.err})
		u, _ = u.Update(at)
		second := ansi.Strip(u.View().Content)
		for order, s := range map[string]string{"date first": first, "target first": second} {
			if !strings.Contains(s, "mail.example.com · installed 2026-06-14") {
				t.Errorf("%s (%s): install date missing:\n%s", name, order, s)
			}
		}
	}
}

// The app hands WithTargetLookup's function to the Update model: opening
// Update from the menu asks it once, and its answer reaches the screen.
func TestAppWithTargetLookup_OpeningUpdateCallsTheInjectedLookup(t *testing.T) {
	dir := shortTempDir(t)
	writeEnv(t, dir, "APP_URL="+wiringApp+"\n")
	calls := 0
	m := NewAppModelNoAnimation().WithoutVolumeCheck().WithVersion("v9.9.9").
		WithTargetLookup(func(context.Context) (release.Target, error) {
			calls++
			return release.Target{Version: "v0.3.6", Pin: true}, nil
		})
	m.targetDir = dir

	m = openUpdate(t, m, nil)
	if m.state != appStateUpdate {
		t.Fatalf("state = %v, want the Update flow", m.state)
	}
	if calls != 1 {
		t.Errorf("the injected lookup was called %d times on opening Update, want 1", calls)
	}
	if s := screen(m); !strings.Contains(s, "v0.3.6") {
		t.Errorf("the lookup's answer did not reach the confirm screen:\n%s", s)
	}
}
