package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/tomlawesome/orbit-launcher/tools/releasereadiness/readiness"
)

// lookupConfig configures the real lookups.
type lookupConfig struct {
	Dir          string        // the repository
	Remote       string        // e.g. origin
	Target       string        // preview or main; its ref is <Remote>/<Target>
	Source       string        // the source commit; default HEAD
	GitBin       string        // default git
	GitTimeout   time.Duration // limit on every git call
	HTTPTimeout  time.Duration // limit on every HTTP call, body included
	MaxBody      int64         // cap on an HTTP body, in bytes
	GitLabAPI    string        // the project's API base, e.g. https://host/api/v4/projects/12
	GitLabToken  string
	NoIssuesWhy  string // why issues cannot be read when GitLabAPI is empty
	OrbitRepoURL string // git URL of the Orbit mirror
	OrbitBranch  string // overrides the branch orbit/pin.json names
}

const (
	defaultGitTimeout  = time.Minute
	defaultHTTPTimeout = 20 * time.Second
	defaultMaxBody     = 1 << 20
	maxGitOutput       = 64 << 20
)

type realLookups struct {
	cfg      lookupConfig
	http     *http.Client
	fetchErr error
}

// prepareLookups is newLookups plus, when fetch is set, the fetch of the
// remote's tags into the snapshot the tag checks read. A failed fetch is
// kept and fails only those checks. CI fetches before it drops the job's
// credentials and passes -no-fetch (fetch false), because by then origin
// is anonymous and the project refuses anonymous reads.
func prepareLookups(cfg lookupConfig, fetch bool) *realLookups {
	l := newLookups(cfg)
	if fetch {
		l.fetchErr = l.fetchTags()
	}
	return l
}

// tagSnapshot is where the remote's tags are copied: its own namespace, so a
// tag made only in this checkout never counts as the remote's.
func tagSnapshot(remote string) string { return "refs/remote-tags/" + remote + "/" }

// tagFetchCommand is the fetch that writes the snapshot, as a person types it.
func tagFetchCommand(remote string) string {
	return fmt.Sprintf("git fetch --force --prune %s '+refs/tags/*:%s*'", remote, tagSnapshot(remote))
}

func (l *realLookups) fetchTags() error {
	_, err := l.git("", []string{"fetch", "--quiet", "--force", "--prune", "--no-tags", l.cfg.Remote, "+refs/tags/*:" + tagSnapshot(l.cfg.Remote) + "*"})
	if err != nil {
		return fmt.Errorf("could not fetch the tags from %s (%v); run %s and then the tool with -no-fetch", l.cfg.Remote, err, tagFetchCommand(l.cfg.Remote))
	}
	// A remote with no tags leaves the snapshot empty; the marker says the
	// fetch happened, so that reads as "no tags", not "never fetched".
	if _, err := l.git("", []string{"update-ref", fetchedMarker(l.cfg.Remote), l.cfg.Source}); err != nil {
		return fmt.Errorf("could not record the tag fetch: %v", err)
	}
	return nil
}

// fetchedMarker is the ref the tool's own tag fetch leaves behind.
func fetchedMarker(remote string) string { return "refs/remote-tags-fetched/" + remote }

func newLookups(cfg lookupConfig) *realLookups {
	if cfg.GitBin == "" {
		cfg.GitBin = "git"
	}
	if cfg.Source == "" {
		cfg.Source = "HEAD"
	}
	if cfg.GitTimeout <= 0 {
		cfg.GitTimeout = defaultGitTimeout
	}
	if cfg.HTTPTimeout <= 0 {
		cfg.HTTPTimeout = defaultHTTPTimeout
	}
	if cfg.MaxBody <= 0 {
		cfg.MaxBody = defaultMaxBody
	}
	return &realLookups{cfg: cfg, http: &http.Client{
		// A redirect would carry the token header to wherever it points.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

var _ readiness.Lookups = (*realLookups)(nil)

// ---- git --------------------------------------------------------------------

// childEnvNames is all of the environment a git child sees: no token, no
// CI_* variable, nothing the job holds beyond what git itself needs.
var childEnvNames = []string{"PATH", "HOME", "USER", "TMPDIR", "XDG_CONFIG_HOME", "SSH_AUTH_SOCK", "GIT_SSH_COMMAND",
	"SSL_CERT_FILE", "SSL_CERT_DIR", "HTTP_PROXY", "HTTPS_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "no_proxy"}

func childEnv() []string {
	env := []string{"LC_ALL=C", "GIT_TERMINAL_PROMPT=0"}
	for _, n := range childEnvNames {
		if v, ok := os.LookupEnv(n); ok {
			env = append(env, n+"="+v)
		}
	}
	return env
}

// capWriter keeps at most max bytes and remembers that it dropped some.
type capWriter struct {
	buf  bytes.Buffer
	max  int
	over bool
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - w.buf.Len(); len(p) > room {
		w.over = true
		if room > 0 {
			w.buf.Write(p[:room])
		}
		return len(p), nil
	}
	return w.buf.Write(p)
}

var userinfo = regexp.MustCompile(`://[^/@\s]+@`)

// gitResult is a finished git call: its output and exit code.
type gitResult struct {
	out  string
	code int
}

// git runs one git command under the time limit. ok lists the exit codes
// that are answers rather than failures (0 always is).
func (l *realLookups) git(stdin string, args []string, ok ...int) (gitResult, error) {
	name := "git " + args[0]
	for i := 0; i < len(args); i++ {
		if args[i] == "-c" {
			i++
			continue
		}
		name = "git " + args[i]
		break
	}
	ctx, cancel := context.WithTimeout(context.Background(), l.cfg.GitTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, l.cfg.GitBin, args...) //nolint:gosec // G204: the git binary with argument lists this tool builds itself; no shell.
	cmd.Dir = l.cfg.Dir
	cmd.Env = childEnv()
	// A child that outlives git holds the pipes open; stop waiting for it.
	cmd.WaitDelay = time.Second
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	stdout := &capWriter{max: maxGitOutput}
	stderr := &capWriter{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	err := cmd.Run()
	if ctx.Err() != nil {
		return gitResult{}, fmt.Errorf("%s: no answer within %s, the limit on every git call", name, l.cfg.GitTimeout)
	}
	if stdout.over {
		return gitResult{}, fmt.Errorf("%s: output over the %d-byte cap", name, maxGitOutput)
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return gitResult{}, fmt.Errorf("%s: %v", name, err)
		}
		code = exit.ExitCode()
	}
	if code != 0 && !contains(ok, code) {
		return gitResult{}, fmt.Errorf("%s failed: %s", name, said(stderr.buf.String(), code))
	}
	return gitResult{out: stdout.buf.String(), code: code}, nil
}

func contains(list []int, v int) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// said picks git's own error line, without any credential in a URL.
func said(stderr string, code int) string {
	var last string
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "fatal:") || strings.HasPrefix(line, "error:") {
			return userinfo.ReplaceAllString(line, "://")
		}
		last = line
	}
	if last == "" {
		return "exit " + strconv.Itoa(code)
	}
	return userinfo.ReplaceAllString(last, "://")
}

func (l *realLookups) gitOut(args ...string) (string, error) {
	r, err := l.git("", args)
	return strings.TrimSpace(r.out), err
}

// targetRef is the remote-tracking ref of the target, which must have been
// fetched.
func (l *realLookups) targetRef() (string, error) {
	ref := "refs/remotes/" + l.cfg.Remote + "/" + l.cfg.Target
	r, err := l.git("", []string{"rev-parse", "--verify", "--quiet", ref + "^{commit}"}, 1)
	if err != nil {
		return "", err
	}
	if r.code != 0 {
		return "", fmt.Errorf("%s/%s is not in this checkout: run git fetch %s %s first", l.cfg.Remote, l.cfg.Target, l.cfg.Remote, l.cfg.Target)
	}
	return ref, nil
}

// remoteTags lists the remote's tags from the fetched snapshot. No snapshot
// at all means nothing was fetched, which is an error, never "no tags".
func (l *realLookups) remoteTags() (map[string]bool, error) {
	if l.fetchErr != nil {
		return nil, l.fetchErr
	}
	ns := tagSnapshot(l.cfg.Remote)
	out, err := l.gitOut("for-each-ref", "--format=%(refname)", ns)
	if err != nil {
		return nil, err
	}
	tags := map[string]bool{}
	for _, ref := range strings.Fields(out) {
		tags[strings.TrimPrefix(ref, ns)] = true
	}
	if len(tags) == 0 && !l.fetchedBefore() {
		return nil, fmt.Errorf("the remote's tags were never fetched into this checkout: run %s, or run the tool without -no-fetch", tagFetchCommand(l.cfg.Remote))
	}
	return tags, nil
}

func (l *realLookups) fetchedBefore() bool {
	r, err := l.git("", []string{"rev-parse", "--verify", "--quiet", fetchedMarker(l.cfg.Remote)}, 1)
	return err == nil && r.code == 0
}

// Tags lists the remote's tags, from the snapshot, so neither a stale nor a
// local-only tag can pick the version.
func (l *realLookups) Tags() ([]string, error) {
	set, err := l.remoteTags()
	if err != nil {
		return nil, err
	}
	var tags []string
	for t := range set {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return tags, nil
}

func (l *realLookups) RemoteTagExists(tag string) (bool, error) {
	set, err := l.remoteTags()
	if err != nil {
		return false, err
	}
	return set[tag], nil
}

func (l *realLookups) SourceFile(path string) (string, bool, error) {
	ls, err := l.gitOut("ls-tree", l.cfg.Source, "--", path)
	if err != nil {
		return "", false, err
	}
	if ls == "" {
		return "", false, nil
	}
	r, err := l.git("", []string{"cat-file", "blob", l.cfg.Source + ":" + path})
	if err != nil {
		return "", false, err
	}
	return r.out, true, nil
}

func (l *realLookups) GuideBlob() (string, error) {
	return l.gitOut("rev-parse", "--verify", l.cfg.Source+":docs/releasing.md")
}

// RecordSignoff: exactly one Signed-off-by line in the record, a name with
// no email, equal to the author of the last commit touching the record and
// to one of that commit's Signed-off-by trailers.
func (l *realLookups) RecordSignoff(path string) (readiness.Signoff, error) {
	src, found, err := l.SourceFile(path)
	if err != nil {
		return readiness.Signoff{}, err
	}
	if !found {
		return readiness.Signoff{Why: path + " is missing"}, nil
	}
	signers := readiness.ParseRecord(src).Signers
	switch len(signers) {
	case 0:
		return readiness.Signoff{Why: "the record has no Signed-off-by line"}, nil
	case 1:
	default:
		return readiness.Signoff{Why: fmt.Sprintf("the record has %d Signed-off-by lines; want exactly one", len(signers))}, nil
	}
	line := signers[0]
	if strings.ContainsAny(line, "@<>") {
		return readiness.Signoff{Why: "the Signed-off-by line holds an email address: a name only, no email"}, nil
	}
	out, err := l.gitOut("log", "-1", "--format=%an%x00%(trailers:key=Signed-off-by,valueonly,separator=%x01)", l.cfg.Source, "--", path)
	if err != nil {
		return readiness.Signoff{}, err
	}
	if out == "" {
		return readiness.Signoff{Why: "no commit touches " + path}, nil
	}
	author, trailers, _ := strings.Cut(out, "\x00")
	if line != author {
		return readiness.Signoff{Why: fmt.Sprintf("the Signed-off-by line names %q, but the commit's author is %q", line, author)}, nil
	}
	for _, t := range strings.Split(trailers, "\x01") {
		if strings.TrimSpace(t) == line {
			return readiness.Signoff{Signed: true}, nil
		}
	}
	if strings.TrimSpace(trailers) == "" {
		return readiness.Signoff{Why: "the last commit touching the record has no Signed-off-by trailer"}, nil
	}
	return readiness.Signoff{Why: fmt.Sprintf("no Signed-off-by trailer of the last commit touching the record names %q", line)}, nil
}

// isAncestor runs merge-base --is-ancestor: exit 0 yes, 1 no.
func (l *realLookups) isAncestor(a, b string) (bool, error) {
	r, err := l.git("", []string{"merge-base", "--is-ancestor", a, b}, 1)
	if err != nil {
		return false, err
	}
	return r.code == 0, nil
}

func (l *realLookups) ContainsTarget() (bool, error) {
	ref, err := l.targetRef()
	if err != nil {
		return false, err
	}
	return l.isAncestor(ref, l.cfg.Source)
}

func (l *realLookups) MainTreeEqualsPreview() (bool, error) {
	ref, err := l.targetRef()
	if err != nil {
		return false, err
	}
	if ok, err := l.isAncestor(ref, l.cfg.Source); err != nil || !ok {
		return false, err
	}
	r, err := l.git("", []string{"merge-tree", "--write-tree", ref, l.cfg.Source}, 1)
	if err != nil {
		return false, err
	}
	if r.code != 0 {
		return false, nil // conflicts
	}
	merged := strings.TrimSpace(strings.SplitN(r.out, "\n", 2)[0])
	tree, err := l.gitOut("rev-parse", "--verify", l.cfg.Source+"^{tree}")
	if err != nil {
		return false, err
	}
	return merged == tree, nil
}

// auditRecord is one .agents/reviews/*.md: its issue and Audited-through commits.
type auditRecord struct {
	issue   int
	through []string // resolved to full commit ids
}

var (
	auditHeading = regexp.MustCompile(`^# Audit:.*?#(\d+)`)
	auditedLine  = regexp.MustCompile("^Audited-through:[\\s`*]*([0-9a-fA-F]{7,64})")
)

// auditRecords reads every audit record on the source branch.
func (l *realLookups) auditRecords() ([]auditRecord, error) {
	r, err := l.git("", []string{"grep", "-I", "--null", "-E", "-e", "^# Audit:", "-e", "^Audited-through:", l.cfg.Source, "--", ".agents/reviews/*.md"}, 1)
	if err != nil {
		return nil, err
	}
	byFile := map[string]*auditRecord{}
	var order []string
	var ids []string
	for _, line := range strings.Split(r.out, "\n") {
		file, text, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}
		rec := byFile[file]
		if rec == nil {
			rec = &auditRecord{}
			byFile[file] = rec
			order = append(order, file)
		}
		if m := auditHeading.FindStringSubmatch(text); m != nil && rec.issue == 0 {
			rec.issue, _ = strconv.Atoi(m[1])
		}
		if m := auditedLine.FindStringSubmatch(text); m != nil {
			rec.through = append(rec.through, m[1])
			ids = append(ids, m[1])
		}
	}
	resolved, err := l.resolveCommits(ids)
	if err != nil {
		return nil, err
	}
	var out []auditRecord
	for _, f := range order {
		rec := byFile[f]
		var full []string
		for _, id := range rec.through {
			if c, ok := resolved[id]; ok {
				full = append(full, c)
			}
		}
		rec.through = full
		out = append(out, *rec)
	}
	return out, nil
}

// resolveCommits maps each id to its full commit id, leaving out ids this
// checkout does not hold, in one git call.
func (l *realLookups) resolveCommits(ids []string) (map[string]string, error) {
	out := map[string]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var in strings.Builder
	for _, id := range ids {
		in.WriteString(id + "^{commit}\n")
	}
	r, err := l.git(in.String(), []string{"cat-file", "--batch-check=%(objectname) %(objecttype)"})
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimRight(r.out, "\n"), "\n")
	for i, id := range ids {
		if i >= len(lines) {
			break
		}
		f := strings.Fields(lines[i])
		if len(f) == 2 && f[1] == "commit" {
			out[id] = f[0]
		}
	}
	return out, nil
}

func (l *realLookups) UncoveredCommits() ([]readiness.Commit, error) {
	ref, err := l.targetRef()
	if err != nil {
		return nil, err
	}
	recs, err := l.auditRecords()
	if err != nil {
		return nil, err
	}
	args := []string{"-c", "core.quotePath=false", "log", "--no-merges", "--format=%x00%H", "--name-only", l.cfg.Source, "^" + ref}
	for _, rec := range recs {
		for _, c := range rec.through {
			args = append(args, "^"+c)
		}
	}
	args = append(args, "--")
	out, err := l.gitOut(args...)
	if err != nil {
		return nil, err
	}
	var commits []readiness.Commit
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "\x00"):
			commits = append(commits, readiness.Commit{SHA: strings.TrimPrefix(line, "\x00")})
		case line != "" && len(commits) > 0:
			commits[len(commits)-1].Files = append(commits[len(commits)-1].Files, line)
		}
	}
	return commits, nil
}

func (l *realLookups) AuditIssues() ([]int, error) {
	ref, err := l.targetRef()
	if err != nil {
		return nil, err
	}
	recs, err := l.auditRecords()
	if err != nil {
		return nil, err
	}
	out, err := l.gitOut("rev-list", l.cfg.Source, "^"+ref, "--")
	if err != nil {
		return nil, err
	}
	inRange := map[string]bool{}
	for _, c := range strings.Fields(out) {
		inRange[c] = true
	}
	seen := map[int]bool{}
	var issues []int
	for _, rec := range recs {
		if rec.issue == 0 || seen[rec.issue] {
			continue
		}
		for _, c := range rec.through {
			if inRange[c] {
				seen[rec.issue] = true
				issues = append(issues, rec.issue)
				break
			}
		}
	}
	sort.Ints(issues)
	return issues, nil
}

// ---- GitLab issues ----------------------------------------------------------

// IssueState reads one issue's state from the GitLab API, with a time limit
// that runs until the body is read and a cap on the body. Errors name the
// call and the status only: never the URL, a header or the body.
func (l *realLookups) IssueState(n int) (string, error) {
	if l.cfg.GitLabAPI == "" {
		why := l.cfg.NoIssuesWhy
		if why == "" {
			why = "no token to read issues"
		}
		return "", fmt.Errorf("%s; check by hand that audit issue #%d is closed", why, n)
	}
	what := fmt.Sprintf("GET issue %d", n)
	u, err := url.Parse(l.cfg.GitLabAPI)
	if err != nil {
		return "", fmt.Errorf("%s: the GitLab API address does not parse", what)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/issues/" + strconv.Itoa(n)
	u.RawPath = ""
	ctx, cancel := context.WithTimeout(context.Background(), l.cfg.HTTPTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return "", fmt.Errorf("%s: could not build the request", what)
	}
	if l.cfg.GitLabToken != "" {
		req.Header.Set("PRIVATE-TOKEN", l.cfg.GitLabToken)
	}
	req.Header.Set("Accept", "application/json")
	timedOut := func() error {
		return fmt.Errorf("%s: no answer within %s, the limit on every HTTP call", what, l.cfg.HTTPTimeout)
	}
	resp, err := l.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return "", timedOut()
		}
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return "", fmt.Errorf("%s: %s", what, userinfo.ReplaceAllString(err.Error(), "://"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("%s: HTTP %d", what, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, l.cfg.MaxBody+1))
	if err != nil {
		if ctx.Err() != nil {
			return "", timedOut()
		}
		return "", fmt.Errorf("%s: reading the answer failed", what)
	}
	if int64(len(body)) > l.cfg.MaxBody {
		return "", fmt.Errorf("%s: the answer is over the %d-byte cap", what, l.cfg.MaxBody)
	}
	var issue struct {
		State string `json:"state"`
	}
	if err := json.Unmarshal(body, &issue); err != nil || issue.State == "" {
		return "", fmt.Errorf("%s: the answer is not an issue", what)
	}
	return issue.State, nil
}

// ---- Orbit pin --------------------------------------------------------------

var (
	fullCommit = regexp.MustCompile(`^[0-9a-f]{40}$`)
	plainRef   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
)

func (l *realLookups) pin() (branch, commit string, err error) {
	src, found, err := l.SourceFile("orbit/pin.json")
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", errors.New("orbit/pin.json is missing")
	}
	var p struct{ Branch, Commit string }
	if err := json.Unmarshal([]byte(src), &p); err != nil {
		return "", "", errors.New("orbit/pin.json is not JSON")
	}
	if !fullCommit.MatchString(p.Commit) {
		return "", "", errors.New("orbit/pin.json's commit is not a 40-hex commit")
	}
	if !plainRef.MatchString(p.Branch) || strings.Contains(p.Branch, "..") {
		return "", "", errors.New("orbit/pin.json's branch is not a plain branch name")
	}
	return p.Branch, p.Commit, nil
}

func (l *realLookups) OrbitPin() (string, error) {
	_, c, err := l.pin()
	return c, err
}

// OrbitBranchTip reads the tip of the branch orbit/pin.json names from the
// public Orbit mirror, with no credential (decision C2).
func (l *realLookups) OrbitBranchTip() (string, string, error) {
	branch := l.cfg.OrbitBranch
	if branch == "" {
		b, _, err := l.pin()
		if err != nil {
			return "", "", err
		}
		branch = b
	}
	if l.cfg.OrbitRepoURL == "" {
		return branch, "", errors.New("no Orbit mirror address is configured")
	}
	refs, err := l.orbitHeads(branch)
	if err != nil {
		return branch, "", err
	}
	tip, ok := refs["refs/heads/"+branch]
	if !ok {
		return branch, "", fmt.Errorf("the Orbit mirror has no branch %s", branch)
	}
	return branch, tip, nil
}

// orbitHeads lists the mirror's head for branch, with no credential helper,
// so nothing the host stores is offered to it.
func (l *realLookups) orbitHeads(branch string) (map[string]string, error) {
	out, err := l.gitOut("-c", "credential.helper=", "ls-remote", "--heads", l.cfg.OrbitRepoURL, "refs/heads/"+branch)
	if err != nil {
		return nil, err
	}
	refs := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, name, ok := strings.Cut(strings.TrimSpace(line), "\t"); ok {
			refs[name] = sha
		}
	}
	return refs, nil
}
