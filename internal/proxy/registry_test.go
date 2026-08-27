package proxy

import (
	"strings"
	"testing"
)

// The --targets help used to be a string literal copied into six init()
// functions. With a per-platform registry, a copy would necessarily go stale
// on one of the platforms; these tests fail if the help ever stops being
// derived from AllTargets().

func TestTargetsFlagUsageNamesEveryRegisteredTarget(t *testing.T) {
	usage := TargetsFlagUsage()
	for _, target := range AllTargets() {
		if !strings.Contains(usage, target.Name()) {
			t.Errorf("--targets help does not mention %q: %s", target.Name(), usage)
		}
	}
}

func TestTargetsFlagUsageOffersTheAllShorthand(t *testing.T) {
	// "all" is the flag's default value, so a help text that omits it would
	// document a set of targets the user cannot actually ask for.
	if usage := TargetsFlagUsage(); !strings.Contains(usage, "all") {
		t.Errorf("--targets help does not offer the \"all\" shorthand: %s", usage)
	}
}

func TestTargetsFlagUsageNamesNothingUnregistered(t *testing.T) {
	// The inverse direction: a leftover name from another platform's registry
	// would advertise a target ByNames cannot resolve.
	usage := TargetsFlagUsage()
	open := strings.Index(usage, "(")
	close := strings.LastIndex(usage, ")")
	if open < 0 || close < open {
		t.Fatalf("--targets help is not in the expected %q shape: %s", "... (a,b,all)", usage)
	}

	registered := map[string]bool{"all": true}
	for _, target := range AllTargets() {
		registered[target.Name()] = true
	}
	for _, name := range strings.Split(usage[open+1:close], ",") {
		if name = strings.TrimSpace(name); !registered[name] {
			t.Errorf("--targets help offers %q, which no registered target answers to", name)
		}
	}
}
