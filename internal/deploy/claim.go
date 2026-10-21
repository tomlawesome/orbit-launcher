package deploy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// claimLinkTimeout bounds the read-only `docker compose logs`: the link
// only decorates the success screen, so an unreachable daemon costs a
// few seconds and the screen stays as it was (#207: the limit lives
// beside the call, not in the UI).
const claimLinkTimeout = 4 * time.Second

// The two forms of the notice Orbit prints while nobody has claimed it
// (#259): a JSON log line with this event, or this line of text with
// the link on the next line after two spaces.
const (
	claimEvent      = "bootstrap.claim"
	claimTextHeader = "Orbit is not yet claimed. Open this link to create the first administrator:"
	claimTextIndent = "  "
	claimFragment   = "claim="
)

// ReadClaimLink is the claim link in the app's logs since the install
// started, or "" when there is none. It is read-only. The link and its
// code go back to the caller only: never into an error, a log or a
// file. The folder must be one nobody else could have written
// (RequireTrustedPath), as for every other Compose call.
func ReadClaimLink(ctx context.Context, dir, appURL string, since time.Time) (string, error) {
	if err := RequireTrustedPath(dir, EnvFile); err != nil {
		return "", err
	}
	limit := limitApplied(ctx, claimLinkTimeout)
	ctx, cancel := context.WithTimeout(ctx, claimLinkTimeout)
	defer cancel()
	// Output keeps stderr on the error value, but the error's text is
	// only the exit status, so neither stream reaches the message.
	out, err := claimLogsCommand(ctx, dir, since).Output()
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("docker compose logs did not finish within %s: %w", limit, err)
		}
		return "", fmt.Errorf("docker compose logs: %w", err)
	}
	return ParseClaimLink(out, appURL), nil
}

// claimLogsCommand builds the exact command ReadClaimLink runs.
// --env-file is passed for the same reason StandDown passes it.
func claimLogsCommand(ctx context.Context, dir string, since time.Time) *exec.Cmd {
	envFile := filepath.Join(dir, EnvFile)
	return dockerCommand(ctx, "compose",
		"--project-directory", dir, "--env-file", envFile,
		"logs", "--no-color", "--no-log-prefix", "--since", since.Format(time.RFC3339),
		"orbit-app")
}

// ParseClaimLink is the claim link in Orbit's log output, or "". The
// last notice in the output is the one judged, and a link is trusted
// only if it is this deployment's claim page: the scheme and host of
// appURL and a fragment starting claim=. A last notice that fails the
// checks gives "", never an earlier one: a forged line after an honest
// one shows nothing rather than a link nobody can vouch for.
func ParseClaimLink(out []byte, appURL string) string {
	lines := strings.Split(string(out), "\n")
	link, found := "", false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if candidate, ok := jsonClaimNotice(line); ok {
			link, found = candidate, true
			continue
		}
		if strings.TrimSpace(line) == claimTextHeader {
			link, found = "", true
			if i+1 < len(lines) {
				next := strings.TrimRight(lines[i+1], "\r")
				if strings.HasPrefix(next, claimTextIndent) {
					link = strings.TrimSpace(next)
					i++
				}
			}
		}
	}
	if !found || !isClaimLinkFor(link, appURL) {
		return ""
	}
	return link
}

// jsonClaimNotice reads one log line as the JSON notice: ok when the
// line is a JSON object whose event is the claim event, with its url
// ("" when the url is missing or not a string).
func jsonClaimNotice(line string) (link string, ok bool) {
	trimmed := bytes.TrimSpace([]byte(line))
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return "", false
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(trimmed, &fields) != nil {
		return "", false
	}
	var event string
	if json.Unmarshal(fields["event"], &event) != nil || event != claimEvent {
		return "", false
	}
	_ = json.Unmarshal(fields["url"], &link)
	return link, true
}

// isClaimLinkFor reports whether link is a claim link for appURL. It is
// printable ASCII only, since it is drawn on the screen and handed to
// the terminal as a hyperlink target, so no byte of it can be read as
// a control sequence; it carries no user name, which could make
// another host read as this one.
func isClaimLinkFor(link, appURL string) bool {
	if link == "" {
		return false
	}
	for i := 0; i < len(link); i++ {
		if link[i] <= ' ' || link[i] >= 0x7f {
			return false
		}
	}
	app, err := url.Parse(appURL)
	if err != nil || app.Host == "" {
		return false
	}
	u, err := url.Parse(link)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	return u.Scheme == app.Scheme && strings.EqualFold(u.Host, app.Host) &&
		strings.HasPrefix(u.Fragment, claimFragment)
}
