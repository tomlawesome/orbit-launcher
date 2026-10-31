package deploy

import "context"

// RunningApp is the deployment's app container as Docker reports it.
// stub for #316's tests; the build replaces it
type RunningApp struct {
	Found   bool
	Image   string
	Version string
	Folder  string
	State   string
}

// RunningAppOf reads the deployment's app container from Docker.
// stub for #316's tests; the build replaces it
func RunningAppOf(ctx context.Context, d *Deployment) (RunningApp, error) {
	return RunningApp{}, nil
}

// MismatchKind says how the app container contradicts the record.
// stub for #316's tests; the build replaces it
type MismatchKind int

// stub for #316's tests; the build replaces it
const (
	MismatchNone MismatchKind = iota
	MismatchFolder
	MismatchBuild
)

// Mismatch is what CompareRunning found.
// stub for #316's tests; the build replaces it
type Mismatch struct {
	Kind              MismatchKind
	Folder            string
	Started, Recorded string
}

// CompareRunning compares the app container with the record in dir.
// stub for #316's tests; the build replaces it
func CompareRunning(d *Deployment, dir string, r RunningApp) Mismatch {
	return Mismatch{}
}
