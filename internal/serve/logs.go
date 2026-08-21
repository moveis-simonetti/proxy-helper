package serve

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"proxy-helper/internal/proxy"
)

// LogEntry is one decoded daemon log line.
type LogEntry struct {
	Time     time.Time `json:"time"`
	Level    string    `json:"level"`
	Msg      string    `json:"msg"`
	Method   string    `json:"method"`
	Host     string    `json:"host"`
	Port     string    `json:"port"`
	Decision string    `json:"decision"`
	Upstream string    `json:"upstream"`
	Status   int       `json:"status"`
	BytesIn  int64     `json:"bytes_in"`
	BytesOut int64     `json:"bytes_out"`
	Duration int64     `json:"duration_ms"`
	Err      string    `json:"error"`
}

// journalLine is journald's own envelope; the daemon's JSON is in MESSAGE.
type journalLine struct {
	Message string `json:"MESSAGE"`
}

// maxLineBytes caps how much of a single journal line we keep in memory.
// A line that exceeds it is truncated (and so fails to parse as JSON and is
// skipped) rather than being buffered without bound.
const maxLineBytes = 4 * 1024 * 1024

// ParseEntries decodes "journalctl -o json" output. Lines that are not
// daemon JSON are skipped rather than failing the whole read, and a single
// oversized line cannot abort the scan or exhaust memory: it is truncated,
// fails to parse, and reading continues with the next line.
func ParseEntries(r io.Reader) ([]LogEntry, error) {
	var entries []LogEntry
	reader := bufio.NewReader(r)

	for {
		line, readErr := readLine(reader, maxLineBytes)
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if entry, ok := decodeJournalLine(trimmed); ok {
				entries = append(entries, entry)
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return entries, nil
			}
			return entries, readErr
		}
	}
}

// StreamEntries decodes the same input as ParseEntries but hands each entry
// to fn the moment it is decoded, instead of collecting them all first.
// That is what makes "--follow" work: "journalctl -f" never reaches EOF, so
// anything that waits for the end of the stream prints nothing at all.
//
// It returns nil at EOF; any other read error, or the first error returned
// by fn, stops the stream and is returned.
func StreamEntries(r io.Reader, fn func(LogEntry) error) error {
	reader := bufio.NewReader(r)

	for {
		line, readErr := readLine(reader, maxLineBytes)
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			if entry, ok := decodeJournalLine(trimmed); ok {
				if err := fn(entry); err != nil {
					return err
				}
			}
		}
		if readErr != nil {
			if readErr == io.EOF {
				return nil
			}
			return readErr
		}
	}
}

// decodeJournalLine unwraps journald's "MESSAGE" envelope (if present) and
// decodes the daemon's JSON payload. It reports ok=false for anything that
// isn't a valid request-log entry, so callers can skip it silently.
func decodeJournalLine(line string) (LogEntry, bool) {
	var envelope journalLine
	payload := line
	if err := json.Unmarshal([]byte(line), &envelope); err == nil && envelope.Message != "" {
		payload = envelope.Message
	}
	var entry LogEntry
	if err := json.Unmarshal([]byte(payload), &entry); err != nil {
		return LogEntry{}, false
	}
	if entry.Msg == "" {
		return LogEntry{}, false
	}
	return entry, true
}

// readLine reads a single line from r, never buffering more than maxLen
// bytes of it: once a line's content reaches maxLen, the remaining bytes up
// to the next newline are read and discarded in bounded chunks rather than
// accumulated, so one huge line cannot grow memory without limit. It returns
// the line (trimmed of its trailing newline, truncated to maxLen if the
// source line was longer) and any read error, following the same
// last-line-without-EOF convention as bufio.Scanner: a final non-empty line
// with no trailing newline is returned together with io.EOF.
func readLine(r *bufio.Reader, maxLen int) (string, error) {
	var buf []byte
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 && len(buf) < maxLen {
			take := chunk
			if len(buf)+len(take) > maxLen {
				take = take[:maxLen-len(buf)]
			}
			buf = append(buf, take...)
		}
		if err == nil {
			// Found the delimiter; chunk included it.
			break
		}
		if err == bufio.ErrBufferFull {
			// Line continues past the reader's internal buffer; keep
			// reading (and discarding once over maxLen) until the
			// newline shows up or the input ends.
			continue
		}
		// io.EOF or a real read error: return what we have.
		return strings.TrimRight(string(buf), "\r\n"), err
	}
	return strings.TrimRight(string(buf), "\r\n"), nil
}

// FilterOptions narrows a set of entries.
type FilterOptions struct {
	Host       string
	ErrorsOnly bool
	DirectOnly bool
}

func Filter(entries []LogEntry, opts FilterOptions) []LogEntry {
	var out []LogEntry
	for _, e := range entries {
		if Match(e, opts) {
			out = append(out, e)
		}
	}
	return out
}

// Match reports whether one entry belongs in the rendered view. Lifecycle
// events (startup, reload, reload_failed) carry none of the request columns,
// so they would render as an empty row; they are dropped here rather than in
// the parser, which keeps "--json" showing the raw journal untouched.
func Match(e LogEntry, opts FilterOptions) bool {
	if e.Msg != "request" {
		return false
	}
	if opts.Host != "" && !strings.Contains(e.Host, opts.Host) {
		return false
	}
	if opts.ErrorsOnly && e.Status < 400 && e.Err == "" {
		return false
	}
	if opts.DirectOnly && e.Decision != "direct" {
		return false
	}
	return true
}

const (
	ansiReset  = "\x1b[0m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiRed    = "\x1b[31m"
	ansiCyan   = "\x1b[36m"
	ansiGrey   = "\x1b[90m"
)

// cell is one column of a rendered row: plain, uncolored text plus the ANSI
// code (if any) that should wrap it once column widths are settled. Keeping
// text and color separate lets us compute alignment from the visible
// characters only, never from escape-sequence bytes.
type cell struct {
	text  string
	color string
}

// RenderEntries prints the human-facing view. Color is applied only when the
// caller says the destination is a terminal, so piping stays clean.
func RenderEntries(w io.Writer, entries []LogEntry, color bool) error {
	rows := make([][]cell, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, entryCells(e))
	}
	return writeAlignedRows(w, rows, color)
}

// followWidths are the column widths used when entries are rendered one at a
// time. Follow mode cannot measure the widest row in advance — the next line
// may not exist yet — so it pads to fixed, roomy widths instead. A cell that
// overflows simply pushes its row wider, exactly as tabular output does.
var followWidths = []int{8, 7, 3, 7, 9, 28, 0}

// RenderEntry prints a single entry and flushes it immediately, so a caller
// following the journal sees each request as it happens.
func RenderEntry(w io.Writer, e LogEntry, color bool) error {
	bw := bufio.NewWriter(w)
	if err := writeRow(bw, entryCells(e), followWidths, color); err != nil {
		return err
	}
	return bw.Flush()
}

// entryCells lays one entry out as the columns of a rendered row.
func entryCells(e LogEntry) []cell {
	statusColor := ansiGreen
	switch {
	case e.Status >= 500:
		statusColor = ansiRed
	case e.Status >= 400:
		statusColor = ansiYellow
	}

	arrowText := "-> " + proxy.Redact(e.Upstream)
	arrowColor := ""
	if e.Decision == "direct" {
		arrowText = "-> DIRECT"
		arrowColor = ansiCyan
	}
	if e.Err != "" {
		arrowText = "x " + e.Err
		arrowColor = ansiRed
	}

	host := e.Host
	if e.Port != "" && e.Port != "80" {
		host = host + ":" + e.Port
	}

	return []cell{
		{e.Time.Local().Format("15:04:05"), ansiGrey},
		{e.Method, ""},
		{fmt.Sprint(e.Status), statusColor},
		{fmt.Sprintf("%dms", e.Duration), ansiGrey},
		{humanBytes(e.BytesIn + e.BytesOut), ""},
		{host, ""},
		{arrowText, arrowColor},
	}
}

// writeAlignedRows prints rows with each column padded to its widest cell's
// visible width, measured in runes so multi-byte text still lines up. Color
// escapes are appended only after padding is computed, so they never throw
// off alignment the way feeding them through text/tabwriter would (tabwriter
// counts the escape bytes as part of the cell's width).
func writeAlignedRows(w io.Writer, rows [][]cell, color bool) error {
	if len(rows) == 0 {
		return nil
	}
	cols := len(rows[0])
	widths := make([]int, cols)
	for _, row := range rows {
		for i, c := range row {
			if n := utf8.RuneCountInString(c.text); n > widths[i] {
				widths[i] = n
			}
		}
	}

	bw := bufio.NewWriter(w)
	for _, row := range rows {
		if err := writeRow(bw, row, widths, color); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// writeRow prints one row, padding each cell to the given width before any
// color escape is added, so the escapes never count toward alignment. A
// width of zero (or one smaller than the text) means "no padding".
func writeRow(bw *bufio.Writer, row []cell, widths []int, color bool) error {
	for i, c := range row {
		if i > 0 {
			if _, err := bw.WriteString("  "); err != nil {
				return err
			}
		}
		text := c.text
		if i < len(row)-1 && i < len(widths) {
			// Last column needs no trailing padding.
			if pad := widths[i] - utf8.RuneCountInString(text); pad > 0 {
				text += strings.Repeat(" ", pad)
			}
		}
		if color && c.color != "" {
			text = c.color + text + ansiReset
		}
		if _, err := bw.WriteString(text); err != nil {
			return err
		}
	}
	return bw.WriteByte('\n')
}

// RenderStats prints an aggregate summary over the same data.
func RenderStats(w io.Writer, entries []LogEntry) error {
	var proxied, direct, errors int
	var bytes int64
	byHost := map[string]int{}
	bytesByHost := map[string]int64{}

	for _, e := range entries {
		if e.Decision == "direct" {
			direct++
		} else {
			proxied++
		}
		if e.Status >= 400 || e.Err != "" {
			errors++
		}
		n := e.BytesIn + e.BytesOut
		bytes += n
		byHost[e.Host]++
		bytesByHost[e.Host] += n
	}

	total := len(entries)
	fmt.Fprintf(w, "requests: %d  (proxied %d, direct %d)\n", total, proxied, direct)
	if total > 0 {
		fmt.Fprintf(w, "errors:   %d (%.1f%%)\n", errors, float64(errors)*100/float64(total))
	} else {
		fmt.Fprintf(w, "errors:   0\n")
	}
	fmt.Fprintf(w, "traffic:  %s\n\n", humanBytes(bytes))

	fmt.Fprintln(w, "top hosts by requests:")
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	for _, h := range topKeys(byHost, 10) {
		fmt.Fprintf(tw, "  %s\t%d\t%s\n", h, byHost[h], humanBytes(bytesByHost[h]))
	}
	return tw.Flush()
}

func topKeys(counts map[string]int, n int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	if len(keys) > n {
		keys = keys[:n]
	}
	return keys
}

func humanBytes(n int64) string {
	switch {
	case n <= 0:
		return "-"
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
