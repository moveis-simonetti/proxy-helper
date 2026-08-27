package wingui

import (
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"
)

// deps wires the interface to the same orchestration the CLI uses, so both
// go through one implementation of "apply a profile".
//
// Notify stays nil on purpose: the CLI prints notices as they are raised, in
// step with the executor's own output; a window has no such stream and reads
// Report.Notices when the work ends.
func deps() app.Deps {
	return app.Deps{
		ResolveTargets: proxy.ByNames,
		DaemonActive:   serve.DaemonActive,
		ReloadDaemon:   serve.ReloadDaemon,
	}
}

// dryRunEnv turns every write into a preview.
//
// It exists for looking at the interface on a machine you do not want it to
// reconfigure — this app is developed on Linux, where "apply" would rewrite
// the developer's own shell, git and desktop settings. It is opt-in through
// the environment and never set by the app itself, so a real user cannot end
// up in it by accident and believe a change was applied.
const dryRunEnv = "MSPROXY_DRY_RUN"

// executor returns the executor the interface acts through.
func executor() *proxy.Executor {
	return &proxy.Executor{
		DryRun: os.Getenv(dryRunEnv) != "",
		Out:    consoleOut(),
		Stderr: consoleOut(),
	}
}

// consoleOut is where a command's output goes.
//
// On Windows this is io.Discard, and that is not a style choice. The
// interface is linked with -H windowsgui, so the process has no console:
// os.Stdout is an invalid handle and every write to it fails. exec.Command
// reports that failure as the command's own, which is how "schtasks
// /Create" came back as "exit status 1" — it was never rejected by Task
// Scheduler, it just could not write to the pipe we handed it.
//
// Elsewhere the output is kept: the interface is run from a terminal during
// development, where the dry-run preview is the whole point.
func consoleOut() io.Writer {
	if isWindows {
		return io.Discard
	}
	return os.Stdout
}

// DryRun reports whether writes are being previewed rather than made, so the
// window can say so instead of claiming a change it did not make.
func DryRun() bool { return os.Getenv(dryRunEnv) != "" }

// TurnOn points the system proxy at the local daemon and marks the profile
// active.
//
// viaLocal is always true, and that is the product: the daemon holds the
// credentials and injects them, so the password never reaches the registry
// and the browser never asks for it. Applying the upstream address directly
// would put us back to a proxy that prompts for a password on every
// application.
func TurnOn(profileName string) (*app.Report, error) {
	if profileName == "" {
		return nil, fmt.Errorf("nenhum perfil selecionado")
	}
	// Order matters: the daemon has to be answering before anything points
	// the system proxy at it, or the machine ends up routing every request
	// to a port nothing listens on.
	if err := EnsureDaemon(); err != nil {
		return nil, err
	}
	res, err := app.Enable(deps(), executor(), profileName, []string{"all"}, true)
	return res.Report, err
}

// TurnOff removes the proxy settings from every target and clears the
// active profile.
func TurnOff(profileName string) (*app.Report, error) {
	if profileName == "" {
		return nil, fmt.Errorf("nenhum perfil selecionado")
	}
	return app.Disable(deps(), executor(), profileName, []string{"all"})
}

// CurrentState reports what the machine looks like right now: which profile
// is active, and whether the targets actually carry it.
//
// It reads the targets rather than trusting the config file. A person can
// change the Windows proxy settings by hand, and a window that showed
// "Ligado" over settings that say otherwise would be lying about the one
// thing it exists to report.
func CurrentState() (on bool, profileName string, err error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return false, "", err
	}
	profileName = pf.ActiveProfile

	statuses, err := app.Collect(deps(), executor(), []string{"all"}, false)
	if err != nil {
		return false, profileName, err
	}
	for _, st := range statuses {
		if st.Available && st.Enabled {
			return true, profileName, nil
		}
	}
	return false, profileName, nil
}

// Profiles lists the saved profiles and which one is active.
func Profiles() (names []string, active string, err error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, "", err
	}
	for name := range pf.Profiles {
		if name == proxy.CurrentProfileName {
			// The reserved slot for a one-off "proxy set" is not a profile
			// anyone chose, so it never appears in a list of choices.
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return names, pf.ActiveProfile, nil
}

// UpstreamTrouble reports what the daemon last observed about the proxy,
// and for which profile.
//
// The daemon is a separate process, so this is the only way the window
// learns that something stopped working. Without it the person would see
// pages failing and find no explanation in the one app that exists to
// manage the proxy.
func UpstreamTrouble() (problem serve.UpstreamProblem, profile string) {
	state := serve.ReadUpstreamState()
	return state.Problem, state.Profile
}

// ProfileSummary is a profile as the list shows it: what it is called and
// where it points.
type ProfileSummary struct {
	Name    string
	Address string
	Active  bool
}

// ProfileSummaries lists the saved profiles with their addresses.
//
// The list shows the address under each name because the name alone does
// not say what the profile does — two profiles called "Casa" and "Casa 2"
// are indistinguishable until you can see where each one points.
func ProfileSummaries() ([]ProfileSummary, error) {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return nil, err
	}

	var out []ProfileSummary
	for name, cfg := range pf.Profiles {
		if name == proxy.CurrentProfileName {
			continue
		}
		out = append(out, ProfileSummary{
			Name:    name,
			Address: ProfileAddress(cfg),
			Active:  name == pf.ActiveProfile,
		})
	}
	slices.SortFunc(out, func(a, b ProfileSummary) int {
		return strings.Compare(a.Name, b.Name)
	})
	return out, nil
}

// ProfileAddress renders where a profile points, in the words the person
// typed it in.
func ProfileAddress(cfg proxy.Config) string {
	if cfg.PACURL != "" {
		return "configuração automática (.pac)"
	}
	if cfg.Host == "" {
		return "sem endereço"
	}
	return cfg.Host + ":" + cfg.Port
}

// SelectProfile marks a profile as the one to use, without applying it.
//
// Choosing which profile to use and turning the proxy on are two different
// decisions. Switching used to go through TurnOn, so picking a profile
// while the proxy was off turned it on — an action nobody asked for, and on
// a machine where that changes how every program reaches the internet.
func SelectProfile(name string) error {
	if name == "" {
		return fmt.Errorf("nenhum perfil informado")
	}
	_, err := app.On(deps(), executor(), name)
	return err
}
