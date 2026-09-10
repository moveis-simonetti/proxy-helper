package cmd

import (
	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Configure proxy settings across shells and dev tools",
}

func init() {
	rootCmd.AddCommand(proxyCmd)
}

// Seams for tests. The real implementations reach into systemd and into the
// user's actual tool configs, which a unit test must never do; swapping them
// lets the tests assert exactly which Config each target receives.
var (
	resolveTargets = proxy.ByNames
	daemonActive   = serve.DaemonActive
	reloadDaemon   = serve.ReloadDaemon
	bridgeAddr     = serve.DockerBridgeAddr
)

// deps hands internal/app the seams this package owns, read at call time so
// the tests can swap them.
func deps() app.Deps {
	return app.Deps{
		ResolveTargets: resolveTargets,
		DaemonActive:   daemonActive,
		ReloadDaemon:   reloadDaemon,
		BridgeAddr:     bridgeAddr,
		InstallDaemon:  installDaemon,
		// Notify keeps warnings in their original position: printed at the
		// moment they are raised, interleaved with the executor's own
		// output and before the sudo prompt they warn about, rather than
		// only at the end.
		Notify: func(n app.Notice) { renderNotice(stdout(), n) },
	}
}

// applyConfig resolves targetNames and applies cfg to each, printing a
// result table. This is the legacy path: every target ends up holding the
// real upstream, credentials included.
//
// With viaLocal the ad-hoc cfg is parked in the reserved "_current" profile
// for the daemon to read, and only the plumbing is written to the targets.
//
// With restartDocker, and only when the dockerd target was itself applied
// successfully, the Docker daemon is restarted afterwards so the drop-in
// dockerd just wrote actually takes effect. Without the flag nothing is
// restarted — that is the existing behavior, and it is what keeps a routine
// "proxy set" from taking down someone else's running containers.
// profileName is the saved profile cfg came from, or "" when it was typed
// into flags. It travels with cfg so that applying a named profile via the
// local daemon keeps the name active instead of collapsing it into the
// reserved "_current" slot.
func applyConfig(profileName string, cfg proxy.Config, targetNames []string, dryRun, viaLocal, restartDocker bool) error {
	ex := &proxy.Executor{DryRun: dryRun}
	rep, err := app.Apply(deps(), ex, profileName, cfg, targetNames, viaLocal)
	if err != nil {
		return err
	}
	renderReport(stdout(), rep)
	if restartDocker && dockerdSucceeded(rep, app.OutcomeApplied) {
		if err := ex.RunPrivileged("systemctl", "restart", "docker"); err != nil {
			return err
		}
	}
	return rep.Err()
}

// clearTargets resolves targetNames and removes proxy settings from each.
//
// restartDocker behaves as it does for applyConfig, but gated on a
// successful Unset of the dockerd target instead of a Set.
func clearTargets(targetNames []string, dryRun, restartDocker bool) error {
	ex := &proxy.Executor{DryRun: dryRun}
	rep, err := app.Clear(deps(), ex, targetNames)
	if err != nil {
		return err
	}
	renderReport(stdout(), rep)
	if restartDocker && dockerdSucceeded(rep, app.OutcomeCleared) {
		if err := ex.RunPrivileged("systemctl", "restart", "docker"); err != nil {
			return err
		}
	}
	return rep.Err()
}

// dockerdSucceeded reports whether the dockerd target's Result carries the
// given outcome — the only condition under which restarting the daemon
// makes sense: it means dockerd actually wrote (or removed) the drop-in
// that the restart is meant to pick up.
func dockerdSucceeded(rep *app.Report, want app.Outcome) bool {
	for _, res := range rep.Results {
		if res.Target == "dockerd" && res.Outcome == want {
			return true
		}
	}
	return false
}
