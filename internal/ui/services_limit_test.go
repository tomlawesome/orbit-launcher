package ui

// Pins #277 A4: "services · checking" has a time limit. RepairModel gains
// a field servicesLimit time.Duration; zero means 4 seconds, the
// production reader's own limit. If the services reader has not answered
// within it, the block shows "services · could not check", as for a reader
// error. An answer that arrives later is ignored.

import (
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// blockedReader is a services reader that waits on a channel; the test
// releases it in t.Cleanup so no goroutine outlives it.
type blockedReader struct {
	release chan struct{}
	once    sync.Once
	entered chan struct{}
	left    chan struct{}
	answer  []deploy.ServiceState
}

func newBlockedReader(t *testing.T, answer []deploy.ServiceState) *blockedReader {
	t.Helper()
	b := &blockedReader{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		left:    make(chan struct{}, 4),
		answer:  answer,
	}
	t.Cleanup(b.unblock)
	return b
}

func (b *blockedReader) unblock() { b.once.Do(func() { close(b.release) }) }

func (b *blockedReader) read(string) ([]deploy.ServiceState, error) {
	b.entered <- struct{}{}
	<-b.release
	b.left <- struct{}{}
	return b.answer, nil
}

// liveDriver runs commands the way the program does: each on its own
// goroutine, its message fed to Update when it arrives. A tea.BatchMsg is
// expanded; sky ticks are dropped. Nothing sleeps: it waits on the
// messages themselves, with one overall deadline so a broken build fails
// rather than hangs.
type liveDriver struct {
	t           *testing.T
	msgs        chan tea.Msg
	outstanding int
}

func newLiveDriver(t *testing.T) *liveDriver {
	return &liveDriver{t: t, msgs: make(chan tea.Msg, 32)}
}

func (d *liveDriver) start(c tea.Cmd) {
	if c == nil {
		return
	}
	d.outstanding++
	go func() { d.msgs <- c() }()
}

// run feeds messages to m until done(m) holds or no command is left.
func (d *liveDriver) run(m RepairModel, done func(RepairModel) bool) RepairModel {
	d.t.Helper()
	deadline := time.After(5 * time.Second)
	for !done(m) && d.outstanding > 0 {
		select {
		case msg := <-d.msgs:
			d.outstanding--
			if batch, ok := msg.(tea.BatchMsg); ok {
				for _, c := range batch {
					d.start(c)
				}
				continue
			}
			if _, tick := msg.(tickMsg); tick || msg == nil {
				continue
			}
			var more tea.Cmd
			m, more = updateAs(d.t, m, msg)
			d.start(more)
		case <-deadline:
			d.t.Fatalf("still waiting on %d command(s) 5s in:\n%s", d.outstanding, screen(m))
		}
	}
	return m
}

func couldNotCheck(m RepairModel) bool {
	return strings.Contains(plain(m), "services · could not check")
}

// A reader that does not answer within the limit is "could not check",
// and the answer it gives later changes nothing.
func TestServicesLimit_AReaderThatDoesNotAnswerIsCouldNotCheck(t *testing.T) {
	reader := newBlockedReader(t, up("orbit-db", "orbit-app", "orbit-clamav"))
	m := servicesModelUnread(t, "standard", 80, 30, nil, nil)
	m.serviceStates = reader.read
	m.servicesLimit = 50 * time.Millisecond

	cmd := m.readServices()
	if cmd == nil {
		t.Fatal("readServices returned no command")
	}
	if _, ok := checkingRow(screen(m)); !ok {
		t.Fatalf("setup: no checking row before the read answers:\n%s", screen(m))
	}

	d := newLiveDriver(t)
	d.start(cmd)
	m = d.run(m, couldNotCheck)
	s := screen(m)
	if !couldNotCheck(m) {
		t.Fatalf("no `services · could not check` once the limit passed (reader still blocked):\n%s", s)
	}
	if row, ok := checkingRow(s); ok {
		t.Errorf("the checking row %q stayed after the limit:\n%s", row, s)
	}
	if rows := svcRows(t, m); len(rows) != 0 {
		t.Errorf("service rows shown for a reader that never answered: %v", rows)
	}

	// The reader answers late; whatever reaches Update from it is ignored.
	reader.unblock()
	select {
	case <-reader.left:
	case <-time.After(5 * time.Second):
		t.Fatal("the released reader never returned")
	}
	m = d.run(m, func(RepairModel) bool { return false })
	if rows := svcRows(t, m); len(rows) != 0 {
		t.Errorf("a late answer put service rows on the screen: %v\n%s", rows, screen(m))
	}
	if !couldNotCheck(m) {
		t.Errorf("a late answer replaced `could not check`:\n%s", screen(m))
	}
	if after := screen(m); after != s {
		t.Errorf("a late answer changed the screen:\n--- before\n%s\n--- after\n%s", s, after)
	}
}

// A reader that answers within the limit is read as before.
func TestServicesLimit_AnAnswerInTimeIsShown(t *testing.T) {
	m := servicesModelUnread(t, "standard", 80, 30, up("orbit-db", "orbit-app", "orbit-clamav"), nil)
	m.servicesLimit = 5 * time.Second
	m = servicesRead(t, m)
	if rows := svcRows(t, m); len(rows) != 3 {
		t.Errorf("rows = %v, want the three running services:\n%s", rows, screen(m))
	}
	if strings.Contains(plain(m), "could not check") {
		t.Errorf("`could not check` for a reader that answered in time:\n%s", screen(m))
	}
}
