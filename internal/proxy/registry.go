package proxy

import (
	"fmt"
	"strings"
)

// AllTargets returns every known target, in a fixed, stable order.
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
