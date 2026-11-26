package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/engine"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
	"github.com/tomlawesome/orbit-launcher/internal/ui/style"
)

// Maintenance (#242): start and end Orbit's maintenance mode from the
// launcher. The flow runs the deployment's own scripts/start-maintenance.sh
// and end-maintenance.sh the way Repair runs repair.sh and reads the one
// `maintenance …` outcome line plus the exit code, never prose.

const maintLinesKept = 40

const (
	defaultMaintenanceNotice = "Orbit is closed for maintenance and will be back soon."
	maxMaintenanceNotice     = 500
	maxMaintenanceLength     = 7 * 24 * time.Hour

	maintenanceDBHint = "Orbit's database must be running — Repair shows whether it is."
	badEndTime        = "That isn't a length or a time — try 45m, 2h or 22:30"
)

type maintState int

const (
	maintChoose maintState = iota
	maintNotice
	maintWhen
	maintConfirm
	maintReopen
	maintWorking
	maintDone
	maintNotFound // no deployment, or the engine's exit 5
	maintNewer    // an Orbit without the maintenance scripts
)

type prepareMaintenanceFunc func(ctx context.Context, targetDir string, start bool, notice, until string) (*engine.Stream, error)

// MaintenanceModel is the Maintenance flow.
type MaintenanceModel struct {
	d         *deploy.Deployment
	targetDir string
	version   string
	detectErr error

	state         maintState
	width, height int
	star          starfield.Model
	frame         int
	menu          menu

	notice, when inputLine
	refusal      string

	// what the person asked for, kept for Try again
	start       bool
	noticeText  string
	until       time.Time
	untilArg    string
	health      deploy.HealthReason
	healthKnown bool

	lines      []string
	outcome    string // the first outcome line, kept outside the line cap
	stderrTail []string
	runErr     error
	result     engine.MaintenanceOutcome
	canClose   bool // start-maintenance.sh is present
	canReopen  bool // end-maintenance.sh is present
	// exitMismatch: the line said success but the exit code did not.
	exitMismatch bool
	signalQuit   bool

	zone     *time.Location
	zoneName string
	probe    func(ctx context.Context, healthURL string) deploy.HealthReason
	send     func(tea.Msg)

	Done      bool
	WantsMenu bool

	engineRunSeams
}

type (
	maintHealthMsg struct{ reason deploy.HealthReason }
	maintReadyMsg  struct {
		stream *engine.Stream
		err    error
	}
	maintStreamMsg      struct{ msg any }
	maintStreamEndedMsg struct {
		stream *engine.Stream
		reason error
	}
)

var maintStream = streamWrap{
	msg:   func(msg any) tea.Msg { return maintStreamMsg{msg: msg} },
	ended: func(s *engine.Stream, reason error) tea.Msg { return maintStreamEndedMsg{stream: s, reason: reason} },
}

// NewMaintenanceModel constructs the flow. A nil deployment is the
// no-deployment screen; one whose folder lacks the scripts is "needs a
// newer Orbit".
func NewMaintenanceModel(d *deploy.Deployment, targetDir, version string) MaintenanceModel {
	m := MaintenanceModel{d: d, targetDir: targetDir, version: version, zone: time.Local, zoneName: serverZoneName()}
	m.canClose, m.canReopen = deploy.MaintenanceScripts(targetDir)
	m.menu = newMenu(m.chooseRows()...)
	switch {
	case d == nil:
		m.state, m.menu = maintNotFound, newMenu(deadEndRows...)
	case !m.canClose && !m.canReopen:
		m.state, m.menu = maintNewer, newMenu(maintOKRows...)
	}
	return m
}

// chooseRows offers each action only when its script is present.
func (m MaintenanceModel) chooseRows() []string {
	var rows []string
	if m.canClose {
		rows = append(rows, "Close Orbit for maintenance")
	}
	if m.canReopen {
		rows = append(rows, "Reopen Orbit")
	}
	return append(rows, "Cancel")
}

var (
	maintConfirmRows = []string{"Close Orbit", "Cancel"}
	maintReopenRows  = []string{"Reopen Orbit", "Cancel"}
	maintOKRows      = []string{"Menu", "Exit"}
	maintFailRows    = []string{"Try again", "Menu", "Exit"}
)

// Outcome surfaces the flow result to AppModel.
func (m MaintenanceModel) Outcome() flowOutcome {
	return flowOutcome{Done: m.Done, WantsMenu: m.WantsMenu}
}

func (m MaintenanceModel) nowTime() time.Time {
	if m.now != nil {
		return m.now()
	}
	return time.Now()
}

func (m MaintenanceModel) loc() *time.Location {
	if m.zone != nil {
		return m.zone
	}
	return time.Local
}

// Init asks the deployment's health once, for the status line.
func (m MaintenanceModel) Init() tea.Cmd {
	if m.state != maintChoose || m.probe == nil || m.d == nil {
		return nil
	}
	url, err := deploy.LocalHealthURL(m.d)
	if err != nil {
		return nil
	}
	probe := m.probe
	return func() tea.Msg { return maintHealthMsg{reason: probe(context.Background(), url)} }
}

// Update implements tea.Model.
func (m MaintenanceModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if _, ok := msg.(SignalQuitMsg); ok {
		if m.state == maintWorking {
			// One database write is under way: wait for it, then quit.
			m.signalQuit = true
			return m, nil
		}
		return m, tea.Quit
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.star = newSky(msg.Width, msg.Height)
		return m, nil
	case tickMsg:
		m.star = m.star.Advance()
		m.frame = nextFrame(m.frame)
		return m, tick()
	case maintHealthMsg:
		m.health, m.healthKnown = msg.reason, true
		return m, nil
	case maintStreamMsg:
		if m.state != maintWorking {
			return m, nil
		}
		if raw, ok := msg.msg.(engine.RawLineMsg); ok {
			if _, ok := engine.ParseMaintenanceOutcome(raw.Text); ok && m.outcome == "" {
				m.outcome = raw.Text
			}
			m.lines = append(m.lines, raw.Text)
			if len(m.lines) > maintLinesKept {
				m.lines = append([]string(nil), m.lines[len(m.lines)-maintLinesKept:]...)
			}
		}
		if done, ok := msg.msg.(engine.DoneMsg); ok {
			return m.finish(done)
		}
		return m, nil
	case maintStreamEndedMsg:
		// The reader could not read the run at all (no sender wired).
		if msg.reason != nil && m.state == maintWorking {
			return m.failed(msg.reason), m.quitIfSignalled()
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.handleKey(msg)
	case tea.PasteMsg:
		switch m.state {
		case maintNotice:
			m.notice, m.refusal = m.notice.paste(msg.Content), ""
		case maintWhen:
			m.when, m.refusal = m.when.paste(msg.Content), ""
		}
		return m, nil
	}
	return m, nil
}

func (m MaintenanceModel) quitIfSignalled() tea.Cmd {
	if m.signalQuit {
		return tea.Quit
	}
	return nil
}

// finish reads the run's end: the outcome line and the exit code.
func (m MaintenanceModel) finish(done engine.DoneMsg) (tea.Model, tea.Cmd) {
	m.stderrTail = done.StderrTail
	if done.ExitCode == engine.ExitNotInstalled {
		m.state, m.menu = maintNotFound, newMenu(deadEndRows...)
		return m, m.quitIfSignalled()
	}
	exit := done.ExitCode
	if done.Err != nil && exit == 0 {
		exit = engine.MaintenanceExitFailed
	}
	lines := m.lines
	if m.outcome != "" {
		lines = []string{m.outcome}
	}
	m.result = engine.ResolveMaintenance(lines, exit)
	m.exitMismatch = exit != 0 && !m.failedResultOf(engine.ResolveMaintenance(lines, 0))
	m.state, m.runErr = maintDone, nil
	m.menu = newMenu(maintOKRows...)
	if m.failedResult() {
		m.menu = newMenu(maintFailRows...)
	}
	return m, m.quitIfSignalled()
}

func (m MaintenanceModel) failed(err error) MaintenanceModel {
	switch {
	case errors.Is(err, deploy.ErrNoDeployment):
		m.state, m.menu = maintNotFound, newMenu(deadEndRows...)
		return m
	case errors.Is(err, deploy.ErrMaintenanceUnavailable):
		m.state, m.menu = maintNewer, newMenu(maintOKRows...)
		return m
	}
	m.runErr = err
	m.result = engine.ResolveMaintenance(nil, engine.MaintenanceExitFailed)
	m.state, m.menu = maintDone, newMenu(maintFailRows...)
	return m
}

func (m MaintenanceModel) failedResult() bool { return m.failedResultOf(m.result) }

func (m MaintenanceModel) failedResultOf(r engine.MaintenanceOutcome) bool {
	switch r.Result {
	case "started", "already-active", "ended", "not-active":
		return false
	}
	return true
}

func (m MaintenanceModel) leave() (tea.Model, tea.Cmd) {
	m.Done, m.WantsMenu = true, true
	return m, nil
}

func (m MaintenanceModel) goTo(s maintState, rows []string) MaintenanceModel {
	m.state, m.menu, m.refusal = s, newMenu(rows...), ""
	return m
}

func (m MaintenanceModel) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if m.state == maintWorking {
		// A single database write: Ctrl-C and Esc do nothing.
		return m, nil
	}
	if isCtrlC(msg) {
		return m, tea.Quit
	}
	switch m.state {
	case maintNotice, maintWhen:
		return m.handleTyping(msg)
	}
	next, chosen, back := m.menu.onScreen(msg)
	m.menu = next
	if back {
		return m.back()
	}
	switch chosen {
	case "":
		return m, nil
	case "Menu", "Cancel":
		return m.leave()
	case "Exit":
		return m, tea.Quit
	case "Close Orbit for maintenance":
		return m.goTo(maintNotice, nil), nil
	case "Reopen Orbit":
		if m.state == maintChoose {
			return m.goTo(maintReopen, maintReopenRows), nil
		}
		return m.run(false)
	case "Close Orbit":
		return m.run(true)
	case "Try again":
		return m.run(m.start)
	}
	return m, nil
}

// back is Esc: one step back, the menu from the first screen.
func (m MaintenanceModel) back() (tea.Model, tea.Cmd) {
	switch m.state {
	case maintNotice, maintReopen:
		return m.goTo(maintChoose, m.chooseRows()), nil
	case maintWhen:
		return m.goTo(maintNotice, nil), nil
	case maintConfirm:
		return m.goTo(maintWhen, nil), nil
	}
	return m.leave()
}

func (m MaintenanceModel) handleTyping(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Code {
	case tea.KeyEsc:
		return m.back()
	case tea.KeyEnter:
		return m.submit()
	}
	line := &m.notice
	if m.state == maintWhen {
		line = &m.when
	}
	edited, _ := line.edit(msg)
	*line = edited
	m.refusal = ""
	return m, nil
}

// submit takes the typed line on Enter, or refuses it inline.
func (m MaintenanceModel) submit() (tea.Model, tea.Cmd) {
	if m.state == maintNotice {
		text := strings.TrimSpace(m.notice.String())
		switch {
		case utf8.RuneCountInString(text) > maxMaintenanceNotice:
			m.refusal = fmt.Sprintf("That is over %d characters.", maxMaintenanceNotice)
			return m, nil
		case strings.IndexFunc(text, unicode.IsControl) >= 0:
			m.refusal = "A notice can't hold control characters."
			return m, nil
		}
		if text == "" {
			text = defaultMaintenanceNotice
		}
		m.noticeText = text
		return m.goTo(maintWhen, nil), nil
	}
	now := m.nowTime()
	until, ok := parseMaintenanceEnd(m.when.String(), now, m.loc())
	if !ok {
		m.refusal = badEndTime
		return m, nil
	}
	m.until, m.untilArg = until, ""
	if !until.IsZero() {
		m.untilArg = until.UTC().Format(time.RFC3339)
	}
	return m.goTo(maintConfirm, maintConfirmRows), nil
}

// run starts the script for the stored request.
func (m MaintenanceModel) run(start bool) (tea.Model, tea.Cmd) {
	m.start = start
	m.state, m.menu = maintWorking, menu{}
	m.lines, m.outcome, m.stderrTail, m.runErr = nil, "", nil, nil
	prepare := m.prepareMaintenance
	if prepare == nil {
		prepare = defaultPrepareMaintenance
	}
	notice, until := m.noticeText, m.untilArg
	if start && notice == "" {
		notice = defaultMaintenanceNotice
	}
	if !start {
		notice, until = "", ""
	}
	// Started here, not in a command: preparing is a few file checks and a
	// process start, and the run is under way before this Update returns,
	// whichever keys follow.
	stream, err := prepare(context.Background(), m.targetDir, start, notice, until)
	if err != nil {
		return m.failed(err), m.quitIfSignalled()
	}
	if msg := readEngineStream(m.send, stream, maintStream)(); msg != nil {
		return m.Update(msg)
	}
	return m, nil
}

func defaultPrepareMaintenance(_ context.Context, targetDir string, start bool, notice, until string) (*engine.Stream, error) {
	cmd, err := deploy.MaintenanceCommand(targetDir, start, notice, until)
	if err != nil {
		return nil, err
	}
	return engine.Start(cmd)
}

// ---- time ----

var (
	// Each unit at most once, in d, h, m order.
	lengthPattern = regexp.MustCompile(`^(?:([0-9]{1,5})d)?(?:([0-9]{1,5})h)?(?:([0-9]{1,5})m)?$`)
	clockPattern  = regexp.MustCompile(`^([0-9]{1,2}):([0-9]{2})$`)
)

// parseMaintenanceEnd reads the end-time answer: empty (no end), a length
// between 1m and 7d, or HH:MM in loc, tomorrow's date if not after now.
func parseMaintenanceEnd(input string, now time.Time, loc *time.Location) (time.Time, bool) {
	s := strings.TrimSpace(input)
	if s == "" {
		return time.Time{}, true
	}
	if parts := lengthPattern.FindStringSubmatch(s); parts != nil {
		var d time.Duration
		for i, unit := range []time.Duration{24 * time.Hour, time.Hour, time.Minute} {
			if parts[i+1] == "" {
				continue
			}
			n, _ := strconv.Atoi(parts[i+1])
			// Each part is at most 99999 units, so this cannot overflow.
			d += time.Duration(n) * unit
			if d > maxMaintenanceLength {
				return time.Time{}, false
			}
		}
		if d < time.Minute {
			return time.Time{}, false
		}
		return now.Add(d), true
	}
	if c := clockPattern.FindStringSubmatch(s); c != nil {
		h, _ := strconv.Atoi(c[1])
		mi, _ := strconv.Atoi(c[2])
		if h > 23 || mi > 59 {
			return time.Time{}, false
		}
		local := now.In(loc)
		until := time.Date(local.Year(), local.Month(), local.Day(), h, mi, 0, 0, loc)
		if !until.After(now) {
			until = time.Date(local.Year(), local.Month(), local.Day()+1, h, mi, 0, 0, loc)
		}
		return until, true
	}
	return time.Time{}, false
}

// utcOffset reads an offset in seconds as UTC+1, UTC-3 or UTC+5:30.
func utcOffset(t time.Time) string {
	_, off := t.Zone()
	sign := "+"
	if off < 0 {
		sign, off = "-", -off
	}
	out := fmt.Sprintf("UTC%s%d", sign, off/3600)
	if mins := off % 3600 / 60; mins != 0 {
		out += fmt.Sprintf(":%02d", mins)
	}
	return out
}

// maintenanceZoneLine is the zone and current time the end-time screen
// always shows; now is already in the server zone.
func maintenanceZoneLine(now time.Time, zoneName string) string {
	abbr, _ := now.Zone()
	zone := utcOffset(now)
	if zoneName != "" {
		zone = fmt.Sprintf("%s (%s, %s)", zoneName, abbr, utcOffset(now))
	}
	return "Server time zone: " + zone + style.Separator + "now " + now.Format("15:04")
}

// maintenanceBackBy is the confirm screen's full end; until is already in
// the server zone.
func maintenanceBackBy(until time.Time, zoneName string) string {
	label := zoneName
	if label == "" {
		label = utcOffset(until)
	}
	return "Back by " + until.Format("Monday 2 January, 15:04") + " " + label + " (server time)"
}

// serverZoneName is the IANA name of the server's zone: TZ (a leading
// colon dropped), else /etc/localtime's target, else "".
func serverZoneName() string {
	valid := func(name string) bool {
		if name == "" || name == "Local" {
			return false
		}
		_, err := time.LoadLocation(name)
		return err == nil
	}
	if tz, ok := os.LookupEnv("TZ"); ok {
		tz = strings.TrimPrefix(tz, ":")
		if _, after, found := strings.Cut(tz, "zoneinfo/"); found {
			tz = after
		}
		if valid(tz) {
			return tz
		}
		return ""
	}
	if target, err := os.Readlink("/etc/localtime"); err == nil {
		if _, after, found := strings.Cut(target, "zoneinfo/"); found && valid(after) {
			return after
		}
	}
	return ""
}

// ---- views ----

// View implements tea.Model.
func (m MaintenanceModel) View() tea.View { return tea.NewView(m.view()) }

func (m MaintenanceModel) view() string {
	if m.width == 0 {
		return ""
	}
	switch m.state {
	case maintNotFound:
		if m.detectErr != nil {
			return viewUnreadableDeployment(m.star, m.width, m.height, m.targetDir, m.detectErr)
		}
		return viewNoDeployment(m.star, m.width, m.height, m.targetDir,
			"There's nothing to put into maintenance in this directory —",
			"Install is the way to get into Orbit first.")
	}
	var b strings.Builder
	limit, edge := measure(m.width, errorWidth), textArea(m.width)
	prose := func(ink lipgloss.Style, text string) { writeWrappedIn(&b, ink, text, limit, edge) }
	mark := style.AccentText.Render(style.SymbolMark)
	identity := func() {
		fmt.Fprintln(&b, style.Ink.Render(identityLine(m.d, time.Time{}, limit)))
	}

	switch m.state {
	case maintNewer:
		writeHeader(&b, mark, "Maintenance needs a newer Orbit")
		if m.canReopen {
			prose(style.MutedText, "This Orbit release doesn't ship its maintenance commands yet. Update brings them. Until then this reopens Orbit from the deployment folder:")
			fmt.Fprintln(&b)
			writeCommandBox(&b, commandBox("bash scripts/end-maintenance.sh", m.width))
		} else {
			prose(style.MutedText, "This Orbit release doesn't ship its maintenance commands yet. Update brings them.")
		}
		fmt.Fprintln(&b)
		writeStackedMenu(&b, maintOKRows, 0)
	case maintChoose:
		writeHeader(&b, mark, "Maintenance")
		identity()
		switch {
		case !m.healthKnown:
		case m.health == deploy.HealthMaintenance:
			prose(style.MutedText, "Orbit is closed to users right now")
		case m.health == deploy.HealthResponding:
			prose(style.MutedText, "Orbit is open to users")
		}
		fmt.Fprintln(&b)
		m.menu.write(&b)
	case maintNotice:
		writeHeader(&b, mark, "What should people see?")
		prose(style.MutedText, "One short notice, shown on the maintenance page. Leave it empty for: "+defaultMaintenanceNotice)
		fmt.Fprintln(&b)
		m.writeInput(&b, m.notice)
	case maintWhen:
		writeHeader(&b, mark, "When will Orbit be back?")
		prose(style.MutedText, "A length like 45m or 2h, or a time like 22:30. Leave it empty if you can't say.")
		fmt.Fprintln(&b, style.Tagline.Render(maintenanceZoneLine(m.nowTime().In(m.loc()), m.zoneName)))
		fmt.Fprintln(&b)
		m.writeInput(&b, m.when)
	case maintConfirm:
		writeHeader(&b, mark, "Close Orbit for maintenance?")
		identity()
		fmt.Fprintln(&b)
		for _, row := range fitTail([]string{"Notice: " + m.noticeText}, limit, edge, 5) {
			fmt.Fprintln(&b, style.Ink.Render(row))
		}
		if m.until.IsZero() {
			fmt.Fprintln(&b, style.Ink.Render("No end time stated"))
		} else {
			prose(style.Ink, maintenanceBackBy(m.until.In(m.loc()), m.zoneName))
		}
		fmt.Fprintln(&b)
		prose(style.MutedText, "Everyone but administrators sees the maintenance page from now. Administrators can still sign in; the sign-in page stays open.")
		fmt.Fprintln(&b)
		m.menu.write(&b)
	case maintReopen:
		writeHeader(&b, mark, "Reopen Orbit?")
		identity()
		fmt.Fprintln(&b)
		prose(style.MutedText, "Ends maintenance now. Users get back in straight away; a scheduled notice already due is cancelled as well.")
		fmt.Fprintln(&b)
		m.menu.write(&b)
	case maintWorking:
		text := "closing Orbit for maintenance"
		if !m.start {
			text = "reopening Orbit"
		}
		writeHeader(&b, mark, "Maintenance")
		fmt.Fprintln(&b, workingLine(m.frame, text, "a few seconds; Orbit's database must be running"))
	case maintDone:
		m.writeOutcome(&b, limit, edge)
	}
	return skyBlock(m.star, m.width, m.height, b.String())
}

// writeInput draws the typed line as the configuration prompt does: a
// caret, the text, the cursor; an inline refusal beneath.
func (m MaintenanceModel) writeInput(b *strings.Builder, line inputLine) {
	caret := style.MenuCaret.Render(style.SymbolSelected) + " "
	cursor := style.AccentText.Render(style.SymbolCursor)
	room := max(textArea(m.width)-lipgloss.Width(caret)-lipgloss.Width(cursor), 0)
	before, after := line.around(room)
	row := caret + style.Ink.Render(before) + cursor
	if after != "" {
		row += style.Ink.Render(after)
	}
	fmt.Fprintln(b, row)
	if m.refusal != "" {
		fmt.Fprintln(b)
		writeWrappedIn(b, style.DegradedText, m.refusal, measure(m.width, errorWidth), textArea(m.width))
	}
}

func (m MaintenanceModel) writeOutcome(b *strings.Builder, limit, edge int) {
	prose := func(ink lipgloss.Style, text string) { writeWrappedIn(b, ink, text, limit, edge) }
	r := m.result
	switch {
	case m.failedResult():
		title := "Couldn't close Orbit"
		if !m.start {
			title = "Couldn't reopen Orbit"
		}
		writeInlineHeader(b, style.ErrorText.Render(style.SymbolFailure), title)
		if m.runErr != nil {
			prose(style.MutedText, errorWords(m.runErr))
			break
		}
		if !r.Reported {
			prose(style.MutedText, "Orbit didn't report an outcome")
		}
		room := max(m.height-18, 3)
		for _, line := range fitTail(m.stderrTail, limit, edge, room) {
			fmt.Fprintln(b, style.Tagline.Render(line))
		}
		switch r.Reason {
		case "", "database-unavailable":
			switch {
			case m.exitMismatch:
				prose(style.MutedText, "Orbit's report and its exit status disagree, so this isn't treated as done.")
			case r.Reported:
				prose(style.MutedText, maintenanceDBHint)
			}
		case "notice-invalid":
			prose(style.MutedText, "Orbit refused that notice.")
		case "until-invalid":
			prose(style.MutedText, "Orbit refused that end time.")
		case "until-in-past":
			prose(style.MutedText, "That end time has already passed.")
		}
	case r.Result == "started":
		writeInlineHeader(b, style.SuccessText.Render(style.SymbolSuccess), "Orbit is closed for maintenance")
		prose(style.MutedText, "Visitors see the maintenance page; administrators can still sign in. Reopen from this menu or Orbit's Administration screen.")
	case r.Result == "already-active":
		writeInlineHeader(b, style.AccentText.Render(style.SymbolMark), "Orbit was already closed for maintenance")
		prose(style.MutedText, "Nothing changed; the notice and end time you entered were not applied.")
	case r.Result == "ended":
		writeInlineHeader(b, style.SuccessText.Render(style.SymbolSuccess), "Orbit is open again")
		prose(style.MutedText, "Users are back in.")
		switch {
		case r.Cancelled == 1:
			prose(style.MutedText, "1 due scheduled notice was cancelled too.")
		case r.Cancelled > 1:
			prose(style.MutedText, fmt.Sprintf("%d due scheduled notices were cancelled too.", r.Cancelled))
		}
	default: // not-active
		writeInlineHeader(b, style.AccentText.Render(style.SymbolMark), "Maintenance was not active")
		prose(style.MutedText, "Orbit was already open; nothing changed.")
	}
	fmt.Fprintln(b)
	m.menu.write(b)
}
