package readiness

// Step is one counted line of a release record.
type Step struct {
	N        int
	Status   string
	Evidence string
}

// Record is a parsed docs/releases/vX.Y.Z.md.
type Record struct {
	Version   string
	GuideBlob string
	Steps     []Step   // valid lines only
	Signers   []string // the value of each Signed-off-by: line
	Problems  []string // lines that do not count, each saying why
}

// ParseGuideSteps lists the numbered steps under "## What you do, in short".
// STUB.
func ParseGuideSteps(guide string) ([]string, error) { return nil, nil }

// ParseRecord parses a release record. STUB.
func ParseRecord(src string) Record { return Record{} }
