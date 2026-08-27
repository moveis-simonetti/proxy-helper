package serve

import (
	"bytes"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestStreamEntriesRendersBeforeEOF is the regression test for "--follow":
// "journalctl -f" never reaches EOF, so an entry has to be rendered as soon
// as its line arrives. Anything that waits for the end of the stream prints
// nothing at all.
func TestStreamEntriesRendersBeforeEOF(t *testing.T) {
	pr, pw := io.Pipe()
	defer pw.Close()

	var mu sync.Mutex
	var out bytes.Buffer

	done := make(chan error, 1)
	go func() {
		done <- StreamEntries(pr, func(e LogEntry) error {
			mu.Lock()
			defer mu.Unlock()
			return RenderEntry(&out, e, false)
		})
	}()

	rendered := func() string {
		mu.Lock()
		defer mu.Unlock()
		return out.String()
	}

	// waitFor polls for text to appear while the stream is still open.
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) {
			if strings.Contains(rendered(), want) {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatalf("%q never rendered while the stream was still open; got:\n%s", want, rendered())
	}

	for _, host := range []string{"first.example", "second.example"} {
		line := mustJournalLine(t, LogEntry{
			Msg: "request", Method: "GET", Host: host,
			Decision: "http", Upstream: "http://proxy.corp:8080", Status: 200,
		})
		if _, err := io.WriteString(pw, line+"\n"); err != nil {
			t.Fatal(err)
		}
		waitFor(host)
	}

	pw.Close()
	if err := <-done; err != nil {
		t.Fatalf("StreamEntries: %v", err)
	}
}

// TestStreamEntriesStopsOnCallbackError lets a caller abort the follow.
func TestStreamEntriesStopsOnCallbackError(t *testing.T) {
	line := mustJournalLine(t, LogEntry{Msg: "request", Host: "a.example"})
	err := StreamEntries(strings.NewReader(line+"\n"+line+"\n"), func(LogEntry) error {
		return io.ErrClosedPipe
	})
	if err != io.ErrClosedPipe {
		t.Errorf("StreamEntries error = %v, want ErrClosedPipe", err)
	}
}

// TestRenderEntryHasNoANSIWhenColorIsOff mirrors the batch renderer's rule.
func TestRenderEntryHasNoANSIWhenColorIsOff(t *testing.T) {
	var buf bytes.Buffer
	entry := LogEntry{
		Msg: "request", Method: "GET", Host: "example.com", Status: 200,
		Decision: "http", Upstream: "http://alice:s3cr3t@proxy.corp:8080",
	}
	if err := RenderEntry(&buf, entry, false); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Error("output must contain no ANSI escapes when color is off")
	}
	if strings.Contains(out, "s3cr3t") {
		t.Error("follow-mode render leaked a password")
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("each entry must be a complete line")
	}
}

// TestMatchDropsLifecycleEvents: startup/reload lines carry none of the
// request columns and would render as an empty row.
func TestMatchDropsLifecycleEvents(t *testing.T) {
	for _, msg := range []string{"startup", "reload", "reload_failed"} {
		if Match(LogEntry{Msg: msg}, FilterOptions{}) {
			t.Errorf("%q must not reach the rendered table", msg)
		}
	}
	if !Match(LogEntry{Msg: "request", Host: "a.example"}, FilterOptions{}) {
		t.Error("request entries must be kept")
	}

	entries := []LogEntry{
		{Msg: "startup"},
		{Msg: "request", Host: "a.example"},
		{Msg: "reload"},
	}
	if got := Filter(entries, FilterOptions{}); len(got) != 1 || got[0].Host != "a.example" {
		t.Errorf("Filter kept %+v, want only the request entry", got)
	}
}
