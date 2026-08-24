package cmd

import (
	"fmt"

	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var proxyOffCmd = &cobra.Command{
	Use:   "off",
	Short: "Route everything direct without touching any target",
	Long: "Clear the active profile so the local proxy sends every request " +
		"direct. Targets keep pointing at the local proxy, so this needs no " +
		"sudo and takes effect immediately. Use \"proxy on\" to go back.",
	RunE: func(cmd *cobra.Command, args []string) error {
		var lastProfile string
		wasOn := false
		err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			if pf.ActiveProfile == "" {
				return nil
			}
			pf.Off()
			lastProfile = pf.LastProfile
			wasOn = true
			return nil
		})
		if err != nil {
			return err
		}
		if !wasOn {
			fmt.Println("already off")
			return nil
		}
		if err := reloadDaemon(&proxy.Executor{}); err != nil {
			return err
		}
		fmt.Printf("off (was %q); traffic now goes direct\n", lastProfile)
		return nil
	},
}

var proxyOnCmd = &cobra.Command{
	Use:   "on [profile]",
	Short: "Restore proxying through the last profile, or a named one",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		var activeProfile string
		err := proxy.WithProfileLock(func(pf *proxy.ProfileFile) error {
			if err := pf.On(name); err != nil {
				return err
			}
			activeProfile = pf.ActiveProfile
			return nil
		})
		if err != nil {
			return err
		}
		if err := reloadDaemon(&proxy.Executor{}); err != nil {
			return err
		}
		fmt.Printf("on (profile %q)\n", activeProfile)
		return nil
	},
}

func init() {
	proxyCmd.AddCommand(proxyOffCmd)
	proxyCmd.AddCommand(proxyOnCmd)
}
