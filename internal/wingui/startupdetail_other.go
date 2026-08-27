//go:build !windows

package wingui

// daemonStartupDetail has nothing to read outside Windows: the Unix daemon
// logs to the journal, and this build exists only so the package compiles
// and its logic stays testable on the development machine.
func daemonStartupDetail() string { return "" }

// daemonLogLocation is likewise Windows-only.
func daemonLogLocation() string { return "" }
