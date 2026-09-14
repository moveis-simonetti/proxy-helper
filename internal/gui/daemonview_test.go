package gui

import (
	"sort"
	"strings"
	"testing"
	"time"

	"proxy-helper/internal/serve"
)

func TestLogRowsFormatting(t *testing.T) {
	t1 := time.Date(2026, 8, 24, 9, 5, 3, 0, time.UTC)
	entries := []serve.LogEntry{
		{
			Msg:      "request",
			Time:     t1,
			Method:   "GET",
			Host:     "example.com",
			Port:     "",
			Decision: "proxy",
			Status:   200,
			Duration: 12,
		},
		{
			Msg:      "request",
			Time:     t1.Add(time.Second),
			Method:   "GET",
			Host:     "example.com",
			Port:     "8443",
			Decision: "direct",
			Status:   0,
			Err:      "timeout",
			Duration: 5,
		},
		{
			Msg:      "request",
			Time:     t1.Add(2 * time.Second),
			Method:   "GET",
			Host:     "example.com",
			Decision: "proxy",
			Status:   0,
			Duration: 3,
		},
	}

	rows := logRows(entries)
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(rows))
	}

	if rows[0].Route != "proxy" {
		t.Errorf("Route = %q, want %q", rows[0].Route, "proxy")
	}
	if rows[1].Route != "direto" {
		t.Errorf("Route = %q, want %q", rows[1].Route, "direto")
	}

	if rows[0].Status != "200" {
		t.Errorf("Status = %q, want %q", rows[0].Status, "200")
	}
	if rows[1].Status != "erro" {
		t.Errorf("Status (with Err) = %q, want %q", rows[1].Status, "erro")
	}
	if rows[2].Status != "—" {
		t.Errorf("Status (zero) = %q, want %q", rows[2].Status, "—")
	}

	if rows[0].Duration != "12 ms" {
		t.Errorf("Duration = %q, want %q", rows[0].Duration, "12 ms")
	}

	if rows[0].Target != "example.com" {
		t.Errorf("Target (no port) = %q, want %q", rows[0].Target, "example.com")
	}
	if rows[1].Target != "example.com:8443" {
		t.Errorf("Target (with port) = %q, want %q", rows[1].Target, "example.com:8443")
	}

	if rows[0].Time != "09:05:03" {
		t.Errorf("Time = %q, want %q", rows[0].Time, "09:05:03")
	}
}

// Sorting the visible strings would put "12 ms" before "9 ms" (as text,
// "12 ms" < "9 ms" because '1' < '9'). The hidden numeric key is the whole
// reason a real sort — e.g. sort.Slice by SortDuration, which is what the
// table's column click will do — lands the rows in the right order instead.
func TestLogRowsCarryNumericSortKeys(t *testing.T) {
	entries := []serve.LogEntry{
		{Msg: "request", Duration: 12},
		{Msg: "request", Duration: 9},
	}
	rows := logRows(entries)
	if rows[0].SortDuration <= rows[1].SortDuration {
		t.Fatal("setup")
	}
	// Confirms the premise: the display strings, compared as text, already
	// land in the wrong order.
	if rows[0].Duration >= rows[1].Duration {
		t.Fatalf("setup: expected the display strings to demonstrate the wrong order, got %q then %q", rows[0].Duration, rows[1].Duration)
	}

	sorted := append([]logRow(nil), rows...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].SortDuration < sorted[j].SortDuration })
	if sorted[0].SortDuration != 9 || sorted[1].SortDuration != 12 {
		t.Errorf("sorting by SortDuration gave the wrong order: got %d then %d, want 9 then 12", sorted[0].SortDuration, sorted[1].SortDuration)
	}
}

func TestLogRowsSortTimeIncreasing(t *testing.T) {
	base := time.Date(2026, 8, 24, 10, 0, 0, 0, time.UTC)
	entries := []serve.LogEntry{
		{Msg: "request", Time: base},
		{Msg: "request", Time: base.Add(time.Minute)},
		{Msg: "request", Time: base.Add(2 * time.Minute)},
	}
	rows := logRows(entries)
	if !(rows[0].SortTime < rows[1].SortTime && rows[1].SortTime < rows[2].SortTime) {
		t.Errorf("SortTime not increasing: %v", []int64{rows[0].SortTime, rows[1].SortTime, rows[2].SortTime})
	}
}

func TestLogRowsLifecycleEntryDoesNotPanic(t *testing.T) {
	// serve.Match already drops non-"request" entries; logRows must not
	// refilter, but it also must not panic if one slips through unfiltered.
	entries := []serve.LogEntry{
		{Msg: "startup", Time: time.Now()},
	}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("logRows panicked on a lifecycle entry: %v", r)
		}
	}()
	rows := logRows(entries)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row (unfiltered passthrough), got %d", len(rows))
	}
}

func TestSummarizeStates(t *testing.T) {
	cases := []struct {
		name      string
		active    bool
		installed bool
		want      string
	}{
		{"active", true, false, "Ativo"},
		{"installed not active", false, true, "Parado"},
		{"not installed", false, false, "Não instalado"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := summarize(c.active, c.installed, nil, "")
			if got.State != c.want {
				t.Errorf("State = %q, want %q", got.State, c.want)
			}
		})
	}
}

// Listen now renders what was *observed* on the machine, not what config
// implies. The distinction is the whole point of the change: a saved
// docker_bridge preference the running daemon never acted on used to show up
// here as an address nothing was bound to.
func TestSummarizeListenShowsObservedAddrs(t *testing.T) {
	got := summarize(true, true, []string{"127.0.0.1:8888"}, "")
	if got.Listen != "127.0.0.1:8888" {
		t.Errorf("Listen (loopback only) = %q, want %q", got.Listen, "127.0.0.1:8888")
	}

	got = summarize(true, true, []string{"127.0.0.1:8888", "172.17.0.1:8888"}, "")
	want := "127.0.0.1:8888, 172.17.0.1:8888"
	if got.Listen != want {
		t.Errorf("Listen (with bridge) = %q, want %q", got.Listen, want)
	}
}

// A daemon that is installed but stopped is bound to nothing, and saying
// "127.0.0.1:8888" there would be the same lie in the other direction. The
// State label already carries "Parado"; Listen says there is no address.
func TestSummarizeListenWhenNothingIsBound(t *testing.T) {
	got := summarize(false, true, nil, "")
	if got.Listen != "—" {
		t.Errorf("Listen (stopped) = %q, want %q", got.Listen, "—")
	}
}

func TestDaemonPending(t *testing.T) {
	baseline := daemonFormValues{Port: 8888, DockerBridge: false}

	if daemonPending(baseline, baseline) {
		t.Error("identical values reported as pending")
	}
	if !daemonPending(baseline, daemonFormValues{Port: 8889, DockerBridge: false}) {
		t.Error("changed port not reported as pending")
	}
	if !daemonPending(baseline, daemonFormValues{Port: 8888, DockerBridge: true}) {
		t.Error("changed bridge not reported as pending")
	}
}

func TestSummarizeProfile(t *testing.T) {
	got := summarize(true, true, nil, "")
	if got.Profile != "nenhum" {
		t.Errorf("Profile (empty) = %q, want %q", got.Profile, "nenhum")
	}

	got = summarize(true, true, nil, "work")
	if got.Profile != "work" {
		t.Errorf("Profile = %q, want %q", got.Profile, "work")
	}
}

func TestSameRowsSpotsAnAddedEntry(t *testing.T) {
	a := []logRow{{Time: "10:00:00", Target: "a"}}
	b := []logRow{{Time: "10:00:00", Target: "a"}, {Time: "10:00:01", Target: "b"}}
	if sameRows(a, b) {
		t.Error("sameRows = true com uma entrada nova")
	}
}

func TestSameRowsSpotsAChangedField(t *testing.T) {
	a := []logRow{{Time: "10:00:00", Target: "a", Status: "200"}}
	b := []logRow{{Time: "10:00:00", Target: "a", Status: "502"}}
	if sameRows(a, b) {
		t.Error("sameRows = true com o status diferente")
	}
}

// The quiet case is the one that matters: a table that rebuilds every tick
// throws away the user's selection and scroll for nothing.
func TestSameRowsIsTrueForAnUnchangedRead(t *testing.T) {
	a := []logRow{{Time: "10:00:00", Target: "a"}, {Time: "10:00:01", Target: "b"}}
	b := []logRow{{Time: "10:00:00", Target: "a"}, {Time: "10:00:01", Target: "b"}}
	if !sameRows(a, b) {
		t.Error("sameRows = false com duas leituras iguais")
	}
}

func TestSameRowsIsTrueForTwoEmptyReads(t *testing.T) {
	if !sameRows(nil, []logRow{}) {
		t.Error("sameRows = false para duas leituras vazias")
	}
}

// TestPortChangeStrandsTargets covers the gap this warning exists for:
// applyPrimary saves the new port and reinstalls the unit, but never
// re-applies the targets, so every one of them keeps pointing at the old
// port until the user goes back to the Status page and applies again.
// Silently, that surfaces later as "the proxy stopped working".
func TestPortChangeStrandsTargets(t *testing.T) {
	if !portChangeStrandsTargets(8888, 9090) {
		t.Error("a port change leaves every target on the old port; it must warn")
	}
	if portChangeStrandsTargets(8888, 8888) {
		t.Error("the port did not change, so nothing went stale")
	}
}

// TestStaleTargetsWarningNamesBothPorts keeps the message actionable: the
// user needs to see what the targets still say and what they should say,
// otherwise the warning is just noise.
func TestStaleTargetsWarningNamesBothPorts(t *testing.T) {
	got := staleTargetsWarning(8888, 9090)
	if !strings.Contains(got, "8888") || !strings.Contains(got, "9090") {
		t.Errorf("the warning must name the old and the new port, got: %q", got)
	}
}

// TestStaleBridgeWarningNamesTheTargets keeps the fallback message
// actionable even without port numbers to point at.
func TestStaleBridgeWarningNamesTheTargets(t *testing.T) {
	got := staleBridgeWarning()
	if !strings.Contains(got, "dockerd") || !strings.Contains(got, "docker-config") {
		t.Errorf("the warning must name the two docker targets, got: %q", got)
	}
}

// TestDaemonApplyMessageCarriesTheWarning keeps the warning attached to the
// result the user is already reading. Shown anywhere else it would compete
// with the "alterações aplicadas" line and be missed.
func TestDaemonApplyMessageCarriesTheWarning(t *testing.T) {
	got := daemonApplyMessage(true, staleTargetsWarning(8888, 9090))
	if !strings.Contains(got, "aplicadas") {
		t.Errorf("the usual confirmation must survive, got: %q", got)
	}
	if !strings.Contains(got, "9090") {
		t.Errorf("the warning must be part of the same message, got: %q", got)
	}
}

// TestDaemonApplyMessageWithoutWarningIsUnchanged guards the normal path:
// the warning is the exception, and its absence must leave the existing
// message exactly as it was.
func TestDaemonApplyMessageWithoutWarningIsUnchanged(t *testing.T) {
	if got := daemonApplyMessage(true, ""); got != "alterações aplicadas" {
		t.Errorf("expected the untouched confirmation, got: %q", got)
	}
	if got := daemonApplyMessage(false, ""); got != "serviço instalado" {
		t.Errorf("expected the first-install message, got: %q", got)
	}
}

// The bug this covers: the bridge switch changes intent, not the system, and
// the only signal that something was pending was the primary button turning
// sensitive — quiet enough that a user turned the switch on, closed the
// window, and reported the bridge as broken. The notice says it in words.
func TestDaemonPendingNotice(t *testing.T) {
	if got := daemonPendingNotice(false); got != "" {
		t.Errorf("daemonPendingNotice(false) = %q, want empty", got)
	}

	got := daemonPendingNotice(true)
	if got == "" {
		t.Fatal("daemonPendingNotice(true) = empty, want a notice")
	}
	// Naming the button is what makes the notice actionable rather than
	// just alarming, so it must stay in sync with daemonSaveLabel.
	if !strings.Contains(got, daemonSaveLabel) {
		t.Errorf("daemonPendingNotice(true) = %q, want it to name %q",
			got, daemonSaveLabel)
	}
}
