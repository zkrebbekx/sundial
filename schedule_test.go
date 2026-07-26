package sundial

import (
	"errors"
	"math/rand"
	"testing"
	"time"
)

func TestWorkingEdges(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given the standard schedule", func(t *testing.T) {
		t.Run("When from is after to", func(t *testing.T) {
			got := s.Working(ts(loc, 0, 12, 0), ts(loc, 0, 9, 0))
			t.Run("Then Working is zero", func(t *testing.T) {
				if got != 0 {
					t.Fatalf("Working = %v, want 0", got)
				}
			})
		})
		t.Run("When from equals to", func(t *testing.T) {
			got := s.Working(ts(loc, 0, 12, 0), ts(loc, 0, 12, 0))
			t.Run("Then Working is zero", func(t *testing.T) {
				if got != 0 {
					t.Fatalf("Working = %v, want 0", got)
				}
			})
		})
		t.Run("When the interval is wholly a weekend", func(t *testing.T) {
			got := s.Working(ts(loc, 5, 0, 0), ts(loc, 6, 23, 0)) // Sat 00:00 → Sun 23:00
			t.Run("Then Working is zero", func(t *testing.T) {
				if got != 0 {
					t.Fatalf("Working = %v, want 0", got)
				}
			})
		})
		t.Run("When the interval sits entirely inside non-working hours of a workday", func(t *testing.T) {
			got := s.Working(ts(loc, 0, 18, 0), ts(loc, 0, 23, 0)) // Mon 18:00 → 23:00
			t.Run("Then Working is zero", func(t *testing.T) {
				if got != 0 {
					t.Fatalf("Working = %v, want 0", got)
				}
			})
		})
		t.Run("When the interval spans a full working week", func(t *testing.T) {
			got := s.Working(ts(loc, 0, 0, 0), ts(loc, 4, 23, 59)) // Mon 00:00 → Fri 23:59
			t.Run("Then Working is 5 × 8 = 40 hours", func(t *testing.T) {
				if got != 40*time.Hour {
					t.Fatalf("Working = %v, want 40h", got)
				}
			})
		})
		t.Run("When a day has two windows split by lunch", func(t *testing.T) {
			split := Schedule{Loc: loc, Week: Weekdays(
				Window{Open: At(9, 0, 0), Close: At(12, 0, 0)},
				Window{Open: At(13, 0, 0), Close: At(17, 0, 0)},
			)}
			t.Run("Then the lunch hour is excluded", func(t *testing.T) {
				if got := split.Working(ts(loc, 0, 9, 0), ts(loc, 0, 17, 0)); got != 7*time.Hour {
					t.Fatalf("Working = %v, want 7h", got)
				}
				if got := split.Working(ts(loc, 0, 12, 30), ts(loc, 0, 13, 30)); got != 30*time.Minute {
					t.Fatalf("Working across lunch = %v, want 30m", got)
				}
			})
		})
	})
}

func TestAddEdges(t *testing.T) {
	loc := nyLoc(t)
	s := stdSchedule(loc)
	t.Run("Given the standard schedule", func(t *testing.T) {
		t.Run("When advancing zero working time", func(t *testing.T) {
			start := ts(loc, 0, 3, 0) // non-working (03:00)
			t.Run("Then Add returns the start unchanged", func(t *testing.T) {
				if got := s.Add(start, 0); !got.Equal(start) {
					t.Fatalf("Add(_,0) = %v, want %v", got, start)
				}
			})
		})
		t.Run("When advancing a negative duration", func(t *testing.T) {
			start := ts(loc, 0, 10, 0)
			t.Run("Then Add returns the start unchanged", func(t *testing.T) {
				if got := s.Add(start, -time.Hour); !got.Equal(start) {
					t.Fatalf("Add(_,-1h) = %v, want %v", got, start)
				}
			})
		})
		t.Run("When starting in non-working time", func(t *testing.T) {
			start := ts(loc, 0, 3, 0) // Monday 03:00, before the window
			t.Run("Then the budget begins consuming at the next window open", func(t *testing.T) {
				got := s.Add(start, 2*time.Hour) // → Monday 11:00
				if !got.Equal(ts(loc, 0, 11, 0)) {
					t.Fatalf("Add = %v, want Mon 11:00", got)
				}
			})
		})
		t.Run("When the budget lands exactly on a window Close", func(t *testing.T) {
			got := s.Add(ts(loc, 0, 9, 0), 8*time.Hour) // exactly Mon 17:00
			t.Run("Then Add returns the Close, not the next Open", func(t *testing.T) {
				if !got.Equal(ts(loc, 0, 17, 0)) {
					t.Fatalf("Add = %v, want Mon 17:00 (Close)", got)
				}
			})
			t.Run("Then the round-trip is exact", func(t *testing.T) {
				if rt := s.Working(ts(loc, 0, 9, 0), got); rt != 8*time.Hour {
					t.Fatalf("round-trip = %v, want 8h", rt)
				}
			})
		})
		t.Run("When the schedule has no working time and the budget is positive", func(t *testing.T) {
			empty := Schedule{Loc: loc}
			t.Run("Then Add cannot advance and returns the start", func(t *testing.T) {
				start := ts(loc, 0, 9, 0)
				if got := empty.Add(start, time.Hour); !got.Equal(start) {
					t.Fatalf("Add on empty schedule = %v, want %v", got, start)
				}
			})
		})
	})
}

func TestAddWorkingRoundTripProperty(t *testing.T) {
	loc := nyLoc(t)
	// Include the lunch split and a couple of holidays so the property covers
	// partial windows, multi-window days, and skipped days.
	s := Schedule{
		Loc: loc,
		Week: Weekdays(
			Window{Open: At(9, 0, 0), Close: At(12, 0, 0)},
			Window{Open: At(13, 0, 0), Close: At(17, 30, 0)},
		),
		Holidays: Holidays(On(2025, 3, 10), On(2025, 11, 3)),
	}
	// Start on the Friday before spring-forward so many samples cross the DST
	// transition, exercising the civil-time integration.
	base := time.Date(2025, 3, 7, 8, 30, 0, 0, loc)
	t.Run("Given a lunch-split schedule with holidays across a DST transition", func(t *testing.T) {
		t.Run("When Add then Working is applied for many random starts and budgets", func(t *testing.T) {
			r := rand.New(rand.NewSource(1))
			t.Run("Then Working(start, Add(start,d)) == d to the nanosecond", func(t *testing.T) {
				for i := 0; i < 4000; i++ {
					start := base.Add(time.Duration(r.Intn(60*24*60)) * time.Minute) // up to ~60 days out
					d := time.Duration(r.Int63n(int64(200 * time.Hour)))             // up to 200 working hours
					landed := s.Add(start, d)
					if rt := s.Working(start, landed); rt != d {
						t.Fatalf("round-trip broke: start=%v d=%v landed=%v Working=%v", start, d, landed, rt)
					}
				}
			})
		})
	})
}

func TestScheduleValidation(t *testing.T) {
	loc := nyLoc(t)
	good := Window{Open: At(9, 0, 0), Close: At(17, 0, 0)}
	t.Run("Given assorted schedules", func(t *testing.T) {
		cases := []struct {
			name string
			s    Schedule
			want error // nil means valid
		}{
			{"well-formed", stdSchedule(loc), nil},
			{"end-of-day close 24:00", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(22, 0, 0), Close: At(24, 0, 0)})}, nil},
			{"empty week is valid", Schedule{Loc: loc}, nil},
			{"nil Loc", Schedule{Week: Weekdays(good)}, ErrBadSchedule},
			{"close before open", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(17, 0, 0), Close: At(9, 0, 0)})}, ErrBadWindow},
			{"close equals open", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(9, 0, 0), Close: At(9, 0, 0)})}, ErrBadWindow},
			{"open out of range hour", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(24, 0, 0), Close: At(24, 30, 0)})}, ErrBadWindow},
			{"minute out of range", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(9, 60, 0), Close: At(17, 0, 0)})}, ErrBadWindow},
			{"second out of range", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(9, 0, 60), Close: At(17, 0, 0)})}, ErrBadWindow},
			{"hour 24 with nonzero minute", Schedule{Loc: loc, Week: Weekdays(Window{Open: At(9, 0, 0), Close: At(24, 1, 0)})}, ErrBadWindow},
			{
				"overlapping windows",
				Schedule{Loc: loc, Week: Weekdays(
					Window{Open: At(9, 0, 0), Close: At(13, 0, 0)},
					Window{Open: At(12, 0, 0), Close: At(17, 0, 0)},
				)},
				ErrBadWindow,
			},
			{
				"out-of-order windows",
				Schedule{Loc: loc, Week: Weekdays(
					Window{Open: At(13, 0, 0), Close: At(17, 0, 0)},
					Window{Open: At(9, 0, 0), Close: At(12, 0, 0)},
				)},
				ErrBadWindow,
			},
			{"bad holiday date", Schedule{Loc: loc, Week: Weekdays(good), Holidays: map[Date]struct{}{{Year: 2026, Month: 13, Day: 1}: {}}}, ErrBadSchedule},
		}
		for _, tc := range cases {
			t.Run("When validating the "+tc.name+" schedule", func(t *testing.T) {
				t.Run("Then Validate reports the expected result", func(t *testing.T) {
					err := tc.s.Validate()
					if tc.want == nil {
						if err != nil {
							t.Fatalf("Validate = %v, want nil", err)
						}
						return
					}
					if !errors.Is(err, tc.want) {
						t.Fatalf("Validate = %v, want %v", err, tc.want)
					}
				})
			})
		}
	})
}

func TestDayTimeValid(t *testing.T) {
	t.Run("Given assorted times of day", func(t *testing.T) {
		cases := []struct {
			dt   DayTime
			want bool
		}{
			{At(0, 0, 0), true},
			{At(9, 30, 15), true},
			{At(24, 0, 0), true}, // end-of-day close
			{At(25, 0, 0), false},
			{At(-1, 0, 0), false},
			{At(9, 60, 0), false},
			{At(9, -1, 0), false},
			{At(9, 0, 60), false},
			{At(9, 0, -1), false},
			{At(24, 30, 0), false},
		}
		for _, tc := range cases {
			t.Run("When checking "+tc.dt.String(), func(t *testing.T) {
				t.Run("Then valid reports as expected", func(t *testing.T) {
					if got := tc.dt.valid(); got != tc.want {
						t.Fatalf("valid(%v) = %v, want %v", tc.dt, got, tc.want)
					}
				})
			})
		}
	})
}

func TestTouchingWindowsAllowed(t *testing.T) {
	loc := nyLoc(t)
	t.Run("Given a day whose two windows touch at a shared instant", func(t *testing.T) {
		s := Schedule{Loc: loc, Week: Weekdays(
			Window{Open: At(9, 0, 0), Close: At(12, 0, 0)},
			Window{Open: At(12, 0, 0), Close: At(17, 0, 0)},
		)}
		t.Run("When validated", func(t *testing.T) {
			t.Run("Then touching (non-overlapping) windows are accepted", func(t *testing.T) {
				if err := s.Validate(); err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				if got := s.Working(ts(loc, 0, 9, 0), ts(loc, 0, 17, 0)); got != 8*time.Hour {
					t.Fatalf("Working = %v, want 8h", got)
				}
			})
		})
	})
}

func TestDayTimeAndDateHelpers(t *testing.T) {
	t.Run("Given the small value types", func(t *testing.T) {
		t.Run("When formatting", func(t *testing.T) {
			t.Run("Then DayTime and Date render canonically", func(t *testing.T) {
				if got := At(9, 5, 30).String(); got != "09:05:30" {
					t.Fatalf("DayTime.String = %q, want 09:05:30", got)
				}
				if got := On(2026, time.December, 25).String(); got != "2026-12-25" {
					t.Fatalf("Date.String = %q, want 2026-12-25", got)
				}
			})
		})
		t.Run("When comparing dates", func(t *testing.T) {
			t.Run("Then before orders by year, then month, then day", func(t *testing.T) {
				if !On(2026, 1, 1).before(On(2026, 1, 2)) {
					t.Fatal("same month day order wrong")
				}
				if !On(2026, 1, 31).before(On(2026, 2, 1)) {
					t.Fatal("month rollover order wrong")
				}
				if !On(2025, 12, 31).before(On(2026, 1, 1)) {
					t.Fatal("year rollover order wrong")
				}
				if On(2026, 1, 2).before(On(2026, 1, 2)) {
					t.Fatal("equal dates should not be before")
				}
			})
		})
	})
}
