// No build tag: this file is compiled with and without -tags live, so
// the matcher's own test runs in the ordinary unit suite and the live
// suite picks the matcher up unchanged.

package live

import (
	"bytes"

	expect "github.com/Netflix/go-expect"
)

// matchWindow is how far back a tailMatcher looks on each rune: longer
// than any marker this harness waits for, so a marker that straddles
// two looks is still found. expectAny refuses a longer marker.
const matchWindow = 256

// tailMatcher is the go-expect matcher this harness uses instead of
// expect.String and expect.Regexp (#159, #165, orbit #1004).
//
// go-expect calls Match once per rune read from the pty, handing it the
// whole buffer accumulated since the Expect call began. Its own matchers
// rescan that buffer from the start every time, so draining n bytes
// costs O(n²). Measured on this harness's own pty log: with 70 KB of
// unmatched output one look at acceptMenusUntil's alternation took
// 12.7 ms, so the harness read about 80 bytes a second while the
// launcher repainted 2–3 KB frames far faster than that. The install
// finished, the launcher sat idle on its success screen with the frame
// in the kernel's pty buffer, and the pty log — written rune by rune as
// the harness drains — stopped hundreds of KB earlier at an
// "application starting" frame. That is the freeze #159 and #165
// describe, and its goroutine dumps (no engine goroutines, event loop
// idle for minutes) are what a finished run looks like.
//
// This matcher remembers how much it has seen and looks only at the new
// bytes plus matchWindow of overlap, so each look costs the same
// whatever the run has printed so far. Markers are plain strings, not
// regexps: every wait in this harness is for one of a few fixed
// phrases, and bytes.Contains is two orders of magnitude cheaper per
// look than a regexp alternation over the same window.
type tailMatcher struct {
	markers []string
	scanned int
}

func (m *tailMatcher) Match(v any) bool {
	buf, ok := v.(*bytes.Buffer)
	if !ok {
		return false
	}
	b := buf.Bytes()
	from := min(max(m.scanned-matchWindow, 0), len(b))
	m.scanned = len(b)
	window := b[from:]
	for _, marker := range m.markers {
		if bytes.Contains(window, []byte(marker)) {
			return true
		}
	}
	return false
}

func (m *tailMatcher) Criteria() any { return m.markers }

// expectAny waits for the first of markers to appear, like
// expect.String(markers...) but without the rescan. As with go-expect's
// own matchers, the returned output is everything read since the Expect
// began; a caller that needs to know which marker matched checks the
// output, as acceptMenusUntil does.
func expectAny(markers ...string) expect.ExpectOpt {
	for _, marker := range markers {
		if len(marker) > matchWindow {
			panic("live harness: marker longer than matchWindow: " + marker)
		}
	}
	return func(opts *expect.ExpectOpts) error {
		opts.Matchers = append(opts.Matchers, &tailMatcher{markers: markers})
		return nil
	}
}
