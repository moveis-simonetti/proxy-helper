//go:build !windows

package wingui

import "os/exec"

const isWindows = false

var lookPath = exec.LookPath
