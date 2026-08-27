//go:build !windows

package proxy

// AllTargets returns every known target, in a fixed, stable order.
//
// The registry is per-platform: the places a system stores proxy settings
// are the product, and they have almost nothing in common between Linux and
// Windows. Everything that consumes this list — ByNames, SelectsAll*,
// TargetsFlagUsage — is shared and needs no platform knowledge.
func AllTargets() []Target {
	return []Target{
		NewShellTarget(),
		NewGitTarget(),
		NewNpmTarget(),
		NewVscodeTarget(),
		NewGnomeTarget(),
		NewKdeTarget(),
		NewDockerdTarget(),
		NewDockerConfigTarget(),
		NewLxdTarget(),
		NewSnapTarget(),
		NewAptTarget(),
	}
}
