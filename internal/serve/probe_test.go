package serve

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"testing"
	"time"
)

// The tracker is the anti-flapping rule, and it is pure: no clock, no
// network, so the thresholds can be exercised exactly.
func TestReachabilityTrackerThresholds(t *testing.T) {
	tr := newReachabilityTracker(true, 3, 1)

	// Below the failure threshold nothing changes: a single dropped packet
	// on a working upstream must not reroute the whole machine.
	for i := 0; i < 2; i++ {
		if changed := tr.observe(false); changed {
			t.Fatalf("flipped after %d failures, want the 3rd to be decisive", i+1)
		}
		if !tr.up {
			t.Fatalf("marked down after %d failures", i+1)
		}
	}
	if changed := tr.observe(false); !changed {
		t.Error("3rd failure did not flip the verdict")
	}
	if tr.up {
		t.Error("still up after crossing the failure threshold")
	}

	// Recovery is deliberately asymmetric: one success is enough, because
	// staying on direct when the proxy is back costs the user more than a
	// premature switch back costs anyone.
	if changed := tr.observe(true); !changed {
		t.Error("first success did not flip the verdict back")
	}
	if !tr.up {
		t.Error("still down after a success")
	}
}

// A success part-way through a losing streak clears it, or intermittent
// failures would eventually add up to an outage that never happened.
func TestReachabilityTrackerResetsPartialStreak(t *testing.T) {
	tr := newReachabilityTracker(true, 3, 1)
	tr.observe(false)
	tr.observe(false)
	if changed := tr.observe(true); changed {
		t.Error("a success on an up tracker reported a change")
	}
	tr.observe(false)
	tr.observe(false)
	if !tr.up {
		t.Error("the streak was not reset by the intervening success")
	}
}

// scriptedProber returns a canned answer per call and records how many
// times it was asked.
type scriptedProber struct {
	mu      sync.Mutex
	answers []bool
	calls   int
	addrs   []string
}

func (p *scriptedProber) Reachable(_ context.Context, addr string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.addrs = append(p.addrs, addr)
	i := p.calls
	p.calls++
	if i < len(p.answers) {
		return p.answers[i]
	}
	return p.answers[len(p.answers)-1]
}

func (p *scriptedProber) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newAutoState(t *testing.T, upstream string) *State {
	t.Helper()
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"auto","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"`+upstream+`","port":"8080"}}}`)
	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	return st
}

func fastWatch(p Prober) WatchOptions {
	return WatchOptions{
		Prober:        p,
		IntervalUp:    time.Millisecond,
		IntervalDown:  time.Millisecond,
		FailThreshold: 3,
		OKThreshold:   1,
	}
}

// waitFor polls cond until it holds or the deadline passes, so the tests
// never depend on a fixed sleep.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestWatchDegradesAndRecovers(t *testing.T) {
	st := newAutoState(t, "proxy.corp")
	p := &scriptedProber{answers: []bool{false, false, false}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Watch(ctx, fastWatch(p))

	waitFor(t, "the upstream to be marked down", func() bool { return !st.Reachable() })
	if up := st.Router().Route("example.com"); up.Kind != KindDirect {
		t.Errorf("Route = %v, want DIRECT once the upstream is down", up)
	}

	p.mu.Lock()
	p.answers = []bool{true}
	p.calls = 0
	p.mu.Unlock()

	waitFor(t, "the upstream to come back", func() bool { return st.Reachable() })
	if up := st.Router().Route("example.com"); up.Kind != KindHTTP {
		t.Errorf("Route = %v, want the upstream once it answers again", up)
	}
}

// Regression test for a livelock: NoteUpstreamFailure used to share its
// wake-up channel with Reload, and that channel's handler unconditionally
// reset the tracker's fail streak. During a real outage every live request
// fails and calls NoteUpstreamFailure — so with a shared channel, each of
// those wake-ups reset the streak back to zero before three consecutive
// probe failures could ever accumulate, and the verdict stayed "up" no
// matter how long the outage lasted. This drives exactly that flood
// (concurrently with the probe loop, like real traffic would) and requires
// the verdict to flip anyway.
func TestWatchFlipsDownUnderAConstantStreamOfLiveFailures(t *testing.T) {
	st := newAutoState(t, "proxy.corp")
	p := &scriptedProber{answers: []bool{false}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Watch(ctx, fastWatch(p))

	dialErr := &net.OpError{Op: "dial", Err: errors.New("connection refused")}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				st.NoteUpstreamFailure(Upstream{Kind: KindHTTP, Addr: "proxy.corp:8080"}, dialErr)
			}
		}
	}()
	defer wg.Wait()
	defer close(stop)

	waitFor(t, "the upstream to be marked down despite a constant stream of live failures", func() bool {
		return !st.Reachable()
	})
}

// It dials the upstream, not the request's host.
func TestWatchProbesTheUpstreamAddress(t *testing.T) {
	st := newAutoState(t, "proxy.corp")
	p := &scriptedProber{answers: []bool{true}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Watch(ctx, fastWatch(p))

	waitFor(t, "a probe", func() bool { return p.count() > 0 })
	p.mu.Lock()
	got := p.addrs[0]
	p.mu.Unlock()
	if got != "proxy.corp:8080" {
		t.Errorf("probed %q, want %q", got, "proxy.corp:8080")
	}
}

// Only auto consults the prober, so the other modes must generate no probe
// traffic at all.
func TestWatchIsIdleOutsideAuto(t *testing.T) {
	path := withConfigDir(t)
	writeConfig(t, path, `{"mode":"upstream","active_profile":"corp","profiles":{
		"corp":{"scheme":"http","host":"proxy.corp","port":"8080"}}}`)
	st, err := NewState(NewLogger(io.Discard, false))
	if err != nil {
		t.Fatalf("NewState: %v", err)
	}
	p := &scriptedProber{answers: []bool{false}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go st.Watch(ctx, fastWatch(p))

	time.Sleep(30 * time.Millisecond)
	if n := p.count(); n != 0 {
		t.Errorf("probed %d times in mode upstream, want 0", n)
	}
}

func TestWatchStopsOnContextCancel(t *testing.T) {
	st := newAutoState(t, "proxy.corp")
	p := &scriptedProber{answers: []bool{true}}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { st.Watch(ctx, fastWatch(p)); close(done) }()

	waitFor(t, "a probe", func() bool { return p.count() > 0 })
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Watch did not return after the context was cancelled")
	}
}

// DialProber is the real probe; it must agree with reality on a live
// listener and on a dead address.
func TestDialProberAgreesWithReality(t *testing.T) {
	addr, ln := listenOn(t, "127.0.0.1")
	p := DialProber{Timeout: time.Second}

	if !p.Reachable(context.Background(), addr) {
		t.Errorf("Reachable(%q) = false for a live listener", addr)
	}
	ln.Close()
	if p.Reachable(context.Background(), addr) {
		t.Errorf("Reachable(%q) = true after the listener closed", addr)
	}
}

// Pacing is a pure decision, so it is tested as one. The case that matters
// is the middle row: a failure that has NOT yet flipped the verdict must
// still speed the loop up, or crossing a 3-failure threshold would take
// three slow intervals — minutes on a broken upstream before auto reacts.
func TestProbeIntervalAcceleratesOnSuspicion(t *testing.T) {
	o := WatchOptions{IntervalUp: 30 * time.Second, IntervalDown: 5 * time.Second}

	cases := []struct {
		name  string
		up    bool
		fails int
		want  time.Duration
	}{
		{"settled and healthy", true, 0, 30 * time.Second},
		{"up but a probe just failed", true, 1, 5 * time.Second},
		{"already down", false, 0, 5 * time.Second},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tr := &reachabilityTracker{up: c.up, fails: c.fails}
			if got := probeInterval(tr, o); got != c.want {
				t.Errorf("probeInterval = %v, want %v", got, c.want)
			}
		})
	}
}
