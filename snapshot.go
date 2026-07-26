package sundial

import (
	"fmt"
	"time"
)

// Snapshot is a serializable, point-in-time copy of a [Timer]'s mutable state:
// its start, its pause intervals (including an open one), and its write-once
// firing and breach markers. It is a plain exported struct with stable JSON
// tags, so a caller can persist it with encoding/json (or anything else) on
// shutdown and hand it to [Restore] on boot to resume exactly — with escalation
// still idempotent, because the recorded firing instants survive.
//
// A Snapshot carries no [Config]: the schedule, budget and levels are supplied
// afresh to [Restore]. Persist the Config's [Schedule] separately if you need
// it — it is itself JSON-serializable (its Loc marshals as an IANA zone name).
type Snapshot struct {
	// Start is the timer's start instant.
	Start time.Time `json:"start"`
	// Clock is the high-water mark of observed pause/resume instants.
	Clock time.Time `json:"clock"`
	// Pauses are the pause intervals in chronological order; the last may be
	// open (still freezing the clock).
	Pauses []PauseSpan `json:"pauses,omitempty"`
	// Fired are the write-once level firing markers, by level index.
	Fired []LevelFiring `json:"fired,omitempty"`
	// Breached records whether the timer has been observed to breach, and when.
	Breached   bool      `json:"breached,omitempty"`
	BreachedAt time.Time `json:"breachedAt,omitempty"`
}

// PauseSpan is one pause interval inside a [Snapshot]. When Open is true the
// pause has no resume yet and End is unused.
type PauseSpan struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitempty"`
	Open  bool      `json:"open,omitempty"`
}

// LevelFiring is one write-once escalation marker inside a [Snapshot]: the index
// of the fired level in the [Config]'s Levels, and the instant it fired.
type LevelFiring struct {
	Index int       `json:"index"`
	At    time.Time `json:"at"`
}

// Snapshot returns a consistent point-in-time copy of the timer's state, taken
// under the lock. Every slice is freshly allocated, so the caller may marshal or
// mutate the result while the timer keeps running.
func (t *Timer) Snapshot() Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	s := Snapshot{
		Start:      t.start,
		Clock:      t.clock,
		Breached:   t.breached,
		BreachedAt: t.breachedAt,
	}
	if len(t.pauses) > 0 {
		s.Pauses = make([]PauseSpan, len(t.pauses))
		for i, p := range t.pauses {
			s.Pauses[i] = PauseSpan{Start: p.start, End: p.end, Open: p.open}
		}
	}
	if len(t.fired) > 0 {
		s.Fired = make([]LevelFiring, 0, len(t.fired))
		// Emit in ascending level order so equal state serializes identically.
		for i := range t.cfg.Levels {
			if at, ok := t.fired[i]; ok {
				s.Fired = append(s.Fired, LevelFiring{Index: i, At: at})
			}
		}
	}
	return s
}

// Restore rebuilds a running [Timer] from a fresh cfg and a Snapshot, resuming
// the exact state the snapshot captured — including an open pause (still frozen)
// and every write-once firing and breach marker, so escalation stays idempotent
// across a restart. cfg is validated as in [Start]; the snapshot is trusted
// except for bounds that would corrupt state, which yield [ErrBadSnapshot].
//
// The Config is supplied afresh because a Snapshot carries none. Restoring with
// a different Config resumes the persisted markers under the new schedule,
// budget or levels — that is the caller's choice.
func Restore(cfg Config, s Snapshot) (*Timer, error) {
	t, err := Start(cfg, s.Start)
	if err != nil {
		return nil, err
	}
	t.clock = s.Clock
	if t.clock.Before(t.start) {
		t.clock = t.start
	}

	for i, ps := range s.Pauses {
		p := pause{start: ps.Start, open: ps.Open}
		if !ps.Open {
			p.end = ps.End
		}
		if i > 0 && !s.Pauses[i-1].Open && p.start.Before(s.Pauses[i-1].End) {
			return nil, fmt.Errorf("%w: pause %d starts before the previous resume", ErrBadSnapshot, i)
		}
		t.pauses = append(t.pauses, p)
	}

	for _, lf := range s.Fired {
		if lf.Index < 0 || lf.Index >= len(cfg.Levels) {
			return nil, fmt.Errorf("%w: fired level index %d out of range", ErrBadSnapshot, lf.Index)
		}
		t.fired[lf.Index] = lf.At
	}

	t.breached = s.Breached
	t.breachedAt = s.BreachedAt
	return t, nil
}
