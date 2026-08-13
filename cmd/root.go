package cmd

import "github.com/spf13/cobra"

var rootCmd = &cobra.Command{
	Use:   "proxy-helper",
	Short: "Helper CLI to configure and run your projects",
	Long:  "proxy-helper configures and runs project tooling. The first feature is managing proxy settings across shells and dev tools.",
}

func Execute() error {
	return rootCmd.Execute()
}
