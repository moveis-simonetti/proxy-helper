package cmd

import (
	"fmt"
	"strings"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var proxyConfigCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage global proxy-helper settings",
}

var proxyConfigShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show global settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		fmt.Printf("no-proxy: %s\n", strings.Join(pf.EffectiveGlobalNoProxy(), ","))
		return nil
	},
}

var configSetNoProxy []string

var proxyConfigSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Update global settings",
	RunE: func(cmd *cobra.Command, args []string) error {
		if !cmd.Flags().Changed("no-proxy") {
			return fmt.Errorf("nothing to set (see --no-proxy)")
		}

		effective, err := app.SetGlobalNoProxy(deps(), &proxy.Executor{}, configSetNoProxy)
		if err != nil {
			return err
		}
		fmt.Printf("no-proxy: %s\n", strings.Join(effective, ","))
		return nil
	},
}

var proxyConfigResetNoProxyCmd = &cobra.Command{
	Use:   "reset-no-proxy",
	Short: "Reset the global no-proxy list to its default",
	RunE: func(cmd *cobra.Command, args []string) error {
		// nil, not an empty slice: EffectiveGlobalNoProxy falls back to the
		// default only on nil, and an empty slice would mean "no host
		// bypasses the proxy" instead.
		effective, err := app.SetGlobalNoProxy(deps(), &proxy.Executor{}, nil)
		if err != nil {
			return err
		}
		fmt.Printf("no-proxy: %s\n", strings.Join(effective, ","))
		return nil
	},
}

func init() {
	proxyConfigSetCmd.Flags().StringSliceVar(&configSetNoProxy, "no-proxy", nil, "comma-separated hosts to bypass the proxy globally, applied to every profile and one-off "+
		"\"proxy set\" (default: "+strings.Join(proxy.DefaultGlobalNoProxy, ",")+")")

	proxyConfigCmd.AddCommand(proxyConfigShowCmd, proxyConfigSetCmd, proxyConfigResetNoProxyCmd)
	proxyCmd.AddCommand(proxyConfigCmd)
}
