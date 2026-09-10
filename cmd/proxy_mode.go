package cmd

import (
	"fmt"

	"proxy-helper/internal/app"
	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var proxyModeCmd = &cobra.Command{
	Use:   "mode [auto|upstream|direct]",
	Short: "Show or set how the local proxy routes traffic",
	Long: "Choose what the local proxy does with a request:\n\n" +
		"  auto      forward to the upstream while it answers, fall back to\n" +
		"            direct when it stops responding\n" +
		"  upstream  always forward; never let traffic out any other way\n" +
		"  direct    never forward; send everything straight out\n\n" +
		"Targets keep pointing at the local proxy either way, so switching " +
		"needs no sudo, rewrites no configuration and takes effect at once. " +
		"Run without an argument to print the current mode.",
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			mode, profile, err := app.CurrentMode()
			if err != nil {
				return err
			}
			if profile == "" {
				fmt.Printf("%s (no profile selected)\n", mode)
				return nil
			}
			fmt.Printf("%s (profile %q)\n", mode, profile)
			return nil
		}

		mode, err := proxy.ParseMode(args[0])
		if err != nil {
			return err
		}
		res, err := app.SetMode(deps(), &proxy.Executor{}, mode)
		if err != nil {
			return err
		}
		if res.Unchanged {
			fmt.Printf("already %s\n", res.Mode)
			return nil
		}
		fmt.Printf("%s (was %s)\n", res.Mode, res.Previous)
		return nil
	},
}

func init() {
	proxyCmd.AddCommand(proxyModeCmd)
}
