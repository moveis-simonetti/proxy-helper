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
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		if pf.ActiveProfile == "" {
			fmt.Println("already off")
			return nil
		}
		pf.Off()
		if err := pf.Save(); err != nil {
			return err
		}
		if err := reloadDaemon(&proxy.Executor{}); err != nil {
			return err
		}
		fmt.Printf("off (was %q); traffic now goes direct\n", pf.LastProfile)
		return nil
	},
}

var proxyOnCmd = &cobra.Command{
	Use:   "on [profile]",
	Short: "Restore proxying through the last profile, or a named one",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		pf, err := proxy.LoadProfiles()
		if err != nil {
			return err
		}
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		if err := pf.On(name); err != nil {
			return err
		}
		if err := pf.Save(); err != nil {
			return err
		}
		if err := reloadDaemon(&proxy.Executor{}); err != nil {
			return err
		}
		fmt.Printf("on (profile %q)\n", pf.ActiveProfile)
		return nil
	},
}

func init() {
	proxyCmd.AddCommand(proxyOffCmd)
	proxyCmd.AddCommand(proxyOnCmd)
}
