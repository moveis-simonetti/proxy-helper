// jobs.go has no "gui" build tag and imports no GTK/glib, on purpose: that is
// what makes the runner testable without a display. Adding the tag would
// still compile fine under `-tags gui`, but it would silently drop
// jobs_test.go from plain `go test ./...` runs — nothing would visibly
// break, the tests would simply stop running. Keep this file, and its test,
// tag-free.
package gui

import (
	"fmt"
	"os"
	"runtime/debug"
	"sync"
)

// runner serializes the GUI's blocking work. GTK3 is not thread-safe: every
// widget mutation has to happen on the main loop's thread, and anything that
// blocks has to leave it. A job therefore runs off that thread and returns a
// closure that is delivered back onto it.
//
// One job at a time, always. Apply and Clear are not safe to run
// concurrently: the profile lock protects config.json, but textblock.go does
// an unprotected read-modify-write on ~/.bashrc and the vscode/docker targets
// do the same on their JSON. Two quick clicks on "Aplicar" would corrupt the
// user's shell rc. Serializing is load-bearing, not an optimisation.
//
// There is no cancellation. A Set interrupted halfway leaves some targets
// configured and some not, and the core has no rollback; a Cancel button that
// cannot honour what it promises is worse than none.
//
// The worker goroutine must survive a panic anywhere — in the job, in the
// closure it returns, or in delivery of that closure — because a dead worker
// means stop() blocks forever and the UI stays disabled with no error and no
// way out. See run() for how that is guaranteed.
type runner struct {
	jobs    chan job
	deliver func(func())
	done    chan struct{}

	mu       sync.Mutex
	running  bool
	started  bool
	stopped  bool
	onBusy   []func(bool)
	onPanic  func(any)
	stopOnce sync.Once
}

// jobPanic carries a job's panic value and stack trace to the panic handler
// registered via setPanicHandler. The stack is captured inside run's
// deferred recover, the only place it is still available.
type jobPanic struct {
	Value any
	Stack []byte
}

// job is one unit of work plus whether it should announce itself.
//
// quiet exists for periodic background refreshes. Every job used to raise
// the busy signal, which disables the action buttons on EVERY page — right
// for a click the user made and is waiting on, wrong for a poll they did not
// ask for. Once the Daemon page started re-reading the journal every two
// seconds, that turned into every button in the window flickering on a
// two-second cycle.
type job struct {
	work  func() func()
	quiet bool
}

// newRunner builds a runner. deliver puts a closure on the UI thread; in
// production that is glib.IdleAdd, in tests it runs inline.
func newRunner(deliver func(func())) *runner {
	return &runner{
		jobs:    make(chan job, 16),
		deliver: deliver,
		done:    make(chan struct{}),
	}
}

// setBusyHandler registers the callback that enables and disables the
// controls while a job runs. It is delivered on the UI thread.
func (r *runner) setBusyHandler(fn func(bool)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// Appends, never replaces. Every page registers one of these, and a
	// single-slot field meant the last page to be set up silently won: the
	// other pages' buttons were never disabled during a job, and any state
	// they recompute from the busy callback simply stopped happening. The
	// symptom is invisible until two handlers exist, which is why this held
	// while there was only one page.
	r.onBusy = append(r.onBusy, fn)
}

// setPanicHandler registers the callback that receives a job's panic value
// (wrapped, along with its stack trace, in a jobPanic passed as the any
// argument) so a later task can route it to a real error surface instead of
// the click silently doing nothing. Delivered on the UI thread, symmetric
// with setBusyHandler. When no handler is registered the panic is still not
// lost: it goes to stderr instead.
func (r *runner) setPanicHandler(fn func(any)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onPanic = fn
}

func (r *runner) start() {
	r.mu.Lock()
	r.started = true
	r.mu.Unlock()
	go func() {
		for j := range r.jobs {
			r.run(j)
		}
		close(r.done)
	}()
}

// stop stops accepting new jobs and waits for the worker goroutine to drain
// and exit. It is safe to call more than once: the close of the jobs channel
// happens at most once (guarded by stopOnce), so a second call just waits on
// the already-closed done channel and returns immediately instead of
// panicking on a double close.
//
// It is also safe to call before start(): with no worker goroutine ranging
// over r.jobs, nothing would ever close r.done, so <-r.done would block
// forever. stop() closes r.done itself in that case. (There is no live path
// that calls stop() before start() today; this only prevents a future one
// from wedging.) That guard assumes start() is never called after stop() —
// which also has no live path — since the worker would then try to close an
// already-closed r.done and panic.
func (r *runner) stop() {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		r.stopped = true
		started := r.started
		r.mu.Unlock()
		close(r.jobs)
		if !started {
			close(r.done)
		}
	})
	<-r.done
}

// run executes one job and delivers its result. A single deferred recover
// wraps the whole body: it is what keeps a panicking job, a panicking apply
// closure, or a panicking deliver call from killing the worker goroutine
// mid-range over r.jobs. If that happened, close(r.done) would never run and
// stop() would block forever — the UI stuck busy/disabled with no error and
// no way out. setRunning(false) is folded into that same deferred call so it
// always fires, keeping every onBusy(true) matched with an onBusy(false),
// panic or not. Note the defer is registered before setRunning(true) is
// called, precisely so a panic during that first call is covered too.
//
// A recovered panic is never just dropped: debug.Stack() is captured right
// here, inside the deferred function, because that is the only place it is
// still available — return from here and the frames are gone. The value and
// stack are then handed to handlePanic, which delivers them to whatever is
// watching, or to stderr if nothing is. Silently doing nothing on a click is
// the worst failure mode a GUI can have: it is indistinguishable from the
// button not working at all, and unlike a crash it leaves no evidence to
// file a bug against.
//
// This cannot catch a panic that happens *inside* the delivered closure once
// it actually runs on the GTK main loop in production: glib.IdleAdd merely
// queues it, runs it later, and does so off this goroutine entirely. That is
// out of reach and out of scope here — deliverNow in tests runs it
// synchronously instead, which is what makes an apply-closure panic visible
// to this recover during tests.
func (r *runner) run(j job) {
	defer func() {
		if p := recover(); p != nil {
			r.handlePanic(p, debug.Stack())
		}
		if !j.quiet {
			r.setRunning(false)
		}
	}()
	if !j.quiet {
		r.setRunning(true)
	}

	apply := j.work()
	if apply != nil {
		r.deliver(apply)
	}
}

// handlePanic reports a job's panic on the UI thread via onPanic, or to
// stderr when no handler is registered, so the information is never simply
// lost. The delivery itself is wrapped in a last-resort, deliberately silent
// recover: if the panic handler (or deliver) panics in turn, there is
// nowhere further to report that to, and the priority is that this must
// never be the thing that kills the worker goroutine.
func (r *runner) handlePanic(v any, stack []byte) {
	r.mu.Lock()
	fn := r.onPanic
	r.mu.Unlock()

	if fn == nil {
		fmt.Fprintf(os.Stderr, "gui: job panicked: %v\n%s", v, stack)
		return
	}

	info := jobPanic{Value: v, Stack: stack}
	func() {
		defer func() { recover() }()
		r.deliver(func() { fn(info) })
	}()
}

// setRunning updates the busy flag and notifies onBusy on the UI thread. A
// panicking deliver or onBusy callback is routed through handlePanic rather
// than swallowed, for the same reason as in run: a broken busy indicator
// should surface, not vanish.
func (r *runner) setRunning(v bool) {
	r.mu.Lock()
	r.running = v
	// Copied under the lock: a handler registered mid-notification must not
	// race with the range below.
	fns := append([]func(bool){}, r.onBusy...)
	r.mu.Unlock()
	if len(fns) == 0 {
		return
	}
	func() {
		defer func() {
			if p := recover(); p != nil {
				r.handlePanic(p, debug.Stack())
			}
		}()
		r.deliver(func() {
			for _, fn := range fns {
				fn(v)
			}
		})
	}()
}

// submit queues a job. It is called from signal handlers on the UI thread.
// With the default queue depth of 16 it will not block in practice — jobs
// are processed one at a time and the GUI has no way to fire them anywhere
// near that fast — but it is not non-blocking in the strict sense: a full
// queue would block the caller until a slot frees up.
//
// A submit racing window close (stop() already called) is dropped silently
// rather than panicking on a closed channel — a click landing while the
// window is tearing down is a real scenario, not a hypothetical one. It
// stays fire-and-forget with no return value: every call site is a GTK
// signal handler that has nothing useful to do with a bool it will almost
// never see false, and forcing every future call site to handle a "job was
// dropped" case for a shutdown race that is already a no-op by design would
// be needless ceremony.
//
// That silence is only correct under one invariant: stop() is called at
// teardown and nothing else calls it. If something ever stops the runner
// for another reason — pausing it mid-session, say — a dropped submit would
// no longer be a harmless shutdown race, it would be a silently lost click,
// and this design would need revisiting.
func (r *runner) submit(fn func() func()) {
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}
	// stop() can close r.jobs concurrently between the check above and the
	// send below; recover turns that race into a silently dropped job
	// instead of a panic on a closed channel.
	defer func() { recover() }()
	r.jobs <- job{work: fn}
}

// post schedules f to run on the UI thread via the same deliver function
// submit's job results use (glib.IdleAdd in production, inline in tests
// that pass a synchronous deliver). This is the runner's only sanctioned
// path for "run this after the current handler returns" — see the package
// doc comment on jobs.go: this file is the sole boundary between GTK and
// the rest of the package, so nothing outside it may call glib.IdleAdd
// directly.
//
// Deferring is not a nicety for every caller: reverting a GtkSwitch from
// inside its own "state-set" handler does not stick, because
// gtk_switch_set_active reasserts the requested state right after the
// handler returns false — confirmed the hard way while building the Daemon
// page's bridge switch (page_daemon.go). r.post(...) is what buys the
// "after, not during" timing that revert needs, without requiring an
// otherwise I/O-free revert to go through a full submit() job.
func (r *runner) post(f func()) {
	r.deliver(f)
}

func (r *runner) busy() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

// submitQuiet queues work that must stay serialized with everything else but
// must NOT disable the UI: a background refresh nobody clicked for. Use
// submit for anything the user is waiting on — the busy signal is what tells
// them the click landed.
func (r *runner) submitQuiet(fn func() func()) {
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}
	defer func() { recover() }()
	r.jobs <- job{work: fn, quiet: true}
}
