package proxy

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// swapNMConnectivityPaths points both files nm-connectivity manages at a
// throwaway directory, so tests never touch the real
// /etc/NetworkManager/conf.d or dispatcher.d — and, just as important,
// never see whatever a previous test run (or a real "proxy profile
// enable" on this machine) may have left behind at the real paths, which
// would silently change what "not there yet" means for the no-op checks
// below.
func swapNMConnectivityPaths(t *testing.T) {
	t.Helper()
	origConf, origScript := nmConnectivityConfPath, nmDispatcherScriptPath
	dir := t.TempDir()
	nmConnectivityConfPath = filepath.Join(dir, "connectivity.conf")
	nmDispatcherScriptPath = filepath.Join(dir, "dispatcher-script")
	t.Cleanup(func() { nmConnectivityConfPath, nmDispatcherScriptPath = origConf, origScript })
}

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
// nothing configured and nothing left over to clean up — neither the conf
// file nor the dispatcher script.
func TestNMConnectivitySetIsANoOpWithoutURLOrStaleFile(t *testing.T) {
	swapNMConnectivityPaths(t)

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
	if _, err := os.Stat(nmDispatcherScriptPath); !os.IsNotExist(err) {
		t.Error("Set with nothing configured must not create the dispatcher script")
	}
}

// TestNMConnectivitySetCleansUpAStaleFile covers switching from a profile
// that had this configured to one that does not: the old conf file and
// dispatcher script must not be left behind, pointing at the wrong
// network's check URL.
func TestNMConnectivitySetCleansUpAStaleFile(t *testing.T) {
	swapNMConnectivityPaths(t)

	if err := os.WriteFile(nmConnectivityConfPath, []byte("[connectivity]\nuri=http://old.example/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nmDispatcherScriptPath, []byte("#!/bin/sh\n"), 0o755); err != nil {
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
	swapNMConnectivityPaths(t)

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

// TestNMConnectivitySetWritesTheDispatcherScript covers the actual
// reconciliation mechanism: Set must write an executable script that
// invokes this binary's own path (not a bare command name — the
// dispatcher runs with no PATH set up for a user session) with this
// user's HOME/XDG_CONFIG_HOME pinned into the command line, since root has
// none of its own and guessing the logged-in user would be wrong on a
// multi-user machine.
//
// DryRun, not a real write: RequiresRoot is true for this target, and a
// real (non-DryRun, non-EscalateNone) Executor would actually try to sudo
// in a test. DryRun's preview still contains the exact content that would
// have been written (see WritePrivilegedFile), which is what is under test
// here — not the Executor's own privilege-escalation mechanics, already
// covered by executor_test.go.
func TestNMConnectivitySetWritesTheDispatcherScript(t *testing.T) {
	swapNMConnectivityPaths(t)
	t.Setenv("XDG_CONFIG_HOME", "/home/tester/.config")

	tgt := NewNMConnectivityTarget()
	var out strings.Builder
	ex := &Executor{DryRun: true, Out: &out}
	cfg := Config{ConnectivityCheckURL: "http://proxy.corp/", ConnectivityCheckResponse: "<html>"}
	if err := tgt.Set(ex, cfg); err != nil {
		t.Fatalf("Set: %v", err)
	}

	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{nmDispatcherScriptPath, "/home/tester/.config", self, "sync-connectivity"} {
		if !strings.Contains(got, want) {
			t.Errorf("dry-run preview is missing %q\n---\n%s", want, got)
		}
	}
}

// TestSyncNMConnectivityTogglesOnlyTheConfFile is the other half of the
// reconciliation: unlike Set/Unset, it must never touch the dispatcher
// script — it is what the script itself calls on every network change, and
// removing its own trigger on a temporary "unreachable right now" reading
// would stop it from ever running again. DryRun for the same reason as
// above; the preview text is enough to tell which file an operation named.
func TestSyncNMConnectivityTogglesOnlyTheConfFile(t *testing.T) {
	swapNMConnectivityPaths(t)

	cfg := Config{ConnectivityCheckURL: "http://proxy.corp/", ConnectivityCheckResponse: "<html>"}

	var reachableOut strings.Builder
	if err := SyncNMConnectivity(&Executor{DryRun: true, Out: &reachableOut}, cfg, true); err != nil {
		t.Fatalf("SyncNMConnectivity(reachable): %v", err)
	}
	if got := reachableOut.String(); !strings.Contains(got, nmConnectivityConfPath) || strings.Contains(got, nmDispatcherScriptPath) {
		t.Errorf("reachable=true must write only the conf file, got:\n%s", got)
	}

	var unreachableOut strings.Builder
	if err := SyncNMConnectivity(&Executor{DryRun: true, Out: &unreachableOut}, cfg, false); err != nil {
		t.Fatalf("SyncNMConnectivity(unreachable): %v", err)
	}
	if got := unreachableOut.String(); !strings.Contains(got, nmConnectivityConfPath) || strings.Contains(got, nmDispatcherScriptPath) {
		t.Errorf("reachable=false must remove only the conf file, got:\n%s", got)
	}
}

func TestProbeConnectivityCheckMatchesResponsePrefix(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html><body>It works!</body></html>"))
	}))
	defer srv.Close()

	if !ProbeConnectivityCheck(srv.URL, "<html>", time.Second) {
		t.Error("expected a match on the response prefix")
	}
	if ProbeConnectivityCheck(srv.URL, "<not-this>", time.Second) {
		t.Error("expected no match for a prefix the body does not start with")
	}
}

func TestProbeConnectivityCheckHandlesEmptyResponseAs204OrEmptyBody(t *testing.T) {
	noContent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer noContent.Close()
	if !ProbeConnectivityCheck(noContent.URL, "", time.Second) {
		t.Error("expected a 204 to count as success when no response prefix is configured")
	}

	withBody := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("unexpected"))
	}))
	defer withBody.Close()
	if ProbeConnectivityCheck(withBody.URL, "", time.Second) {
		t.Error("expected a 200 with a body to fail when no response prefix is configured")
	}
}

func TestProbeConnectivityCheckFailsWhenUnreachable(t *testing.T) {
	if ProbeConnectivityCheck("http://127.0.0.1:1", "<html>", 200*time.Millisecond) {
		t.Error("expected an unreachable address to report false")
	}
}
