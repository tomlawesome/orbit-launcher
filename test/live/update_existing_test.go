package live

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeInfo is a minimal os.FileInfo for the stat stub.
type fakeInfo struct{ name string }

func (f fakeInfo) Name() string       { return f.name }
func (f fakeInfo) Size() int64        { return 1 }
func (f fakeInfo) Mode() fs.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

const (
	exDir        = "/srv/orbit-existing"
	exManifest   = "/work/release-manifest.json"
	exOldDigest  = "sha256:2a16d15bf2be55bcd40e80947f05f6e32e9c9d55cc5abf4021f72c71dc7bbed8"
	exNewDigest  = "sha256:affc502ebf6820b7bd14f42a35f51c6f5827003bd1dfd81d8ae8c7ba9a647101"
	exEnvFile    = "ORBIT_IMAGE=127.0.0.1:5000/orbit@" + exOldDigest + "\nAPP_URL=https://orbit.example.test\n"
	exManifestOK = `{"version":"1.2.3","image":{"repository":"127.0.0.1:5000/orbit","digest":"` + exNewDigest + `"}}`
)

func TestExistingDeploymentInputs(t *testing.T) {
	baseEnv := map[string]string{
		"ORBIT_LIVE_EXISTING_DIR":            exDir,
		"ORBIT_RELEASE_MANIFEST":             exManifest,
		"ORBIT_LAUNCHER_LIVE_BINARY":         "/opt/launcher/orbit-launcher",
		"ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH": "/work/install.sh",
	}
	baseFiles := map[string]string{
		exDir + "/.env-orbit":         exEnvFile,
		exDir + "/docker-compose.yml": "name: orbit\n",
		exManifest:                    exManifestOK,
	}

	cases := []struct {
		name    string
		env     func(map[string]string)
		files   func(map[string]string)
		want    existingInputs
		wantErr string // substring; empty means success
	}{
		{
			name: "success with the install script path",
			want: existingInputs{
				Dir: exDir, Project: "orbit", AppURL: "https://orbit.example.test",
				OldDigest: exOldDigest, NewDigest: exNewDigest, NewVersion: "1.2.3",
				Binary:            "/opt/launcher/orbit-launcher",
				InstallScriptPath: "/work/install.sh",
			},
		},
		{
			name: "success with the install script URL alone and a recorded project",
			env: func(e map[string]string) {
				delete(e, "ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH")
				e["ORBIT_LAUNCHER_INSTALL_SCRIPT_URL"] = "https://scripts.example.test/install.sh"
			},
			files: func(f map[string]string) {
				f[exDir+"/.env-orbit"] = exEnvFile + "COMPOSE_PROJECT_NAME=acme-orbit\n"
			},
			want: existingInputs{
				Dir: exDir, Project: "acme-orbit", AppURL: "https://orbit.example.test",
				OldDigest: exOldDigest, NewDigest: exNewDigest, NewVersion: "1.2.3",
				Binary:           "/opt/launcher/orbit-launcher",
				InstallScriptURL: "https://scripts.example.test/install.sh",
			},
		},
		{
			name:    "dir unset",
			env:     func(e map[string]string) { delete(e, "ORBIT_LIVE_EXISTING_DIR") },
			wantErr: "ORBIT_LIVE_EXISTING_DIR",
		},
		{
			name:    "dir not absolute",
			env:     func(e map[string]string) { e["ORBIT_LIVE_EXISTING_DIR"] = "relative/orbit" },
			wantErr: "ORBIT_LIVE_EXISTING_DIR",
		},
		{
			name:    "env-orbit missing",
			files:   func(f map[string]string) { delete(f, exDir+"/.env-orbit") },
			wantErr: ".env-orbit",
		},
		{
			name:    "image missing",
			files:   func(f map[string]string) { f[exDir+"/.env-orbit"] = "APP_URL=https://orbit.example.test\n" },
			wantErr: "ORBIT_IMAGE",
		},
		{
			name: "image not pinned by digest",
			files: func(f map[string]string) {
				f[exDir+"/.env-orbit"] = "ORBIT_IMAGE=127.0.0.1:5000/orbit:latest\nAPP_URL=https://orbit.example.test\n"
			},
			wantErr: "ORBIT_IMAGE",
		},
		{
			name: "image digest too short",
			files: func(f map[string]string) {
				f[exDir+"/.env-orbit"] = "ORBIT_IMAGE=127.0.0.1:5000/orbit@sha256:abc123\nAPP_URL=https://orbit.example.test\n"
			},
			wantErr: "ORBIT_IMAGE",
		},
		{
			name: "app url missing",
			files: func(f map[string]string) {
				f[exDir+"/.env-orbit"] = "ORBIT_IMAGE=127.0.0.1:5000/orbit@" + exOldDigest + "\n"
			},
			wantErr: "APP_URL",
		},
		{
			name:    "compose file missing",
			files:   func(f map[string]string) { delete(f, exDir+"/docker-compose.yml") },
			wantErr: "docker-compose.yml",
		},
		{
			name:    "manifest variable unset",
			env:     func(e map[string]string) { delete(e, "ORBIT_RELEASE_MANIFEST") },
			wantErr: "ORBIT_RELEASE_MANIFEST",
		},
		{
			name:    "manifest unreadable",
			files:   func(f map[string]string) { delete(f, exManifest) },
			wantErr: "ORBIT_RELEASE_MANIFEST",
		},
		{
			name:    "manifest is not JSON",
			files:   func(f map[string]string) { f[exManifest] = "not json {" },
			wantErr: "ORBIT_RELEASE_MANIFEST",
		},
		{
			name:    "manifest has no image digest",
			files:   func(f map[string]string) { f[exManifest] = `{"version":"1.2.3","image":{"repository":"x"}}` },
			wantErr: "digest",
		},
		{
			name:    "manifest has no version",
			files:   func(f map[string]string) { f[exManifest] = `{"image":{"digest":"` + exNewDigest + `"}}` },
			wantErr: "version",
		},
		{
			name: "manifest digest equals the recorded digest",
			files: func(f map[string]string) {
				f[exManifest] = `{"version":"1.2.3","image":{"digest":"` + exOldDigest + `"}}`
			},
			wantErr: "digest",
		},
		{
			name:    "launcher binary unset",
			env:     func(e map[string]string) { delete(e, "ORBIT_LAUNCHER_LIVE_BINARY") },
			wantErr: "ORBIT_LAUNCHER_LIVE_BINARY",
		},
		{
			name:    "no install script source",
			env:     func(e map[string]string) { delete(e, "ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH") },
			wantErr: "ORBIT_LAUNCHER_INSTALL_SCRIPT",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			env := map[string]string{}
			for k, v := range baseEnv {
				env[k] = v
			}
			files := map[string]string{}
			for k, v := range baseFiles {
				files[k] = v
			}
			if tc.env != nil {
				tc.env(env)
			}
			if tc.files != nil {
				tc.files(files)
			}
			get := func(k string) string { return env[k] }
			stat := func(p string) (os.FileInfo, error) {
				if p == exDir {
					return fakeInfo{name: p}, nil
				}
				if _, ok := files[p]; ok {
					return fakeInfo{name: p}, nil
				}
				return nil, &fs.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
			}
			read := func(p string) ([]byte, error) {
				if s, ok := files[p]; ok {
					return []byte(s), nil
				}
				return nil, &fs.PathError{Op: "open", Path: p, Err: errors.New("no such file")}
			}

			got, err := existingDeploymentInputs(get, stat, read)
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("want an error naming %q, got none (inputs %+v)", tc.wantErr, got)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %q does not name %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("inputs = %+v\nwant     %+v", got, tc.want)
			}
		})
	}
}
