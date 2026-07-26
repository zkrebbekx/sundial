# sundial

A pure-Go **SLA clock**: elapsed *working* time over a business schedule, with
pause/resume, breach detection, and escalation levels. Zero dependencies.

A sundial only advances in daylight. An SLA clock only advances during business
hours — nights, weekends and holidays don't count against a "resolve within 8
working hours" target. `sundial` is that clock, and only that clock. The genuinely
hard part of SLA tooling is never the product; it's the *clock*: measuring time
that runs only inside a weekly schedule, pauses while a ticket waits on the
customer, resumes correctly, is DST- and holiday-safe, and fires escalations
exactly once. `sundial` is that computation, extracted and made storage-free.

- **Pure and clock-driven.** No goroutines, no timers. The caller advances time
  by passing `now` to each query, so the library is deterministic, trivial to
  test, and safe to persist.
- **DST- and holiday-safe.** Windows are civil-time: 09:00–17:00 is eight working
  hours whether or not the clocks changed that day. A budget crossing a
  spring-forward day lands on the correct wall-clock instant.
- **Working-time as a first-class primitive.** `Working(from,to)` and its exact
  inverse `Add(start,d)` — `Working(start, Add(start,d)) == d` to the nanosecond.
- **Pause / resume.** Intervals waiting on the customer are excluded, even across
  business hours; a pause over a weekend consumes no working time and shifts
  nothing.
- **Write-once escalation.** Ascending thresholds fire once, each with a stable
  fired-at instant — poll as often as you like without double-paging.
- **Serializable state.** Snapshot the timer, persist it however you like, and
  restore to resume exactly — escalation stays idempotent across a restart.
- **Zero dependencies.** Standard library only. Go 1.23+.

```go
import "github.com/zkrebbekx/sundial"
```

## Example

```go
sched := sundial.Schedule{
    Loc:  loc, // e.g. America/New_York
    Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
}

start := time.Date(2026, 1, 9, 16, 0, 0, 0, loc) // Friday 16:00
t, _ := sundial.Start(sundial.Config{Schedule: sched, Budget: 8 * time.Hour}, start)

t.DueAt()                     // Monday 16:00 — 1h Friday + 7h Monday, weekend skipped
t.Remaining(now)              // working time still to go
t.Breached(now)               // has elapsed reached the budget?
```

## The model

### Schedule — the business calendar

A `Schedule` is a timezone, per-weekday working `Window`s, and a set of full-day
holiday closures the *caller* supplies. It answers the one primitive everything
else is built on:

```go
func (s Schedule) Working(from, to time.Time) time.Duration // working time in [from, to)
func (s Schedule) Add(start time.Time, d time.Duration) time.Time // inverse of Working
```

`Working` sums the overlap of `[from, to)` with each day's windows, skipping
weekends and holidays, in the schedule's zone. `Add` walks windows forward,
consuming `d`, and returns the instant the budget runs out. They are exact
inverses; when a budget lands exactly on a window's `Close`, `Add` returns that
`Close` (the earliest instant the budget is exhausted), not the next `Open`.

`sundial` ships **no holiday data** — a caller who wants country calendars maps
their dates into `Schedule.Holidays`, which keeps `sundial` zero-dependency and
the holiday data where it belongs. The `examples/` program shows the mapping.

### Timer — the SLA clock

```go
type Config struct {
    Schedule Schedule
    Budget   time.Duration // working time allowed (the SLA target)
    Levels   []Level       // optional escalation thresholds, ascending
}
type Level struct{ Name string; At time.Duration }

func Start(cfg Config, start time.Time) (*Timer, error)

func (t *Timer) DueAt() time.Time                      // instant the budget is exhausted
func (t *Timer) Remaining(now time.Time) time.Duration // working time left (0 if breached)
func (t *Timer) Elapsed(now time.Time) time.Duration   // working time consumed
func (t *Timer) Breached(now time.Time) bool
func (t *Timer) Pause(at time.Time) error              // stop the clock (waiting on customer)
func (t *Timer) Resume(at time.Time) error             // restart it
func (t *Timer) Fired(now time.Time) []Firing          // levels whose threshold has passed, each once
```

## Pause / resume

A pause interval is excluded from elapsed working time. Elapsed at `now` is
`Working(start, now)` minus the working time inside all pauses up to `now`. A
completed pause shifts `DueAt` later by the working time it consumed — so a pause
spanning only nights or a weekend shifts nothing, because it holds no working
time. An **open** pause (no resume yet) freezes elapsed at the pause instant.

Pauses do not overlap; a resume without an open pause, or a pause while already
paused, returns a typed error and never panics. Out-of-order pause/resume
instants are clamped forward, so the timeline never runs backward.

## Escalation — write-once and idempotent

`Fired(now)` returns the levels whose elapsed working time has been reached as of
`now`, each with the wall-clock instant it fired. Each firing instant is computed
once, on first observation, and never rewritten:

```go
cfg := sundial.Config{
    Schedule: sched, Budget: 8 * time.Hour,
    Levels: []sundial.Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}},
}
t, _ := sundial.Start(cfg, start)
for _, f := range t.Fired(now) {
    fmt.Printf("%s fired at %s\n", f.Level.Name, f.At) // stable across every poll
}
```

Polling `Fired` repeatedly yields a stable, append-only history — the property
that lets it drive real escalations without double-paging. `Breached` /
`BreachedAt` behave the same way.

## Persistence

Timer state is *yours* to persist, rather than dragging in a database:

```go
snap := t.Snapshot()            // start, pause intervals (incl. an open one), fired/breach markers
blob, _ := json.Marshal(snap)   // persist however you like
// ... restart ...
var s sundial.Snapshot
json.Unmarshal(blob, &s)
t, _ := sundial.Restore(cfg, s) // resumes exactly; Config (with Schedule) supplied fresh
```

`breachedAt` and escalation firings survive a restart, so escalation stays
idempotent across process death. A `Schedule` is itself JSON-serializable — its
`Loc` marshals as an IANA zone name and is resolved with `time.LoadLocation` on
restore.

## DST

Windows are defined in civil time in the schedule's zone, so a 09:00–17:00 window
is eight working hours whether or not the clocks changed that day (a spring-forward
transition at ≈02:00 falls outside the window). `Working` integrates real elapsed
time inside windows, so a budget crossing spring-forward lands on the correct
wall-clock instant — one real hour earlier than the naive arithmetic would give,
while still counting as its full working hours. `Working` and `Add` stay exact
inverses across the transition.

## Non-goals

- **No holiday data.** `sundial` holds the holiday *set* the caller gives it; it
  ships no country calendars.
- **No tickets, queues, assignment, or notification.** `sundial` tells you *when*
  an SLA is due and *which* escalations have fired; acting on that is the caller's.
- **No running process / timers / goroutines.** Like a good clock, `sundial` is a
  computation: the caller supplies `now`. Nothing fires on its own, which makes it
  deterministic and testable. The `examples/` program shows a ticker wrapper.

## License

MIT
