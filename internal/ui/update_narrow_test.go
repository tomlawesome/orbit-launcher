package ui

// Pins #274 (Update's confirm screen inside the screen edge), the sibling
// of #271 for Remove. The build spec (#274, note 40964) says, at heights
// of 30:
//
//   - a. at 50 columns no row is wider than the screen, for an ordinary
//     deployment and for a long ORBIT_IMAGE;
//   - b. "The update runs inside the mission console, right here." and
//     "Your existing configuration is preserved. Nothing is deleted."
//     each wrap at spaces on rows of at most 46 cells at 50 columns, and
//     are one row, unchanged, at 80 and 120;
//   - c. the image row keeps "ghcr.io/tomlawesome/orbit:v0.3.0" whole on
//     one row at 50, 80 and 120; an image of 120 characters with no digest
//     is exactly one row of at most min(width-4, 56) cells, ending in the
//     ellipsis;
//   - d. an escape-heavy image is one row within the same limit and no
//     control rune reaches the screen;
//   - e. the c9 golden update_confirm (80 columns) is unchanged.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

// updateNarrowImageLimit is the most cells an image row may take.
func updateNarrowImageLimit(width int) int { return min(width-4, 56) }

// updateNarrowScreen draws Update's confirm screen for d at width x 30.
func updateNarrowScreen(t *testing.T, d *deploy.Deployment, width int) string {
	t.Helper()
	um, _ := newTestUpdateModel(d, engineRunSeams{})
	um, _ = updateAs(t, um, tea.WindowSizeMsg{Width: width, Height: 30})
	if um.state != updateStateConfirm {
		t.Fatalf("update state = %v, want the confirm screen", um.state)
	}
	return screen(um)
}

func updateNarrowDeployment(image string) *deploy.Deployment {
	return &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com", Image: image}
}

// updateNarrowImageRow is the one row holding marker (sky glyphs taken
// off its ends); it fails the test unless exactly one row does.
func updateNarrowImageRow(t *testing.T, s, marker string, width int) string {
	t.Helper()
	var rows []string
	for _, r := range folderRowsOf(s) {
		if strings.Contains(r, marker) {
			rows = append(rows, r)
		}
	}
	if len(rows) != 1 {
		t.Fatalf("at %d columns %d rows hold %q, want exactly 1:\n%s", width, len(rows), marker, s)
	}
	return rows[0]
}

// a. Nothing on Update's confirm screen is wider than the screen.
func TestUpdateNarrow_A_NoRowIsWiderThanTheScreen(t *testing.T) {
	images := map[string]string{
		"ordinary":   "ghcr.io/tomlawesome/orbit:v0.3.0@sha256:0123456789abcdef",
		"long image": "ghcr.io/tomlawesome/" + strings.Repeat("abcdefghij", 10),
	}
	for name, image := range images {
		for _, width := range narrowWidths {
			got := updateNarrowScreen(t, updateNarrowDeployment(image), width)
			for _, r := range strings.Split(got, "\n") {
				if w := lipgloss.Width(r); w > width {
					t.Errorf("%s at %d: a row is %d cells: %q", name, width, w, r)
				}
			}
		}
	}
}

// b. The two prose lines wrap on their own at 50 columns and stay one
// row at 80 and 120.
func TestUpdateNarrow_B_ProseWrapsAt50AndStaysOneRowWider(t *testing.T) {
	screens := map[int]string{}
	for _, width := range narrowWidths {
		screens[width] = updateNarrowScreen(t, updateNarrowDeployment("ghcr.io/tomlawesome/orbit:v0.3.0"), width)
	}
	for _, phrase := range []string{
		"The update runs inside the mission console, right here.",
		"Your existing configuration is preserved. Nothing is deleted.",
	} {
		narrowCheckProse(t, "Update confirm", screens, phrase, true)
	}
}

// c (1). An ordinary image still shows its name whole, on one row.
func TestUpdateNarrow_C_OrdinaryImageNameStaysWholeOnOneRow(t *testing.T) {
	const name = "ghcr.io/tomlawesome/orbit:v0.3.0"
	d := updateNarrowDeployment(name + "@sha256:0123456789abcdef")
	for _, width := range narrowWidths {
		got := updateNarrowScreen(t, d, width)
		row := updateNarrowImageRow(t, got, name, width)
		if w := lipgloss.Width(row); w > updateNarrowImageLimit(width) {
			t.Errorf("at %d columns the image row is %d cells, want at most %d: %q", width, w, updateNarrowImageLimit(width), row)
		}
		if strings.Contains(got, "sha256") {
			t.Errorf("at %d columns the digest must stay cut:\n%s", width, got)
		}
	}
}

// c (2). An image of 120 characters with no digest is one row, end-cut
// with an ellipsis, within the limit.
func TestUpdateNarrow_C_LongImageIsOneRowEndCutWithEllipsis(t *testing.T) {
	image := "ghcr.io/tomlawesome/" + strings.Repeat("abcdefghij", 10)
	if len(image) != 120 {
		t.Fatalf("test image is %d characters, want 120", len(image))
	}
	for _, width := range narrowWidths {
		got := updateNarrowScreen(t, updateNarrowDeployment(image), width)
		row := updateNarrowImageRow(t, got, "ghcr.io/", width)
		if w := lipgloss.Width(row); w > updateNarrowImageLimit(width) {
			t.Errorf("at %d columns the image row is %d cells, want at most %d: %q", width, w, updateNarrowImageLimit(width), row)
		}
		if !strings.HasSuffix(row, "…") {
			t.Errorf("at %d columns the image row %q does not end in the ellipsis", width, row)
		}
		if !strings.HasPrefix(row, "ghcr.io/tomlawesome/abcdefghij") {
			t.Errorf("at %d columns the image row %q lost the start of the image", width, row)
		}
		if n := len(identityRowsWith(got, "abcdefghij")); n != 1 {
			t.Errorf("at %d columns %d rows hold the image text, want 1:\n%s", width, n, got)
		}
	}
}

// d. An escape-heavy image is one row within the limit, and no control
// rune reaches the screen.
func TestUpdateNarrow_D_EscapeHeavyImageIsOneRowAndShowsNoControlRune(t *testing.T) {
	image := "ghcr.io/x/" + strings.Repeat("\x1b", 40)
	for _, width := range narrowWidths {
		got := updateNarrowScreen(t, updateNarrowDeployment(image), width)
		if r := identityStray(got); r != 0 {
			t.Errorf("at %d columns %U reached the screen:\n%q", width, r, got)
		}
		row := updateNarrowImageRow(t, got, "ghcr.io/x/", width)
		if w := lipgloss.Width(row); w > updateNarrowImageLimit(width) {
			t.Errorf("at %d columns the image row is %d cells, want at most %d: %q", width, w, updateNarrowImageLimit(width), row)
		}
		if !strings.HasSuffix(row, "…") {
			t.Errorf("at %d columns the image row %q does not end in the ellipsis", width, row)
		}
		if n := len(identityRowsWith(got, `\x1b`)); n != 1 {
			t.Errorf("at %d columns %d rows show the escapes, want 1:\n%q", width, n, got)
		}
		for _, r := range strings.Split(got, "\n") {
			if w := lipgloss.Width(r); w > width {
				t.Errorf("at %d columns a row is %d cells: %q", width, w, r)
			}
		}
	}
}

// e. The c9 golden for Update's confirm screen at 80 columns is
// unchanged: same deployment, same build as screen_grammar_c9_output_test.
func TestUpdateNarrow_E_C9ConfirmGoldenIsUnchanged(t *testing.T) {
	deployment := &deploy.Deployment{TargetDir: c9LongDir, AppURL: "https://mail.example.com", Image: "registry.example.com/orbit:1.2.3"}
	up := c9Size(t, NewUpdateModel(deployment, deployment.TargetDir, "v1"), 80)
	up.installedAt = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
	up.state = updateStateConfirm
	golden, err := os.ReadFile(filepath.Join("testdata", "c9", "update_confirm.golden"))
	if err != nil {
		t.Fatal(err)
	}
	if got := up.View().Content; got != string(golden) {
		t.Errorf("update_confirm no longer renders as it did:\nwant %q\ngot  %q", string(golden), got)
	}
}
