package sundial

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// DayTime, Date and Schedule marshal to stable, human-readable JSON so a caller
// can persist a whole business calendar. A DayTime is an "HH:MM:SS" string, a
// Date a "YYYY-MM-DD" string, and a Schedule's Loc its IANA zone name, resolved
// with [time.LoadLocation] on the way back in.

// MarshalJSON encodes the DayTime as an "HH:MM:SS" string.
func (dt DayTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(dt.String())
}

// UnmarshalJSON decodes an "HH:MM:SS" (or "HH:MM") string into a DayTime.
func (dt *DayTime) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	var h, m, sec int
	switch n, _ := fmt.Sscanf(s, "%d:%d:%d", &h, &m, &sec); n {
	case 3, 2:
		// %d:%d:%d fills what it can; a missing seconds field leaves sec at 0.
	default:
		return fmt.Errorf("sundial: invalid DayTime %q", s)
	}
	*dt = DayTime{Hour: h, Min: m, Sec: sec}
	return nil
}

// MarshalJSON encodes the Date as a "YYYY-MM-DD" string.
func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.String())
}

// UnmarshalJSON decodes a "YYYY-MM-DD" string into a Date.
func (d *Date) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	var y, m, day int
	if n, err := fmt.Sscanf(s, "%d-%d-%d", &y, &m, &day); n != 3 || err != nil {
		return fmt.Errorf("sundial: invalid Date %q", s)
	}
	*d = Date{Year: y, Month: time.Month(m), Day: day}
	return nil
}

// scheduleJSON is the wire form of a [Schedule]: the Loc as an IANA zone name,
// the week of windows, and the holidays as a sorted list of dates.
type scheduleJSON struct {
	Loc      string      `json:"loc"`
	Week     [7][]Window `json:"week"`
	Holidays []Date      `json:"holidays,omitempty"`
}

// MarshalJSON encodes the Schedule, writing Loc as its IANA zone name and the
// holidays as a date list sorted for a byte-stable result.
func (s Schedule) MarshalJSON() ([]byte, error) {
	out := scheduleJSON{Week: s.Week}
	if s.Loc != nil {
		out.Loc = s.Loc.String()
	}
	if len(s.Holidays) > 0 {
		out.Holidays = make([]Date, 0, len(s.Holidays))
		for d := range s.Holidays {
			out.Holidays = append(out.Holidays, d)
		}
		sort.Slice(out.Holidays, func(i, j int) bool { return out.Holidays[i].before(out.Holidays[j]) })
	}
	return json.Marshal(out)
}

// UnmarshalJSON decodes a Schedule, resolving Loc with [time.LoadLocation]. An
// empty Loc name decodes to a nil Loc, which [Schedule.Validate] then rejects.
func (s *Schedule) UnmarshalJSON(b []byte) error {
	var in scheduleJSON
	if err := json.Unmarshal(b, &in); err != nil {
		return err
	}
	var loc *time.Location
	if in.Loc != "" {
		l, err := time.LoadLocation(in.Loc)
		if err != nil {
			return fmt.Errorf("%w: load zone %q: %v", ErrBadSchedule, in.Loc, err)
		}
		loc = l
	}
	s.Loc = loc
	s.Week = in.Week
	if len(in.Holidays) > 0 {
		s.Holidays = Holidays(in.Holidays...)
	} else {
		s.Holidays = nil
	}
	return nil
}
