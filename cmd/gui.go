//go:build gui

package cmd

import (
	"github.com/spf13/cobra"

	"proxy-helper/internal/gui"
)

var guiHidden bool

var guiCmd = &cobra.Command{
	Use:   "gui",
	Short: "Launch the graphical interface",
	Long:  "Launch the proxy-helper graphical interface, a GTK3 window for managing proxy configuration.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return gui.Run(guiHidden)
	},
}

func init() {
	guiCmd.Flags().BoolVar(&guiHidden, "hidden", false, "start in the tray instead of showing the window (used by the autostart entry; ignored when no tray is available)")
	rootCmd.AddCommand(guiCmd)
}
