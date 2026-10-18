package live

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"
)

// The update leg's evidence (#198): which image the configure one-offs ran
// while the launcher collected configuration. Kept free of the live build
// tag so `go test ./...` checks it on every run; the leg itself, which needs
// Docker and a real Orbit, is update_test.go.

// containerCreate is one container-create record from
// `docker events --format '{{json .}}'`.
type containerCreate struct {
	At    time.Time
	Image string // the image the container was created from, as the daemon names it
	Name  string
	// Project is the container's com.docker.compose.project label: set on a
	// Compose service, empty on a one-off such as configure.sh's
	// `docker run --rm`.
	Project string
}

// dockerEvent is the part of a `docker events` JSON line this reads. The
// deprecated top-level status/id/from fields are not used: newer daemons
// no longer send them.
type dockerEvent struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	TimeNano int64 `json:"timeNano"`
}

// parseContainerCreates reads a `docker events --format '{{json .}}'`
// recording and returns its container creates, in order. Any line that is
// not an event fails the parse: a recording broken that way must not read
// as "no container ran on the old image".
func parseContainerCreates(r io.Reader) ([]containerCreate, error) {
	var creates []containerCreate
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for n := 1; scanner.Scan(); n++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var ev dockerEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return nil, fmt.Errorf("docker events line %d is not an event: %w: %q", n, err, line)
		}
		if ev.Type != "container" || ev.Action != "create" {
			continue
		}
		attrs := ev.Actor.Attributes
		creates = append(creates, containerCreate{
			At:      time.Unix(0, ev.TimeNano),
			Image:   attrs["image"],
			Name:    attrs["name"],
			Project: attrs["com.docker.compose.project"],
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read docker events: %w", err)
	}
	return creates, nil
}

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// digestOf returns the sha256 digest a digest-pinned reference names, or ""
// for a reference without one.
func digestOf(ref string) string {
	_, digest, ok := strings.Cut(ref, "@")
	if !ok || !digestPattern.MatchString(digest) {
		return ""
	}
	return digest
}

// imageIdentity is one Orbit image: the manifest digest every reference to
// it carries, and the local image ID, in case a daemon reports that instead
// of the reference it was asked for.
type imageIdentity struct{ Digest, ID string }

// matches reports whether image, as a container-create event names it, is
// this image. The registry part is ignored: install.sh, configure.sh and
// Compose may each spell the repository differently, never the digest.
func (id imageIdentity) matches(image string) bool {
	if image == "" {
		return false
	}
	if d := digestOf(image); d != "" {
		return id.Digest != "" && d == id.Digest
	}
	if id.ID == "" {
		return false
	}
	return strings.TrimPrefix(image, "sha256:") == strings.TrimPrefix(id.ID, "sha256:")
}

// judgeConfigureImage is the update leg's verdict. creates is everything
// created during the update; [from, to] is the stretch in which only the
// launcher ran anything: after install.sh's refused first attempt had exited
// and before the second attempt started, while the launcher ran configure.sh
// in the tree install.sh handed over.
//
// It fails if any container at all came from the old image, and if no
// one-off came from the new image inside the window. The second rule is what
// stops a recording that missed the launcher's configure runs from passing.
// On success it returns a summary for the log.
func judgeConfigureImage(creates []containerCreate, from, to time.Time, old, updated imageIdentity) (string, error) {
	if old.Digest == "" || updated.Digest == "" || old.Digest == updated.Digest {
		return "", fmt.Errorf("the update must go from one image to a different one, got old %q and new %q", old.Digest, updated.Digest)
	}
	var fromOld []string
	inWindowNew, total := 0, 0
	for _, c := range creates {
		total++
		if old.matches(c.Image) {
			fromOld = append(fromOld, fmt.Sprintf("%s (%s) at %s", c.Name, c.Image, c.At.UTC().Format(time.RFC3339Nano)))
			continue
		}
		if c.Project == "" && updated.matches(c.Image) && !c.At.Before(from) && !c.At.After(to) {
			inWindowNew++
		}
	}
	if len(fromOld) > 0 {
		return "", fmt.Errorf("%d container(s) were created from the old image %s during the update; configure must run the image being installed:\n  %s",
			len(fromOld), old.Digest, strings.Join(fromOld, "\n  "))
	}
	if inWindowNew == 0 {
		return "", fmt.Errorf("no configure one-off was created from the new image %s while the launcher collected configuration (%s to %s); %d creates recorded in all",
			updated.Digest, from.UTC().Format(time.RFC3339Nano), to.UTC().Format(time.RFC3339Nano), total)
	}
	return fmt.Sprintf("%d configure one-off(s) ran the new image %s while the launcher collected configuration; none of the %d creates recorded during the update used the old image %s",
		inWindowNew, updated.Digest, total, old.Digest), nil
}

// envFileValue reads key from an env file the way configure.sh reads
// ORBIT_IMAGE (a plain read, never sourced; the last assignment wins),
// trimming surrounding whitespace and quotes.
func envFileValue(data []byte, key string) string {
	value := ""
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(k) != key {
			continue
		}
		value = strings.Trim(strings.TrimSpace(v), `"'`)
	}
	return value
}

// manifestImageDigest returns the image digest an Orbit release manifest
// names (.image.digest, the field install.sh installs from).
func manifestImageDigest(data []byte) (string, error) {
	var m struct {
		Image struct {
			Digest string `json:"digest"`
		} `json:"image"`
	}
	if err := json.Unmarshal(data, &m); err != nil {
		return "", fmt.Errorf("release manifest is not JSON: %w", err)
	}
	if !digestPattern.MatchString(m.Image.Digest) {
		return "", fmt.Errorf("release manifest names no valid image digest (got %q)", m.Image.Digest)
	}
	return m.Image.Digest, nil
}
