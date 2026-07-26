package sundial

import (
	"fmt"
	"time"
)

// DayTime is a civil time-of-day — hours, minutes and seconds since midnight —
// within a single day, read in a [Schedule]'s Loc. It carries no date and no
// zone: the same DayTime names 09:00 whether or not the clocks changed that
// day, which is what makes a 09:00–17:00 window exactly eight working hours
// regardless of a DST transition outside it.
//
// An Open bound must lie in [00:00:00, 24:00:00); a Close bound in
// (00:00:00, 24:00:00]. A Close of 24:00:00 denotes the end of the day
// (equivalently, the following midnight). A window does not cross midnight;
// express a night shift as two windows on adjacent weekdays.
type DayTime struct {
	Hour int
	Min  int
	Sec  int
}

// At is a convenience constructor for a [DayTime]: At(9, 0, 0) is 09:00:00.
func At(hour, minute, second int) DayTime {
	return DayTime{Hour: hour, Min: minute, Sec: second}
}

// since returns the duration from midnight to the DayTime.
func (dt DayTime) since() time.Duration {
	return time.Duration(dt.Hour)*time.Hour +
		time.Duration(dt.Min)*time.Minute +
		time.Duration(dt.Sec)*time.Second
}

// valid reports whether the DayTime is a well-formed civil time-of-day in
// [00:00:00, 24:00:00]. 24:00:00 is allowed (an end-of-day Close); any hour of
// 24 must have zero minutes and seconds.
func (dt DayTime) valid() bool {
	if dt.Min < 0 || dt.Min > 59 || dt.Sec < 0 || dt.Sec > 59 {
		return false
	}
	if dt.Hour < 0 || dt.Hour > 24 {
		return false
	}
	if dt.Hour == 24 && (dt.Min != 0 || dt.Sec != 0) {
		return false
	}
	return true
}

// String renders the DayTime as HH:MM:SS.
func (dt DayTime) String() string {
	return fmt.Sprintf("%02d:%02d:%02d", dt.Hour, dt.Min, dt.Sec)
}

// Window is a single contiguous stretch of working time within a day, expressed
// as civil [DayTime] bounds in the [Schedule]'s Loc. Occupancy is half-open:
// [Open, Close). A day may hold several non-overlapping windows in ascending
// order (for example a lunch break splits 09:00–12:00 and 13:00–17:00).
type Window struct {
	Open  DayTime
	Close DayTime
}

// valid reports whether the window's bounds are well-formed and Open is
// strictly before Close, with Open before end-of-day.
func (w Window) valid() bool {
	return w.Open.valid() && w.Close.valid() &&
		w.Open.since() < w.Close.since() && w.Open.since() < 24*time.Hour
}

// Date is a civil calendar date — year, month, day — with no time and no zone.
// It names a whole day in a [Schedule]'s Loc, used for the holiday set.
type Date struct {
	Year  int
	Month time.Month
	Day   int
}

// On is a convenience constructor for a [Date].
func On(year int, month time.Month, day int) Date {
	return Date{Year: year, Month: month, Day: day}
}

// valid reports whether the Date names a plausible calendar day.
func (d Date) valid() bool {
	return d.Month >= time.January && d.Month <= time.December && d.Day >= 1 && d.Day <= 31
}

// before reports whether d falls strictly before o in calendar order.
func (d Date) before(o Date) bool {
	if d.Year != o.Year {
		return d.Year < o.Year
	}
	if d.Month != o.Month {
		return d.Month < o.Month
	}
	return d.Day < o.Day
}

// String renders the Date as YYYY-MM-DD.
func (d Date) String() string {
	return fmt.Sprintf("%04d-%02d-%02d", d.Year, int(d.Month), d.Day)
}

// Holidays builds a holiday set from a list of dates, for use as a
// [Schedule]'s Holidays field.
func Holidays(dates ...Date) map[Date]struct{} {
	set := make(map[Date]struct{}, len(dates))
	for _, d := range dates {
		set[d] = struct{}{}
	}
	return set
}

// Weekdays builds a [Schedule.Week] with the given windows on Monday through
// Friday and no working time on the weekend — the common business-hours shape.
// The windows are shared read-only across the five days.
func Weekdays(windows ...Window) [7][]Window {
	var wk [7][]Window
	for wd := time.Monday; wd <= time.Friday; wd++ {
		wk[wd] = windows
	}
	return wk
}

// Schedule is a business calendar: a timezone, the working windows for each
// weekday, and a set of full-day holiday closures. It answers the one primitive
// question everything else is built on — how much working time lies between two
// instants — via [Schedule.Working], and its inverse via [Schedule.Add].
//
// A Schedule is a plain value: copy it freely. Its fields may be built directly;
// call [Schedule.Validate] (or construct a [Timer], which validates for you)
// before relying on it, so [Schedule.Working] never sees a malformed week.
type Schedule struct {
	// Loc is the zone the windows and holidays are expressed in. Required.
	// Working time is integrated in this zone's civil time, so windows keep
	// their civil length across DST transitions that fall outside them.
	Loc *time.Location

	// Week holds the working windows for each weekday, indexed by
	// [time.Weekday] (0 = Sunday … 6 = Saturday). An empty day is non-working —
	// that is how weekends are expressed. Within a day, windows must be in
	// ascending, non-overlapping order.
	Week [7][]Window

	// Holidays is the set of full-day closures, keyed by civil [Date] in Loc. A
	// date present here contributes zero working time regardless of its weekday.
	Holidays map[Date]struct{}
}

// Validate reports the first structural problem with the Schedule, or nil.
// A validated Schedule is safe for [Schedule.Working] and [Schedule.Add].
func (s Schedule) Validate() error {
	if s.Loc == nil {
		return fmt.Errorf("%w: nil Loc", ErrBadSchedule)
	}
	for wd := range s.Week {
		prevClose := time.Duration(-1)
		for _, w := range s.Week[wd] {
			if !w.valid() {
				return fmt.Errorf("%w: weekday %d window %s-%s", ErrBadWindow, wd, w.Open, w.Close)
			}
			if w.Open.since() < prevClose {
				return fmt.Errorf("%w: weekday %d windows overlap or are out of order", ErrBadWindow, wd)
			}
			prevClose = w.Close.since()
		}
	}
	for d := range s.Holidays {
		if !d.valid() {
			return fmt.Errorf("%w: holiday %s", ErrBadSchedule, d)
		}
	}
	return nil
}

// weeklyWorking is the total civil working time in one week. It is used only as
// a positivity check: a schedule with zero weekly working time can never
// advance a positive budget, so [Schedule.Add] refuses to loop on it.
func (s Schedule) weeklyWorking() time.Duration {
	var sum time.Duration
	for wd := range s.Week {
		for _, w := range s.Week[wd] {
			sum += w.Close.since() - w.Open.since()
		}
	}
	return sum
}

// civil returns the calendar [Date] that instant t falls on, in the Schedule's
// Loc.
func (s Schedule) civil(t time.Time) Date {
	y, m, d := t.In(s.Loc).Date()
	return Date{Year: y, Month: m, Day: d}
}

// next returns the calendar date after d, rolling over months and years, in the
// Schedule's Loc. Advancing by a civil day (rather than 24h) keeps date walks
// immune to DST.
func (s Schedule) next(d Date) Date {
	y, m, dd := time.Date(d.Year, d.Month, d.Day+1, 0, 0, 0, 0, s.Loc).Date()
	return Date{Year: y, Month: m, Day: dd}
}

// weekday returns the [time.Weekday] of civil date d in the Schedule's Loc.
func (s Schedule) weekday(d Date) time.Weekday {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, s.Loc).Weekday()
}

// at resolves a civil (date, time-of-day) pair to a real instant in the
// Schedule's Loc. [time.Date] applies the zone's DST rules, so a Close of
// 24:00:00 resolves to the following midnight and a window that does not span a
// transition keeps its civil length.
func (s Schedule) at(d Date, dt DayTime) time.Time {
	return time.Date(d.Year, d.Month, d.Day, dt.Hour, dt.Min, dt.Sec, 0, s.Loc)
}

// isHoliday reports whether civil date d is a full-day closure.
func (s Schedule) isHoliday(d Date) bool {
	_, ok := s.Holidays[d]
	return ok
}

// Working reports the working duration contained in the half-open interval
// [from, to): the sum, over every civil day the interval touches, of the
// overlap of [from, to) with that day's working windows, skipping holidays and
// non-working days.
//
// The result is real elapsed time inside working windows. For the common case,
// where no DST transition falls inside a window, that equals the windows' civil
// length — a 09:00–17:00 window is eight hours whether or not the clocks changed
// that day, because the changed hour lies outside it. A window that itself
// straddles a transition contributes its real elapsed time (its civil length
// minus, or plus, the one-hour offset change), which is exactly what keeps
// Working and [Schedule.Add] nanosecond-exact inverses.
//
// If from is not before to, the result is zero; a wholly non-working interval
// (a weekend, a holiday, an overnight gap) is likewise zero. Working assumes a
// validated Schedule (see [Schedule.Validate]).
func (s Schedule) Working(from, to time.Time) time.Duration {
	if !from.Before(to) {
		return 0
	}
	var total time.Duration
	last := s.civil(to)
	for d := s.civil(from); !last.before(d); d = s.next(d) {
		if s.isHoliday(d) {
			continue
		}
		for _, w := range s.Week[s.weekday(d)] {
			lo := s.at(d, w.Open)
			hi := s.at(d, w.Close)
			if from.After(lo) {
				lo = from
			}
			if to.Before(hi) {
				hi = to
			}
			if lo.Before(hi) {
				total += hi.Sub(lo)
			}
		}
	}
	return total
}

// Add returns the wall-clock instant reached by advancing d working-time from
// start — the inverse of [Schedule.Working]. It walks working windows forward
// from start, consuming d, and returns the instant the budget is exhausted:
//
//	Working(start, Add(start, d)) == d   for every d >= 0 the schedule can satisfy.
//
// Boundary rule: when the budget is exhausted exactly at a window's Close, Add
// returns that Close (the first instant at which the budget runs out), not the
// next window's Open. Both satisfy the round-trip, since the non-working gap
// between them carries no working time; Add picks the earlier, tighter instant.
//
// A non-positive d returns start unchanged (nothing to advance). A schedule
// with no working time at all cannot advance a positive budget and likewise
// returns start; a [Timer] built on such a schedule with a positive Budget is
// rejected at construction, so this arises only for a directly-used empty
// Schedule. Add assumes a validated Schedule.
func (s Schedule) Add(start time.Time, d time.Duration) time.Time {
	if d <= 0 {
		return start
	}
	if s.weeklyWorking() <= 0 {
		return start
	}
	remaining := d
	// weeklyWorking > 0 guarantees every seven civil days consume a positive
	// amount, so this walk always terminates.
	for day := s.civil(start); ; day = s.next(day) {
		if s.isHoliday(day) {
			continue
		}
		for _, w := range s.Week[s.weekday(day)] {
			segStart := s.at(day, w.Open)
			segClose := s.at(day, w.Close)
			if start.After(segStart) {
				segStart = start
			}
			if !segStart.Before(segClose) { // segStart >= segClose: nothing here
				continue
			}
			avail := segClose.Sub(segStart)
			if remaining <= avail {
				return segStart.Add(remaining)
			}
			remaining -= avail
		}
	}
}
