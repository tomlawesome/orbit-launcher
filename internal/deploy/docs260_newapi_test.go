package deploy

// Blind tests for #260 (the Documents profile in the install flow) that
// need the new API: ProfileEnv, HostMemory, ReadHostMemory and the
// parser parseMemTotal(io.Reader) (uint64, bool). Written from the
// specification alone.

import (
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/iotest"
)

// Spec (#260): the variable that carries the profile to install.sh.
func TestDocs260_ProfileEnvIsTheInstallProfileVariable(t *testing.T) {
	if ProfileEnv != "ORBIT_INSTALL_PROFILE" {
		t.Errorf("ProfileEnv = %q, want ORBIT_INSTALL_PROFILE", ProfileEnv)
	}
}

// Spec 8 (#260): MemTotal is in kB and is returned in bytes.
func TestDocs260_ParseMemTotalReadsKilobytesAsBytes(t *testing.T) {
	got, ok := parseMemTotal(strings.NewReader("MemTotal:       16384000 kB\n"))
	if !ok || got != 16384000*1024 {
		t.Errorf("parseMemTotal = %d, %v; want %d, true", got, ok, uint64(16384000)*1024)
	}
}

// Spec 8 (#260): a whole /proc/meminfo, MemTotal not on its first line,
// with other lines that must not be mistaken for it.
func TestDocs260_ParseMemTotalFindsTheLineInAWholeMeminfo(t *testing.T) {
	const meminfo = "SwapTotal:       2097148 kB\n" +
		"MemFree:         1234567 kB\n" +
		"MemTotal:       16384000 kB\n" +
		"MemAvailable:   12000000 kB\n" +
		"Buffers:           10000 kB\n"
	got, ok := parseMemTotal(strings.NewReader(meminfo))
	if !ok || got != 16384000*1024 {
		t.Errorf("parseMemTotal = %d, %v; want %d, true", got, ok, uint64(16384000)*1024)
	}
}

// Spec 8 (#260): a missing or malformed file gives ok=false.
func TestDocs260_ParseMemTotalRefusesWhatItCannotRead(t *testing.T) {
	for name, in := range map[string]string{
		"empty":             "",
		"no MemTotal":       "MemFree:         1234567 kB\nSwapTotal: 100 kB\n",
		"no number":         "MemTotal:       kB\n",
		"not a number":      "MemTotal:       lots kB\n",
		"negative":          "MemTotal:       -16384000 kB\n",
		"too big for 64":    "MemTotal:       99999999999999999999999 kB\n",
		"unrelated content": "hello\nworld\n",
		"value only":        "16384000\n",
	} {
		t.Run(name, func(t *testing.T) {
			if got, ok := parseMemTotal(strings.NewReader(in)); ok {
				t.Errorf("parseMemTotal(%q) = %d, true; want ok=false", in, got)
			}
		})
	}
}

// Spec 8 (#260): a file that fails to read gives ok=false.
func TestDocs260_ParseMemTotalRefusesAReaderThatFails(t *testing.T) {
	if got, ok := parseMemTotal(iotest.ErrReader(os.ErrPermission)); ok {
		t.Errorf("parseMemTotal on a failing reader = %d, true; want ok=false", got)
	}
}

// Spec (#260): on Linux HostMemory and the ReadHostMemory seam's
// default both report the machine's MemTotal.
func TestDocs260_HostMemoryReadsTheMachinesMemTotal(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("reads /proc/meminfo")
	}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		t.Skip("no /proc/meminfo here")
	}
	defer f.Close()
	want, ok := parseMemTotal(f)
	if !ok {
		t.Skip("this machine's /proc/meminfo has no MemTotal")
	}
	if got, ok := HostMemory(); !ok || got != want {
		t.Errorf("HostMemory() = %d, %v; want %d, true", got, ok, want)
	}
	if ReadHostMemory == nil {
		t.Fatal("ReadHostMemory is nil, want the default reader")
	}
	if got, ok := ReadHostMemory(); !ok || got != want {
		t.Errorf("ReadHostMemory() = %d, %v; want %d, true", got, ok, want)
	}
}
