// Package cli is the command-line skeleton the repository's tools share
// (#199 SU-17): one flag set, one mapping of outcomes to exit codes.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
)

// Run is a tool's whole command bar os.Exit, so a tool's tests need no
// subprocess. It parses args (without the program name) into a flag set
// named name; define registers the tool's flags on it and returns the
// work to do once they are parsed. Flags behave as the default flag
// set's would, usage on stderr. The result is the exit code: 0 for -h or
// when the work succeeds, 2 on a bad flag, 1 when the work fails, its
// error printed on stderr as "name: error".
func Run(name string, args []string, stderr io.Writer, define func(fs *flag.FlagSet) (work func() error)) int {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	work := define(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if err := work(); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", name, err)
		return 1
	}
	return 0
}
