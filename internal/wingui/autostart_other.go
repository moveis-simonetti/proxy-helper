//go:build !windows

package wingui

import "proxy-helper/internal/proxy"

// Autostart is a Windows concern. The Linux GUI is a different program with
// its own .desktop autostart entry, and this build exists only so the
// package compiles and can be tested off Windows.

func InstallAutostart(ex *proxy.Executor) error { return nil }
func RemoveAutostart(ex *proxy.Executor) error  { return nil }
func AutostartEnabled() bool                    { return false }
