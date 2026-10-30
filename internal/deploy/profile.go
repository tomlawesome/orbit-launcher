package deploy

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

// ProfileEnv names the variable that tells install.sh which deployment
// profile a plain-mode install is for (Orbit's "Profile request (v0)",
// docs/engine-events.md). install.sh forwards it to the engine by name,
// so the value rides in the environment and never in argv.
const ProfileEnv = "ORBIT_INSTALL_PROFILE"

// The profile names the engine accepts in v0. The launcher's
// "Documents" row is Orbit's "processing" profile.
const (
	ProfileStandard  = "standard"
	ProfileDocuments = "processing"
)

// ScannerEnv names the variable that tells install.sh whether a fresh
// install keeps the virus scanner. It rides in the environment only,
// and is never sent empty: the engine refuses any value but the two
// below, before it writes anything.
const ScannerEnv = "ORBIT_INSTALL_SCANNER"

// The two values the engine accepts for ScannerEnv.
const (
	ScannerOn  = "on"
	ScannerOff = "off"
)

const gigabyte = uint64(1) << 30

// MemoryDefaults is each container's memory limit as Orbit ships it:
// the default of the matching variable in Orbit's docker-compose.yml
// (`mem_limit: ${NAME:-default}`) and .env-orbit.example. The live test
// reads an installed Orbit and fails when these drift.
var MemoryDefaults = map[string]uint64{
	"ORBIT_MEMORY_LIMIT":  2 * gigabyte, // Orbit itself
	"CLAMAV_MEMORY_LIMIT": 4 * gigabyte, // the scanner
	"TIKA_MEMORY_LIMIT":   1 * gigabyte, // the attachment reader (Documents)
}

// ProfileNeed is the memory a machine needs to run profile: Orbit
// itself, plus the scanner only when scannerOn, plus the reader for
// Documents. Standard is 6 GB with the scanner and 2 GB without;
// Documents is 7 GB and 3 GB. A caller holding a deployment passes
// d.ScanMode != ScanModeDisabled. An unknown profile needs nothing.
func ProfileNeed(profile string, scannerOn bool) uint64 {
	need := MemoryDefaults["ORBIT_MEMORY_LIMIT"]
	if scannerOn {
		need += MemoryDefaults["CLAMAV_MEMORY_LIMIT"]
	}
	switch profile {
	case ProfileStandard:
		return need
	case ProfileDocuments:
		return need + MemoryDefaults["TIKA_MEMORY_LIMIT"]
	}
	return 0
}

// WithScanner adds ScannerEnv=scanner to cmd's environment, after the
// person's own. An empty scanner adds nothing: an update never sends one.
func WithScanner(cmd *exec.Cmd, scanner string) {
	if scanner == "" {
		return
	}
	cmd.Env = append(cmd.Env, ScannerEnv+"="+scanner)
}

// CurrentProfile is the profile an installed deployment runs, read from
// the COMPOSE_PROFILES list Detect has cleaned and split: no profiles is
// Standard, exactly "processing" is Documents. ok is false for anything
// else (a local model, a mix, a duplicate, another case or an unknown
// entry), which the launcher cannot change. Detect drops empty entries,
// so "processing," reads as Documents and "," as Standard.
func CurrentProfile(d *Deployment) (profile string, ok bool) {
	if d == nil {
		return "", false
	}
	switch {
	case len(d.Profiles) == 0:
		return ProfileStandard, true
	case len(d.Profiles) == 1 && d.Profiles[0] == ProfileDocuments:
		return ProfileDocuments, true
	}
	return "", false
}

// WithProfile adds ProfileEnv=profile to cmd's environment, after
// whatever the person's own environment holds so it wins. An empty
// profile adds nothing: an Update run sends none, while Install and
// Configure (a profile change, which also runs as an update) name theirs.
func WithProfile(cmd *exec.Cmd, profile string) {
	if profile == "" {
		return
	}
	cmd.Env = append(cmd.Env, ProfileEnv+"="+profile)
}

// ReadHostMemory is how HostMemory reads the machine; a variable so
// tests can stand in a machine of any size.
var ReadHostMemory = readProcMeminfo

// HostMemory is the machine's total memory in bytes. ok is false when
// it cannot be read (not Linux, or /proc/meminfo unreadable); the
// launcher then never guesses.
func HostMemory() (bytes uint64, ok bool) {
	return ReadHostMemory()
}

func readProcMeminfo() (uint64, bool) {
	if runtime.GOOS != "linux" {
		return 0, false
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	defer f.Close()
	return parseMemTotal(f)
}

// parseMemTotal reads the MemTotal line of /proc/meminfo, kilobytes,
// and returns bytes.
func parseMemTotal(r io.Reader) (uint64, bool) {
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		rest, found := strings.CutPrefix(sc.Text(), "MemTotal:")
		if !found {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) != 2 || fields[1] != "kB" {
			return 0, false
		}
		kb, err := strconv.ParseUint(fields[0], 10, 64)
		if err != nil || kb > ^uint64(0)/1024 {
			return 0, false
		}
		return kb * 1024, true
	}
	return 0, false
}
