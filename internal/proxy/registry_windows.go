//go:build windows

package proxy

// AllTargets returns every known target, in a fixed, stable order.
//
// Windows has one target that matters: WinINET, the registry keys Edge,
// Chrome, Office and .NET read. None of the Linux targets carry over — git,
// npm, Docker and the desktop schemas are developer surface, and this build
// is for people who only need the browser to work.
func AllTargets() []Target {
	return []Target{
		NewWinINETTarget(),
	}
}
