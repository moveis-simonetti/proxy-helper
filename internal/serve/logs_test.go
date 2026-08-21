package serve

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// journald -o json wraps each line: the daemon's JSON lands in MESSAGE.
const sampleJournal = `{"MESSAGE":"{\"time\":\"2026-08-21T14:22:01Z\",\"level\":\"INFO\",\"msg\":\"request\",\"method\":\"CONNECT\",\"host\":\"github.com\",\"port\":\"443\",\"decision\":\"http\",\"upstream\":\"http://proxy.corp:8080\",\"status\":200,\"bytes_in\":1200000,\"bytes_out\":900,\"duration_ms\":142}"}
{"MESSAGE":"{\"time\":\"2026-08-21T14:22:04Z\",\"level\":\"INFO\",\"msg\":\"request\",\"method\":\"GET\",\"host\":\"gitlab.interno\",\"port\":\"80\",\"decision\":\"direct\",\"upstream\":\"DIRECT\",\"status\":200,\"bytes_in\":0,\"bytes_out\":890,\"duration_ms\":2}"}
{"MESSAGE":"{\"time\":\"2026-08-21T14:22:09Z\",\"level\":\"ERROR\",\"msg\":\"request\",\"method\":\"CONNECT\",\"host\":\"api.stripe.com\",\"port\":\"443\",\"decision\":\"http\",\"upstream\":\"http://proxy.corp:8080\",\"status\":502,\"duration_ms\":310,\"error\":\"upstream refused\"}"}
`

func parseSample(t *testing.T) []LogEntry {
	t.Helper()
	entries, err := ParseEntries(strings.NewReader(sampleJournal))
	if err != nil {
		t.Fatalf("ParseEntries: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("got %d entries, want 3", len(entries))
	}
	return entries
}

func TestParseEntries(t *testing.T) {
	entries := parseSample(t)
	if entries[0].Host != "github.com" || entries[0].Status != 200 {
		t.Errorf("first entry = %+v", entries[0])
	}
	if entries[2].Err != "upstream refused" {
		t.Errorf("error field = %q", entries[2].Err)
	}
}

func TestFilter(t *testing.T) {
	entries := parseSample(t)

	if got := Filter(entries, FilterOptions{Host: "github.com"}); len(got) != 1 {
		t.Errorf("host filter returned %d entries, want 1", len(got))
	}
	if got := Filter(entries, FilterOptions{ErrorsOnly: true}); len(got) != 1 || got[0].Status != 502 {
		t.Errorf("errors filter returned %+v", got)
	}
	if got := Filter(entries, FilterOptions{DirectOnly: true}); len(got) != 1 || got[0].Host != "gitlab.interno" {
		t.Errorf("direct filter returned %+v", got)
	}
}

func TestRenderEntriesHasNoANSIWhenColorIsOff(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderEntries(&buf, parseSample(t), false); err != nil {
		t.Fatalf("RenderEntries: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Error("output must contain no ANSI escapes when color is off")
	}
	for _, want := range []string{"CONNECT", "github.com", "DIRECT", "502"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q\n---\n%s", want, out)
		}
	}
}

func TestRenderEntriesRedactsCredentials(t *testing.T) {
	entries := []LogEntry{{
		Method: "GET", Host: "example.com", Decision: "http",
		Upstream: "http://alice:s3cr3t@proxy.corp:8080", Status: 200,
	}}
	var buf bytes.Buffer
	if err := RenderEntries(&buf, entries, false); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "s3cr3t") {
		t.Error("rendered log leaked a password")
	}
}

func TestRenderStats(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderStats(&buf, parseSample(t)); err != nil {
		t.Fatalf("RenderStats: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"3", "proxied", "direct", "github.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("stats output is missing %q\n---\n%s", want, out)
		}
	}
}

// mustJournalLine encodes entry as journald would wrap it: the daemon's
// JSON serialized into the MESSAGE field of journalctl's own envelope.
func mustJournalLine(t *testing.T, entry LogEntry) string {
	t.Helper()
	payload, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	wrapped, err := json.Marshal(journalLine{Message: string(payload)})
	if err != nil {
		t.Fatal(err)
	}
	return string(wrapped)
}

// TestParseEntriesSkipsOversizedLine reproduces the reviewer's finding: a
// single journal line far larger than the scan buffer must not abort the
// whole read. Entries before AND after the oversized line must survive, and
// ParseEntries must return no error.
func TestParseEntriesSkipsOversizedLine(t *testing.T) {
	var buf bytes.Buffer
	for i := 0; i < 3; i++ {
		buf.WriteString(mustJournalLine(t, LogEntry{
			Msg: "request", Method: "GET",
			Host: fmt.Sprintf("before-%d.example", i), Status: 200,
		}))
		buf.WriteByte('\n')
	}

	// One absurdly long line in the middle: bigger than any reasonable
	// scan buffer, and not valid JSON, so it must fail to parse and be
	// skipped rather than blow up the read.
	buf.WriteString(strings.Repeat("x", 5*1024*1024))
	buf.WriteByte('\n')

	for i := 0; i < 3; i++ {
		buf.WriteString(mustJournalLine(t, LogEntry{
			Msg: "request", Method: "GET",
			Host: fmt.Sprintf("after-%d.example", i), Status: 200,
		}))
		buf.WriteByte('\n')
	}

	entries, err := ParseEntries(&buf)
	if err != nil {
		t.Fatalf("ParseEntries returned an error instead of skipping the oversized line: %v", err)
	}
	if len(entries) != 6 {
		t.Fatalf("got %d entries, want 6 (3 before + 3 after the oversized line): %+v", len(entries), entries)
	}
	for i := 0; i < 3; i++ {
		if want := fmt.Sprintf("before-%d.example", i); entries[i].Host != want {
			t.Errorf("entries[%d].Host = %q, want %q", i, entries[i].Host, want)
		}
	}
	for i := 0; i < 3; i++ {
		if want := fmt.Sprintf("after-%d.example", i); entries[3+i].Host != want {
			t.Errorf("entries[%d].Host = %q, want %q", 3+i, entries[3+i].Host, want)
		}
	}
}

// TestRenderEntriesColorAlignsSameAsPlain guards against tabwriter-style
// misalignment: rendering with color must produce the same visible layout
// as rendering without it, once ANSI escapes are stripped back out.
func TestRenderEntriesColorAlignsSameAsPlain(t *testing.T) {
	entries := parseSample(t)

	var plainBuf, colorBuf bytes.Buffer
	if err := RenderEntries(&plainBuf, entries, false); err != nil {
		t.Fatalf("RenderEntries(color=false): %v", err)
	}
	if err := RenderEntries(&colorBuf, entries, true); err != nil {
		t.Fatalf("RenderEntries(color=true): %v", err)
	}

	stripped := stripANSI(colorBuf.String())
	if stripped != plainBuf.String() {
		t.Errorf("colored output misaligns once ANSI is stripped\nplain:\n%s\nstripped color:\n%s", plainBuf.String(), stripped)
	}
}

// stripANSI removes "\x1b[...m" escape sequences, leaving only the visible
// text, so colored and uncolored renders can be compared byte for byte.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) && s[j] != 'm' {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
