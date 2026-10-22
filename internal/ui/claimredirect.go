package ui

import (
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// claimRedirect is the page "Get into Orbit" hands the browser opener in
// place of the claim link (#277): a command line is readable by every
// local account, and the link carries the one-time code. It is the
// redirect file Jupyter opens for its token URL: a small HTML page that
// sends the browser on to the link, in a directory of its own under
// TMPDIR. os.MkdirTemp makes that directory 0700 and os.CreateTemp makes
// the file 0600 as it creates it (O_EXCL), so neither is ever readable by
// another account, and the opener is handed only the file's path. It
// lasts while the launcher runs, so going back to the menu keeps it, and
// remove takes the directory and everything in it when the launcher
// quits. One value is shared by every copy of the screen holding it (a
// pointer), so whichever copy quits removes what an earlier copy wrote.
type claimRedirect struct {
	mu   sync.Mutex
	dir  string
	path string
}

// write puts a page redirecting to link in the private directory,
// making the directory on first use and replacing any earlier page, and
// returns the page's absolute path.
func (r *claimRedirect) write(link string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir == "" {
		// MkdirTemp would make a relative TMPDIR's directory relative,
		// and the opener needs a path that names the file wherever it
		// runs.
		tmp, err := filepath.Abs(os.TempDir())
		if err != nil {
			return "", err
		}
		dir, err := os.MkdirTemp(tmp, "orbit-launcher-claim-")
		if err != nil {
			return "", err
		}
		r.dir = dir
	}
	if r.path != "" {
		_ = os.Remove(r.path)
		r.path = ""
	}
	f, err := os.CreateTemp(r.dir, "claim-*.html")
	if err != nil {
		return "", err
	}
	_, werr := f.WriteString(redirectPage(link))
	cerr := f.Close()
	if werr == nil {
		werr = cerr
	}
	if werr != nil {
		_ = os.Remove(f.Name())
		return "", werr
	}
	r.path = f.Name()
	return r.path, nil
}

// remove deletes the private directory and the page in it. Safe on a
// nil value and when nothing was written.
func (r *claimRedirect) remove() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.dir != "" {
		_ = os.RemoveAll(r.dir)
	}
	r.dir, r.path = "", ""
}

// redirectPage is the page itself: a meta refresh to link, with a plain
// link for a browser that ignores it. link is HTML-escaped, so a quote or
// angle bracket in it cannot end the attribute or add markup.
func redirectPage(link string) string {
	l := html.EscapeString(link)
	return fmt.Sprintf(`<!DOCTYPE html>
<html><head><meta charset="utf-8"><meta http-equiv="refresh" content="0;url=%s"><title>Orbit</title></head>
<body><p><a href="%s">Continue to Orbit</a></p></body></html>
`, l, l)
}

// quits reports whether cmd is tea.Quit itself, the way every screen
// here ends the launcher. A func value cannot be compared or called
// without running it, so its code pointer is compared instead.
func quits(cmd tea.Cmd) bool {
	return cmd != nil && reflect.ValueOf(cmd).Pointer() == reflect.ValueOf(tea.Quit).Pointer()
}
