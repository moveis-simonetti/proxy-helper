package cmd

import (
	"github.com/spf13/cobra"
)

var (
	unsetTargets []string
	unsetDryRun  bool
)

var proxyUnsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Remove proxy settings from the selected targets",
	RunE: func(cmd *cobra.Command, args []string) error {
		return clearTargets(unsetTargets, unsetDryRun)
	},
}

func init() {
	proxyUnsetCmd.Flags().StringSliceVar(&unsetTargets, "targets", []string{"all"}, "comma-separated targets (shell,git,npm,gnome,dockerd,docker-config,snap,apt,all)")
	proxyUnsetCmd.Flags().BoolVar(&unsetDryRun, "dry-run", false, "print what would change without applying it")
	proxyCmd.AddCommand(proxyUnsetCmd)
}
