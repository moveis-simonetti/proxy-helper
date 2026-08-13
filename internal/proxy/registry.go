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
		NewGnomeTarget(),
		NewDockerdTarget(),
		NewDockerConfigTarget(),
		NewSnapTarget(),
		NewAptTarget(),
	}
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
