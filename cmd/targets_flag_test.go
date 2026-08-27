package cmd

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"proxy-helper/internal/proxy"
)

// TestEveryTargetsFlagUsesTheDerivedUsage walks the command tree and requires
// every --targets flag to carry proxy.TargetsFlagUsage().
//
// On its own this is weak: a hand-copied literal listing today's Linux
// targets compares equal to the derived string, so this test passes either
// way here. It earns its keep on a platform whose registry differs — and
// TestNoHardcodedTargetListInFlagUsage below covers the gap meanwhile.
func TestEveryTargetsFlagUsesTheDerivedUsage(t *testing.T) {
	want := proxy.TargetsFlagUsage()

	var checked int
	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		if f := c.Flags().Lookup("targets"); f != nil {
			checked++
			if f.Usage != want {
				t.Errorf("%q --targets help is not derived from AllTargets()\n got: %s\nwant: %s",
					c.CommandPath(), f.Usage, want)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(rootCmd)

	// A tree walk that finds nothing would pass silently and prove nothing.
	if checked == 0 {
		t.Fatal("found no --targets flag in the command tree; the walk is broken")
	}
}

// hardcodedTargetList matches a string literal that spells out a target list
// the way the six removed copies did: several comma-separated bare names
// ending in "all", inside quotes.
var hardcodedTargetList = regexp.MustCompile(`"[^"]*\b(?:[a-z][a-z-]*,){3,}all\)?[^"]*"`)

// TestNoHardcodedTargetListInFlagUsage reads this package's own source and
// fails if a target list was written out by hand.
//
// This is the test that actually bites. Comparing rendered help strings
// cannot tell a literal apart from the derived value while both platforms
// happen to agree; reading the source can. The literal is what goes stale
// once a second registry exists, so the literal is what to forbid.
func TestNoHardcodedTargetListInFlagUsage(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading package directory: %v", err)
	}

	var scanned int
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatalf("reading %s: %v", name, err)
		}
		scanned++
		if m := hardcodedTargetList.Find(src); m != nil {
			t.Errorf("%s spells out a target list by hand: %s\nuse proxy.TargetsFlagUsage() so it follows the platform's registry",
				name, m)
		}
	}

	if scanned == 0 {
		t.Fatal("scanned no source files; the directory walk is broken")
	}
}
