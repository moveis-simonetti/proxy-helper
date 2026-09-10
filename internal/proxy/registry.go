package proxy

import (
	"fmt"
	"strings"
)

// AllTargets returns every known target, in a fixed, stable order.
func AllTargets() []Target {
	return []Target{
		NewShellTarget(),
		NewSessionEnvTarget(),
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
		NewSystemEnvTarget(),
	}
}

// SelectsAllTargets reports whether names covers every known target, either
// through the "all" shorthand or by naming each one. Callers use it to tell
// a full unset apart from a partial one, since only a full unset may claim
// that nothing points at the local daemon any more.
func SelectsAllTargets(names []string) bool {
	selected, err := ByNames(names)
	if err != nil {
		return false
	}
	seen := make(map[string]bool, len(selected))
	for _, t := range selected {
		seen[t.Name()] = true
	}
	for _, t := range AllTargets() {
		if !seen[t.Name()] {
			return false
		}
	}
	return true
}

// SelectsAllAvailableTargets reports whether names covers every target that
// Available() currently returns true for, either through "all" or by naming
// each one. Unlike SelectsAllTargets, an unavailable target (not installed,
// no matching desktop session, ...) does not have to appear in names: Set/
// Unset skip it outright (see eachTarget in internal/app), so it was never
// plumbed via --via-local in the first place, and requiring it to be named
// would make a "full" selection unreachable for a caller — like the GUI,
// which only ever offers the targets a user could actually check — that
// never sends the "all" shorthand and never lists an unselectable target.
// Callers that need to know whether an apply/clear covering every target it
// could possibly have touched should use this instead of SelectsAllTargets.
func SelectsAllAvailableTargets(names []string) bool {
	selected, err := ByNames(names)
	if err != nil {
		return false
	}
	seen := make(map[string]bool, len(selected))
	for _, t := range selected {
		seen[t.Name()] = true
	}
	for _, t := range AllTargets() {
		if !t.Available() {
			continue
		}
		if !seen[t.Name()] {
			return false
		}
	}
	return true
}

// ByNames resolves a list of target names to Targets. The special name
// "all" (used alone) returns every target.
func ByNames(names []string) ([]Target, error) {
	all := AllTargets()
	if len(names) == 1 && strings.TrimSpace(names[0]) == "all" {
		return all, nil
	}

	index := make(map[string]Target, len(all))
	var known []string
	for _, t := range all {
		index[t.Name()] = t
		known = append(known, t.Name())
	}

	var selected []Target
	for _, n := range names {
		name := strings.TrimSpace(n)
		t, ok := index[name]
		if !ok {
			return nil, fmt.Errorf("unknown target %q (available: %s, or \"all\")", name, strings.Join(known, ", "))
		}
		selected = append(selected, t)
	}
	return selected, nil
}
