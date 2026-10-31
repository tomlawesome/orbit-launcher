//go:build live

package live

// The install-flow screens every leg waits on, each as the wordings it
// accepts. Two launchers draw them: the one under test, and the one the
// earlier Orbit release ships, which the update leg's first install runs
// (#307). Orbit pins its launcher by hand to a released tag, and that
// earlier release cannot move until a change here is promoted and
// released. So renaming a screen in the launcher adds the new wording
// beside the old one here; the old one goes only once the live log's
// "InstallEarlier drives the earlier release's launcher <tag>" names a
// launcher that draws the new wording. The same holds for a guard that a
// screen is gone, and for the main-menu order choose() reads from
// ui.MainMenu.
var (
	screenMenuInstall = []string{"Install"}
	screenProfile     = []string{"Choose a deployment profile"}
	screenNotice      = []string{"A note before you install"}
	// Typed, not read: passNotice types whichever wording the notice shows.
	noticeAcknowledgement = []string{"I've read this and I understand"}
	screenRunStarted      = []string{"Contacting the engine", "ORBIT · Install"}
	screenSignInQuestion  = []string{"How will people sign in?"}
	screenLocalAccounts   = []string{"Local accounts"}
	// An update's failure screen says "Update stopped" (flowStoppedTitle).
	screenStopped      = []string{"Install stopped", "Update stopped"}
	screenInstallStop  = []string{"Install stopped"}
	screenOIDCIssuer   = []string{"OIDC issuer URL"}
	screenOIDCClientID = []string{"OIDC client ID"}
	screenOIDCSecret   = []string{"OIDC client secret"}
	screenSuccess      = []string{"Get into Orbit"}
	// The running check's second-folder screens (#316): the splash row
	// that says the stack was started from another folder, and the refusal
	// Update opens on there. Only the launcher under test draws them: the
	// earlier release's launcher never runs in the second folder, so no
	// older wording sits beside these.
	screenOtherFolder        = []string{"started from another folder"}
	screenOtherFolderRefusal = []string{"This Orbit was started from"}
	// The same check from Install (#318): the refusal a launcher in a
	// folder with no deployment opens Install on while the stack runs from
	// another. The launcher under test alone draws it, with no older
	// wording beside it.
	screenInstallOtherFolder = []string{"This machine's Orbit was started from"}
	// And the advisory Install opens on when the Orbit is under another
	// Compose project (#321). The launcher under test alone draws it, with
	// no older wording beside it.
	screenInstallOtherProject = []string{"There is already an Orbit on this machine"}
)

// inConsolePromptMarker is the guidance line the launcher prints under
// its own APP_URL prompt (internal/ui/configcollect.go, promptWords).
//
// It has to be this rather than the field's label: the labels are
// deliberately the same words install.sh's own TTY prompts use, so
// "Public Orbit origin" matches whichever path ran and proves neither.
// This sentence is written in this repository and rendered only by
// viewConfigCollect, i.e. only while the launcher itself is drawing the
// prompt. On the handoff path the launcher draws nothing at all —
// install.sh owns the terminal — and the string appears nowhere in the
// orbit repository, checked.
//
// It stays a single string: acceptMenusUntil's target is one wording.
const inConsolePromptMarker = "the https:// address Orbit will live at"

// removedConfigScreenText is what the screen the engine's configuration
// stop used to land on showed (#251): the stop goes straight into guided
// configuration now. Every wait fails if one of these comes up.
var removedConfigScreenText = []string{"The engine stopped before touching anything", "Continue — guided configuration"}

// screens joins vocabulary entries into one list of wordings for
// vtscreen.ContainsAny, copying so no entry's backing array is written.
func screens(entries ...[]string) []string {
	var all []string
	for _, e := range entries {
		all = append(all, e...)
	}
	return all
}
