package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	scp "github.com/tomlawesome/orbit-launcher/tools/supplychainpolicy/policy"
)

const (
	checkoutSHA    = "3d3c42e5aac5ba805825da76410c181273ba90b1"
	codeqlSHA      = "cdf488f595d80d6e07e03d4674febd5ab45fa938"
	codeqlTagObj   = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	gitleaksDigest = "551f6fc83ea457d62a0d98237cbad105af8d557003051f41f3e7ca7b3f2470eb"
)

const ciWorkflow = "" +
	"      - uses: actions/checkout@" + checkoutSHA + " # v7.0.1\n" +
	"      - uses: github/codeql-action/init@" + codeqlSHA + " # v4.37.9\n" +
	"      - uses: github/codeql-action/analyze@" + codeqlSHA + " # v4.37.9\n"

const secretScanWorkflow = "    env:\n      GITLEAKS_VERSION: 8.30.1\n      GITLEAKS_SHA256: " + gitleaksDigest + "\n"

// repo builds a throwaway repository root with the given workflows and makes
// it the working directory, so the default -root of "." finds it (as `go run`
// from the repository root does).
func repo(t *testing.T, workflows map[string]string) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, ".github", "workflows")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range workflows {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	t.Chdir(root)
	return root
}

func goodRepo(t *testing.T) string {
	t.Helper()
	return repo(t, map[string]string{"ci.yml": ciWorkflow, "secret-scan.yml": secretScanWorkflow})
}

type route struct {
	status int
	body   string
}

// fakeAPI stands in for api.github.com. Routes not listed answer 404, as the
// real API does for an unknown repository or tag.
type fakeAPI struct {
	mu       sync.Mutex
	routes   map[string]route
	requests []*http.Request
}

func (f *fakeAPI) seen() []*http.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]*http.Request(nil), f.requests...)
}

// goodRoutes answers everything goodRepo's workflows need: checkout's tag is
// lightweight, codeql's is annotated and must be dereferenced.
func goodRoutes() map[string]route {
	return map[string]route{
		"/repos/actions/checkout":                              {200, `{"license":{"spdx_id":"MIT"}}`},
		"/repos/github/codeql-action":                          {200, `{"license":{"spdx_id":"MIT"}}`},
		"/repos/gitleaks/gitleaks":                             {200, `{"license":{"spdx_id":"MIT"}}`},
		"/repos/actions/checkout/git/ref/tags/v7.0.1":          {200, `{"object":{"sha":"` + checkoutSHA + `","type":"commit"}}`},
		"/repos/github/codeql-action/git/ref/tags/v4.37.9":     {200, `{"object":{"sha":"` + codeqlTagObj + `","type":"tag"}}`},
		"/repos/github/codeql-action/git/tags/" + codeqlTagObj: {200, `{"object":{"sha":"` + codeqlSHA + `"}}`},
	}
}

// serve points the command at a local server for the rest of the test.
func serve(t *testing.T, routes map[string]route) *fakeAPI {
	t.Helper()
	f := &fakeAPI{routes: routes}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.requests = append(f.requests, r)
		rt, ok := f.routes[r.URL.Path]
		f.mu.Unlock()
		if !ok {
			rt = route{404, `{"message":"Not Found"}`}
		}
		w.WriteHeader(rt.status)
		_, _ = w.Write([]byte(rt.body))
	}))
	t.Cleanup(srv.Close)
	prevBase, prevClient := apiBase, client
	apiBase, client = srv.URL, srv.Client()
	t.Cleanup(func() { apiBase, client = prevBase, prevClient })
	return f
}

func runCmd(args ...string) (code int, stdout, stderr string) {
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func readPolicy(t *testing.T, root string) scp.Policy {
	t.Helper()
	p, err := scp.Load(root)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func writePolicy(t *testing.T, root string, p scp.Policy) {
	t.Helper()
	body, err := scp.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, scp.PolicyPath), body, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWriteGeneratesAPolicyTheCheckThenAccepts(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	root := goodRepo(t)
	serve(t, goodRoutes())

	code, stdout, stderr := runCmd("-write")
	if code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, stderr)
	}
	if stdout != "wrote .github/supply-chain-policy.json: 2 actions, 1 tools\n" {
		t.Errorf("stdout = %q", stdout)
	}

	p := readPolicy(t, root)
	if len(p.Actions) != 2 {
		t.Fatalf("got %d actions, want 2 (codeql's subpaths are one)", len(p.Actions))
	}
	checkout, codeql := p.Actions[0], p.Actions[1]
	if checkout.Name != "actions/checkout" || checkout.Commit != checkoutSHA || checkout.Version != "v7.0.1" ||
		checkout.License != "MIT" || checkout.UpdateOwner != defaultOwner ||
		checkout.Source != "https://github.com/actions/checkout/releases/tag/v7.0.1" {
		t.Errorf("checkout entry = %+v", checkout)
	}
	if codeql.Name != "github/codeql-action" || codeql.Commit != codeqlSHA {
		t.Errorf("codeql entry = %+v; the annotated tag should have resolved to the pinned commit", codeql)
	}
	if len(p.Tools) != 1 {
		t.Fatalf("got %d tools, want 1", len(p.Tools))
	}
	gl := p.Tools[0]
	if gl.Name != "gitleaks" || gl.Version != "8.30.1" || gl.SHA256 != gitleaksDigest || gl.License != "MIT" ||
		gl.Artifact != "gitleaks_8.30.1_linux_x64.tar.gz" ||
		gl.Source != "https://github.com/gitleaks/gitleaks/releases/tag/v8.30.1" ||
		gl.UsedBy != ".github/workflows/secret-scan.yml" || gl.UpdateOwner != defaultOwner || gl.Note == "" {
		t.Errorf("gitleaks entry = %+v", gl)
	}
	if p.Exceptions == nil {
		t.Error("exceptions written as null; want an empty list")
	}
	if strings.Join(p.Comment, "\n") != strings.Join(fileComment, "\n") {
		t.Error("the generated file does not carry the standard comment")
	}

	// The generator and the gate must agree, or a fresh regeneration would fail CI.
	code, stdout, stderr = runCmd()
	if code != 0 || stdout != "supply-chain policy matches the workflows\n" {
		t.Errorf("check after write: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}

func TestWriteWritesUnderRootFromAnotherDirectory(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "")
	root := goodRepo(t)
	serve(t, goodRoutes())
	elsewhere := t.TempDir()
	t.Chdir(elsewhere)

	code, stdout, stderr := runCmd("-write", "-root", root)
	if code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, stderr)
	}
	want := filepath.Join(root, scp.PolicyPath)
	if stdout != "wrote "+want+": 2 actions, 1 tools\n" {
		t.Errorf("stdout = %q; want it to name %s", stdout, want)
	}
	if len(readPolicy(t, root).Actions) != 2 {
		t.Error("the policy under -root was not regenerated")
	}
	if _, err := os.Stat(filepath.Join(elsewhere, scp.PolicyPath)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a policy was written under the working directory (stat err %v); -write must write under -root", err)
	}
}

func TestWriteCarriesHumanAuthoredFieldsAcrossRegeneration(t *testing.T) {
	root := goodRepo(t)
	serve(t, goodRoutes())
	writePolicy(t, root, scp.Policy{
		SchemaVersion: scp.SchemaVersion,
		Actions: []scp.Action{
			{Name: "actions/checkout", Commit: "stale", UpdateOwner: "alice", Note: "checked by hand"},
			{Name: "github/codeql-action", UpdateOwner: "", Note: "owner left blank"},
		},
		Tools:      []scp.Tool{{Name: "gitleaks", UpdateOwner: "bob", Note: "custom note"}},
		Exceptions: []scp.Exception{{Name: "some/action", Reason: "reviewed fork"}},
	})

	if code, _, stderr := runCmd("-write"); code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, stderr)
	}
	p := readPolicy(t, root)
	if a := p.Actions[0]; a.UpdateOwner != "alice" || a.Note != "checked by hand" || a.Commit != checkoutSHA {
		t.Errorf("checkout = %+v; want owner and note carried, commit re-derived", a)
	}
	if a := p.Actions[1]; a.UpdateOwner != defaultOwner || a.Note != "owner left blank" {
		t.Errorf("codeql = %+v; a blank owner falls back to the default, the note is kept", a)
	}
	if gl := p.Tools[0]; gl.UpdateOwner != "bob" || gl.Note != "custom note" {
		t.Errorf("gitleaks = %+v; want owner and note carried", gl)
	}
	if len(p.Exceptions) != 1 || p.Exceptions[0].Reason != "reviewed fork" {
		t.Errorf("exceptions = %+v; want them carried untouched", p.Exceptions)
	}
}

func TestWriteKeepsTheDefaultGitleaksNoteWhenThePreviousOneIsBlank(t *testing.T) {
	root := goodRepo(t)
	serve(t, goodRoutes())
	writePolicy(t, root, scp.Policy{
		SchemaVersion: scp.SchemaVersion,
		Tools:         []scp.Tool{{Name: "gitleaks"}, {Name: "other", UpdateOwner: "carol"}},
	})
	if code, _, stderr := runCmd("-write"); code != 0 {
		t.Fatalf("exit %d; stderr: %s", code, stderr)
	}
	p := readPolicy(t, root)
	if len(p.Tools) != 1 {
		t.Errorf("tools = %+v; only gitleaks is generated", p.Tools)
	}
	if gl := p.Tools[0]; gl.UpdateOwner != defaultOwner || !strings.Contains(gl.Note, "checksum-verified") {
		t.Errorf("gitleaks = %+v; want the default owner and note", gl)
	}
}

// Every refusal must leave the committed file untouched: a half-checked
// regeneration written to disk would pass the offline gate.
func TestWriteRefusesAndWritesNothing(t *testing.T) {
	for _, c := range []struct {
		name      string
		workflows map[string]string
		routes    func(map[string]route)
		wantSub   string
	}{
		{
			name:      "a pin that is not a SHA",
			workflows: map[string]string{"ci.yml": "      - uses: actions/checkout@v7 # v7.0.1\n", "secret-scan.yml": secretScanWorkflow},
			wantSub:   `actions/checkout is pinned to "v7", not a commit SHA`,
		},
		{
			name:      "a pin with no version comment",
			workflows: map[string]string{"ci.yml": "      - uses: actions/checkout@" + checkoutSHA + "\n", "secret-scan.yml": secretScanWorkflow},
			wantSub:   "actions/checkout has no `# vX.Y.Z` comment",
		},
		{
			name: "a licence the API cannot name",
			routes: func(r map[string]route) {
				r["/repos/actions/checkout"] = route{200, `{"license":{"spdx_id":"NOASSERTION"}}`}
			},
			wantSub: `actions/checkout reports licence "NOASSERTION"`,
		},
		{
			name:    "a repository with no licence at all",
			routes:  func(r map[string]route) { r["/repos/actions/checkout"] = route{200, `{"license":null}`} },
			wantSub: `actions/checkout reports licence ""`,
		},
		{
			name:    "an API error",
			routes:  func(r map[string]route) { r["/repos/actions/checkout"] = route{403, "rate limit exceeded\n"} },
			wantSub: "GET /repos/actions/checkout: 403 Forbidden: rate limit exceeded",
		},
		{
			name:    "a response that is not JSON",
			routes:  func(r map[string]route) { r["/repos/actions/checkout"] = route{200, "<html>"} },
			wantSub: "invalid character",
		},
		{
			name:    "a tag that does not exist",
			routes:  func(r map[string]route) { delete(r, "/repos/actions/checkout/git/ref/tags/v7.0.1") },
			wantSub: "GET /repos/actions/checkout/git/ref/tags/v7.0.1: 404 Not Found",
		},
		{
			name:    "an annotated tag object that cannot be read",
			routes:  func(r map[string]route) { delete(r, "/repos/github/codeql-action/git/tags/"+codeqlTagObj) },
			wantSub: "GET /repos/github/codeql-action/git/tags/" + codeqlTagObj + ": 404",
		},
		{
			name: "a comment that names a different commit",
			routes: func(r map[string]route) {
				r["/repos/actions/checkout/git/ref/tags/v7.0.1"] = route{200, `{"object":{"sha":"1111111111111111111111111111111111111111","type":"commit"}}`}
			},
			wantSub: "actions/checkout claims v7.0.1 beside its pin, but that tag resolves to 1111111111111111111111111111111111111111, not the pinned " + checkoutSHA,
		},
		{
			name:      "a secret scan that no longer pins gitleaks",
			workflows: map[string]string{"ci.yml": ciWorkflow, "secret-scan.yml": "    env: {}\n"},
			wantSub:   "no longer sets GITLEAKS_VERSION",
		},
		{
			name:    "gitleaks' licence cannot be read",
			routes:  func(r map[string]route) { delete(r, "/repos/gitleaks/gitleaks") },
			wantSub: "GET /repos/gitleaks/gitleaks: 404",
		},
		{
			name:      "no workflows to read",
			workflows: map[string]string{},
			wantSub:   "no workflow files",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			workflows := c.workflows
			if workflows == nil {
				workflows = map[string]string{"ci.yml": ciWorkflow, "secret-scan.yml": secretScanWorkflow}
			}
			root := repo(t, workflows)
			routes := goodRoutes()
			if c.routes != nil {
				c.routes(routes)
			}
			serve(t, routes)

			code, stdout, stderr := runCmd("-write")
			if code != 1 {
				t.Fatalf("exit %d, want 1; stdout %q", code, stdout)
			}
			if !strings.HasPrefix(stderr, "supplychainpolicy: ") || !strings.Contains(stderr, c.wantSub) {
				t.Errorf("stderr = %q, want it to contain %q", stderr, c.wantSub)
			}
			if _, err := os.Stat(filepath.Join(root, scp.PolicyPath)); !os.IsNotExist(err) {
				t.Errorf("a refused regeneration wrote the policy (stat: %v)", err)
			}
		})
	}
}

func TestWriteReportsAFailureToWriteThePolicy(t *testing.T) {
	root := goodRepo(t)
	serve(t, goodRoutes())
	// A directory where the file should go makes the write fail.
	if err := os.Mkdir(filepath.Join(root, scp.PolicyPath), 0o755); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runCmd("-write")
	if code != 1 || !strings.Contains(stderr, "writing the policy") {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 1 naming the failed write", code, stdout, stderr)
	}
	if stdout != "" {
		t.Errorf("stdout = %q; nothing was written, so nothing should claim it was", stdout)
	}
}

func TestWriteReportsAnUnreachableAPI(t *testing.T) {
	goodRepo(t)
	serve(t, goodRoutes())
	srv := httptest.NewServer(http.NotFoundHandler())
	apiBase = srv.URL
	srv.Close() // nothing is listening any more

	code, _, stderr := runCmd("-write")
	if code != 1 || !strings.Contains(stderr, "GET /repos/actions/checkout:") {
		t.Errorf("exit %d, stderr %q; want exit 1 naming the request that failed", code, stderr)
	}
}

func TestAPISendsTheTokenOnlyWhenOneIsSet(t *testing.T) {
	f := serve(t, goodRoutes())
	var into map[string]any

	t.Setenv("GITHUB_TOKEN", "fake-token-for-tests")
	if err := api("/repos/actions/checkout", &into); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GITHUB_TOKEN", "")
	if err := api("/repos/actions/checkout", &into); err != nil {
		t.Fatal(err)
	}

	reqs := f.seen()
	if len(reqs) != 2 {
		t.Fatalf("server saw %d requests, want 2", len(reqs))
	}
	if got := reqs[0].Header.Get("Authorization"); got != "Bearer fake-token-for-tests" {
		t.Errorf("with a token, Authorization = %q", got)
	}
	if got, ok := reqs[1].Header["Authorization"]; ok {
		t.Errorf("without a token, Authorization was sent as %q", got)
	}
	for _, r := range reqs {
		if got := r.Header.Get("Accept"); got != "application/vnd.github+json" {
			t.Errorf("Accept = %q, want the GitHub media type", got)
		}
	}
}

func TestAPIDecodesTheResponseInto(t *testing.T) {
	serve(t, map[string]route{"/x": {200, `{"name":"value"}`}})
	var into struct{ Name string }
	if err := api("/x", &into); err != nil {
		t.Fatal(err)
	}
	if into.Name != "value" {
		t.Errorf("decoded %+v", into)
	}
}

// The error body is quoted so the reason is visible, but capped so a large
// HTML error page cannot flood the output.
func TestAPITruncatesALongErrorBody(t *testing.T) {
	serve(t, map[string]route{"/x": {500, strings.Repeat("e", 1000)}})
	err := api("/x", new(json.RawMessage))
	if err == nil {
		t.Fatal("a 500 returned no error")
	}
	const prefix = "GET /x: 500 Internal Server Error: "
	if want := prefix + strings.Repeat("e", 200); err.Error() != want {
		t.Errorf("error = %.80q... (%d bytes), want the status and the first 200 bytes of the body", err, len(err.Error()))
	}
}

func TestAPIRejectsAMalformedPath(t *testing.T) {
	f := serve(t, nil)
	if err := api("/repos/%zz", new(json.RawMessage)); err == nil {
		t.Error("a malformed path returned no error")
	}
	if len(f.seen()) != 0 {
		t.Error("a malformed path was still sent")
	}
}

func TestCheckPassesWhenThePolicyMatches(t *testing.T) {
	root := goodRepo(t)
	serve(t, goodRoutes())
	if code, _, stderr := runCmd("-write"); code != 0 {
		t.Fatalf("setup: exit %d; stderr: %s", code, stderr)
	}
	f := serve(t, nil)

	code, stdout, stderr := runCmd("-root", root)
	if code != 0 || stdout != "supply-chain policy matches the workflows\n" || stderr != "" {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	if len(f.seen()) != 0 {
		t.Error("the offline check made a network request")
	}
}

func TestCheckListsEveryProblemAndHowToFixThem(t *testing.T) {
	root := goodRepo(t)
	serve(t, goodRoutes())
	if code, _, stderr := runCmd("-write"); code != 0 {
		t.Fatalf("setup: exit %d; stderr: %s", code, stderr)
	}
	p := readPolicy(t, root)
	p.Actions[0].License = ""
	p.Tools[0].Version = "8.0.0"
	writePolicy(t, root, p)

	code, stdout, stderr := runCmd("-root", root)
	if code != 1 || stdout != "" {
		t.Fatalf("exit %d, stdout %q; want exit 1 and nothing on stdout", code, stdout)
	}
	for _, want := range []string{
		"supplychainpolicy: the policy no longer matches the workflows:\n  ",
		"action actions/checkout records no licence.",
		"\n  secret-scan.yml pins gitleaks 8.30.1 but the policy records 8.0.0.",
		"Regenerate it:\n  go run ./tools/supplychainpolicy -write\n",
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr missing %q:\n%s", want, stderr)
		}
	}
}

func TestCheckFailsWhenThereIsNoPolicy(t *testing.T) {
	root := goodRepo(t)
	code, _, stderr := runCmd("-root", root)
	if code != 1 || !strings.Contains(stderr, "reading the supply-chain policy") {
		t.Errorf("exit %d, stderr %q", code, stderr)
	}
}

func TestRunRejectsAnUnknownFlagWithExitTwo(t *testing.T) {
	code, stdout, stderr := runCmd("-bogus")
	if code != 2 || stdout != "" || !strings.Contains(stderr, "-bogus") {
		t.Errorf("exit %d, stdout %q, stderr %q; want exit 2 naming the flag on stderr", code, stdout, stderr)
	}
}

func TestRunHelpExitsZeroAndDescribesTheFlags(t *testing.T) {
	code, stdout, stderr := runCmd("-h")
	if code != 0 || stdout != "" || !strings.Contains(stderr, "-write") || !strings.Contains(stderr, "-root") {
		t.Errorf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
}
