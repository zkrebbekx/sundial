package sundial

import "errors"

// Sentinel errors returned when a [Schedule], [Config] or [Snapshot] is
// unusable, or a [Timer]'s pause/resume protocol is violated. Match them with
// [errors.Is] rather than by comparing error strings; several are wrapped with
// %w to add context, so string comparison would miss them.
var (
	// ErrBadSchedule is returned when a Schedule is structurally unusable: a nil
	// Loc, a malformed holiday date, or a schedule with no working time at all
	// used under a positive Budget or level threshold (which could never be
	// reached).
	ErrBadSchedule = errors.New("sundial: invalid schedule")

	// ErrBadWindow is returned when a day's working windows are malformed: an
	// out-of-range or zero-length [DayTime] pair, a Close at or before its Open,
	// or windows that overlap or are out of ascending order within a day.
	ErrBadWindow = errors.New("sundial: invalid window")

	// ErrBadLevels is returned when escalation [Level] thresholds are not
	// strictly ascending, or any threshold is negative. Levels model "warn at
	// 4h, breach at 8h": a total order of increasing working-duration marks.
	ErrBadLevels = errors.New("sundial: invalid levels")

	// ErrBadBudget is returned when a [Config]'s Budget is negative. A zero
	// budget is valid and means "due immediately"; a negative one is meaningless.
	ErrBadBudget = errors.New("sundial: Budget must not be negative")

	// ErrAlreadyPaused is returned by [Timer.Pause] when the timer is already
	// paused (its clock is already frozen); pauses do not nest.
	ErrAlreadyPaused = errors.New("sundial: timer already paused")

	// ErrNotPaused is returned by [Timer.Resume] when the timer is not currently
	// paused, so there is no open pause to close.
	ErrNotPaused = errors.New("sundial: timer not paused")

	// ErrBadSnapshot is returned by [Restore] when a Snapshot is malformed — for
	// example a fired marker whose level index is out of range for the supplied
	// Config. A Snapshot produced by [Timer.Snapshot] is always well-formed;
	// this guards against a corrupt or hand-built one.
	ErrBadSnapshot = errors.New("sundial: malformed snapshot")
)
