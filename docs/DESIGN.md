# sundial — design

A pure-Go **SLA clock**: elapsed *working* time over a business schedule, with
pause/resume, breach detection, and escalation levels. Zero dependencies.

A sundial only advances in daylight. An SLA clock only advances during business
hours — nights, weekends and holidays don't count against a "resolve within 8
working hours" target. sundial is that clock, and only that clock.

Status: implemented (Phase 1). See the "Phase 1 — as built" note at the end of
this document for the decisions and corrections the build resolved.

## Why this exists

Helpdesk and ticketing suites (Zendesk, Jira Service Management) gate SLA
tooling into paid tiers around $50/agent/month. The genuinely hard part is not
the product — it's the *clock*: measuring elapsed time that runs only inside a
weekly business schedule, pauses while a ticket waits on the customer, resumes
correctly, is DST- and holiday-safe, and fires escalations exactly once.

`rickar/cal` (maintained) has holiday *data* for many countries — reimplementing
that is a data moat sundial will not fight. But sundial does not need to depend
on it: the working schedule is sundial's own concrete `Schedule` (weekly windows
+ timezone + a caller-supplied holiday set), and a caller who wants
country holidays maps `rickar/cal`'s dates into that set. That keeps sundial
zero-dependency and keeps the holiday data where it belongs — with the caller.

No maintained Go library computes an SLA clock (business-hours elapsed +
pause/resume + breach + escalation) as an embeddable, storage-free core.

## Scope

**In scope — the whole library:**
- A **`Schedule`**: per-weekday working windows, a timezone, and a set of
  holiday dates. Answers "how much working time lies between two instants".
- Exact **working-time integration** across partial days, midnight, DST
  transitions, weekends and holidays.
- An SLA **`Timer`**: a working-duration budget from a start instant → the
  wall-clock instant it is due, remaining working time, and whether it has
  breached.
- **Pause / resume**: intervals during which the clock is stopped (a ticket
  waiting on the customer) are excluded, even across business hours.
- **Escalation levels**: ordered working-duration thresholds (warn at 50%,
  breach at 100%, and beyond) with **write-once, idempotent** firing — asking
  "which levels have fired as of now" never double-fires and never rewrites a
  level's fired-at instant.
- Serializable timer state, so a restart resumes exactly (write-once
  `breachedAt` and escalation markers survive).

**Explicitly NOT in scope — the exclusions keep it a pure clock:**
- **Holiday data.** sundial holds the holiday *set* the caller gives it; it
  ships no country calendars (that is `rickar/cal`'s job, wired in an example).
- **Tickets, queues, assignment, notification.** sundial tells you *when* an SLA
  is due and *which* escalations have fired; acting on that is the caller's.
- **A running process / timers / goroutines.** Like a good clock, sundial is a
  computation: the caller supplies `now`. No background goroutine polls; nothing
  fires on its own. This makes it deterministic and testable.

## The model

### Schedule — the business calendar
```go
type Schedule struct {
    Loc      *time.Location          // the zone the windows are expressed in
    Week     [7][]Window             // working windows per weekday (0=Sunday)
    Holidays map[civilDate]struct{}  // full-day closures, in Loc
}
type Window struct{ Open, Close DayTime }  // e.g. 09:00–17:00, within a day

// Working reports the working duration in [from, to). The core primitive;
// everything else is built on it.
func (s Schedule) Working(from, to time.Time) time.Duration

// Add returns the instant reached by advancing d working-time from start —
// the inverse of Working, and how a Timer computes its due instant.
func (s Schedule) Add(start time.Time, d time.Duration) time.Time
```

The two operations are inverses and are the fiddly heart of the library:
- `Working(from,to)` sums the intersection of `[from,to)` with each day's
  working windows, skipping weekends and holidays, correct across DST (a
  window is defined in civil time in `Loc`, so 09:00–17:00 is 8 hours whether
  or not the clocks changed that day).
- `Add(start, d)` walks working windows forward, consuming `d`, and returns the
  wall-clock instant where the budget runs out. `Working(start, Add(start,d)) ==
  d` for any `d >= 0` (a round-trip property under test).

### Timer — the SLA clock
```go
type Timer struct{ /* start, budget, pauses, escalations, fired markers */ }

type Config struct {
    Schedule Schedule
    Budget   time.Duration   // working time allowed (the SLA target)
    Levels   []Level         // optional escalation thresholds, ascending
}
type Level struct { Name string; At time.Duration }  // working time from start

func Start(cfg Config, start time.Time) (*Timer, error)

func (t *Timer) DueAt() time.Time                 // wall-clock instant the budget is exhausted
func (t *Timer) Remaining(now time.Time) time.Duration  // working time left (0 if breached)
func (t *Timer) Breached(now time.Time) bool
func (t *Timer) Pause(at time.Time) error         // stop the clock (waiting on customer)
func (t *Timer) Resume(at time.Time) error        // restart it
func (t *Timer) Fired(now time.Time) []Firing     // levels whose threshold has passed, each once
```

- **DueAt** = `Schedule.Add(start, Budget)`, shifted by any completed pauses.
  Pausing pushes DueAt later by the working time the pause consumed.
- **Pause/Resume**: a pause interval `[p, r)` is excluded from elapsed working
  time. Elapsed at `now` = `Working(start, now)` minus the working time inside
  all pause intervals up to `now`. An open pause (no resume yet) freezes the
  clock at the pause instant.
- **Fired** is idempotent and write-once: once a level's threshold is reached,
  its firing (name + the instant it fired) is recorded and never recomputed, so
  a caller polling `Fired` repeatedly gets a stable, append-only history — the
  property that lets it drive real escalations without double-paging.

### Persistence
`Snapshot()` / `Restore(cfg, snapshot)` serialize the timer's start, pause
intervals, and fired markers (digestr's pattern). `breachedAt` and escalation
firings survive a restart, so escalation stays idempotent across process death.

## Golden test vectors (become the suite)

Schedule: Mon–Fri 09:00–17:00 (8h/day), America/New_York, no holidays unless
stated.

1. **Basic span.** `Working(Mon 09:00, Mon 12:00)` = 3h.
2. **Overnight skip.** `Working(Mon 16:00, Tue 10:00)` = 1h (Mon) + 1h (Tue) =
   2h — the 16 wall-clock hours between count as 2 working hours.
3. **Weekend skip.** `Working(Fri 16:00, Mon 10:00)` = 1h + 1h = 2h.
4. **Holiday skip.** With Mon a holiday, `Working(Fri 16:00, Tue 10:00)` = 1h +
   1h = 2h (Fri tail + Tue head; Sat/Sun/Mon excluded).
5. **Add round-trip.** `Add(Fri 16:00, 8h)` = Tue 16:00 (Fri 16–17 = 1h, Mon 9–17
   = 8h... = 9h > 8h, so Mon 16:00) — the builder pins the exact instant;
   `Working(Fri 16:00, that) == 8h`.
6. **DST-safe.** A window spanning the US spring-forward day is still 8 working
   hours (defined in civil time), and a budget crossing it lands on the right
   wall-clock instant (which is one real hour shorter).
7. **Pause.** 8h budget started Mon 09:00; pause Mon 11:00–14:00 (3h elapsed
   wall-clock, but the working time inside is what's excluded); DueAt shifts by
   the paused working time.
8. **Escalation once.** Levels warn@4h, breach@8h. `Fired` at 5 working hours →
   [warn]; at 9 → [warn, breach]; polling again never re-adds warn, and warn's
   fired-at instant is stable.
9. **Snapshot round-trip.** Start, pause, fire warn, snapshot → JSON → restore →
   identical DueAt/Fired/Breached thereafter; the warn firing is not re-emitted.

## Open questions

1. `Window` overlap within a day, or windows out of order — reject at
   construction (a validated `Schedule`) so `Working` never sees a malformed
   week.
2. A budget of 0, or `Add` of 0 → the start instant advanced to the next
   working instant, or start itself? Decide and pin.
3. Pause opened but never resumed, then `Snapshot` — the open pause must survive
   and keep freezing the clock after restore.
4. `now` before `start`, or a resume before its pause — reject or clamp;
   document, following digestr's forward-clamp instinct where it fits.

## Phase 1 — as built

Implemented as a zero-dependency library — `Schedule`, `Window`, `DayTime`,
`Date`; `Schedule.Working` and `Schedule.Add`; `Timer`, `Config`, `Level`,
`Firing`; `Start`, `DueAt`, `Remaining`, `Breached`, `BreachedAt`, `Elapsed`,
`Pause`, `Resume`, `Paused`, `Fired`; `Snapshot`/`Restore` — with **100 %
statement coverage** and stdlib-only tests. Where the sketch above left a choice
open, or was wrong, the build resolved it as follows.

### Correction 1 — golden vector 5's headline instant was wrong

The sketch's vector 5 reads "`Add(Fri 16:00, 8h)` = Tue 16:00" but then computes
the arithmetic to "Mon 16:00" in the same line. The arithmetic is right and the
headline is a typo: Fri 16–17 is 1h, then the next Monday 09–16 is 7h, totalling
8h at **Monday 16:00**. The suite pins Monday 16:00 (golden 5) and the DST
variant lands on the equivalent civil instant (golden 6).

### Correction 2 — the working metric is real elapsed time inside windows

`Working(from,to)` sums **real elapsed time** in the schedule's zone inside each
day's windows, and `Add` is its exact inverse using the same instants. For the
normal case this *is* the civil window length — a 09:00–17:00 window is eight
hours whether or not the clocks changed that day, because the DST transition
(≈02:00) falls outside the window, so the changed hour is simply never counted.
A window that itself straddles a transition contributes its real elapsed time
(civil length ∓ one hour); the sketch's "a window is its civil-hours length" is
descriptive of the common case, and choosing real-elapsed on **both** sides is
what makes `Working(start, Add(start,d)) == d` hold to the nanosecond across DST
(verified by a property/fuzz test over a real `America/New_York` and 4000+
random samples straddling spring-forward). A budget crossing spring-forward
lands on the correct wall-clock instant, one real hour shorter than naive
arithmetic (golden 6: Fri 16:00 → Mon 16:00 is 71 real hours, still 8 working).

### Correction 3 — the `Add` tie-boundary rule lands on Close

When a budget is exhausted exactly at a window's `Close`, `Add` returns that
`Close` — the earliest instant the budget runs out — **not** the next window's
`Open`. Both satisfy the round-trip (the non-working gap between Close and the
next Open carries zero working time), so the choice is free; Add takes the
tighter, earlier instant. Tested directly (`Add(Mon 09:00, 8h) == Mon 17:00`)
and swept by the property test.

### Correction 4 — budget 0 and `Add` of 0 return the start unchanged

`Add(start, d)` for `d <= 0` returns `start` itself (open question 2). A
zero-budget `Timer` therefore has `DueAt == start` and is breached from the
start (`elapsed >= 0` always). This satisfies the round-trip trivially
(`Working(start, start) == 0`) and needs no "advance to the next working
instant" special case.

### Correction 5 — an open pause freezes elapsed but does not shift DueAt

Elapsed at `now` is `Working(start, now)` minus the working time inside every
pause up to `now`, with an open pause clamped at `now` — so an open pause
**freezes** elapsed (and hence `Remaining`/`Breached`) at the pause instant.
`DueAt` and every firing/breach instant are *projections* and have no `now`
argument, so they shift by **completed (closed) pauses only**: an open pause's
future is unknowable, and folding it into the projection would make `DueAt`
recede without bound. The consequence is a clean, intuitive rule — **`DueAt`
shifts later exactly when a pause is resumed (closes)**, by that pause's working
time. A pause spanning only nights or a weekend closes with zero working time
inside, so it shifts nothing (golden 7, both halves). The open pause still
survives `Snapshot`/`Restore` and keeps freezing after restore.

### Correction 6 — firing and breach instants are write-once crossing instants

A level's `Firing.At` is the wall-clock instant elapsed working time first
reaches the level threshold — computed as `Add(start, threshold)` pushed later
by completed pauses, via a monotone fixpoint that converges in one step per
pause. The **set** of fired levels as of `now` is a pure function of
`elapsed(now)` (levels ascend, so it is a prefix); each firing **instant** is
cached on first observation and never rewritten, and `Breached`/`BreachedAt`
follow the same shape. So polling `Fired` repeatedly yields a stable,
append-only history (golden 8), and the markers survive a snapshot round-trip so
escalation stays idempotent across a restart (golden 9).

### Smaller decisions

- **Forward-clamp of pause/resume instants.** A `Timer` tracks a high-water mark
  of observed pause/resume instants; `Pause`/`Resume` clamp an out-of-order
  instant forward to it (open question 4). Pauses therefore never overlap or run
  backward *by construction*, so "pauses must not overlap" is *prevented*, not
  errored. The two genuine protocol violations — `Resume` with no open pause,
  `Pause` while already paused — return `ErrNotPaused` / `ErrAlreadyPaused` and
  never panic. `now` before `start` yields zero elapsed (a `Working` with
  `from >= to` is zero).
- **Validation sentinels** are `ErrBadSchedule`, `ErrBadWindow`, `ErrBadLevels`,
  `ErrBadBudget` (plus `ErrAlreadyPaused`, `ErrNotPaused`, `ErrBadSnapshot`), all
  `errors.Is`-matchable. Windows within a day must be strictly ascending and
  non-overlapping (touching is allowed); levels must be **strictly** ascending
  with non-negative thresholds; `Budget` must be non-negative.
- **An empty schedule with a positive budget or level is rejected at
  construction** (`ErrBadSchedule`), because `Add` could never satisfy it. The
  standalone `Schedule.Add` guards the same case by returning `start` rather than
  looping.
- **`Close` may be `24:00:00`** (end of day); a window does not cross midnight
  (express a night shift as two windows on adjacent weekdays).
- **A `Schedule` is JSON-serializable**: `Loc` marshals as its IANA zone name
  (resolved with `time.LoadLocation` on the way back), `DayTime` as `HH:MM:SS`,
  `Date` as `YYYY-MM-DD`, holidays as a sorted list. A `Snapshot` carries no
  `Config`; the schedule/budget/levels are supplied afresh to `Restore`.
- **Concurrency.** A `*Timer` is safe for concurrent use (an internal mutex, as
  digestr), though the core owns no goroutine or timer — the caller supplies
  `now`.

### Deferred

- **Whole-week fast-skip.** `Working` and `Add` walk civil day by day, which is
  O(days). It is DST-exact and fast for the intended spans (a working year is
  ~260 iterations); a future version could skip whole weeks that contain no DST
  transition or holiday to bound very long spans, but the risk to exactness was
  not worth taking in Phase 1.
- **A `Store`-interface variant** persisting per-change rather than at snapshot
  boundaries, mirroring digestr's deferred item.
