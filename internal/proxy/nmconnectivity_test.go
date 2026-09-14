package proxy

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNMConnectivityAvailableWhenNmcliPresent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nmcli"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	tgt := NewNMConnectivityTarget()
	if !tgt.Available() {
		t.Fatal("expected Available() to be true when nmcli is on PATH")
	}
}

func TestNMConnectivityStatusUnavailableReportsReason(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	tgt := NewNMConnectivityTarget()
	if tgt.Available() {
		t.Fatal("expected Available() to be false with an empty PATH")
	}

	ex := &Executor{Escalation: EscalateNone}
	st, err := tgt.Status(ex, true)
	if err != nil {
		t.Fatalf("expected no error, got: %v", err)
	}
	if st.Available {
		t.Fatal("expected Available to be false")
	}
	if st.Detail != "nmcli not found" {
		t.Errorf("expected a real unavailability reason, got: %q", st.Detail)
	}
}

// TestNMConnectivitySetIsANoOpWithoutURLOrStaleFile is what protects every
// profile that never configures this: Set must not attempt any privileged
// operation (which would mean an unexpected sudo prompt) when there is
// nothing configured and nothing left over to clean up.
func TestNMConnectivitySetIsANoOpWithoutURLOrStaleFile(t *testing.T) {
	origPath := nmConnectivityConfPath
	nmConnectivityConfPath = filepath.Join(t.TempDir(), "does-not-exist.conf")
	t.Cleanup(func() { nmConnectivityConfPath = origPath })

	tgt := NewNMConnectivityTarget()
	// EscalateNone: any privileged call (write, remove, or the reload) would
	// return an error here instead of silently succeeding, so a false pass
	// is not possible.
	ex := &Executor{Escalation: EscalateNone}
	if err := tgt.Set(ex, Config{}); err != nil {
		t.Fatalf("Set with nothing configured must be a true no-op, got: %v", err)
	}
	if _, err := os.Stat(nmConnectivityConfPath); !os.IsNotExist(err) {
		t.Error("Set with nothing configured must not create the conf file")
	}
}

// TestNMConnectivitySetCleansUpAStaleFile covers switching from a profile
// that had this configured to one that does not: the old file must not be
// left pointing at the wrong network's check URL.
func TestNMConnectivitySetCleansUpAStaleFile(t *testing.T) {
	origPath := nmConnectivityConfPath
	dir := t.TempDir()
	nmConnectivityConfPath = filepath.Join(dir, "stale.conf")
	t.Cleanup(func() { nmConnectivityConfPath = origPath })

	if err := os.WriteFile(nmConnectivityConfPath, []byte("[connectivity]\nuri=http://old.example/\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	tgt := NewNMConnectivityTarget()
	// DryRun, not EscalateNone: this path must actually reach the
	// privileged remove+reload (that is the behavior under test), and
	// DryRun is the only way to exercise that without a real root prompt.
	ex := &Executor{DryRun: true}
	if err := tgt.Set(ex, Config{}); err != nil {
		t.Fatalf("Set: %v", err)
	}
}

func TestNMConnectivityStatusReportsNotSet(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nmcli"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	origPath := nmConnectivityConfPath
	nmConnectivityConfPath = filepath.Join(t.TempDir(), "does-not-exist.conf")
	t.Cleanup(func() { nmConnectivityConfPath = origPath })

	tgt := NewNMConnectivityTarget()
	st, err := tgt.Status(&Executor{Escalation: EscalateNone}, false)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Enabled {
		t.Error("expected Enabled to be false when the conf file does not exist")
	}
	if st.Detail != "not set" {
		t.Errorf("Detail = %q, want %q", st.Detail, "not set")
	}
}
