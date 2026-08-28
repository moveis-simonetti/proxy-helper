//go:build !windows

package wingui

// daemonAutostartRegistered is Windows-only; elsewhere the daemon is a
// systemd unit and this build exists so the package compiles here.
func daemonAutostartRegistered() bool { return true }
