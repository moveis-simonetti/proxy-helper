package gui

import (
	"sync"
	"testing"
	"time"
)

// deliverNow stands in for glib.IdleAdd: in production the closure is queued
// on the GTK main loop; in the test it runs inline.
func deliverNow(f func()) { f() }

func TestRunnerSerializesJobs(t *testing.T) {
	r := newRunner(deliverNow)
	r.start()
	defer r.stop()

	var mu sync.Mutex
	var trace []string
	var wg sync.WaitGroup
	wg.Add(2)

	r.submit(func() func() {
		mu.Lock()
		trace = append(trace, "start-1")
		mu.Unlock()
		time.Sleep(20 * time.Millisecond)
		mu.Lock()
		trace = append(trace, "end-1")
		mu.Unlock()
		return func() { wg.Done() }
	})
	r.submit(func() func() {
		mu.Lock()
		trace = append(trace, "start-2")
		mu.Unlock()
		return func() { wg.Done() }
	})
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"start-1", "end-1", "start-2"}
	if len(trace) != len(want) {
		t.Fatalf("expected %v, got %v", want, trace)
	}
	for i := range want {
		if trace[i] != want[i] {
			t.Fatalf("jobs overlapped: expected %v, got %v", want, trace)
		}
	}
}

// TestRunnerSurvivesAPanickingJob covers the failure that would otherwise
// wedge the UI in the busy state forever, with no error and no way out.
func TestRunnerSurvivesAPanickingJob(t *testing.T) {
	r := newRunner(deliverNow)
	r.start()
	defer r.stop()

	var wg sync.WaitGroup
	wg.Add(1)
	r.submit(func() func() { panic("boom") })
	r.submit(func() func() { return func() { wg.Done() } })

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the runner died with the panicking job; the UI would stay busy forever")
	}
	if r.busy() {
		t.Error("the runner should be idle after both jobs finished")
	}
}

// TestRunnerSurvivesAPanickingApplyClosure covers the case where the job
// itself succeeds but the closure it returns for the UI thread panics. With
// deliverNow that panic happens synchronously inside run(), which is exactly
// what the widget handlers added in the next task will be made of. Both the
// worker continuing to the next job and stop() returning are bounded by an
// explicit timeout so a regression fails fast instead of hanging the suite.
func TestRunnerSurvivesAPanickingApplyClosure(t *testing.T) {
	r := newRunner(deliverNow)
	r.start()

	var wg sync.WaitGroup
	wg.Add(1)
	r.submit(func() func() { return func() { panic("boom-apply") } })
	r.submit(func() func() { return func() { wg.Done() } })

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("the runner died delivering a panicking apply closure; a later job never ran")
	}

	stopped := make(chan struct{})
	go func() { r.stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() hung after a panicking apply closure; close(r.done) never ran")
	}

	if r.busy() {
		t.Error("the runner should be idle after both jobs finished")
	}
}

// TestRunnerBalancesBusyAroundPanic makes sure a panicking job does not
// leave onBusy stuck at true, or skip the matching false: the buttons a
// later task wires to onBusy must always be re-enabled.
func TestRunnerBalancesBusyAroundPanic(t *testing.T) {
	r := newRunner(deliverNow)

	var mu sync.Mutex
	var busyTrace []bool
	r.setBusyHandler(func(v bool) {
		mu.Lock()
		busyTrace = append(busyTrace, v)
		mu.Unlock()
	})

	r.start()
	defer r.stop()

	var wg sync.WaitGroup
	wg.Add(1)
	r.submit(func() func() { panic("boom") })
	r.submit(func() func() { return func() { wg.Done() } })
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	want := []bool{true, false, true, false}
	if len(busyTrace) != len(want) {
		t.Fatalf("expected %v, got %v", want, busyTrace)
	}
	for i := range want {
		if busyTrace[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, busyTrace)
		}
	}
}

// TestRunnerDeliversResultBeforeBusyFalse guards the ordering the status
// page depends on: if onBusy(false) re-enabled the "Aplicar" button before
// the apply closure ran, a second click could race the first job's own
// result being applied to the widgets.
func TestRunnerDeliversResultBeforeBusyFalse(t *testing.T) {
	r := newRunner(deliverNow)

	var mu sync.Mutex
	var trace []string
	r.setBusyHandler(func(v bool) {
		if v {
			return
		}
		mu.Lock()
		trace = append(trace, "busy-false")
		mu.Unlock()
	})

	r.start()
	defer r.stop()

	var wg sync.WaitGroup
	wg.Add(1)
	r.submit(func() func() {
		return func() {
			mu.Lock()
			trace = append(trace, "apply")
			mu.Unlock()
			wg.Done()
		}
	})
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	want := []string{"apply", "busy-false"}
	if len(trace) != len(want) || trace[0] != want[0] || trace[1] != want[1] {
		t.Fatalf("expected the apply closure delivered before busy-false, got %v", trace)
	}
}

// TestSubmitAfterStopDoesNotPanic covers a click racing window close: a real
// scenario, since submit is called from GTK signal handlers.
func TestSubmitAfterStopDoesNotPanic(t *testing.T) {
	r := newRunner(deliverNow)
	r.start()
	r.stop()

	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("submit after stop panicked: %v", p)
		}
	}()
	r.submit(func() func() { return nil })
}

// TestStopIsIdempotent covers a second stop() call, which app.go's own
// cleanup path can trigger (e.g. once on window-creation failure and once
// more at the end of Run in some refactor) without it being a bug.
func TestStopIsIdempotent(t *testing.T) {
	r := newRunner(deliverNow)
	r.start()

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.stop()
		r.stop()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("a second stop() call hung or panicked")
	}
}

// TestRunnerReportsPanicToHandler covers the failure mode a bare recover
// leaves behind: a click that greys and un-greys the button with nothing
// else happening, and no evidence to file a bug against. The registered
// handler must receive the panic value and a non-empty stack.
func TestRunnerReportsPanicToHandler(t *testing.T) {
	r := newRunner(deliverNow)

	var mu sync.Mutex
	var got []any
	r.setPanicHandler(func(v any) {
		mu.Lock()
		got = append(got, v)
		mu.Unlock()
	})

	r.start()
	defer r.stop()

	var wg sync.WaitGroup
	wg.Add(1)
	r.submit(func() func() { panic("boom-reported") })
	r.submit(func() func() { return func() { wg.Done() } })
	wg.Wait()

	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly one panic report, got %d: %v", len(got), got)
	}
	info, ok := got[0].(jobPanic)
	if !ok {
		t.Fatalf("expected a jobPanic, got %T", got[0])
	}
	if info.Value != "boom-reported" {
		t.Errorf("expected panic value %q, got %v", "boom-reported", info.Value)
	}
	if len(info.Stack) == 0 {
		t.Error("expected a non-empty stack trace")
	}
}

// TestStopBeforeStartDoesNotHang covers stop() called on a runner that was
// never start()ed: with no worker goroutine ranging over r.jobs, nothing
// would otherwise ever close r.done. Bounded by a timeout so a regression
// fails the test instead of wedging the suite.
func TestStopBeforeStartDoesNotHang(t *testing.T) {
	r := newRunner(deliverNow)

	stopped := make(chan struct{})
	go func() {
		r.stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("stop() before start() hung")
	}
}

// Every page registers a busy handler. A single-slot field meant the last one
// registered silently won, and the other pages' buttons were never disabled
// during a job — a bug that stays invisible until a second page exists, which
// is exactly when it appeared here.
func TestSetBusyHandlerNotifiesEveryRegisteredHandler(t *testing.T) {
	var got []string
	r := newRunner(func(f func()) { f() })
	r.setBusyHandler(func(busy bool) {
		if busy {
			got = append(got, "primeira")
		}
	})
	r.setBusyHandler(func(busy bool) {
		if busy {
			got = append(got, "segunda")
		}
	})
	r.start()
	defer r.stop()

	done := make(chan struct{})
	r.submit(func() func() { return func() { close(done) } })
	<-done

	if len(got) != 2 || got[0] != "primeira" || got[1] != "segunda" {
		t.Errorf("handlers notificados = %v, queria [primeira segunda]; um handler perdido deixa a página dele sem proteção durante o job", got)
	}
}

// A background poll must not disable the UI. Every job used to raise the
// busy signal, which turns off the action buttons on every page — right for
// a click the user is waiting on, wrong for a refresh nobody asked for. Once
// the Daemon page began re-reading the journal every two seconds, the result
// was every button in the window flickering on a two-second cycle.
func TestSubmitQuietDoesNotRaiseTheBusySignal(t *testing.T) {
	r := newRunner(func(f func()) { f() })
	var seen []bool
	r.setBusyHandler(func(busy bool) { seen = append(seen, busy) })
	r.start()

	done := make(chan struct{})
	r.submitQuiet(func() func() { return func() { close(done) } })
	<-done
	r.stop()

	if len(seen) != 0 {
		t.Errorf("submitQuiet sinalizou ocupado: %v", seen)
	}
}

// The other half of the same rule: a click still has to be acknowledged.
func TestSubmitStillRaisesTheBusySignal(t *testing.T) {
	r := newRunner(func(f func()) { f() })
	var seen []bool
	r.setBusyHandler(func(busy bool) { seen = append(seen, busy) })
	r.start()

	done := make(chan struct{})
	r.submit(func() func() { return func() { close(done) } })
	<-done
	r.stop()

	if len(seen) < 2 || !seen[0] || seen[len(seen)-1] {
		t.Errorf("submit deveria sinalizar ocupado e depois livre, veio %v", seen)
	}
}
