package ui

// Pins #214 (identity wording; #199 WI-10, UI-3): one deploymentIdentity
// in installedat.go names a deployment on the splash, on the Remove and
// Update confirm screens and in Update's title. A deployment whose
// .env-orbit has no APP_URL is "deployment detected" everywhere; the
// confirm screens used to say "no deployment details found" and Update's
// title was a bare "Update".

import (
	"go/ast"
	"go/token"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/tomlawesome/orbit-launcher/internal/deploy"
)

const (
	wi10Detected = "deployment detected"
	wi10NoDetail = "no deployment details found"
)

// wi10Compact drops blank rows, so a failure prints the words, not 26 rows.
func wi10Compact(s string) string {
	var rows []string
	for _, r := range strings.Split(s, "\n") {
		if strings.TrimSpace(r) != "" {
			rows = append(rows, strings.TrimRight(r, " "))
		}
	}
	return strings.Join(rows, "\n")
}

// wi10IdentityRow is the row under the screen's heading line.
func wi10IdentityRow(compact string) string {
	rows := strings.Split(compact, "\n")
	for i, r := range rows {
		if strings.Contains(r, "this deployment") || strings.Contains(r, "removes its containers") {
			if i+1 < len(rows) {
				return strings.TrimSpace(rows[i+1])
			}
		}
	}
	return ""
}

func wi10Header(compact string) string {
	rows := strings.Split(compact, "\n")
	if len(rows) == 0 {
		return ""
	}
	return strings.TrimSpace(rows[0])
}

func wi10NoURL() *deploy.Deployment { return &deploy.Deployment{TargetDir: "/opt/orbit"} }

func wi10UpdateModel(d *deploy.Deployment) UpdateModel {
	m := NewUpdateModel(d, "/opt/orbit", "v9.9.9")
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(UpdateModel)
}

func wi10RemoveModel(d *deploy.Deployment) RemoveModel {
	m := NewRemoveModel(d)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	return updated.(RemoveModel)
}

func TestIdentity_ConfirmScreensNameADeploymentWithoutAURLAsDetected(t *testing.T) {
	screens := map[string]string{
		"update": wi10Compact(wi10Plain(wi10UpdateModel(wi10NoURL()).View().Content)),
		"remove": wi10Compact(wi10Plain(wi10RemoveModel(wi10NoURL()).View().Content)),
	}
	for name, s := range screens {
		if !strings.Contains(s, wi10Detected) {
			t.Errorf("%s confirm screen lacks %q; its identity row reads %q", name, wi10Detected, wi10IdentityRow(s))
		}
		if strings.Contains(s, wi10NoDetail) {
			t.Errorf("%s confirm screen says %q for a deployment that was found", name, wi10NoDetail)
		}
	}
}

// Passed already: with a URL both screens name the host as written.
func TestIdentity_ConfirmScreensKeepTheHostWhenThereIsOne(t *testing.T) {
	for url, want := range map[string]string{"https://mail.example.com": "mail.example.com", "orbit.lan": "orbit.lan"} {
		d := &deploy.Deployment{TargetDir: "/opt/orbit", AppURL: url}
		for name, s := range map[string]string{
			"update": wi10Compact(wi10Plain(wi10UpdateModel(d).View().Content)),
			"remove": wi10Compact(wi10Plain(wi10RemoveModel(d).View().Content)),
		} {
			if !strings.Contains(s, want) || strings.Contains(s, wi10Detected) || strings.Contains(s, wi10NoDetail) {
				t.Errorf("%s screen for %q does not name just %q; its identity row reads %q", name, url, want, wi10IdentityRow(s))
			}
		}
	}
}

// Update's title on the run screen carries the identity after a dash.
// With a URL it already does (Passed already); without one it was bare.
func TestIdentity_UpdateTitleNamesTheDeployment(t *testing.T) {
	cases := map[*deploy.Deployment]string{
		{TargetDir: "/opt/orbit", AppURL: "https://mail.example.com"}: "Update — mail.example.com",
		wi10NoURL(): "Update — " + wi10Detected,
	}
	for d, want := range cases {
		updated, _ := wi10UpdateModel(d).Update(key(tea.KeyEnter)) // Update Orbit
		if got := updated.(UpdateModel); got.state != updateStateRunning {
			t.Fatalf("state = %v, want updateStateRunning", got.state)
		}
		if s := wi10Compact(wi10Plain(updated.View().Content)); !strings.Contains(s, want) {
			t.Errorf("run screen for %+v lacks the title %q; its header reads %q", d, want, wi10Header(s))
		}
	}
}

// The splash identity goes through the same function: Passed already
// for both wordings (TestAppModel_DeploymentWithoutAProperURLStillShowsSomething
// holds the full table).
func TestIdentity_SplashNamesADeploymentWithoutAURLAsDetected(t *testing.T) {
	dir := t.TempDir()
	writeEnv(t, dir, "ORBIT_IMAGE=x\n")
	m := NewAppModelNoAnimation()
	m.targetDir = dir
	m = m.WithDeploymentStatus(nil)
	if m.splash.fqdn != wi10Detected {
		t.Errorf("splash identity = %q, want %q", m.splash.fqdn, wi10Detected)
	}
}

// One function, and each wording is typed once, inside it.
func TestIdentity_OneFunctionInInstalledatOwnsTheWording(t *testing.T) {
	var top []string
	for _, f := range uiSourceFiles(t) {
		if !strings.Contains(f, "/") {
			top = append(top, f)
		}
	}
	at := wi10Declared(t, top, "deploymentIdentity")
	if len(at) != 1 || !strings.HasPrefix(at[0], "installedat.go:") {
		t.Errorf("deploymentIdentity declared at %v, want once, in installedat.go", at)
	}
	for _, phrase := range []string{wi10Detected, wi10NoDetail} {
		var where []string
		for _, name := range top {
			fset, f := wi10Parse(t, name)
			ast.Inspect(f, func(n ast.Node) bool {
				lit, ok := n.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				if s, err := strconv.Unquote(lit.Value); err == nil && strings.Contains(strings.ToLower(s), phrase) {
					where = append(where, fset.Position(lit.Pos()).String())
				}
				return true
			})
		}
		if len(where) != 1 || !strings.HasPrefix(where[0], "installedat.go:") {
			t.Errorf("%q is written at %v, want once, in installedat.go", phrase, where)
		}
	}
	for _, name := range []string{"app.go", "update.go"} {
		if _, f := wi10Parse(t, name); !wi10Calls(f, "deploymentIdentity") {
			t.Errorf("%s does not call deploymentIdentity", name)
		}
	}
}
