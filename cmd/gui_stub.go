//go:build !gui

package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var guiCmd = &cobra.Command{
	Use:   "gui",
	Short: "Launch the graphical interface",
	Long:  "Launch the proxy-helper graphical interface, a GTK3 window for managing proxy configuration.",
	RunE: func(cmd *cobra.Command, args []string) error {
		return fmt.Errorf("this binary was built without the graphical interface; install proxy-helper-gui to use the \"gui\" command")
	},
}

func init() {
	rootCmd.AddCommand(guiCmd)
}
