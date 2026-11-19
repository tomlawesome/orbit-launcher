// Command releasereadiness checks a promotion before it opens (#359). STUB.
package main

import (
	"io"
	"os"

	"github.com/tomlawesome/orbit-launcher/tools/releasereadiness/readiness"
)

func main() {
	os.Exit(run(os.Args[1:], func(target, remote string) readiness.Report { return readiness.Report{} }, os.Stdout, os.Stderr))
}

// run is main with its inputs passed in: the arguments, the function that
// runs the checks for a target and remote, and the two output streams. STUB.
func run(args []string, check func(target, remote string) readiness.Report, stdout, stderr io.Writer) int {
	return 0
}
