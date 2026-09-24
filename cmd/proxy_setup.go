package cmd

import (
	"fmt"
	"os"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/spf13/cobra"
)

var (
	setupProfile      string
	setupScheme       string
	setupHost         string
	setupPort         string
	setupUser         string
	setupPass         string
	setupNoProxy      []string
	setupTargets      []string
	setupMode         string
	setupLocalPort    int
	setupDockerBridge bool
	setupDryRun       bool
)

// installDaemon writes and starts the user unit. It is the app.Deps hook
// Setup calls; every write goes through the Executor, so --dry-run previews
// the unit instead of installing it.
func installDaemon(ex *proxy.Executor) error {
	execPath, err := os.Executable()
	if err != nil {
		return err
	}
	pf, err := proxy.LoadProfiles()
	if err != nil {
		return err
	}
	port := setupLocalPort
	if port == 0 {
		port = pf.EffectiveLocalPort()
	}
	return serve.InstallUnit(ex, execPath, port, setupDockerBridge || pf.DockerBridge, false)
}

var proxySetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Set the machine up so switching the proxy on and off is instant",
	Long: "Do the one-time arrangement the rest of this tool is built around:\n\n" +
		"  1. install and start the local proxy daemon\n" +
		"  2. point every target at it, instead of at the upstream\n" +
		"  3. leave the routing mode ready (auto by default)\n\n" +
		"After this, switching between forwarding and direct is \"proxy mode\" " +
		"— no sudo, no configuration rewritten, no application to relaunch. " +
		"Safe to re-run: a daemon that is already running is left alone.",
	RunE: func(cmd *cobra.Command, args []string) error {
		mode := proxy.Mode(setupMode)
		if setupMode == "" {
			mode = proxy.ModeAuto
		} else if _, err := proxy.ParseMode(setupMode); err != nil {
			return err
		}

		var cfg proxy.Config
		if setupHost != "" {
			cfg = proxy.Config{
				Scheme:   setupScheme,
				Host:     setupHost,
				Port:     setupPort,
				Username: setupUser,
				Password: setupPass,
				NoProxy:  setupNoProxy,
			}
		}

		ex := &proxy.Executor{DryRun: setupDryRun, Out: stdout()}
		res, err := app.Setup(deps(), ex, app.SetupOptions{
			Profile:  setupProfile,
			Config:   cfg,
			Targets:  setupTargets,
			Mode:     mode,
			Progress: func(line string) { fmt.Fprintf(stdout(), "  %s\n", line) },
		})
		if err != nil {
			return err
		}
		renderReport(stdout(), res.Report)
		fmt.Printf("\nperfil %q, modo %s\n", res.Profile, res.Mode)
		fmt.Printf("a partir daqui, alternar não pede sudo:\n")
		fmt.Printf("  proxy-helper proxy mode direct\n")
		fmt.Printf("  proxy-helper proxy mode %s\n", res.Mode)
		return res.Report.Err()
	},
}

func init() {
	proxySetupCmd.Flags().StringVar(&setupProfile, "profile", "trabalho", "name to save the upstream under")
	proxySetupCmd.Flags().StringVar(&setupScheme, "scheme", "http", "proxy scheme: http, https, socks5")
	proxySetupCmd.Flags().StringVar(&setupHost, "host", "", "upstream proxy host (omit to reuse a saved profile)")
	proxySetupCmd.Flags().StringVar(&setupPort, "port", "", "upstream proxy port")
	proxySetupCmd.Flags().StringVar(&setupUser, "user", "", "upstream proxy username")
	proxySetupCmd.Flags().StringVar(&setupPass, "pass", "", "upstream proxy password")
	proxySetupCmd.Flags().StringSliceVar(&setupNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy")
	proxySetupCmd.Flags().StringSliceVar(&setupTargets, "targets", []string{"all"}, "comma-separated targets (shell,session-env,system-env,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,nm-connectivity,all)")
	proxySetupCmd.Flags().StringVar(&setupMode, "mode", "", "routing mode to leave behind: auto (default), upstream or direct")
	proxySetupCmd.Flags().IntVar(&setupLocalPort, "local-port", 0, "port for the local daemon (default: the configured one, or 8888)")
	proxySetupCmd.Flags().BoolVar(&setupDockerBridge, "docker-bridge", false, "also listen on the Docker bridge so build containers can reach the proxy")
	proxySetupCmd.Flags().BoolVar(&setupDryRun, "dry-run", false, "print what would change without applying it")
	proxyCmd.AddCommand(proxySetupCmd)
}
