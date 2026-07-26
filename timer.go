package sundial

import (
	"fmt"
	"sync"
	"time"
)

// Level is one escalation threshold: a name and the amount of elapsed working
// time from a [Timer]'s start at which it fires. Levels within a [Config] must
// be strictly ascending by At (warn at 4h, breach at 8h, page at 12h).
type Level struct {
	// Name identifies the level for the caller (for example "warn").
	Name string
	// At is the elapsed working time from start at which the level fires.
	At time.Duration
}

// Firing records that a [Level] has fired: the level itself and the wall-clock
// instant at which the timer's elapsed working time first reached the level's
// threshold. The instant is write-once — computed on first observation and
// stable across every later poll and across a snapshot round-trip.
type Firing struct {
	// Level is the level that fired.
	Level Level
	// At is the wall-clock instant elapsed working time reached Level.At.
	At time.Time
}

// Config describes a [Timer]: the business [Schedule] its clock runs on, the
// working-time Budget that is the SLA target, and the optional escalation
// [Level]s. It is validated by [Start] and [Restore].
type Config struct {
	// Schedule is the business calendar the timer's clock advances on. Required
	// and validated.
	Schedule Schedule

	// Budget is the working time the SLA allows from start. A zero budget means
	// "due immediately"; a negative budget is rejected.
	Budget time.Duration

	// Levels are optional escalation thresholds, strictly ascending by At. A
	// common shape is a warn level below Budget and a breach level at Budget.
	Levels []Level
}

// validate reports the first configuration problem, or nil.
func (c Config) validate() error {
	if err := c.Schedule.Validate(); err != nil {
		return err
	}
	if c.Budget < 0 {
		return ErrBadBudget
	}
	prev := time.Duration(-1)
	for _, lv := range c.Levels {
		if lv.At < 0 {
			return fmt.Errorf("%w: level %q has a negative threshold", ErrBadLevels, lv.Name)
		}
		if lv.At <= prev {
			return fmt.Errorf("%w: level %q threshold %s does not strictly ascend", ErrBadLevels, lv.Name, lv.At)
		}
		prev = lv.At
	}
	// A schedule with no working time can never reach a positive threshold, so
	// Add would spin forever; refuse such a Timer at construction.
	if c.Schedule.weeklyWorking() == 0 {
		if c.Budget > 0 {
			return fmt.Errorf("%w: schedule has no working time but Budget is positive", ErrBadSchedule)
		}
		for _, lv := range c.Levels {
			if lv.At > 0 {
				return fmt.Errorf("%w: schedule has no working time but level %q is positive", ErrBadSchedule, lv.Name)
			}
		}
	}
	return nil
}

// pause is one clock-stopped interval. While open (no resume yet) it freezes
// elapsed working time; once closed, its working time is excluded and shifts the
// due instant later.
type pause struct {
	start time.Time
	end   time.Time
	open  bool
}

// Timer is a pure, clock-driven SLA clock: elapsed working time from a start
// instant over a [Schedule], with pause/resume, breach detection, and write-once
// escalation firing. It owns no goroutines and no timers — the caller supplies
// now to every query — so it is deterministic and safe to persist.
//
// The zero value is not usable; construct one with [Start] or [Restore]. A
// *Timer is safe for concurrent use; every method takes an internal mutex.
type Timer struct {
	mu    sync.Mutex
	cfg   Config
	start time.Time

	// pauses are in chronological, non-overlapping order; at most the last is
	// open. clock is the high-water mark of observed pause/resume instants,
	// keeping that order monotonic even under an out-of-order caller.
	pauses []pause
	clock  time.Time

	// fired caches each level's write-once firing instant, keyed by level index.
	fired map[int]time.Time
	// breached and breachedAt record the write-once breach instant.
	breached   bool
	breachedAt time.Time
}

// Start builds a running [Timer] from cfg with the given start instant,
// returning a typed sentinel (see the package errors) if cfg is unusable. The
// timer's clock begins at start; a now handed to a query before start yields
// zero elapsed working time.
func Start(cfg Config, start time.Time) (*Timer, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Timer{
		cfg:   cfg,
		start: start,
		clock: start,
		fired: make(map[int]time.Time),
	}, nil
}

// Start reports the instant the timer was started.
func (t *Timer) Start() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.start
}

// advance clamps at forward to the highest pause/resume instant observed,
// keeping the pause timeline monotonic and non-overlapping even if the caller
// supplies an out-of-order instant. It must be called under the lock.
func (t *Timer) advance(at time.Time) time.Time {
	if at.Before(t.clock) {
		at = t.clock
	}
	t.clock = at
	return at
}

// isPaused reports whether the last pause is still open. Must hold the lock.
func (t *Timer) isPaused() bool {
	return len(t.pauses) > 0 && t.pauses[len(t.pauses)-1].open
}

// elapsed is the working time consumed from start as of now: the working time
// in [start, now) minus the working time inside every pause interval up to now.
// An open pause is clamped at now, so it freezes elapsed at the pause instant.
// Must hold the lock.
func (t *Timer) elapsed(now time.Time) time.Duration {
	e := t.cfg.Schedule.Working(t.start, now)
	for _, p := range t.pauses {
		end := p.end
		if p.open || now.Before(end) {
			end = now
		}
		e -= t.cfg.Schedule.Working(p.start, end)
	}
	if e < 0 {
		e = 0
	}
	return e
}

// closedPausedWorking is the working time inside all closed pauses up to upTo.
// Open pauses are excluded: a due-instant projection assumes the clock resumes,
// so only completed pauses shift it. Must hold the lock.
func (t *Timer) closedPausedWorking(upTo time.Time) time.Duration {
	var sum time.Duration
	for _, p := range t.pauses {
		if p.open {
			continue
		}
		end := p.end
		if upTo.Before(end) {
			end = upTo
		}
		sum += t.cfg.Schedule.Working(p.start, end)
	}
	return sum
}

// firingInstant is the wall-clock instant at which elapsed working time reaches
// threshold, accounting for completed pauses that push it later. It solves
// f = Add(start, threshold + closedPausedWorking(f)) by monotone iteration to a
// fixpoint: each step either settles or advances f past another pause boundary,
// so it converges within one step per pause. Must hold the lock.
func (t *Timer) firingInstant(threshold time.Duration) time.Time {
	if threshold <= 0 {
		return t.start
	}
	frontier := t.cfg.Schedule.Add(t.start, threshold)
	for i := 0; i < len(t.pauses)+2; i++ {
		pw := t.closedPausedWorking(frontier)
		next := t.cfg.Schedule.Add(t.start, threshold+pw)
		if next.Equal(frontier) {
			return frontier
		}
		frontier = next
	}
	return frontier
}

// Elapsed reports the working time consumed since start as of now, excluding
// paused intervals. It is zero for a now at or before start and never exceeds
// the working time the schedule places in the interval.
func (t *Timer) Elapsed(now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.elapsed(now)
}

// DueAt reports the wall-clock instant the Budget is exhausted: Add(start,
// Budget) shifted later by the working time of every completed pause. It takes
// no now because it is a projection of state, not a reading at an instant.
//
// While a pause is open the timer's elapsed time is frozen, so the true due
// instant recedes until the timer resumes; DueAt projects as if the clock
// resumes at the pause instant (completed pauses shift it, the open one does
// not). Resuming closes the pause and shifts DueAt later by that pause's
// working time.
func (t *Timer) DueAt() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.firingInstant(t.cfg.Budget)
}

// Remaining reports the working time left before breach as of now, clamped at
// zero once elapsed reaches the Budget.
func (t *Timer) Remaining(now time.Time) time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	if r := t.cfg.Budget - t.elapsed(now); r > 0 {
		return r
	}
	return 0
}

// Breached reports whether elapsed working time has reached the Budget as of
// now. On the first observation that it has, the write-once breach instant is
// recorded (see [Timer.BreachedAt]); later calls neither un-breach nor rewrite
// it. A zero Budget is breached from start.
func (t *Timer) Breached(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	b := t.elapsed(now) >= t.cfg.Budget
	if b && !t.breached {
		t.breached = true
		t.breachedAt = t.firingInstant(t.cfg.Budget)
	}
	return b
}

// BreachedAt reports the write-once instant the timer breached, and whether it
// has been observed to breach yet. The instant is the wall-clock time elapsed
// working time reached the Budget, stable once recorded.
func (t *Timer) BreachedAt() (time.Time, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.breachedAt, t.breached
}

// Pause stops the clock at instant at (a ticket waiting on the customer). While
// paused, elapsed working time is frozen. It returns [ErrAlreadyPaused] if the
// timer is already paused. at is clamped forward to the latest pause/resume
// instant already observed, so pauses never overlap or run backward.
func (t *Timer) Pause(at time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.isPaused() {
		return ErrAlreadyPaused
	}
	at = t.advance(at)
	t.pauses = append(t.pauses, pause{start: at, open: true})
	return nil
}

// Resume restarts the clock at instant at, closing the open pause. The working
// time inside the just-closed pause is excluded from elapsed and shifts the due
// instant later. It returns [ErrNotPaused] if the timer is not paused. at is
// clamped forward to the pause instant, so a resume never precedes its pause.
func (t *Timer) Resume(at time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.isPaused() {
		return ErrNotPaused
	}
	at = t.advance(at)
	last := &t.pauses[len(t.pauses)-1]
	last.end = at
	last.open = false
	return nil
}

// Paused reports whether the timer is currently paused (its last pause is still
// open).
func (t *Timer) Paused() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.isPaused()
}

// Fired returns the escalation levels whose threshold has been reached as of
// now, in ascending order, each paired with the write-once wall-clock instant
// it fired. The set is a pure function of elapsed working time at now; each
// firing instant is computed once, on first observation, and never rewritten —
// so polling Fired repeatedly yields a stable, append-only history that can
// drive real escalations without double-firing.
func (t *Timer) Fired(now time.Time) []Firing {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.elapsed(now)
	var out []Firing
	for i, lv := range t.cfg.Levels {
		if e < lv.At {
			continue // levels ascend, so nothing further has fired either
		}
		at, ok := t.fired[i]
		if !ok {
			at = t.firingInstant(lv.At)
			t.fired[i] = at
		}
		out = append(out, Firing{Level: lv, At: at})
	}
	return out
}
