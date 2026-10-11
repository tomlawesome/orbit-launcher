package deploy

// The launcher's environment variables (EN-14): every ORBIT_LAUNCHER_*
// name it reads, typed once. A switch set to anything non-empty is on.
const (
	// ConfigTreeEnv names the private directory install.sh copies its
	// verified configuration tree into when it refuses for missing
	// configuration (ai/orbit#1225). The launcher runs configure.sh from
	// there and nowhere else (#190).
	ConfigTreeEnv = "ORBIT_LAUNCHER_CONFIG_TREE"
	// InstallScriptURLEnv overrides where install.sh is fetched from
	// (see FetchInstallScript).
	InstallScriptURLEnv = "ORBIT_LAUNCHER_INSTALL_SCRIPT_URL"
	// InstallScriptPathEnv names a local install.sh to use instead of
	// fetching one (see FetchInstallScript).
	InstallScriptPathEnv = "ORBIT_LAUNCHER_INSTALL_SCRIPT_PATH"
	// NoAnimationEnv skips the splash's arrival animation and its ticks.
	NoAnimationEnv = "ORBIT_LAUNCHER_NO_ANIMATION"
	// NoUpdateCheckEnv skips the check for a newer launcher release.
	NoUpdateCheckEnv = "ORBIT_LAUNCHER_NO_UPDATE_CHECK"
	// NoHealthProbeEnv skips probing the detected deployment's APP_URL.
	NoHealthProbeEnv = "ORBIT_LAUNCHER_NO_HEALTH_PROBE"
	// NoVolumeCheckEnv skips Install's stale-database-volume pre-flight
	// (#105).
	NoVolumeCheckEnv = "ORBIT_LAUNCHER_NO_VOLUME_CHECK"
	// RequireInConsoleConfigEnv makes a fallback from in-console
	// configuration to the terminal handoff a hard stop (#190).
	RequireInConsoleConfigEnv = "ORBIT_LAUNCHER_REQUIRE_IN_CONSOLE_CONFIG"
)
