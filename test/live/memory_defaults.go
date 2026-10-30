package live

import (
	"fmt"
	"sort"
	"strconv"
)

// memoryBytes reads a Compose size such as "2g" or "512m".
func memoryBytes(v string) (uint64, bool) {
	if len(v) < 2 {
		return 0, false
	}
	n, err := strconv.ParseUint(v[:len(v)-1], 10, 64)
	if err != nil {
		return 0, false
	}
	switch v[len(v)-1] {
	case 'k', 'K':
		return n << 10, true
	case 'm', 'M':
		return n << 20, true
	case 'g', 'G':
		return n << 30, true
	}
	return 0, false
}

// memoryDefaultProblems compares what the installed Orbit ships (found, from
// memoryLimitDefaults) with the launcher's table (want) and returns one
// message per disagreement. It is pure so a test can feed it a mismatch.
func memoryDefaultProblems(found map[string][]string, want map[string]uint64) []string {
	var problems []string
	names := make([]string, 0, len(want))
	for name := range want {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		bytes := want[name]
		values := found[name]
		if len(values) == 0 {
			problems = append(problems, fmt.Sprintf("the installed docker-compose.yml and .env-orbit.example name no default for %s; the launcher's table assumes %d bytes", name, bytes))
			continue
		}
		for _, v := range values {
			if got, ok := memoryBytes(v); !ok || got != bytes {
				problems = append(problems, fmt.Sprintf("%s defaults to %q in the installed Orbit; the launcher's table (deploy.MemoryDefaults) has %d bytes", name, v, bytes))
			}
		}
	}
	return problems
}
