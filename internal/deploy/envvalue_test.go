package deploy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// #208 (#199 WI-4, EN-2). Every .env-orbit value Detect reads goes through
// one rule, Compose's own for an env file: trim the surrounding space,
// then strip one matching pair of quotes. A quoted or padded APP_URL must
// reach ProbeHealth clean, not as `"https://..."` that no client can dial.

// detectEnv writes body as the target's .env-orbit and returns what Detect
// makes of it.
func detectEnv(t *testing.T, body string) *Deployment {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env-orbit"), []byte(body), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	d, err := Detect(dir)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if d == nil {
		t.Fatalf("Detect = nil for %q, want a recognised deployment", body)
	}
	return d
}

// Done-criterion of #208: the URL the splash probes is the one the person
// configured, whichever way the file quotes or pads it.
func TestDetect_QuotedOrPaddedAppURLReachesProbeHealthClean(t *testing.T) {
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for name, line := range map[string]string{
		"double quotes":         `APP_URL="` + srv.URL + `"`,
		"single quotes":         `APP_URL='` + srv.URL + `'`,
		"padded":                "APP_URL=  " + srv.URL + "  ",
		"padded then quoted":    `APP_URL= "` + srv.URL + `" `,
		"tab padded and quoted": "APP_URL=\t'" + srv.URL + "'\t",
		"CRLF line ending":      "APP_URL=\"" + srv.URL + "\"\r",
	} {
		t.Run(name, func(t *testing.T) {
			d := detectEnv(t, line+"\n")
			if d.AppURL != srv.URL {
				t.Errorf("AppURL = %q, want %q: the value reaches ProbeHealth with its quotes or padding", d.AppURL, srv.URL)
			}
			before := requests.Load()
			if !ProbeHealth(context.Background(), d.AppURL) {
				t.Errorf("ProbeHealth(%q) = degraded against a healthy server: the probe never reached it", d.AppURL)
			}
			if requests.Load() == before {
				t.Errorf("ProbeHealth(%q) sent no request to %s", d.AppURL, srv.URL)
			}
		})
	}
}

// scalarKeys are the single-value keys Detect reads, with the field each
// one lands in.
var scalarKeys = map[string]func(*Deployment) string{
	"APP_URL":                      func(d *Deployment) string { return d.AppURL },
	"ORBIT_IMAGE":                  func(d *Deployment) string { return d.Image },
	"COMPOSE_PROJECT_NAME":         func(d *Deployment) string { return d.Project },
	"ORBIT_CONFIG_APPLIED_VERSION": func(d *Deployment) string { return d.Version },
}

// envValueCases is the rule itself, raw value to cleaned value: trim, then
// one matching pair of quotes comes off, and nothing more.
var envValueCases = []struct {
	name, raw, want string
}{
	{"bare", `v1`, `v1`},
	{"double quoted", `"v1"`, `v1`},
	{"single quoted", `'v1'`, `v1`},
	{"padded", "  v1  ", `v1`},
	{"tab padded", "\tv1\t", `v1`},
	{"padded then double quoted", ` "v1" `, `v1`},
	{"padded then single quoted", ` 'v1' `, `v1`},
	{"space inside the quotes is kept", `" v1 "`, ` v1 `},
	{"only one pair comes off", `""v1""`, `"v1"`},
	{"single inside double is kept", `"'v1'"`, `'v1'`},
	{"mismatched quotes are not a pair", `"v1'`, `"v1'`},
	{"opening quote alone is kept", `"v1`, `"v1`},
	{"closing quote alone is kept", `v1'`, `v1'`},
	{"a lone quote is not a pair", `"`, `"`},
	{"empty quotes are empty", `""`, ``},
	{"inner quotes are kept", `v"1"`, `v"1"`},
}

// EN-2: the same rule for every key, not five rules in one parser. The one
// difference after cleaning is the project name, where an empty value is
// no name and Compose uses the compose file's own, as with no key at all.
func TestDetect_CleansEveryKeyTheSameWay(t *testing.T) {
	defaultProject := detectEnv(t, "APP_URL=https://orbit.example.com\n").Project
	for key, field := range scalarKeys {
		for _, c := range envValueCases {
			t.Run(key+"/"+c.name, func(t *testing.T) {
				// APP_URL keeps the deployment recognised whichever key is
				// under test.
				body := key + "=" + c.raw + "\n"
				if key != "APP_URL" {
					body = "APP_URL=https://orbit.example.com\n" + body
				}
				want := c.want
				if key == "COMPOSE_PROJECT_NAME" && want == "" {
					want = defaultProject
				}
				d := detectEnv(t, body)
				if got := field(d); got != want {
					t.Errorf("%s=%s read as %q, want %q", key, c.raw, got, want)
				}
			})
		}
	}
}

// COMPOSE_PROFILES is the one list: the value is cleaned as a whole first,
// then split on commas, so a quoted list is not read as `"processing` and
// `ai"`.
func TestDetect_QuotedProfilesListIsCleanedBeforeItIsSplit(t *testing.T) {
	for name, raw := range map[string]string{
		"bare":                      `processing,ai`,
		"double quoted":             `"processing,ai"`,
		"single quoted":             `'processing,ai'`,
		"padded then quoted":        ` "processing,ai" `,
		"quoted with padded items":  `"processing, ai"`,
		"padded with padded items":  `  processing , ai  `,
		"single quoted, CRLF ended": "'processing,ai'\r",
	} {
		t.Run(name, func(t *testing.T) {
			d := detectEnv(t, "APP_URL=https://orbit.example.com\nCOMPOSE_PROFILES="+raw+"\n")
			if want := []string{"processing", "ai"}; !equalStrings(d.Profiles, want) {
				t.Errorf("COMPOSE_PROFILES=%s read as %q, want %q", raw, d.Profiles, want)
			}
		})
	}
}

// Empty quotes are an empty list, as a bare empty value already is.
func TestDetect_EmptyQuotedProfilesIsEmpty(t *testing.T) {
	for _, raw := range []string{`""`, `''`, ` "" `} {
		d := detectEnv(t, "APP_URL=https://orbit.example.com\nCOMPOSE_PROFILES="+raw+"\n")
		if len(d.Profiles) != 0 {
			t.Errorf("COMPOSE_PROFILES=%s read as %q, want no profiles", raw, d.Profiles)
		}
	}
}

// The rule covers the value only: a value holding "=" keeps it.
func TestDetect_ValueKeepsItsOwnEquals(t *testing.T) {
	d := detectEnv(t, `APP_URL="https://orbit.example.com/?a=b"`+"\n")
	if want := "https://orbit.example.com/?a=b"; d.AppURL != want {
		t.Errorf("AppURL = %q, want %q", d.AppURL, want)
	}
	if strings.ContainsAny(d.AppURL, `"'`) {
		t.Errorf("AppURL = %q still carries quotes", d.AppURL)
	}
}
