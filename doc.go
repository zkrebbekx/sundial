// Package sundial is a pure-Go SLA clock: it measures elapsed working time over
// a business schedule, with pause/resume, breach detection, and write-once
// escalation levels. It has zero dependencies — only the standard library.
//
// A sundial only advances in daylight; an SLA clock only advances during
// business hours. Nights, weekends and holidays do not count against a "resolve
// within 8 working hours" target. sundial is that clock, and only that clock: it
// owns no tickets, no queues, no notifications, no goroutines and no timers. The
// caller supplies now to every query, which makes the whole library
// deterministic, trivially testable, and safe to persist.
//
//	sched := sundial.Schedule{
//		Loc:  loc, // e.g. America/New_York
//		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
//	}
//	t, _ := sundial.Start(sundial.Config{Schedule: sched, Budget: 8 * time.Hour}, start)
//	due := t.DueAt()               // wall-clock instant the budget is exhausted
//	left := t.Remaining(now)       // working time still to go
//	over := t.Breached(now)        // has elapsed reached the budget?
//
// # Schedule — the business calendar
//
// A [Schedule] is a timezone, per-weekday working [Window]s, and a set of
// full-day holiday closures. Its one primitive is [Schedule.Working], which sums
// the working time in a half-open interval [from, to), skipping non-working
// days and holidays. [Schedule.Add] is its inverse: it advances a working-time
// budget from an instant and returns the wall-clock instant the budget runs
// out, so that Working(start, Add(start, d)) == d for every d the schedule can
// satisfy. When a budget lands exactly on a window's Close, Add returns that
// Close — the earliest instant the budget is exhausted — not the next Open.
//
// # DST
//
// Windows are civil-time: a 09:00–17:00 window is eight working hours whether or
// not the clocks changed that day, because a DST transition (typically 02:00)
// falls outside it. Working integrates real elapsed time inside windows in the
// schedule's zone, so a budget crossing a spring-forward day lands on the
// correct wall-clock instant — one real hour earlier than the naive arithmetic
// would suggest — while still counting as its full working hours.
//
// # Timer — the SLA clock
//
// [Start] builds a [Timer] from a [Config] (schedule, budget, levels) and a
// start instant. [Timer.DueAt] projects the due instant; [Timer.Remaining] and
// [Timer.Breached] read the state at a caller-supplied now; [Timer.Elapsed]
// reports consumed working time.
//
// # Pause and resume
//
// [Timer.Pause] stops the clock (a ticket waiting on the customer) and
// [Timer.Resume] restarts it. Working time inside a pause is excluded from
// elapsed, and a completed pause shifts [Timer.DueAt] later by the working time
// it consumed — so a pause spanning only nights or a weekend shifts nothing,
// because it contains no working time. An open pause (no resume yet) freezes
// elapsed at the pause instant. Pauses do not overlap; a resume without an open
// pause, or a pause while already paused, returns a typed error and never
// panics. Out-of-order pause/resume instants are clamped forward.
//
// # Escalation — write-once and idempotent
//
// [Config] carries ascending [Level] thresholds. [Timer.Fired] returns the
// levels whose elapsed working time has been reached as of now, each with the
// wall-clock instant it fired. Each firing instant is computed once, on first
// observation, and never rewritten, so a caller polling Fired repeatedly gets a
// stable, append-only history — the property that lets it drive real
// escalations without double-paging. Breach is the same shape: the breach
// instant is write-once (see [Timer.BreachedAt]).
//
// # Persistence
//
// [Timer.Snapshot] returns a serializable copy of the timer's start, pause
// intervals (including an open one), and firing and breach markers;
// [Restore] rebuilds a timer from a fresh [Config] and that snapshot, resuming
// exactly — escalation still idempotent across a restart. Only data persists;
// the Config is supplied afresh. A [Schedule] is itself JSON-serializable, its
// Loc written as an IANA zone name.
//
// # Not in scope
//
// No holiday data (sundial holds the set the caller gives it), no tickets,
// queues, assignment or notification, and no running process. sundial computes
// an SLA clock; everything else is the caller's. See docs/DESIGN.md, including
// the "Phase 1 — as built" note recording the decisions this implementation made
// where the design left a choice open.
package sundial
