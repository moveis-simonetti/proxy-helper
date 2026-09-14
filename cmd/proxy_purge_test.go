package cmd

import (
	"os"
	"testing"
)

// resetPurgeFlags puts purge's package-global flag variables back to their
// defaults between tests, same reason resetProfileFlags exists.
func resetPurgeFlags(t *testing.T) {
	t.Helper()
	purgeTargets = []string{"all"}
	purgeDryRun = false
	t.Cleanup(func() {
		purgeTargets = []string{"all"}
		purgeDryRun = false
	})
}

// TestPurgeDryRunUnsetsEveryTargetButTouchesNoRealFile pins purge's two
// distinct effects apart: it always calls Unset on every resolved target
// (Clear does that unconditionally, trusting each real target to honour
// ex.DryRun for its own file writes — see internal/app/apply.go's Clear),
// while the command's OWN file removal (config.json, via ex.RemoveFile)
// does respect --dry-run and must leave the real file in place.
//
// This does not exercise serve.UninstallUnit's systemctl calls at all —
// with DryRun set, Executor.Run only prints what it would run, never
// exec.Command's the real "systemctl --user disable/daemon-reload" against
// whatever session happens to be running go test. Nothing in this package
// mocks that layer (proxy serve uninstall itself has no test either, for
// the same reason); a non-dry-run purge is exercised only manually.
func TestPurgeDryRunUnsetsEveryTargetButTouchesNoRealFile(t *testing.T) {
	h := newHarness(t, `{"active_profile":"work","profiles":{"work":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	resetPurgeFlags(t)
	purgeDryRun = true

	if err := proxyPurgeCmd.RunE(proxyPurgeCmd, nil); err != nil {
		t.Fatalf("purge --dry-run: %v", err)
	}

	if got, want := h.unsets(), len(h.targets); got != want {
		t.Errorf("unsets = %d, want %d (every resolved target)", got, want)
	}
	if _, err := os.Stat(h.path); err != nil {
		t.Errorf("dry-run must leave the real config file in place, but it is gone: %v", err)
	}
}

// TestPurgeTargetsFlagIsRegistered guards the --targets flag purge shares
// with "proxy unset" (same clearTargets call, same flag registration
// pattern) — a typo in the flag name would silently make every purge run
// with the "all" default no matter what the caller asked for.
func TestPurgeTargetsFlagIsRegistered(t *testing.T) {
	flag := proxyPurgeCmd.Flags().Lookup("targets")
	if flag == nil {
		t.Fatal(`"purge" has no --targets flag`)
	}
	if flag.DefValue != "[all]" {
		t.Errorf("--targets default = %q, want [all]", flag.DefValue)
	}
}
