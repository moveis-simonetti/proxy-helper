package serve

import (
	"context"
	"log/slog"
	"net"
	"time"

	"proxy-helper/internal/proxy"
)

// Default probe pacing. The two intervals are deliberately different: while
// the upstream is up there is nothing to learn from asking often, but while
// it is down the user is on the degraded path and every second of delay is a
// second of traffic leaving direct that did not have to.
const (
	DefaultProbeIntervalUp   = 30 * time.Second
	DefaultProbeIntervalDown = 5 * time.Second
	DefaultProbeTimeout      = 2 * time.Second
	// Asymmetric on purpose, for the same reason as the intervals: three
	// failures to give up, one success to come back.
	DefaultFailThreshold = 3
	DefaultOKThreshold   = 1
)

// Prober answers whether an upstream is reachable. It is an interface so
// tests can script the answers instead of depending on a real network.
type Prober interface {
	Reachable(ctx context.Context, addr string) bool
}

// DialProber is the real probe: a plain TCP connect.
//
// It proves the port accepts connections, not that whatever is behind it
// will actually proxy — a captive portal or a split-tunnel VPN can answer
// and still refuse to forward. That is a known limit, and the reason mode
// upstream exists as a strict pin.
type DialProber struct{ Timeout time.Duration }

func (p DialProber) Reachable(ctx context.Context, addr string) bool {
	timeout := p.Timeout
	if timeout <= 0 {
		timeout = DefaultProbeTimeout
	}
	d := net.Dialer{Timeout: timeout}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// reachabilityTracker turns a stream of probe results into a verdict,
// applying the hysteresis that keeps a blip from rerouting the machine.
//
// It holds no clock and does no I/O: the pacing lives in Watch, so this part
// can be tested exactly rather than approximately.
type reachabilityTracker struct {
	up            bool
	fails         int
	oks           int
	failThreshold int
	okThreshold   int
}

func newReachabilityTracker(up bool, failThreshold, okThreshold int) *reachabilityTracker {
	if failThreshold < 1 {
		failThreshold = DefaultFailThreshold
	}
	if okThreshold < 1 {
		okThreshold = DefaultOKThreshold
	}
	return &reachabilityTracker{up: up, failThreshold: failThreshold, okThreshold: okThreshold}
}

// observe records one probe result and reports whether the verdict flipped.
//
// A result that agrees with the current verdict clears the opposing streak:
// without that, intermittent failures would accumulate over hours into an
// outage that never actually happened.
func (t *reachabilityTracker) observe(ok bool) bool {
	if ok {
		t.fails = 0
		if t.up {
			return false
		}
		t.oks++
		if t.oks >= t.okThreshold {
			t.up = true
			t.oks = 0
			return true
		}
		return false
	}

	t.oks = 0
	if !t.up {
		return false
	}
	t.fails++
	if t.fails >= t.failThreshold {
		t.up = false
		t.fails = 0
		return true
	}
	return false
}

// WatchOptions configures the prober loop. The zero value of each field
// falls back to the Default* constants above.
type WatchOptions struct {
	Prober        Prober
	IntervalUp    time.Duration
	IntervalDown  time.Duration
	FailThreshold int
	OKThreshold   int
}

func (o WatchOptions) withDefaults() WatchOptions {
	if o.Prober == nil {
		o.Prober = DialProber{Timeout: DefaultProbeTimeout}
	}
	if o.IntervalUp <= 0 {
		o.IntervalUp = DefaultProbeIntervalUp
	}
	if o.IntervalDown <= 0 {
		o.IntervalDown = DefaultProbeIntervalDown
	}
	if o.FailThreshold < 1 {
		o.FailThreshold = DefaultFailThreshold
	}
	if o.OKThreshold < 1 {
		o.OKThreshold = DefaultOKThreshold
	}
	return o
}

// Watch keeps the reachability verdict current for mode auto. It blocks
// until ctx is cancelled, so callers run it in a goroutine.
//
// Outside auto it probes nothing at all: upstream is a strict pin that
// ignores the verdict, and direct has no upstream to ask about. The loop
// still wakes on State.kick, so a reload into auto starts probing without
// waiting out an interval.
//
// The verdict lives in State.reachable rather than in the snapshot, so this
// loop never builds a snapshot and never contends with Reload for the
// pointer that the request path reads.
func (s *State) Watch(ctx context.Context, o WatchOptions) {
	o = o.withDefaults()
	tracker := newReachabilityTracker(s.Reachable(), o.FailThreshold, o.OKThreshold)

	for {
		addr := ""
		if s.Mode() == proxy.ModeAuto {
			addr = s.UpstreamAddr()
		}

		wait := o.IntervalUp
		if addr != "" {
			ok := o.Prober.Reachable(ctx, addr)
			if tracker.observe(ok) {
				s.setReachable(tracker.up)
				s.logTransition(tracker.up, addr, o.FailThreshold)
				// The verdict is half of what "forwarding" means in auto,
				// so anything reading from outside is stale until this runs.
				s.publish()
			}
			wait = probeInterval(tracker, o)
		}

		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			// Reload changed the upstream: it reset the verdict to
			// optimistic, so the tracker has to agree or the next failure
			// would count against a streak that no longer applies.
			tracker.up = s.Reachable()
			tracker.fails, tracker.oks = 0, 0
		case <-time.After(wait):
		}
	}
}

// probeInterval decides how long to wait before the next probe.
//
// The slow interval is only for a settled, healthy upstream. The moment a
// probe fails the loop speeds up, even though the verdict has not flipped
// yet: otherwise crossing a 3-failure threshold at the leisurely pace would
// take three slow intervals, and the user would sit on a broken upstream for
// minutes before auto did anything about it. Suspicion accelerates; only
// confidence is allowed to be slow.
func probeInterval(t *reachabilityTracker, o WatchOptions) time.Duration {
	if !t.up || t.fails > 0 {
		return o.IntervalDown
	}
	return o.IntervalUp
}

// logTransition records only the edges. Logging every probe would bury the
// two moments that matter under a steady drip of "still fine".
func (s *State) logTransition(up bool, addr string, failThreshold int) {
	if up {
		s.logger.Info("upstream_up",
			slog.String("addr", addr),
			slog.String("detail", "forwarding again"))
		return
	}
	s.logger.Warn("upstream_down",
		slog.String("addr", addr),
		slog.Int("consecutive_failures", failThreshold),
		slog.String("detail", "mode auto: routing everything direct until it answers again"))
}
