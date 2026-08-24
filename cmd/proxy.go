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
func applyConfig(cfg proxy.Config, targetNames []string, dryRun, viaLocal bool) error {
	ex := &proxy.Executor{DryRun: dryRun}
	rep, err := app.Apply(deps(), ex, cfg, targetNames, viaLocal)
	if err != nil {
		return err
	}
	renderReport(stdout(), rep)
	return rep.Err()
}

// applyViaLocal writes the plumbing: it points the targets at the local
// daemon and records that fact. The caller has already decided which profile
// the daemon should serve (an ad-hoc "_current" or a named one) by setting
// pf.ActiveProfile; this function never inspects or changes that choice, it
// only makes sure the targets reach the daemon and that the daemon re-reads
// the config.
func applyViaLocal(pf *proxy.ProfileFile, cfg proxy.Config, targetNames []string, dryRun bool) error {
	ex := &proxy.Executor{DryRun: dryRun}
	rep, err := app.ApplyViaLocal(deps(), ex, pf, cfg, targetNames)
	if err != nil {
		return err
	}
	renderReport(stdout(), rep)
	return rep.Err()
}

// clearTargets resolves targetNames and removes proxy settings from each.
func clearTargets(targetNames []string, dryRun bool) error {
	ex := &proxy.Executor{DryRun: dryRun}
	rep, err := app.Clear(deps(), ex, targetNames)
	if err != nil {
		return err
	}
	renderReport(stdout(), rep)
	return rep.Err()
}
