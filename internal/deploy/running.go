package deploy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// The labels Compose and the image put on the app container. Compose
// stamps the first three on everything it starts; the last is the OCI
// image label for the version the image was built as.
const (
	labelComposeProject    = "com.docker.compose.project"
	labelComposeService    = "com.docker.compose.service"
	labelComposeWorkingDir = "com.docker.compose.project.working_dir"
	labelImageVersion      = "org.opencontainers.image.version"
)

// RunningAppTimeout is the limit RunningAppOf puts on both its Docker
// calls together; a caller that waits on it waits no longer.
const RunningAppTimeout = dockerQueryTimeout

// appService is the Compose service orbit's app container runs as. The
// container is found by this label, never by its container_name.
const appService = "orbit-app"

// containerIDPattern is what `docker ps --no-trunc` prints for a container.
var containerIDPattern = regexp.MustCompile(`^[0-9a-f]+$`)

// RunningApp is the deployment's app container as Docker reports it.
type RunningApp struct {
	// Found is true when exactly one orbit-app container exists in the
	// project, running or not.
	Found bool
	// Image is the reference the container was created from.
	Image string
	// Version is the image's version label, empty when it has none.
	Version string
	// Folder is the working directory Compose was started from, empty
	// when the container carries no such label.
	Folder string
	// State is Docker's state word: running, exited, ...
	State string
}

// RunningAppOf reads the deployment's app container from Docker: the
// one Compose started for service orbit-app in d's project, found by
// label and looked at with `ps -a`, so a stopped container is read too
// (as the installer's ownership proof reads it).
//
// No container is an answer (Found false), not an error. Everything else
// that stops the question being answered is an error: no docker on PATH,
// no reachable daemon, no answer within dockerQueryTimeout (one limit
// over both calls), more than one app container, output that does not
// parse. The caller turns every error into silence (#316).
func RunningAppOf(ctx context.Context, d *Deployment) (RunningApp, error) {
	if d == nil || d.Project == "" {
		return RunningApp{}, errors.New("no deployment to look for")
	}
	ctx, cancel := context.WithTimeout(ctx, RunningAppTimeout)
	defer cancel()

	// The working_dir label is printed JSON-quoted, as in
	// UnownedDatabaseVolumes: a label can hold a newline, and printed raw
	// it would start a line that reads like another container (#277).
	// The ID and the state cannot hold a tab or a newline.
	ps, err := dockerCommand(ctx, "ps", "-a", "--no-trunc",
		"--filter", "label="+labelComposeProject+"="+d.Project,
		"--filter", "label="+labelComposeService+"="+appService,
		"--format", `{{.ID}}	{{json (.Label "`+labelComposeWorkingDir+`")}}	{{.State}}`).Output()
	if err != nil {
		return RunningApp{}, fmt.Errorf("list the app container: %w", err)
	}

	var found []RunningApp
	var id string
	for _, line := range strings.Split(string(ps), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 || !containerIDPattern.MatchString(fields[0]) {
			return RunningApp{}, errors.New("unreadable container list")
		}
		id = fields[0]
		found = append(found, RunningApp{
			Found:  true,
			Folder: labelValue(fields[1]),
			State:  strings.TrimSpace(fields[2]),
		})
	}
	switch len(found) {
	case 0:
		return RunningApp{}, nil
	case 1:
	default:
		return RunningApp{}, fmt.Errorf("%d app containers in project %s", len(found), d.Project)
	}

	inspect, err := dockerCommand(ctx, "inspect",
		"--format", `{{json .Config.Image}}	{{json (index .Config.Labels "`+labelImageVersion+`")}}`, id).Output()
	if err != nil {
		return RunningApp{}, fmt.Errorf("inspect the app container: %w", err)
	}
	image, version, ok := strings.Cut(strings.TrimRight(string(inspect), "\r\n"), "\t")
	app := found[0]
	if app.Image = labelValue(image); !ok || app.Image == "" {
		return RunningApp{}, errors.New("unreadable container description")
	}
	app.Version = labelValue(version)
	return app, nil
}

// MismatchKind says how the app container contradicts the record.
type MismatchKind int

// The kinds of contradiction. MismatchNone is the zero value, so a
// verdict nobody set says nothing.
const (
	MismatchNone MismatchKind = iota
	// MismatchFolder: the container was started from another folder.
	MismatchFolder
	// MismatchBuild: this folder's container is not the build the
	// record names.
	MismatchBuild
)

// Mismatch is what CompareRunning found.
type Mismatch struct {
	Kind MismatchKind
	// Folder is the other folder (MismatchFolder).
	Folder string
	// Started and Recorded name the two builds (MismatchBuild):
	// Recorded is empty when the record holds no applied version.
	Started, Recorded string
}

// CompareRunning says whether the app container r contradicts the record
// d in dir, the folder the launcher was started in. Another folder wins
// over another build: a build started elsewhere is not compared with
// this record.
func CompareRunning(d *Deployment, dir string, r RunningApp) Mismatch {
	if d == nil || !r.Found {
		return Mismatch{}
	}
	if r.Folder != "" && !sameFolder(r.Folder, dir) {
		return Mismatch{Kind: MismatchFolder, Folder: r.Folder}
	}
	// The image check is the exact comparison the installer's ownership
	// proof makes, so a row here predicts its refusal. A record naming no
	// image has nothing to differ from.
	imageDiffers := d.Image != "" && r.Image != d.Image
	versionDiffers := r.Version != "" && d.Version != "" && r.Version != d.Version
	if !imageDiffers && !versionDiffers {
		return Mismatch{}
	}
	started := r.Version
	if started == "" {
		started, _, _ = strings.Cut(r.Image, "@")
	}
	return Mismatch{Kind: MismatchBuild, Started: started, Recorded: d.Version}
}

// sameFolder reports whether a and b name one folder: equal once
// cleaned, or equal once their symbolic links are resolved. A folder
// that cannot be resolved (it is gone) is not shown to be the other.
func sameFolder(a, b string) bool {
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// OtherProjectApp is an Orbit app container that belongs to a Compose
// project other than the one an install here would use (#321).
type OtherProjectApp struct {
	// Found is true when exactly one such container exists, running or
	// not.
	Found bool
	// Project is the Compose project it belongs to.
	Project string
	// Folder is the working directory Compose was started from, empty
	// when the container carries no such label.
	Folder string
	// State is Docker's state word: running, exited, ...
	State string
}

// OtherFolder is the folder to name for the container: Folder when it is
// set and is not dir, else "". The caller compares nothing itself. An
// empty answer means the screen names the project: the label is missing,
// or the container was started from dir itself under another project
// name, where "that folder" would point here.
func (a OtherProjectApp) OtherFolder(dir string) string {
	if a.Folder == "" || sameFolder(a.Folder, dir) {
		return ""
	}
	return a.Folder
}

// OtherProjectAppOf asks Docker for Orbit app containers under any
// Compose project but project, found by the service label and read with
// `ps -a`, so a stopped container counts: Docker's names are unique over
// stopped containers too, and Compose fails on them just the same.
//
// Docker has no not-equal filter, so every project's app container is
// asked for and project's own are dropped here: they are the
// same-project clash's (#318). None left is an answer (Found false). Two
// or more is an error: two Orbit stacks under two other projects cannot
// both hold the fixed container names, so one is not an Orbit as
// shipped, and naming either would be a guess. Everything else that
// stops the question being answered is an error too (no docker on PATH,
// no daemon, no answer within RunningAppTimeout, a line that does not
// parse, an empty project label or argument). The caller turns every
// error into silence.
func OtherProjectAppOf(ctx context.Context, project string) (OtherProjectApp, error) {
	if project == "" {
		return OtherProjectApp{}, errors.New("no project to look past")
	}
	ctx, cancel := context.WithTimeout(ctx, RunningAppTimeout)
	defer cancel()

	// Both labels are printed JSON-quoted, as RunningAppOf's is: a label
	// can hold a newline, and printed raw it would start a line that
	// reads like another container (#277).
	ps, err := dockerCommand(ctx, "ps", "-a", "--no-trunc",
		"--filter", "label="+labelComposeService+"="+appService,
		"--filter", "label="+labelComposeProject,
		"--format", `{{.ID}}`+"\t"+`{{json (.Label "`+labelComposeProject+`")}}`+"\t"+
			`{{json (.Label "`+labelComposeWorkingDir+`")}}`+"\t"+`{{.State}}`).Output()
	if err != nil {
		return OtherProjectApp{}, fmt.Errorf("list the app containers: %w", err)
	}

	var found []OtherProjectApp
	for _, line := range strings.Split(string(ps), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 || !containerIDPattern.MatchString(fields[0]) {
			return OtherProjectApp{}, errors.New("unreadable container list")
		}
		other := labelValue(fields[1])
		if other == "" {
			return OtherProjectApp{}, errors.New("an app container with no project label")
		}
		if other == project {
			continue
		}
		found = append(found, OtherProjectApp{
			Found:   true,
			Project: other,
			Folder:  labelValue(fields[2]),
			State:   strings.TrimSpace(fields[3]),
		})
	}
	switch len(found) {
	case 0:
		return OtherProjectApp{}, nil
	case 1:
		return found[0], nil
	}
	return OtherProjectApp{}, fmt.Errorf("%d app containers under projects other than %s", len(found), project)
}
