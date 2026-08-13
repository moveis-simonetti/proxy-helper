package cmd

import (
	"fmt"

	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var (
	setScheme  string
	setHost    string
	setPort    string
	setUser    string
	setPass    string
	setNoProxy []string
	setProfile string
	setTargets []string
	setDryRun  bool
)

var proxySetCmd = &cobra.Command{
	Use:   "set",
	Short: "Apply proxy settings to the selected targets",
	RunE: func(cmd *cobra.Command, args []string) error {
		if setProfile != "" {
			if setHost != "" {
				return fmt.Errorf("--host and --profile are mutually exclusive")
			}
			pf, err := proxy.LoadProfiles()
			if err != nil {
				return err
			}
			cfg, ok := pf.Get(setProfile)
			if !ok {
				return fmt.Errorf("profile %q not found (see \"proxy profile list\")", setProfile)
			}
			return applyConfig(cfg, setTargets, setDryRun)
		}

		if setHost == "" {
			return fmt.Errorf("--host is required (or use --profile)")
		}

		cfg := proxy.Config{
			Scheme:   setScheme,
			Host:     setHost,
			Port:     setPort,
			Username: setUser,
			Password: setPass,
			NoProxy:  setNoProxy,
		}
		return applyConfig(cfg, setTargets, setDryRun)
	},
}

func init() {
	proxySetCmd.Flags().StringVar(&setScheme, "scheme", "http", "proxy scheme: http, https, socks5")
	proxySetCmd.Flags().StringVar(&setHost, "host", "", "proxy host (required unless --profile is used)")
	proxySetCmd.Flags().StringVar(&setPort, "port", "", "proxy port")
	proxySetCmd.Flags().StringVar(&setUser, "user", "", "proxy username")
	proxySetCmd.Flags().StringVar(&setPass, "pass", "", "proxy password")
	proxySetCmd.Flags().StringSliceVar(&setNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy")
	proxySetCmd.Flags().StringVar(&setProfile, "profile", "", "apply a saved profile instead of --host/--port/etc (see \"proxy profile\")")
	proxySetCmd.Flags().StringSliceVar(&setTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,gnome,dockerd,docker-config,snap,apt,all)")
	proxySetCmd.Flags().BoolVar(&setDryRun, "dry-run", false, "print what would change without applying it")
	proxyCmd.AddCommand(proxySetCmd)
}
