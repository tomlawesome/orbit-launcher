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
