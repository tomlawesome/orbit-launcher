package ui

// The success screen's claim link (#259): when an install finishes and
// Orbit has not been claimed yet, the address becomes a clickable link
// to the claim page, the status says "unclaimed", and the menu gains
// Copy claim link. Written from the contract alone. Names the contract
// gives: SuccessModel.readClaim, claimLinkMsg{link}, SuccessModel.clipboard.

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/ansi"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

const (
	claimApp  = "https://mail.example.com"
	claimLink = "https://mail.example.com/#claim=Zm9yLXRlc3Qtb25seS1ub3QtYS1yZWFsLWNvZGU"

	// A deliberately long address, so that the address and the fragment
	// cannot share a row at 50, 60 or 80 columns, while each fits alone
	// at 50.
	claimLongApp  = "https://orbit-mail.long-hostname.example.com"
	claimLongFrag = "#claim=Zm9yLXRlc3Qtb25seS1ub3Q"
	claimLongLink = claimLongApp + "/" + claimLongFrag

	osc8Close = "\x1b]8;;\a"
)

func osc8Open(target string) string { return "\x1b]8;;" + target + "\a" }

// claimLabelSet are the rows the success screen can carry with a claim
// link; the shared rowLabels does not know Copy claim link.
var claimLabelSet = map[string]bool{
	"Get into Orbit": true, "Copy claim link": true, "Copy claim link · copied": true,
	"Terminal": true, "Menu": true,
}

func claimLabels(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if row := screenRow(line); claimLabelSet[row] {
			out = append(out, row)
		}
	}
	return out
}

func claimCaret(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, "▸") && claimLabelSet[screenRow(line)] {
			return screenRow(line)
		}
	}
	return ""
}

// claimModel is a success screen at width whose claim read has answered
// with link (delivered as claimLinkMsg, the way Init's command does);
// clip receives the clipboard writes.
func claimModel(t *testing.T, appURL, link string, width int, clip *bytes.Buffer) SuccessModel {
	t.Helper()
	m := NewSuccessModel(appURL, 3*time.Minute, "v9.9.9")
	m.noAnimation = true
	m.clipboard = clip
	m.readClaim = func() (string, error) { return link, nil }
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: width, Height: 40})
	m, _ = updateAs(t, m, claimLinkMsg{link: link})
	return m
}

func rowsOf(s string) []string { return strings.Split(strings.TrimRight(s, "\n"), "\n") }

// rowIndex is the index of the first row whose trimmed text is want, or -1.
func rowIndex(s, want string) int {
	for i, r := range rowsOf(s) {
		if screenRow(r) == want {
			return i
		}
	}
	return -1
}

func decodeOSC52(t *testing.T, got string) string {
	t.Helper()
	const prefix, suffix = "\x1b]52;c;", "\x07"
	if !strings.HasPrefix(got, prefix) || !strings.HasSuffix(got, suffix) {
		t.Fatalf("clipboard write = %q, want one OSC 52 sequence", got)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(strings.TrimPrefix(got, prefix), suffix))
	if err != nil {
		t.Fatalf("OSC 52 payload is not standard base64: %v", err)
	}
	return string(decoded)
}

// --- Init and the read ------------------------------------------------------

// Every existing caller leaves readClaim nil: no command, no read.
func TestSuccessClaim_NilReadClaimIssuesNoCommand(t *testing.T) {
	m := NewSuccessModel(claimApp, 0, "v")
	m.noAnimation = true
	if cmd := m.Init(); cmd != nil {
		t.Fatalf("Init() returned a command with readClaim nil (it produced %#v)", cmd())
	}
}

// Init issues one command; it calls readClaim once and returns
// claimLinkMsg{link}.
func TestSuccessClaim_InitReadsOnceAndReturnsTheLink(t *testing.T) {
	calls := 0
	m := NewSuccessModel(claimApp, 0, "v")
	m.noAnimation = true
	m.readClaim = func() (string, error) { calls++; return claimLink, nil }
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned no command with readClaim set")
	}
	if calls != 0 {
		t.Fatalf("readClaim ran inside Init itself (%d calls); it must run in the command", calls)
	}
	msg := cmd()
	if want := (claimLinkMsg{link: claimLink}); msg != want {
		t.Fatalf("command returned %#v, want %#v", msg, want)
	}
	if calls != 1 {
		t.Fatalf("readClaim called %d times, want 1", calls)
	}
}

// An error gives an empty link, whatever string came with it.
func TestSuccessClaim_ReadErrorGivesAnEmptyLink(t *testing.T) {
	m := NewSuccessModel(claimApp, 0, "v")
	m.noAnimation = true
	m.readClaim = func() (string, error) { return claimLink, errors.New("docker did not answer") }
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init() returned no command with readClaim set")
	}
	if msg := cmd(); msg != (claimLinkMsg{}) {
		t.Fatalf("command returned %#v, want an empty claimLinkMsg", msg)
	}
}

// The whole round trip, with the sky running: Init's commands, driven
// the way the program would, put the link on the screen.
func TestSuccessClaim_InitThenDriveShowsTheClaimScreen(t *testing.T) {
	m := NewSuccessModel(claimApp, 0, "v")
	m.readClaim = func() (string, error) { return claimLink, nil }
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	m = drive(t, m, m.Init())
	if !strings.Contains(m.View().Content, osc8Open(claimLink)) {
		t.Fatalf("after the read answered, the address is not a link to the claim page:\n%s", screen(m))
	}
}

// --- No link: today's screen ------------------------------------------------

func TestSuccessClaim_NoLinkIsExactlyTodaysScreen(t *testing.T) {
	today := NewSuccessModel(claimApp, 3*time.Minute, "v9.9.9")
	today.noAnimation = true
	today, _ = updateAs(t, today, tea.WindowSizeMsg{Width: 80, Height: 40})
	want := today.View().Content

	cases := map[string]func() SuccessModel{
		"empty link": func() SuccessModel {
			m := NewSuccessModel(claimApp, 3*time.Minute, "v9.9.9")
			m.noAnimation = true
			m.readClaim = func() (string, error) { return "", nil }
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
			m, _ = updateAs(t, m, claimLinkMsg{link: ""})
			return m
		},
		"read error": func() SuccessModel {
			m := NewSuccessModel(claimApp, 3*time.Minute, "v9.9.9")
			m.noAnimation = true
			m.readClaim = func() (string, error) { return "", errors.New("no logs") }
			m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
			m = drive(t, m, m.Init())
			return m
		},
	}
	for name, build := range cases {
		t.Run(name, func(t *testing.T) {
			m := build()
			if got := m.View().Content; got != want {
				t.Errorf("screen differs from today's:\n--- today ---\n%s\n--- now ---\n%s", ansi.Strip(want), ansi.Strip(got))
			}
			s := screen(m)
			if strings.Contains(s, "unclaimed") || strings.Contains(s, "Copy claim link") {
				t.Errorf("claim wording on a screen with no link:\n%s", s)
			}
			if strings.Contains(m.View().Content, "\x1b]8;") {
				t.Error("a hyperlink on a screen with no link")
			}
		})
	}
}

func TestSuccessClaim_NoLinkMenuIsTodays(t *testing.T) {
	m := NewSuccessModel(claimApp, 0, "v")
	m.noAnimation = true
	m, _ = updateAs(t, m, tea.WindowSizeMsg{Width: 80, Height: 40})
	want := []string{"Get into Orbit", "Terminal", "Menu"}
	if got := claimLabels(screen(m)); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

// --- With a link ------------------------------------------------------------

// The visible address wrapped in an OSC 8 hyperlink whose target is the
// claim link. Screen helpers strip OSC 8, so this reads the raw content.
func TestSuccessClaim_AddressIsAHyperlinkToTheClaimLink(t *testing.T) {
	m := claimModel(t, claimApp, claimLink, 80, &bytes.Buffer{})
	raw := m.View().Content

	open := strings.Index(raw, osc8Open(claimLink))
	if open < 0 {
		t.Fatalf("no OSC 8 opener targeting the claim link in %q", raw)
	}
	rest := raw[open+len(osc8Open(claimLink)):]
	closeAt := strings.Index(rest, osc8Close)
	if closeAt < 0 {
		t.Fatal("the hyperlink is never closed (ESC ] 8 ; ; BEL)")
	}
	if visible := ansi.Strip(rest[:closeAt]); visible != claimApp {
		t.Errorf("the link wraps %q, want the visible address %q", visible, claimApp)
	}
	// The address row on screen still reads as the plain address.
	if rowIndex(screen(m), claimApp) < 0 {
		t.Errorf("no row reads the address %q:\n%s", claimApp, screen(m))
	}
	if strings.Contains(screen(m), "\x1b") {
		t.Error("an escape reached the stripped screen")
	}
}

// The status row reads "alive · unclaimed", unclaimed in amber.
func TestSuccessClaim_StatusReadsAliveUnclaimed(t *testing.T) {
	m := claimModel(t, claimApp, claimLink, 80, &bytes.Buffer{})
	if rowIndex(screen(m), "alive · unclaimed") < 0 {
		t.Errorf("no status row reads %q:\n%s", "alive · unclaimed", screen(m))
	}
	if amber := style.DegradedText.Render("unclaimed"); !strings.Contains(m.View().Content, amber) {
		t.Errorf("unclaimed is not rendered with style.DegradedText (want %q in the view)", amber)
	}
}

func TestSuccessClaim_MenuOrder(t *testing.T) {
	m := claimModel(t, claimApp, claimLink, 80, &bytes.Buffer{})
	want := []string{"Get into Orbit", "Copy claim link", "Terminal", "Menu"}
	got := claimLabels(screen(m))
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("rows = %q, want %q", got, want)
	}
	if c := claimCaret(screen(m)); c != "Get into Orbit" {
		t.Errorf("caret starts on %q, want Get into Orbit", c)
	}
}

func TestSuccessClaim_GetIntoOrbitOpensTheClaimLink(t *testing.T) {
	var opened []string
	var clip bytes.Buffer
	m := claimModel(t, claimApp, claimLink, 80, &clip)
	m.openURL = func(url string) error { opened = append(opened, url); return nil }

	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if len(opened) != 1 || opened[0] != claimLink {
		t.Errorf("opened %q, want exactly the claim link %q", opened, claimLink)
	}
	if cmd != nil || m.Chosen != "" {
		t.Errorf("Get into Orbit must stay on the screen (cmd = %v, Chosen = %q)", cmd != nil, m.Chosen)
	}
	if clip.Len() != 0 {
		t.Errorf("opening the page wrote to the clipboard: %q", clip.String())
	}
}

func TestSuccessClaim_TerminalAndMenuStillLeave(t *testing.T) {
	m := claimModel(t, claimApp, claimLink, 80, &bytes.Buffer{})
	term, cmd := updateAs(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // Terminal
	if term.Chosen != "terminal" || !isQuit(cmd) {
		t.Errorf("Terminal: Chosen = %q, quit = %v", term.Chosen, isQuit(cmd))
	}
	menu, cmd := updateAs(t, m, key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyDown), key(tea.KeyEnter)) // Menu
	if menu.Chosen != "menu" || cmd != nil {
		t.Errorf("Menu: Chosen = %q, cmd = %v", menu.Chosen, cmd != nil)
	}
}

// Copy claim link writes the link as OSC 52 through the clipboard seam,
// the row says it has copied, and the full link appears beneath the menu.
func TestSuccessClaim_CopyWritesOSC52AndShowsTheLinkBelow(t *testing.T) {
	var clip bytes.Buffer
	m := claimModel(t, claimApp, claimLink, 80, &clip)
	m, _ = updateAs(t, m, key(tea.KeyDown))
	if c := claimCaret(screen(m)); c != "Copy claim link" {
		t.Fatalf("caret on %q after Down, want Copy claim link:\n%s", c, screen(m))
	}
	if clip.Len() != 0 {
		t.Fatal("moving onto Copy claim link wrote to the clipboard")
	}

	m, cmd := updateAs(t, m, key(tea.KeyEnter))
	if cmd == nil {
		t.Fatal("Copy claim link ran nothing")
	}
	cmd()
	if got := decodeOSC52(t, clip.String()); got != claimLink {
		t.Errorf("copied %q, want the claim link %q", got, claimLink)
	}

	s := screen(m)
	if c := claimCaret(s); c != "Copy claim link · copied" {
		t.Errorf("caret row = %q, want it to read %q:\n%s", c, "Copy claim link · copied", s)
	}
	menuAt, linkAt := rowIndex(s, "Menu"), rowIndex(s, claimLink)
	if linkAt < 0 {
		t.Fatalf("the full claim link is not on any row:\n%s", s)
	}
	if linkAt <= menuAt {
		t.Errorf("the link is on row %d, want it beneath the menu (Menu is row %d)", linkAt, menuAt)
	}
}

// --- A link too long for the row -------------------------------------------

// Cut at the measure in whole units: the address on one row, then the
// fragment on the next; never a unit split across rows, no row wider
// than the screen, and the clipboard still gets the whole link.
func TestSuccessClaim_LongLinkIsCutInWholeUnits(t *testing.T) {
	for _, width := range []int{50, 60, 80} {
		t.Run("width "+strconv.Itoa(width), func(t *testing.T) {
			var clip bytes.Buffer
			m := claimModel(t, claimLongApp, claimLongLink, width, &clip)
			m, _ = updateAs(t, m, key(tea.KeyDown))
			m, cmd := updateAs(t, m, key(tea.KeyEnter))
			if cmd == nil {
				t.Fatal("Copy claim link ran nothing")
			}
			cmd()
			if got := decodeOSC52(t, clip.String()); got != claimLongLink {
				t.Errorf("copied %q, want the whole link %q", got, claimLongLink)
			}

			s := screen(m)
			for i, r := range rowsOf(s) {
				if w := lipgloss.Width(r); w > width {
					t.Errorf("row %d is %d cells at width %d: %q", i, w, width, r)
				}
			}
			menuAt := rowIndex(s, "Menu")
			addrAt := -1
			for i, r := range rowsOf(s) {
				if i > menuAt && screenRow(r) == claimLongApp {
					addrAt = i
				}
			}
			fragAt := rowIndex(s, claimLongFrag)
			if addrAt < 0 {
				t.Errorf("the address is not whole on a row beneath the menu:\n%s", s)
			}
			if fragAt < 0 {
				t.Errorf("the %q fragment is not whole on a row:\n%s", claimLongFrag, s)
			}
			if addrAt >= 0 && fragAt >= 0 && fragAt <= addrAt {
				t.Errorf("fragment row %d is not after address row %d", fragAt, addrAt)
			}
			if n := strings.Count(s, "#claim="); n != 1 {
				t.Errorf("%q appears %d times on the screen, want once:\n%s", "#claim=", n, s)
			}
			if strings.Contains(s, "…") || strings.Contains(s, "...") {
				t.Errorf("the link was elided rather than cut in whole units:\n%s", s)
			}
		})
	}
}
