//go:build windows

package proxy

// AllTargets returns every known target, in a fixed, stable order.
//
// Two targets on Windows. WinINET is the registry keys Edge, Chrome, Office
// and .NET read. Firefox is separate because it keeps its own proxy
// configuration and ignores the system's — a machine can have everything
// else routed correctly while Firefox talks to the corporate proxy directly
// and asks the person for a password nothing else needs.
//
// None of the Linux targets carry over: git, npm, Docker and the desktop
// schemas are developer surface, and this build is for people who only need
// the browser to work.
func AllTargets() []Target {
	return []Target{
		NewWinINETTarget(),
		NewFirefoxTarget(),
	}
}
