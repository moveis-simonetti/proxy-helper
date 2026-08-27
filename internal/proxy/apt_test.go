package proxy

import (
	"testing"
)

// TestAptStatusUnavailableReportsReason covers Finding 1 for apt. Available()
// consults the aptConfDir seam instead of the literal "/etc/apt", so this
// test can force the unavailable path deterministically on every host
// (Debian/Ubuntu included), rather than skipping wherever /etc/apt happens
// to exist.
func TestAptStatusUnavailableReportsReason(t *testing.T) {
	origDir := aptConfDir
	aptConfDir = t.TempDir() + "/does-not-exist"
	t.Cleanup(func() { aptConfDir = origDir })

	tgt := NewAptTarget()
	if tgt.Available() {
		t.Fatal("expected Available() to be false without /etc/apt")
	}

	ex := &Executor{Escalation: EscalateNone}
	st, err := tgt.Status(ex, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if st.Available {
		t.Fatal("expected Available to be false")
	}
	if st.Detail != "apt not installed" {
		t.Errorf("expected a real unavailability reason, got: %q", st.Detail)
	}
}
