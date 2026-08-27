package cmd

import (
	"github.com/spf13/cobra"

	"proxy-helper/internal/proxy"
)

var (
	unsetTargets       []string
	unsetDryRun        bool
	unsetRestartDocker bool
)

var proxyUnsetCmd = &cobra.Command{
	Use:   "unset",
	Short: "Remove proxy settings from the selected targets",
	RunE: func(cmd *cobra.Command, args []string) error {
		return clearTargets(unsetTargets, unsetDryRun, unsetRestartDocker)
	},
}

func init() {
	proxyUnsetCmd.Flags().StringSliceVar(&unsetTargets, "targets", []string{"all"}, proxy.TargetsFlagUsage())
	proxyUnsetCmd.Flags().BoolVar(&unsetDryRun, "dry-run", false, "print what would change without applying it")
	proxyUnsetCmd.Flags().BoolVar(&unsetRestartDocker, "restart-docker", false, "restart the Docker daemon after applying, so the change takes effect (this restarts running containers)")
	proxyCmd.AddCommand(proxyUnsetCmd)
}
