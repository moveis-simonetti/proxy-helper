package serve

import (
	"io"
	"log/slog"
	"time"
)

// NewLogger builds the daemon's logger. Output is always JSON: the stored
// form is meant for machines (journald), and "proxy logs" is what renders it
// for humans. quiet drops per-request lines, keeping only warnings and above.
func NewLogger(w io.Writer, quiet bool) *slog.Logger {
	level := slog.LevelInfo
	if quiet {
		level = slog.LevelWarn
	}
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}

// RequestEvent is one proxied request, as it lands in the journal. Field
// names here are the contract "proxy logs" filters and renders on, so
// renaming one is a breaking change to the log reader.
type RequestEvent struct {
	Method   string
	Host     string
	Port     string
	Decision string // direct, http, socks5
	Upstream string // redacted; "DIRECT" when not proxied
	Status   int
	BytesIn  int64
	BytesOut int64
	Duration time.Duration
	Err      string
}

// Attrs renders the event as slog attributes.
func (e RequestEvent) Attrs() []any {
	attrs := []any{
		slog.String("method", e.Method),
		slog.String("host", e.Host),
		slog.String("port", e.Port),
		slog.String("decision", e.Decision),
		slog.String("upstream", e.Upstream),
		slog.Int("status", e.Status),
		slog.Int64("bytes_in", e.BytesIn),
		slog.Int64("bytes_out", e.BytesOut),
		slog.Int64("duration_ms", e.Duration.Milliseconds()),
	}
	if e.Err != "" {
		attrs = append(attrs, slog.String("error", e.Err))
	}
	return attrs
}
