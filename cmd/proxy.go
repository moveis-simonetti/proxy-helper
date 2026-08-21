package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

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
)

// runAcross applies fn to each target and prints a result table. It keeps
// going after a per-target failure so one bad target doesn't block the rest,
// then returns a combined error if anything failed.
func runAcross(targets []proxy.Target, fn func(proxy.Target) (string, error)) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tRESULT")

	var failed []string
	for _, t := range targets {
		result, err := fn(t)
		if err != nil {
			fmt.Fprintf(w, "%s\tFAILED: %v\n", t.Name(), err)
			failed = append(failed, t.Name())
			continue
		}
		fmt.Fprintf(w, "%s\t%s\n", t.Name(), result)
	}
	w.Flush()

	if len(failed) > 0 {
		return fmt.Errorf("failed targets: %s", strings.Join(failed, ", "))
	}
	return nil
}

// applyConfig resolves targetNames and applies cfg to each, printing a
// result table. This is the legacy path: every target ends up holding the
// real upstream, credentials included.
//
// With viaLocal the ad-hoc cfg is parked in the reserved "_current" profile
// for the daemon to read, and only the plumbing is written to the targets.
func applyConfig(cfg proxy.Config, targetNames []string, dryRun, viaLocal bool) error {
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return err
	}
	cfg.NoProxy = proxy.MergeNoProxy(pf.EffectiveGlobalNoProxy(), cfg.NoProxy)

	if viaLocal {
		// An ad-hoc config is not a named profile, so it goes in the
		// reserved slot: the daemon only ever reads active_profile.
		pf.SetCurrent(cfg)
		return applyViaLocal(pf, cfg, targetNames, dryRun)
	}

	targets, err := resolveTargets(targetNames)
	if err != nil {
		return err
	}
	warnUnreachablePassword(cfg)
	// The targets are about to hold the real upstream again, so the plumbing
	// flag must stop claiming they point at the loopback — but only when this
	// covers every target. A partial set (say, just the Docker ones) leaves
	// the rest plumbed, and clearing the flag there would silence the warning
	// that those targets depend on a daemon that may not be running.
	if pf.ViaLocal && !dryRun && proxy.SelectsAllTargets(targetNames) {
		pf.ViaLocal = false
		if err := pf.Save(); err != nil {
			return err
		}
	}
	return setAcross(targets, proxy.TargetConfig(cfg, false, 0), dryRun)
}

// warnUnreachablePassword reports a configuration that cannot work: the
// password lives in a file or environment variable, which only the daemon
// resolves, but the config is being written straight into each tool's own
// settings. Those tools would get a URL carrying a username and no password
// and would fail to authenticate, with nothing explaining why.
func warnUnreachablePassword(cfg proxy.Config) {
	if cfg.Password != "" || cfg.Username == "" {
		return
	}
	source := "password_file"
	if cfg.PasswordFile == "" {
		if cfg.PasswordEnv == "" {
			return
		}
		source = "password_env"
	}
	fmt.Printf("  warning: this profile's password comes from %s, which only the local proxy reads.\n", source)
	fmt.Println("           The targets below get a username with no password, so they will fail to authenticate.")
	fmt.Println("           Use --via-local to keep the credential in one place (see \"proxy serve\").")
}

// applyViaLocal writes the plumbing: it points the targets at the local
// daemon and records that fact. The caller has already decided which profile
// the daemon should serve (an ad-hoc "_current" or a named one) by setting
// pf.ActiveProfile; this function never inspects or changes that choice, it
// only makes sure the targets reach the daemon and that the daemon re-reads
// the config.
func applyViaLocal(pf *proxy.ProfileFile, cfg proxy.Config, targetNames []string, dryRun bool) error {
	targets, err := resolveTargets(targetNames)
	if err != nil {
		return err
	}
	if !daemonActive() && !dryRun {
		return fmt.Errorf("the local proxy is not running; run \"proxy serve install\" first")
	}

	pf.ViaLocal = true
	if !dryRun {
		if err := pf.Save(); err != nil {
			return err
		}
		if err := reloadDaemon(&proxy.Executor{}); err != nil {
			return err
		}
	}

	port := pf.EffectiveLocalPort()
	loopback := proxy.TargetConfig(cfg, true, port)

	// Docker's settings are read from inside containers, where 127.0.0.1 is
	// the container itself. Those targets need an address a container can
	// reach, which only exists when the daemon was told to listen on the
	// bridge as well.
	dockerCfg := loopback
	if pf.DockerBridge {
		bridge, err := serve.DockerBridgeAddr()
		if err != nil {
			return fmt.Errorf("docker_bridge is enabled but the bridge is unusable: %w", err)
		}
		dockerCfg = proxy.TargetConfigAt(cfg, true, port, bridge)
	} else {
		warnDockerLoopback(targets)
	}

	return setEachTarget(targets, dryRun, func(t proxy.Target) proxy.Config {
		if serve.IsDockerTarget(t.Name()) {
			return dockerCfg
		}
		return loopback
	})
}

// warnDockerLoopback flags the failure that is otherwise baffling: image pulls
// keep working, because dockerd runs on the host, while any build step that
// needs the network dies on a connection refused to 127.0.0.1.
func warnDockerLoopback(targets []proxy.Target) {
	for _, t := range targets {
		if !serve.IsDockerTarget(t.Name()) || !t.Available() {
			continue
		}
		fmt.Printf("  note: %s will point at 127.0.0.1, which containers cannot reach.\n", t.Name())
		fmt.Println("        Pulls will work, but build steps that need the network will fail.")
		fmt.Println("        Run \"proxy serve install --docker-bridge\" to also listen where containers can reach.")
		return
	}
}

// setAcross pushes one already-decided config into every target.
func setAcross(targets []proxy.Target, applied proxy.Config, dryRun bool) error {
	return setEachTarget(targets, dryRun, func(proxy.Target) proxy.Config { return applied })
}

// setEachTarget lets the caller decide the config per target, which is what
// the Docker targets need when the plumbing is in place.
func setEachTarget(targets []proxy.Target, dryRun bool, configFor func(proxy.Target) proxy.Config) error {
	ex := &proxy.Executor{DryRun: dryRun}
	return runAcross(targets, func(t proxy.Target) (string, error) {
		if !t.Available() {
			return "skipped (not available on this system)", nil
		}
		if t.RequiresRoot() && !proxy.IsRoot() && !dryRun {
			fmt.Printf("  note: %s needs sudo, you may be prompted for your password\n", t.Name())
		}
		if err := t.Set(ex, configFor(t)); err != nil {
			return "", err
		}
		return "configured", nil
	})
}

// clearTargets resolves targetNames and removes proxy settings from each.
func clearTargets(targetNames []string, dryRun bool) error {
	targets, err := resolveTargets(targetNames)
	if err != nil {
		return err
	}

	// Only a full unset can honestly say the plumbing is gone. Clearing
	// one target still leaves the others pointing at the loopback, and
	// "proxy status" must keep warning about that.
	if !dryRun && proxy.SelectsAllTargets(targetNames) {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if pf.ViaLocal {
			pf.ViaLocal = false
			if err := pf.Save(); err != nil {
				return err
			}
		}
	}

	ex := &proxy.Executor{DryRun: dryRun}
	return runAcross(targets, func(t proxy.Target) (string, error) {
		if !t.Available() {
			return "skipped (not available on this system)", nil
		}
		if t.RequiresRoot() && !proxy.IsRoot() && !dryRun {
			fmt.Printf("  note: %s needs sudo, you may be prompted for your password\n", t.Name())
		}
		if err := t.Unset(ex); err != nil {
			return "", err
		}
		return "cleared", nil
	})
}
