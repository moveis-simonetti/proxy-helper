//go:build gui

package cmd

import (
	"github.com/spf13/cobra"

	"proxy-helper/internal/gui"
)

var guiCmd = &cobra.Command{
	Use:   "gui",
	Short: "Launch the graphical interface",
	Long:  "Launch the proxy-helper graphical interface, a GTK3 window for managing proxy configuration.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return gui.Run()
	},
}

func init() {
	rootCmd.AddCommand(guiCmd)
}
