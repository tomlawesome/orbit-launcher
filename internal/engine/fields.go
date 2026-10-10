package engine

import (
	"strconv"
	"strings"
)

// keyValueFields reads the key=value tokens shared by every line
// grammar this package parses — engine events, machine prompts and
// repair output. The value is everything after the first '='; the last
// occurrence of a key wins; an empty value is kept as empty, and each
// parser refuses a line whose required keys are empty. ok is false when
// any token is a bare word or has an empty key: that line is prose, not
// protocol.
func keyValueFields(tokens []string) (map[string]string, bool) {
	fields := make(map[string]string, len(tokens))
	for _, token := range tokens {
		key, value, found := strings.Cut(token, "=")
		if !found || key == "" {
			return nil, false
		}
		fields[key] = value
	}
	return fields, true
}

// nonNegative reads a count field: anything malformed, negative or out
// of range reads as 0.
func nonNegative(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n
}
