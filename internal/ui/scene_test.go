package ui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
	"github.com/tomlawesome/orbit-launcher/internal/ui/starfield"
)

// A screen with no size yet has no sky to composite against, so skyBlock
// must degrade to the bare-terminal renderer rather than rasterise a
// zero-width grid.
func TestSkyBlock_DegenerateSizeFallsBackToCentreBlock(t *testing.T) {
	star := starfield.New(0, 0, 1)
	for _, size := range []struct{ width, height int }{{0, 24}, {80, 0}, {0, 0}} {
		got := skyBlock(star, size.width, size.height, "nothing to see")
		want := centreBlock(size.width, size.height, "nothing to see")
		if got != want {
			t.Errorf("skyBlock(%dx%d) = %q, want centreBlock's %q", size.width, size.height, got, want)
		}
	}
}

// The scene screens all render exactly one row per terminal line; a
// flow screen that rendered more would scroll the sky off the top.
func TestSkyBlock_FillsExactlyTheScreenHeight(t *testing.T) {
	const width, height = 80, 24
	star := starfield.New(width, height, 1)

	rows := strings.Split(skyBlock(star, width, height, "Stand down Orbit"), "\n")
	if len(rows) != height {
		t.Errorf("skyBlock rendered %d rows, want %d", len(rows), height)
	}
}

// Every assertion in this package's view tests, in test/pty and in
// test/live matches a substring of one rendered line. Compositing keeps
// the sky in the margins, so those lines must survive intact — if a star
// were ever drawn through the content, every one of those tests would
// start failing for a reason that has nothing to do with what it checks.
func TestSkyBlock_LeavesContentLinesIntact(t *testing.T) {
	const width, height = 80, 24
	star := starfield.New(width, height, 1)
	content := "This stops Orbit and removes its containers\nStand down Orbit"

	got := skyBlock(star, width, height, content)
	for _, line := range strings.Split(content, "\n") {
		if !strings.Contains(got, line) {
			t.Errorf("content line %q was broken up by the sky", line)
		}
	}
}

// skyBlock is centreBlock's grammar over the sky, so a block of the same
// height must land on the same row either way — the flow screens must not
// shift vertically now that they carry a background.
func TestSkyBlock_KeepsCentreBlocksVerticalRhythm(t *testing.T) {
	const width, height = 80, 24
	star := starfield.New(width, height, 1)
	const content = "Ready to install"

	rowOf := func(rendered string) int {
		for i, row := range strings.Split(rendered, "\n") {
			if strings.Contains(row, content) {
				return i
			}
		}
		return -1
	}

	want := rowOf(centreBlock(width, height, content))
	got := rowOf(skyBlock(star, width, height, content))
	if got != want || want < 0 {
		t.Errorf("skyBlock placed the content on row %d, centreBlock on row %d", got, want)
	}
}

// There is exactly one animation tick chain in this application:
// SplashModel.Init starts it and every model re-arms it from its own
// tickMsg branch. AppModel sends a synthetic WindowSizeMsg on every flow
// transition, so a flow model that armed a chain from its resize branch
// would leave two racing — the sky would speed up on every trip through
// the menu, and ORBIT_LAUNCHER_NO_ANIMATION (which starts no chain at
// all) would animate anyway.
func TestFlowModels_ResizeArmsNoTickChain(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	models := map[string]tea.Model{
		"remove":  NewRemoveModel(d),
		"repair":  NewRepairModel("/opt/orbit", "v0.1.0"),
		"install": NewInstallModel("/opt/orbit", "v0.1.0"),
		"update":  NewUpdateModel(d, "/opt/orbit", "v0.1.0"),
	}

	for name, m := range models {
		t.Run(name, func(t *testing.T) {
			resized, cmd := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
			if cmd != nil {
				t.Error("a resize must not start a second tick chain")
			}

			// The chain the application already runs must still be
			// carried forward, or the sky freezes on this screen.
			if _, cmd := resized.Update(tickMsg{}); cmd == nil {
				t.Error("a tick must re-arm the one running chain")
			}
		})
	}
}

// The tick can land before the synthetic resize that follows a flow
// transition, so advancing a sky that was never built must not panic.
func TestFlowModels_TickBeforeFirstResize(t *testing.T) {
	d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}
	for _, m := range []tea.Model{
		NewRemoveModel(d),
		NewRepairModel("/opt/orbit", "v0.1.0"),
		NewInstallModel("/opt/orbit", "v0.1.0"),
		NewUpdateModel(d, "/opt/orbit", "v0.1.0"),
	} {
		updated, _ := m.Update(tickMsg{})
		_ = updated.View()
	}
}
