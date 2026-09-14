package cmd

import (
	"fmt"

	"proxy-helper/internal/proxy"
	"proxy-helper/internal/serve"

	"github.com/spf13/cobra"
)

var (
	purgeTargets []string
	purgeDryRun  bool
)

// proxyPurgeCmd is "unset every target" + "uninstall the daemon" + "delete
// config.json" in one command, in that order: clearing targets and
// uninstalling the unit both still need config.json (the active profile,
// the port), so deleting it first would leave them with nothing to read.
//
// It always deletes config.json — there is no flag to keep it. A saved
// profile's password lives there in plain text (see ProfileFile's own doc
// comment), so "purge" meaning "gone, not just disabled" is the point;
// anyone who wants to keep their profiles should run "proxy unset" and
// "proxy serve uninstall" separately instead of this command.
//
// --targets scopes what gets unset, same flag and same target names as
// "proxy unset" (see clearTargets) — the .deb's prerm uses this to unset
// only the user-level targets for each logged-in user, leaving the
// privileged ones (apt, system-env, dockerd) to the single root-level
// "proxy unset" it already runs once, itself.
var proxyPurgeCmd = &cobra.Command{
	Use:   "purge",
	Short: "Remove every target's proxy settings, uninstall the daemon, and delete the saved configuration",
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := clearTargets(purgeTargets, purgeDryRun, false); err != nil {
			return err
		}

		ex := &proxy.Executor{DryRun: purgeDryRun}
		if err := serve.UninstallUnit(ex); err != nil {
			return err
		}

		path, err := proxy.ConfigFilePath()
		if err != nil {
			return err
		}
		if err := ex.RemoveFile(path); err != nil {
			return err
		}
		if err := ex.RemoveFile(path + ".lock"); err != nil {
			return err
		}

		if !purgeDryRun {
			fmt.Println("removed " + serve.UnitName + " and " + path)
		}
		return nil
	},
}

func init() {
	proxyPurgeCmd.Flags().StringSliceVar(&purgeTargets, "targets", []string{"all"}, "comma-separated targets (shell,session-env,system-env,git,npm,vscode,gnome,kde,dockerd,docker-config,lxd,snap,apt,all)")
	proxyPurgeCmd.Flags().BoolVar(&purgeDryRun, "dry-run", false, "print what would change without applying it")
	proxyCmd.AddCommand(proxyPurgeCmd)
}
