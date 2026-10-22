package ui

// The claim link's wiring through AppModel (#259): only a successful
// Install reads the claim link; Update never does. Written from the
// contract alone. Names the contract gives: AppModel.flowReadClaim,
// AppModel.success.readClaim, claimLinkMsg{link}.

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const (
	wiringApp  = "https://mail.example.test"
	wiringLink = "https://mail.example.test/#claim=Zm9yLXRlc3Qtb25seS1ub3QtYS1yZWFsLWNvZGU"
)

// claimCall is one call the app made to flowReadClaim.
type claimCall struct {
	dir, appURL string
	since       time.Time
	at          time.Time
}

// claimRecorder stands in for flowReadClaim and answers with link.
type claimRecorder struct {
	calls []claimCall
	link  string
}

func (r *claimRecorder) read(_ context.Context, dir, appURL string, since time.Time) (string, error) {
	r.calls = append(r.calls, claimCall{dir: dir, appURL: appURL, since: since, at: time.Now()})
	return r.link, nil
}

// wiringModel builds an AppModel whose engine completes at dir, the
// deployment found there answering with appURL, and the claim read
// replaced by rec.
func wiringModel(dir, appURL string, rec *claimRecorder) (AppModel, *sink) {
	queue := newSink()
	m := NewAppModelNoAnimation().WithSender(queue.Send).WithoutVolumeCheck().WithVersion("v9.9.9")
	m.targetDir = dir
	m.flowSeams = engineRunSeams{
		prepareEngine: fakeEngine(nil, successStream()...),
		detect: func(string) (*deploy.Deployment, error) {
			return &deploy.Deployment{TargetDir: dir, AppURL: appURL}, nil
		},
	}
	m.flowNoticeDuration = time.Millisecond
	m.flowReadClaim = rec.read
	return m, queue
}

// installToEnd runs Install through the notice to the end of the run,
// returning the model and the time just before the run was started.
func installToEnd(t *testing.T, m AppModel, queue *sink) (AppModel, time.Time) {
	t.Helper()
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 30})
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Install
	m = drive(t, m, cmd)
	m, cmd = updateAs(t, m, key(tea.KeyEnter)) // Standard profile -> the notice
	m = drive(t, m, cmd)
	if s := screen(m); !strings.Contains(s, "A note before you install") {
		t.Fatalf("not on Install's notice:\n%s", s)
	}
	before := time.Now()
	m, cmd = updateAs(t, m, key(tea.KeyEnd), tea.KeyPressMsg{Text: noticePhrase}, key(tea.KeyEnter))
	m = drive(t, m, cmd, queue)
	return m, before
}

// A successful Install reads the claim link once, in the folder it
// installed into, for the address that came up, since its console
// started; the link the read returns reaches the success screen.
func TestClaimWiring_InstallSuccessReadsTheClaimOnce(t *testing.T) {
	dir := t.TempDir()
	rec := &claimRecorder{link: wiringLink}
	m, queue := wiringModel(dir, wiringApp, rec)

	m, before := installToEnd(t, m, queue)
	if m.state != appStateSuccess {
		t.Fatalf("Install did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	if len(rec.calls) != 1 {
		t.Fatalf("flowReadClaim was called %d times, want exactly once", len(rec.calls))
	}
	c := rec.calls[0]
	if c.dir != dir {
		t.Errorf("claim read in %q, want the folder Install installed into, %q", c.dir, dir)
	}
	if c.appURL != wiringApp {
		t.Errorf("claim read for %q, want the success address %q", c.appURL, wiringApp)
	}
	if c.since.IsZero() {
		t.Fatal("since is the zero time; want the time the install's console started")
	}
	if c.since.After(c.at) {
		t.Errorf("since %v is after the read itself (%v)", c.since, c.at)
	}
	if c.since.Before(before.Add(-time.Second)) {
		t.Errorf("since %v is earlier than the install's start (%v)", c.since, before)
	}
	if m.success.readClaim == nil {
		t.Error("the success screen has no claim reader after Install")
	}
	if s := screen(m); !strings.Contains(s, "Copy claim link") {
		t.Errorf("the link the read returned did not reach the success screen (no Copy claim link row):\n%s", s)
	}
}

// Update never reads the claim: the success screen it ends on has no
// claim reader and the read seam is never called.
func TestClaimWiring_UpdateSuccessNeverReadsTheClaim(t *testing.T) {
	dir := shortTempDir(t)
	writeEnv(t, dir, "APP_URL="+wiringApp+"\n")
	rec := &claimRecorder{link: wiringLink}
	m, queue := wiringModel(dir, wiringApp, rec)

	m = openUpdate(t, m, nil)
	if s := screen(m); !strings.Contains(s, "Pull the latest Orbit and update this deployment") {
		t.Fatalf("Update should open on its confirm screen:\n%s", s)
	}
	m, cmd := updateAs(t, m, key(tea.KeyEnter)) // Update Orbit
	m = drive(t, m, cmd, queue)

	if m.state != appStateSuccess {
		t.Fatalf("Update did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	if m.success.readClaim != nil {
		t.Error("the success screen after Update has a claim reader")
	}
	if len(rec.calls) != 0 {
		t.Errorf("flowReadClaim was called %d times after Update, want never", len(rec.calls))
	}
	if s := screen(m); strings.Contains(s, "Copy claim link") {
		t.Errorf("Update's success screen offers a claim link:\n%s", s)
	}
}

// An Install that succeeds with no address has nothing to read the
// claim for.
func TestClaimWiring_InstallSuccessWithNoAddressReadsNothing(t *testing.T) {
	rec := &claimRecorder{link: wiringLink}
	m, queue := wiringModel(t.TempDir(), "", rec)

	m, _ = installToEnd(t, m, queue)
	if m.state != appStateSuccess {
		t.Fatalf("Install did not reach the success screen (state %v):\n%s", m.state, screen(m))
	}
	if len(rec.calls) != 0 {
		t.Errorf("flowReadClaim was called %d times with no address, want never", len(rec.calls))
	}
	if s := screen(m); strings.Contains(s, "Copy claim link") {
		t.Errorf("a claim link row appeared with no address:\n%s", s)
	}
}
