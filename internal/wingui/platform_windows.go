//go:build windows

package wingui

import "os/exec"

const isWindows = true

var lookPath = exec.LookPath
