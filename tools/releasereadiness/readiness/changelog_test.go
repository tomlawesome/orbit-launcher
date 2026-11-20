package readiness

import (
	"strings"
	"testing"
)

const clHead = "# Changelog\n\nAll notable changes.\n\n"

func problemsText(p []string) string { return strings.Join(p, "\n") }

func TestChangelog_AFilledVersionSectionAndAnEmptyUnreleasedPasses(t *testing.T) {
	src := clHead + "## [Unreleased]\n\n## [0.4.7] - 2026-11-20\n\n### Fixed\n\n- A thing. (#354)\n\n## [0.4.6] - 2026-10-01\n\n### Added\n\n- Old.\n"
	v, problems := CheckChangelog(src)
	if v != "0.4.7" {
		t.Errorf("version = %q, want 0.4.7", v)
	}
	if len(problems) != 0 {
		t.Errorf("unexpected problems: %v", problems)
	}
}

func TestChangelog_OnlyUnreleasedFails(t *testing.T) {
	src := clHead + "## [Unreleased]\n\n### Added\n\n- Pending.\n"
	v, problems := CheckChangelog(src)
	if v != "" {
		t.Errorf("version = %q, want none", v)
	}
	if len(problems) == 0 {
		t.Error("a changelog with only Unreleased must fail")
	}
}

func TestChangelog_AnEmptyVersionSectionFails(t *testing.T) {
	for name, section := range map[string]string{
		"nothing":                "",
		"heading but no entries": "### Fixed\n\n",
	} {
		src := clHead + "## [Unreleased]\n\n## [0.4.7] - 2026-11-20\n\n" + section + "## [0.4.6] - 2026-10-01\n\n### Added\n\n- Old.\n"
		_, problems := CheckChangelog(src)
		if len(problems) == 0 {
			t.Errorf("%s: an empty section must fail", name)
		}
	}
}

func TestChangelog_AnEntryUnderANonKeepAChangelogHeadingDoesNotCount(t *testing.T) {
	src := clHead + "## [0.4.7] - 2026-11-20\n\n### Misc\n\n- Not a Keep a Changelog heading.\n"
	if _, problems := CheckChangelog(src); len(problems) == 0 {
		t.Error("an entry under ### Misc must not make the section non-empty")
	}
}

func TestChangelog_EveryKeepAChangelogHeadingCounts(t *testing.T) {
	for _, h := range []string{"Added", "Changed", "Deprecated", "Removed", "Fixed", "Security"} {
		src := clHead + "## [0.4.7] - 2026-11-20\n\n### " + h + "\n\n- Entry.\n"
		v, problems := CheckChangelog(src)
		if v != "0.4.7" || len(problems) != 0 {
			t.Errorf("### %s: version %q problems %v", h, v, problems)
		}
	}
}

func TestChangelog_AHeadingWithoutADateOrOfTheWrongShapeFails(t *testing.T) {
	for _, h := range []string{
		"## [0.4.7]",
		"## [0.4.7] - ",
		"## 0.4.7 - 2026-11-20",
		"## [0.4.7] 2026-11-20",
		"## [v0.4.7] - 2026-11-20",
		"## [0.4.7] - 20-11-2026",
		"## [0.4] - 2026-11-20",
	} {
		src := clHead + "## [Unreleased]\n\n" + h + "\n\n### Fixed\n\n- Entry.\n"
		v, problems := CheckChangelog(src)
		if len(problems) == 0 {
			t.Errorf("%q: must fail (version read: %q)", h, v)
		}
	}
}

func TestChangelog_AnUnreleasedSectionWithEntriesFailsInEitherSpelling(t *testing.T) {
	for _, h := range []string{"## [Unreleased]", "## Unreleased"} {
		src := clHead + h + "\n\n### Added\n\n- Not shipped yet.\n\n## [0.4.7] - 2026-11-20\n\n### Fixed\n\n- Shipped.\n"
		_, problems := CheckChangelog(src)
		if len(problems) == 0 || !strings.Contains(problemsText(problems), "Unreleased still holds entries") {
			t.Errorf("%q with entries: problems %v, want one saying Unreleased still holds entries", h, problems)
		}
	}
}

func TestChangelog_TheNewestReleaseHeadingIsTheOneRead(t *testing.T) {
	src := clHead + "## [Unreleased]\n\n## [0.5.0] - 2026-12-01\n\n### Added\n\n- New.\n\n## [0.4.7] - 2026-11-20\n\n### Fixed\n\n- Older.\n"
	if v, _ := CheckChangelog(src); v != "0.5.0" {
		t.Errorf("version = %q, want the first (newest) release heading 0.5.0", v)
	}
}
