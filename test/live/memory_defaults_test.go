package live

import (
	"strings"
	"testing"
)

// Untagged so the merge gate runs it, not only the live legs.
func TestMemoryDefaultProblems(t *testing.T) {
	want := map[string]uint64{"ORBIT_MEMORY_LIMIT": 2 << 30, "TIKA_MEMORY_LIMIT": 1 << 30}
	for _, tc := range []struct {
		name  string
		found map[string][]string
		want  []string // substrings, one per expected problem
	}{
		{"all match", map[string][]string{"ORBIT_MEMORY_LIMIT": {"2g", "2048m"}, "TIKA_MEMORY_LIMIT": {"1g"}}, nil},
		{"mismatch", map[string][]string{"ORBIT_MEMORY_LIMIT": {"3g"}, "TIKA_MEMORY_LIMIT": {"1g"}}, []string{`ORBIT_MEMORY_LIMIT defaults to "3g"`}},
		{"one of two files disagrees", map[string][]string{"ORBIT_MEMORY_LIMIT": {"2g", "1g"}, "TIKA_MEMORY_LIMIT": {"1g"}}, []string{`ORBIT_MEMORY_LIMIT defaults to "1g"`}},
		{"missing name", map[string][]string{"ORBIT_MEMORY_LIMIT": {"2g"}}, []string{"no default for TIKA_MEMORY_LIMIT"}},
		{"bad unit", map[string][]string{"ORBIT_MEMORY_LIMIT": {"2x"}, "TIKA_MEMORY_LIMIT": {"1g"}}, []string{`ORBIT_MEMORY_LIMIT defaults to "2x"`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := memoryDefaultProblems(tc.found, want)
			if len(got) != len(tc.want) {
				t.Fatalf("problems = %q, want %d matching %q", got, len(tc.want), tc.want)
			}
			for i, sub := range tc.want {
				if !strings.Contains(got[i], sub) {
					t.Errorf("problem %d = %q, want it to contain %q", i, got[i], sub)
				}
			}
		})
	}
}
