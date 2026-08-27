//go:build wingui

// Command msproxy is the MS Proxy desktop interface.
//
// It is a separate binary from the CLI, and built only with the "wingui"
// tag, so the CLI stays free of the GUI toolkit — the same separation the
// Linux build keeps between proxy-helper and proxy-helper-gui.
package main

import (
	"flag"
	"fmt"
	"os"

	"proxy-helper/internal/wingui"
)

func main() {
	hidden := flag.Bool("hidden", false, "start in the tray, without showing the window")
	flag.Parse()

	if err := wingui.Run(*hidden); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
