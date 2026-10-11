package release

import (
	"strings"
	"testing"
)

// Moved from cmd/orbit-launcher/main_test.go with displayVersion itself
// (#212, #199 EN-13).

func TestDisplayVersion(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty is dev", "", "dev"},
		{"literal dev stays dev", "dev", "dev"},
		{"bare version gets v-prefixed", "1.2.3", "v1.2.3"},
		{"already v-prefixed is untouched", "v1.2.3", "v1.2.3"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := DisplayVersion(c.in); got != c.want {
				t.Errorf("DisplayVersion(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

func TestDisplayVersion_NeverEmpty(t *testing.T) {
	// The version foot must always show something; an empty string
	// there would be silently wrong, not visibly broken, so it is worth
	// a dedicated assertion.
	for _, in := range []string{"", "dev", "v0.0.0", "1.0.0"} {
		if got := DisplayVersion(in); strings.TrimSpace(got) == "" {
			t.Errorf("DisplayVersion(%q) returned a blank string", in)
		}
	}
}
