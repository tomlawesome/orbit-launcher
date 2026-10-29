package ui

// v0.4.3 group B, L3 (ratified design, issue #277 row 54): the stopped
// screen's folder read has a 4 second limit, as the services reader has
// (A4). A read that has not answered in time leaves "the folder could not
// be read" in the slot and the next step unchanged; whatever it answers
// later changes nothing; a read that never returns costs only its own
// goroutine.
//
// SPEC GAP: the specification does not name the way to shorten the limit.
// These tests assume a field folderLimit time.Duration on engineRunSeams
// (zero means 4 seconds), the counterpart of RepairModel's servicesLimit.
// The field is set through reflection (gbStWithFolderLimit), so the rest
// of the package compiles before the field exists and these tests fail with
// a message saying so; if the field lands elsewhere, change that one helper.
//
// Written from the specification alone.

import (
	"errors"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	tea "charm.land/bubbletea/v2"
)

// gbStBlockedFolder is a folder reader that waits on a channel; the test
// releases it in t.Cleanup so no goroutine outlives it.
type gbStBlockedFolder struct {
	gbStFolderSeam
	release chan struct{}
	once    sync.Once
	entered chan struct{}
	left    chan struct{}
}

func gbStBlockedSeam(t *testing.T) *gbStBlockedFolder {
	t.Helper()
	b := &gbStBlockedFolder{
		release: make(chan struct{}),
		entered: make(chan struct{}, 4),
		left:    make(chan struct{}, 4),
	}
	b.answer = func(string) ([]os.DirEntry, error) {
		b.entered <- struct{}{}
		<-b.release
		b.left <- struct{}{}
		return []os.DirEntry{gbStEntry{"late.txt", false}}, nil
	}
	// A limit that is not honoured must fail the test, not hang the run:
	// the reader is let go after a while on its own.
	failsafe := time.AfterFunc(8*time.Second, b.unblock)
	t.Cleanup(func() { failsafe.Stop(); b.unblock() })
	return b
}

func (b *gbStBlockedFolder) unblock() { b.once.Do(func() { close(b.release) }) }

// gbStWithFolderLimit sets the assumed engineRunSeams.folderLimit field.
func gbStWithFolderLimit(t *testing.T, seams *engineRunSeams, limit time.Duration) {
	t.Helper()
	f := reflect.ValueOf(seams).Elem().FieldByName("folderLimit")
	if !f.IsValid() || f.Type() != reflect.TypeOf(time.Duration(0)) {
		t.Fatal("engineRunSeams has no folderLimit time.Duration field (assumed name, like RepairModel's servicesLimit)")
	}
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().Set(reflect.ValueOf(limit))
}

// A read that does not answer within the limit is "could not be read" and
// the answer it gives later changes nothing.
func TestGroupB_L3_AReadThatDoesNotAnswerInTimeIsCouldNotBeRead(t *testing.T) {
	blocked := gbStBlockedSeam(t)
	dir := folderWith(t, "late.txt")

	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{
		prepareEngine: gbStRefusedEngine(),
		detect:        fakeDetect("https://mail.example.com"),
		listFolder:    blocked.read,
	}
	gbStWithFolderLimit(t, &m.seams, 50*time.Millisecond)
	q := newSink()
	m.send = q.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := startInstallRun(t, m)
	m, _ = gbStDrive(t, m, cmd, q, &blocked.gbStFolderSeam, false)

	select {
	case <-blocked.entered:
	default:
		t.Fatalf("the folder reader was never entered:\n%s", screen(m))
	}
	s := screen(m)
	if !strings.Contains(flat(s), gbStCouldNotRow) {
		t.Fatalf("no %q once the limit passed (reader still blocked):\n%s", gbStCouldNotRow, s)
	}
	if strings.Contains(s, gbStReadingRow) {
		t.Errorf("%q stayed after the limit:\n%s", gbStReadingRow, s)
	}
	if !strings.Contains(flat(s), stepFixNamedAbove) || strings.Contains(flat(s), "move them out") {
		t.Errorf("the next step is not the general one:\n%s", s)
	}

	// The reader answers late; nothing that reaches the model changes it.
	blocked.unblock()
	select {
	case <-blocked.left:
	case <-time.After(5 * time.Second):
		t.Fatal("the released reader never returned")
	}
	// Nothing is expected to arrive, so this is the one wait on a clock.
	m = driveQuiet(t, m, nil, settleQuiet, q)
	if after := screen(m); after != s {
		t.Errorf("a late answer changed the screen:\n--- before\n%s\n--- after\n%s", s, after)
	}
}

// A read that answers within the limit is shown as before.
func TestGroupB_L3_AnAnswerInTimeIsShown(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := gbStAnswers(gbStEntry{"a.txt", false})
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{
		prepareEngine: gbStRefusedEngine(),
		detect:        fakeDetect("https://mail.example.com"),
		listFolder:    seam.read,
	}
	gbStWithFolderLimit(t, &m.seams, 5*time.Second)
	q := newSink()
	m.send = q.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := startInstallRun(t, m)
	m, _ = gbStDrive(t, m, cmd, q, seam, false)
	s := screen(m)
	if !strings.Contains(flat(s), "in the folder: a.txt") || strings.Contains(flat(s), gbStCouldNotRow) {
		t.Errorf("an answer in time is not shown:\n%s", s)
	}
}

// An error answer is the same row as a slow one.
func TestGroupB_L3_AnErrorBeforeTheLimitIsCouldNotBeRead(t *testing.T) {
	dir := folderWith(t, "a.txt")
	seam := &gbStFolderSeam{answer: func(string) ([]os.DirEntry, error) { return nil, errors.New("input/output error") }}
	m := NewInstallModel(dir, "v9.9.9")
	m.seams = engineRunSeams{
		prepareEngine: gbStRefusedEngine(),
		detect:        fakeDetect("https://mail.example.com"),
		listFolder:    seam.read,
	}
	gbStWithFolderLimit(t, &m.seams, 5*time.Second)
	q := newSink()
	m.send = q.Send
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 26})
	m, cmd := startInstallRun(t, m)
	m, _ = gbStDrive(t, m, cmd, q, seam, false)
	if s := screen(m); !strings.Contains(flat(s), gbStCouldNotRow) {
		t.Errorf("no %q for a failed read:\n%s", gbStCouldNotRow, s)
	}
}
