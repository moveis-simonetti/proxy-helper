package cmd

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"proxy-helper/internal/proxy"

	"github.com/spf13/cobra"
)

var proxyCmd = &cobra.Command{
	Use:   "proxy",
	Short: "Configure proxy settings across shells and dev tools",
}

func init() {
	rootCmd.AddCommand(proxyCmd)
}

// runAcross applies fn to each target and prints a result table. It keeps
// going after a per-target failure so one bad target doesn't block the rest,
// then returns a combined error if anything failed.
func runAcross(targets []proxy.Target, fn func(proxy.Target) (string, error)) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(w, "TARGET\tRESULT")

	var failed []string
	for _, t := range targets {
		result, err := fn(t)
		if err != nil {
			fmt.Fprintf(w, "%s\tFAILED: %v\n", t.Name(), err)
			failed = append(failed, t.Name())
			continue
		}
		fmt.Fprintf(w, "%s\t%s\n", t.Name(), result)
	}
	w.Flush()

	if len(failed) > 0 {
		return fmt.Errorf("failed targets: %s", strings.Join(failed, ", "))
	}
	return nil
}

// applyConfig resolves targetNames and applies cfg to each, printing a
// result table.
func applyConfig(cfg proxy.Config, targetNames []string, dryRun bool) error {
	targets, err := proxy.ByNames(targetNames)
	if err != nil {
		return err
	}

	ex := &proxy.Executor{DryRun: dryRun}
	return runAcross(targets, func(t proxy.Target) (string, error) {
		if !t.Available() {
			return "skipped (not available on this system)", nil
		}
		if t.RequiresRoot() && !proxy.IsRoot() && !dryRun {
			fmt.Printf("  note: %s needs sudo, you may be prompted for your password\n", t.Name())
		}
		if err := t.Set(ex, cfg); err != nil {
			return "", err
		}
		return "configured", nil
	})
}

// clearTargets resolves targetNames and removes proxy settings from each.
func clearTargets(targetNames []string, dryRun bool) error {
	targets, err := proxy.ByNames(targetNames)
	if err != nil {
		return err
	}

	ex := &proxy.Executor{DryRun: dryRun}
	return runAcross(targets, func(t proxy.Target) (string, error) {
		if !t.Available() {
			return "skipped (not available on this system)", nil
		}
		if t.RequiresRoot() && !proxy.IsRoot() && !dryRun {
			fmt.Printf("  note: %s needs sudo, you may be prompted for your password\n", t.Name())
		}
		if err := t.Unset(ex); err != nil {
			return "", err
		}
		return "cleared", nil
	})
}
