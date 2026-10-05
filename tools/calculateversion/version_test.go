package main

import "testing"

func TestParseStableTag(t *testing.T) {
	tests := []struct {
		tag    string
		want   Version
		wantOK bool
	}{
		{"v1.2.3", Version{1, 2, 3}, true},
		{"v0.1.0", Version{0, 1, 0}, true},
		{"v1.2.3-preview.4", Version{}, false},
		{"preview", Version{}, false},
		{"latest", Version{}, false},
		{"1.2.3", Version{}, false}, // missing "v" prefix
	}
	for _, tt := range tests {
		got, ok := ParseStableTag(tt.tag)
		if ok != tt.wantOK || got != tt.want {
			t.Errorf("ParseStableTag(%q) = %v, %v; want %v, %v", tt.tag, got, ok, tt.want, tt.wantOK)
		}
	}
}

func TestNextVersion_NoStableTags(t *testing.T) {
	got := NextVersion([]string{"preview", "not-a-tag"}, false)
	if got.String() != "0.1.0" {
		t.Errorf("NextVersion() with no stable tags = %v, want 0.1.0", got)
	}
}

func TestNextVersion_OrdinaryTrainIncrementsMinor(t *testing.T) {
	got := NextVersion([]string{"v0.1.0", "v0.2.0", "preview"}, false)
	if got.String() != "0.3.0" {
		t.Errorf("NextVersion() = %v, want 0.3.0", got)
	}
}

func TestNextVersion_HotfixIncrementsPatch(t *testing.T) {
	got := NextVersion([]string{"v1.4.0"}, true)
	if got.String() != "1.4.1" {
		t.Errorf("NextVersion(hotfix) = %v, want 1.4.1", got)
	}
}

func TestNextVersion_IgnoresHigherPatchOnLowerMinor(t *testing.T) {
	// v0.2.0 is the highest MINOR even though v0.1.99 has a higher PATCH —
	// semver ordering must compare major.minor.patch as a whole, not just
	// whichever tag sorts last lexically.
	got := NextVersion([]string{"v0.1.99", "v0.2.0"}, false)
	if got.String() != "0.3.0" {
		t.Errorf("NextVersion() = %v, want 0.3.0", got)
	}
}

// A component too large for an int matches the tag pattern's digits but is
// not a version anyone can increment, so it must be ignored rather than
// silently wrapped or truncated.
func TestParseStableTagRejectsComponentsThatOverflow(t *testing.T) {
	const huge = "99999999999999999999"
	for _, tag := range []string{
		"v" + huge + ".0.0",
		"v0." + huge + ".0",
		"v0.0." + huge,
	} {
		if got, ok := ParseStableTag(tag); ok {
			t.Errorf("ParseStableTag(%q) = %v, true; want it rejected", tag, got)
		}
	}
}

func TestHighestStableReportsWhetherAnyStableTagExists(t *testing.T) {
	if _, found := HighestStable([]string{"preview", "v1.0.0-rc.1"}); found {
		t.Error("HighestStable found a stable tag among only non-stable ones")
	}
	if _, found := HighestStable(nil); found {
		t.Error("HighestStable found a stable tag in an empty list")
	}
}

// The ordering must compare major first, then minor, then patch, whatever
// order the tags arrive in.
func TestHighestStableOrdersByMajorThenMinorThenPatch(t *testing.T) {
	for _, c := range []struct {
		tags []string
		want string
	}{
		{[]string{"v2.0.0", "v1.9.9"}, "2.0.0"},
		{[]string{"v1.9.9", "v2.0.0"}, "2.0.0"},
		{[]string{"v1.10.0", "v1.9.0"}, "1.10.0"}, // numeric, not lexical
		{[]string{"v1.2.3", "v1.2.10", "v1.2.4"}, "1.2.10"},
		{[]string{"v1.2.10", "v1.2.3"}, "1.2.10"},
	} {
		got, found := HighestStable(c.tags)
		if !found || got.String() != c.want {
			t.Errorf("HighestStable(%q) = %v, %v; want %s, true", c.tags, got, found, c.want)
		}
	}
}

func TestNextVersion_OrdinaryTrainResetsPatch(t *testing.T) {
	got := NextVersion([]string{"v1.4.7"}, false)
	if got.String() != "1.5.0" {
		t.Errorf("NextVersion() = %v, want 1.5.0", got)
	}
}

func TestNextVersion_HotfixBeforeAnyStableTagIsTheBaseline(t *testing.T) {
	got := NextVersion(nil, true)
	if got.String() != "0.1.0" {
		t.Errorf("NextVersion(nil, hotfix) = %v, want 0.1.0", got)
	}
}
