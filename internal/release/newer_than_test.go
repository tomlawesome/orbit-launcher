package release

import "testing"

// NewerThan claims "a is later than b" only for two plain vX.Y.Z
// versions; any other shape claims no relation (#336).
func TestNewerThan(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"v0.3.7", "v0.3.6", true},
		{"v0.4.0", "v0.3.9", true},
		{"v1.0.0", "v0.99.99", true},
		{"v0.3.10", "v0.3.9", true},
		{"v0.3.6", "v0.3.6", false},
		{"v0.3.5", "v0.3.6", false},
		{"v0.3.6-rc1", "v0.3.5", false},
		{"v0.3.7", "preview", false},
		{"", "v0.3.6", false},
		{"0.3.7", "v0.3.6", false},
	}
	for _, c := range cases {
		if got := NewerThan(c.a, c.b); got != c.want {
			t.Errorf("NewerThan(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
