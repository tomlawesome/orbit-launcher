package deploy

// Blind tests for #286: CurrentProfile reads which profile an installed
// deployment runs, from the COMPOSE_PROFILES list Detect has already
// cleaned and split. Written from the build notes and the settled list:
// no profiles is standard, exactly "processing" is processing, and
// anything else is not a profile the launcher can change.

import "testing"

// Fault: a deployment the launcher can change being refused, or one it
// cannot (a local model, a mix, an odd spelling) being accepted and so
// having its services changed under it. Each unsupported case is paired
// with the supported ones in the same table.
func TestCurrentProfile286_OnlyNothingOrExactlyProcessingIsSupported(t *testing.T) {
	for _, c := range []struct {
		name     string
		profiles []string
		want     string
		ok       bool
	}{
		{"nil list is Standard", nil, "standard", true},
		{"empty list is Standard", []string{}, "standard", true},
		{"processing is Documents", []string{"processing"}, "processing", true},
		{"ai is a local model", []string{"ai"}, "", false},
		{"full is a local model", []string{"full"}, "", false},
		{"processing with ai is a mix", []string{"processing", "ai"}, "", false},
		{"ai with processing is a mix", []string{"ai", "processing"}, "", false},
		{"processing twice is not exactly processing", []string{"processing", "processing"}, "", false},
		{"other case is not processing", []string{"Processing"}, "", false},
		{"an empty entry is unexpected", []string{""}, "", false},
		{"an unknown profile", []string{"bogus"}, "", false},
		{"standard is not a compose profile", []string{"standard"}, "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			got, ok := CurrentProfile(&Deployment{Profiles: c.profiles})
			if ok != c.ok {
				t.Fatalf("CurrentProfile(%q) ok = %v, want %v", c.profiles, ok, c.ok)
			}
			if ok && got != c.want {
				t.Errorf("CurrentProfile(%q) = %q, want %q", c.profiles, got, c.want)
			}
		})
	}
}

// Fault: the spelling of the value in .env-orbit leaking into the
// answer. Spacing, quotes and a CRLF line end are Detect's job, so the
// same profile written any of these ways reads the same; a quoted mix
// is still a mix. Read through Detect, as the launcher does.
func TestCurrentProfile286_ReadsTheValueAsDetectCleansIt(t *testing.T) {
	for _, c := range []struct {
		name, line string
		want       string
		ok         bool
	}{
		{"no line", "", "standard", true},
		{"empty value", "COMPOSE_PROFILES=\n", "standard", true},
		{"empty quotes", "COMPOSE_PROFILES=\"\"\n", "standard", true},
		{"bare processing", "COMPOSE_PROFILES=processing\n", "processing", true},
		{"padded processing", "COMPOSE_PROFILES=  processing  \n", "processing", true},
		{"quoted processing", "COMPOSE_PROFILES=\"processing\"\n", "processing", true},
		{"single quoted, CRLF", "COMPOSE_PROFILES='processing'\r\n", "processing", true},
		{"bare mix", "COMPOSE_PROFILES=processing,ai\n", "", false},
		{"quoted mix", "COMPOSE_PROFILES=\"processing,ai\"\n", "", false},
		{"padded mix", "COMPOSE_PROFILES=  processing , ai \n", "", false},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := detectEnv(t, "APP_URL=https://mail.example.com\n"+c.line)
			got, ok := CurrentProfile(d)
			if ok != c.ok {
				t.Fatalf("%q: ok = %v, want %v (profiles read as %q)", c.line, ok, c.ok, d.Profiles)
			}
			if ok && got != c.want {
				t.Errorf("%q: profile = %q, want %q", c.line, got, c.want)
			}
		})
	}
}
