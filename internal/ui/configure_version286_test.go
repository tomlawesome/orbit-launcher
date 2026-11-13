package ui

// #286 review finding B (owner answer 5a, note 44241): the confirm
// screen's own line carries the release Update's confirm would name,
// when the launcher's lookup (WithTargetLookup, #336) knows it.

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/tomlawesome/orbit-launcher/internal/release"
)

// cfg286NoUnknownVersion is the version-like check: a version-like text on
// the screen is allowed only when it is the one the lookup gave (shown as
// Update shows it); known is "" when the lookup gave none.
func cfg286NoUnknownVersion(t *testing.T, s, known string) {
	t.Helper()
	rest := s
	if known != "" {
		rest = strings.ReplaceAll(s, shownVersion(known), "")
	}
	if v := cfg286VersionLike.FindString(rest); v != "" {
		t.Errorf("the screen shows a version-like %q the launcher was not given:\n%s", v, s)
	}
}

func cfg286WithLookup(m AppModel, target release.Target, err error, calls *int) AppModel {
	return m.WithTargetLookup(func(context.Context) (release.Target, error) {
		if calls != nil {
			*calls++
		}
		return target, err
	})
}

var cfg286Directions = []struct{ profiles, to string }{{"-", "Documents"}, {"processing", "Standard"}}

func TestConfigure286Version_ConfirmLineCarriesTheKnownRelease(t *testing.T) {
	for _, version := range []string{"v0.3.6", "v0.3.6-rc.12345678"} {
		for _, c := range cfg286Directions {
			t.Run(version+" to "+c.to, func(t *testing.T) {
				m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), c.profiles, true))
				calls := 0
				m = cfg286WithLookup(m, release.Target{Version: version, Pin: true}, nil, &calls)
				m = cfg286ToConfirm(t, m, c.to)
				s := screen(m)

				want := "This also updates Orbit to its newest release, " + shownVersion(version) + "."
				at := cfg286RowIndex(s, want)
				if at < 0 {
					t.Fatalf("%q is not on a row of its own:\n%s", want, s)
				}
				if change := cfg286RowIndex(s, "Change to "+c.to); change < 0 || at >= change {
					t.Errorf("the line (row %d) is not above the Change row (row %d):\n%s", at, change, s)
				}
				if calls == 0 {
					t.Errorf("Configure never used the target lookup")
				}
				cfg286NoUnknownVersion(t, s, version)
			})
		}
	}
}

func TestConfigure286Version_FailedLookupLeavesTheLineAsItWas(t *testing.T) {
	for _, c := range cfg286Directions {
		t.Run("to "+c.to, func(t *testing.T) {
			m, _ := cfg286App(t, cfg286Folder(t, shortTempDir(t), c.profiles, true))
			calls := 0
			m = cfg286WithLookup(m, release.Target{}, errors.New("manifest unreachable"), &calls)
			m = cfg286ToConfirm(t, m, c.to)
			s := screen(m)

			if cfg286RowIndex(s, cfg286AlsoLine) < 0 {
				t.Errorf("%q is not on a row of its own:\n%s", cfg286AlsoLine, s)
			}
			if calls == 0 {
				t.Errorf("Configure never used the target lookup, so the failure was not exercised")
			}
			cfg286NoUnknownVersion(t, s, "")
		})
	}
}
