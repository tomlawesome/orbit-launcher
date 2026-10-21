package deploy

// The claim link in Orbit's logs (#259): ParseClaimLink reads the two
// notice forms Orbit prints and trusts neither blindly; ReadClaimLink
// asks docker for the app's recent logs. Written from the contract alone.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tomlawesome/orbit-launcher/internal/testutil"
)

const (
	claimAppURL = "https://mail.example.com"
	claimCode1  = "ZmFrZS1jb2RlLW9uZQ"
	claimCode2  = "ZmFrZS1jb2RlLXR3bw"
	claimLinkA  = claimAppURL + "/#claim=" + claimCode1
	claimLinkB  = claimAppURL + "/#claim=" + claimCode2
	claimHeader = "Orbit is not yet claimed. Open this link to create the first administrator:"
)

func jsonNotice(link, code string) string {
	return fmt.Sprintf(`{"event":"bootstrap.claim","url":%q,"code":%q}`, link, code)
}

func textNotice(link string) string { return claimHeader + "\n  " + link }

func TestParseClaimLink_ReadsEitherNoticeForm(t *testing.T) {
	noise := `{"level":"info","msg":"listening on :8080"}` + "\n"
	cases := map[string]string{
		"json":                         jsonNotice(claimLinkA, claimCode1) + "\n",
		"json among other logs":        noise + jsonNotice(claimLinkA, claimCode1) + "\n" + noise,
		"text":                         textNotice(claimLinkA) + "\n",
		"text among other logs":        "starting up\n" + textNotice(claimLinkA) + "\nready\n",
		"text without a final newline": textNotice(claimLinkA),
		"json without a final newline": jsonNotice(claimLinkA, claimCode1),
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseClaimLink([]byte(out), claimAppURL); got != claimLinkA {
				t.Errorf("got %q, want %q", got, claimLinkA)
			}
		})
	}
}

func TestParseClaimLink_TheLastNoticeWins(t *testing.T) {
	cases := map[string]string{
		"two json":                jsonNotice(claimLinkA, claimCode1) + "\n" + jsonNotice(claimLinkB, claimCode2) + "\n",
		"two text":                textNotice(claimLinkA) + "\n" + textNotice(claimLinkB) + "\n",
		"json then text":          jsonNotice(claimLinkA, claimCode1) + "\nmore logs\n" + textNotice(claimLinkB) + "\n",
		"text then json":          textNotice(claimLinkA) + "\nmore logs\n" + jsonNotice(claimLinkB, claimCode2) + "\n",
		"logs after the last one": jsonNotice(claimLinkA, claimCode1) + "\n" + textNotice(claimLinkB) + "\nlater line\n",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseClaimLink([]byte(out), claimAppURL); got != claimLinkB {
				t.Errorf("got %q, want the last notice's %q", got, claimLinkB)
			}
		})
	}
}

// A JSON line of some other event is not a notice, even with a URL that
// would pass.
func TestParseClaimLink_OtherJSONEventsAreNotNotices(t *testing.T) {
	out := fmt.Sprintf(`{"event":"http.request","url":%q,"code":%q}`, claimLinkA, claimCode1) + "\n"
	if got := ParseClaimLink([]byte(out), claimAppURL); got != "" {
		t.Errorf("got %q from another event, want empty", got)
	}
}

func TestParseClaimLink_EmptyOutputGivesNothing(t *testing.T) {
	for _, out := range []string{"", "\n", "  \n\t\n", "just some logs\nand more\n"} {
		if got := ParseClaimLink([]byte(out), claimAppURL); got != "" {
			t.Errorf("output %q: got %q, want empty", out, got)
		}
	}
	if got := ParseClaimLink(nil, claimAppURL); got != "" {
		t.Errorf("nil output: got %q, want empty", got)
	}
}

// A forged or damaged line must give nothing, so a log line cannot send
// the person somewhere else.
func TestParseClaimLink_RejectsLinksThatAreNotThisDeployments(t *testing.T) {
	bad := map[string]string{
		"another host":                    "https://evil.example/#claim=" + claimCode1,
		"a lookalike host":                "https://mail.example.com.evil.example/#claim=" + claimCode1,
		"a subdomain":                     "https://app.mail.example.com/#claim=" + claimCode1,
		"userinfo hiding the host":        "https://mail.example.com@evil.example/#claim=" + claimCode1,
		"another scheme (http)":           "http://mail.example.com/#claim=" + claimCode1,
		"another scheme (ftp)":            "ftp://mail.example.com/#claim=" + claimCode1,
		"a javascript link":               "javascript:alert(1)//#claim=" + claimCode1,
		"no fragment":                     "https://mail.example.com/",
		"an empty fragment":               "https://mail.example.com/#",
		"a fragment that is not a claim":  "https://mail.example.com/#other=" + claimCode1,
		"claim not at the fragment start": "https://mail.example.com/#x&claim=" + claimCode1,
		"claim without an equals":         "https://mail.example.com/#claim",
		"claim in the query only":         "https://mail.example.com/?claim=" + claimCode1,
		"not a url at all":                "this is not a link",
		"a bare scheme":                   "https://",
		"a broken escape":                 "https://mail.example.com/%zz#claim=" + claimCode1,
		"an escape byte in it":            "https://mail.example.com/#claim=" + claimCode1 + "\x1b]8;;https://evil.example\a",
		"a bell in it":                    "https://mail.example.com/#claim=ab\acd",
	}
	for name, link := range bad {
		t.Run("json: "+name, func(t *testing.T) {
			// %q-quoted JSON would escape controls as \x.. which is not JSON,
			// so build the JSON string with proper escapes.
			out := `{"event":"bootstrap.claim","url":"` + jsonEscape(link) + `","code":"x"}` + "\n"
			if got := ParseClaimLink([]byte(out), claimAppURL); got != "" {
				t.Errorf("accepted %q as %q", link, got)
			}
		})
		t.Run("text: "+name, func(t *testing.T) {
			if strings.ContainsAny(link, "\n\r") {
				t.Skip("a line cannot hold a newline")
			}
			out := claimHeader + "\n  " + link + "\n"
			if got := ParseClaimLink([]byte(out), claimAppURL); got != "" {
				t.Errorf("accepted %q as %q", link, got)
			}
		})
	}
}

func jsonEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '"' || r == '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestParseClaimLink_DamagedNoticesGiveNothing(t *testing.T) {
	cases := map[string]string{
		"json cut short":                `{"event":"bootstrap.claim","url":"` + claimLinkA,
		"json without a url":            `{"event":"bootstrap.claim","code":"x"}`,
		"json url not a string":         `{"event":"bootstrap.claim","url":42,"code":"x"}`,
		"json url empty":                `{"event":"bootstrap.claim","url":"","code":"x"}`,
		"text header, nothing after":    claimHeader,
		"text header, blank line after": claimHeader + "\n\n",
		"text header, prose after":      claimHeader + "\n  please wait\n",
		"link without its header":       "  " + claimLinkA,
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if got := ParseClaimLink([]byte(out), claimAppURL); got != "" {
				t.Errorf("got %q, want empty", got)
			}
		})
	}
}

// The last notice is the one judged. A forged last line must not fall
// back to an earlier, honest one: nothing is shown. (Interpretation of
// "the last notice wins" plus "anything else gives empty".)
func TestParseClaimLink_ALastNoticeThatFailsTheChecksGivesNothing(t *testing.T) {
	forged := jsonNotice("https://evil.example/#claim=x", "x")
	out := jsonNotice(claimLinkA, claimCode1) + "\n" + forged + "\n"
	if got := ParseClaimLink([]byte(out), claimAppURL); got != "" {
		t.Errorf("got %q, want empty when the last notice is forged", got)
	}
}

func TestParseClaimLink_ComparesAgainstTheGivenAppURL(t *testing.T) {
	out := []byte(jsonNotice(claimLinkA, claimCode1) + "\n")
	for _, app := range []string{"https://mail.example.com", "https://mail.example.com/", "https://mail.example.com/orbit"} {
		if got := ParseClaimLink(out, app); got != claimLinkA {
			t.Errorf("appURL %q: got %q, want %q", app, got, claimLinkA)
		}
	}
	for _, app := range []string{"https://other.example.com", "http://mail.example.com", ""} {
		if got := ParseClaimLink(out, app); got != "" {
			t.Errorf("appURL %q: got %q, want empty", app, got)
		}
	}
}

// --- ReadClaimLink ----------------------------------------------------------

// fakeDockerLoggingAndPrinting puts a docker on PATH that records its
// arguments, one line per call, and prints stdout, then exits with code.
func fakeDockerLoggingAndPrinting(t *testing.T, stdout string, code int) (callLog string) {
	t.Helper()
	callLog = filepath.Join(t.TempDir(), "calls.log")
	script := "#!/usr/bin/env bash\necho \"$@\" >> " + callLog + "\ncat <<'END_OF_LOGS'\n" + stdout + "END_OF_LOGS\nexit " + fmt.Sprint(code) + "\n"
	testutil.FakeExecutable(t, "docker", script)
	return callLog
}

func TestReadClaimLink_AsksComposeForTheAppsRecentLogs(t *testing.T) {
	callLog := fakeDockerLoggingAndPrinting(t, jsonNotice(claimLinkA, claimCode1)+"\n", 0)
	dir := servicesDeployment(t)
	since := time.Date(2026, 10, 21, 9, 30, 15, 0, time.UTC)

	got, err := ReadClaimLink(t.Context(), dir, claimAppURL, since)
	if err != nil || got != claimLinkA {
		t.Fatalf("got %q, %v; want %q", got, err, claimLinkA)
	}

	calls, rerr := os.ReadFile(callLog)
	if rerr != nil {
		t.Fatalf("docker was never run: %v", rerr)
	}
	want := fmt.Sprintf("compose --project-directory %s --env-file %s logs --no-color --no-log-prefix --since %s orbit-app",
		dir, filepath.Join(dir, EnvFile), "2026-10-21T09:30:15Z")
	if lines := strings.Split(strings.TrimSpace(string(calls)), "\n"); len(lines) != 1 || lines[0] != want {
		t.Errorf("docker calls = %q, want exactly %q", lines, want)
	}
}

func TestReadClaimLink_ReadsTheTextFormToo(t *testing.T) {
	fakeDockerLoggingAndPrinting(t, "booting\n"+textNotice(claimLinkA)+"\n", 0)
	got, err := ReadClaimLink(t.Context(), servicesDeployment(t), claimAppURL, time.Now().Add(-time.Minute))
	if err != nil || got != claimLinkA {
		t.Errorf("got %q, %v; want %q", got, err, claimLinkA)
	}
}

func TestReadClaimLink_NoNoticeIsNoLinkAndNoError(t *testing.T) {
	fakeDockerLoggingAndPrinting(t, "orbit-app started\n", 0)
	got, err := ReadClaimLink(t.Context(), servicesDeployment(t), claimAppURL, time.Now().Add(-time.Minute))
	if err != nil || got != "" {
		t.Errorf("got %q, %v; want empty and no error", got, err)
	}
}

func TestReadClaimLink_ForgedNoticeIsNoLink(t *testing.T) {
	fakeDockerLoggingAndPrinting(t, jsonNotice("https://evil.example/#claim=x", "x")+"\n", 0)
	got, err := ReadClaimLink(t.Context(), servicesDeployment(t), claimAppURL, time.Now().Add(-time.Minute))
	if err != nil || got != "" {
		t.Errorf("got %q, %v; want empty and no error", got, err)
	}
}

// A docker that fails is an error, and the error text never carries the
// link or the code, even though the logs held them.
func TestReadClaimLink_FailedDockerIsAnErrorThatLeaksNothing(t *testing.T) {
	fakeDockerLoggingAndPrinting(t, jsonNotice(claimLinkA, claimCode1)+"\nCannot connect to the Docker daemon\n", 1)
	_, err := ReadClaimLink(t.Context(), servicesDeployment(t), claimAppURL, time.Now().Add(-time.Minute))
	if err == nil {
		t.Fatal("want an error when docker exits non-zero")
	}
	for _, secret := range []string{claimCode1, "#claim=", claimLinkA} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("the error text carries %q: %v", secret, err)
		}
	}
}

// Nothing is written into the deployment folder: the link goes to the
// screen and the clipboard only.
func TestReadClaimLink_WritesNoFile(t *testing.T) {
	fakeDockerLoggingAndPrinting(t, jsonNotice(claimLinkA, claimCode1)+"\n", 0)
	dir := servicesDeployment(t)
	if _, err := ReadClaimLink(t.Context(), dir, claimAppURL, time.Now().Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if body, rerr := os.ReadFile(path); rerr == nil && strings.Contains(string(body), claimCode1) {
			t.Errorf("%s holds the claim code", path)
		}
		return nil
	})
}

// It owns a time limit: a docker that never answers is cut off, with an
// error, once the context ends.
func TestReadClaimLink_SlowDockerIsCutOffWithAnError(t *testing.T) {
	hangingDockerOnPath(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	start := time.Now()
	got, err := ReadClaimLink(ctx, servicesDeployment(t), claimAppURL, time.Now().Add(-time.Minute))
	if err == nil {
		t.Errorf("got %q, nil; want an error for a docker that never answers", got)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("took %v to give up on a 1s context", took)
	}
}
