package wingui

import "testing"

// The daemon's own error line is the answer someone needs; the message that
// used to be shown only repeated the symptom.
func TestSummarizeLogPrefersTheDaemonsError(t *testing.T) {
	raw := `{"level":"info","msg":"starting"}
{"level":"error","msg":"listen failed","error":"bind: address already in use"}`

	got := summarizeLog(raw)

	want := "listen failed: bind: address already in use"
	if got != want {
		t.Errorf("summarizeLog = %q, want %q", got, want)
	}
}

// A daemon that failed yesterday and started fine today must not have
// yesterday's failure reported as the current one.
func TestSummarizeLogReadsTheLatestFailureNotTheFirst(t *testing.T) {
	raw := `{"level":"error","msg":"old problem"}
{"level":"error","msg":"current problem"}`

	if got := summarizeLog(raw); got != "current problem" {
		t.Errorf("summarizeLog = %q, want the last failure", got)
	}
}

// A panic is not JSON, and it is the case most worth showing verbatim.
func TestSummarizeLogPassesThroughOutputThatIsNotStructured(t *testing.T) {
	if got := summarizeLog("panic: runtime error: index out of range"); got == "" {
		t.Error("a panic line was dropped; it is the one most worth showing")
	}
}

// Nothing logged means the daemon never got that far. Inventing a cause
// would be worse than saying so.
func TestSummarizeLogReturnsNothingWhenThereIsNoFailure(t *testing.T) {
	raw := `{"level":"info","msg":"starting"}`

	if got := summarizeLog(raw); got != "" {
		t.Errorf("summarizeLog = %q, want empty for a log with no failure", got)
	}
}
