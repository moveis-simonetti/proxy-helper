package main

import (
	"fmt"
	"os"
	"runtime"

	"proxy-helper/cmd"
)

func main() {
	// Locked here, unconditionally, so it provably runs on the process's
	// main OS thread before anything else executes — not just incidentally,
	// because cobra happens to invoke RunE synchronously today. The gui
	// build's Run (internal/gui/app.go) depends on this: GTK's main loop
	// must stay pinned to a single OS thread, and gtk.Init must be called
	// from it. Locking unconditionally costs nothing for the non-gui build.
	runtime.LockOSThread()

	if err := cmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
