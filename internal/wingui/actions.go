package wingui

import (
	"fmt"
	"slices"

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

// executor returns the executor the interface acts through. Nothing here is
// ever a dry run: a person pressing a button means it.
func executor() *proxy.Executor {
	return &proxy.Executor{}
}

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
