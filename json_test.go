package sundial

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestScheduleJSONRoundTrip(t *testing.T) {
	loc := nyLoc(t)
	orig := Schedule{
		Loc: loc,
		Week: Weekdays(
			Window{Open: At(9, 0, 0), Close: At(12, 0, 0)},
			Window{Open: At(13, 0, 0), Close: At(17, 30, 0)},
		),
		Holidays: Holidays(On(2026, 12, 25), On(2026, 1, 1)),
	}
	t.Run("Given a schedule with a lunch split and holidays", func(t *testing.T) {
		t.Run("When marshalled to JSON and back", func(t *testing.T) {
			raw, err := json.Marshal(orig)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			var got Schedule
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			t.Run("Then the zone is restored by IANA name", func(t *testing.T) {
				if got.Loc == nil || got.Loc.String() != "America/New_York" {
					t.Fatalf("Loc = %v, want America/New_York", got.Loc)
				}
			})
			t.Run("Then the windows and holidays are preserved and it still validates", func(t *testing.T) {
				if err := got.Validate(); err != nil {
					t.Fatalf("restored schedule invalid: %v", err)
				}
				if got.Working(ts(loc, 0, 9, 0), ts(loc, 0, 17, 30)) != orig.Working(ts(loc, 0, 9, 0), ts(loc, 0, 17, 30)) {
					t.Fatal("restored working time differs")
				}
				if _, ok := got.Holidays[On(2026, 12, 25)]; !ok {
					t.Fatal("holiday lost in round-trip")
				}
			})
			t.Run("Then the holidays serialize in sorted order for byte-stability", func(t *testing.T) {
				var wire struct {
					Holidays []string `json:"holidays"`
				}
				if err := json.Unmarshal(raw, &wire); err != nil {
					t.Fatalf("unmarshal wire: %v", err)
				}
				if len(wire.Holidays) != 2 || wire.Holidays[0] != "2026-01-01" || wire.Holidays[1] != "2026-12-25" {
					t.Fatalf("holidays = %v, want sorted [2026-01-01 2026-12-25]", wire.Holidays)
				}
			})
		})
	})
}

func TestDayTimeJSON(t *testing.T) {
	t.Run("Given DayTime values", func(t *testing.T) {
		t.Run("When marshalled and unmarshalled", func(t *testing.T) {
			raw, _ := json.Marshal(At(9, 5, 30))
			if string(raw) != `"09:05:30"` {
				t.Fatalf("marshal = %s, want \"09:05:30\"", raw)
			}
			var dt DayTime
			if err := json.Unmarshal([]byte(`"14:15:16"`), &dt); err != nil || dt != At(14, 15, 16) {
				t.Fatalf("unmarshal = %v (err %v), want 14:15:16", dt, err)
			}
		})
		t.Run("When given HH:MM without seconds", func(t *testing.T) {
			var dt DayTime
			if err := json.Unmarshal([]byte(`"08:30"`), &dt); err != nil || dt != At(8, 30, 0) {
				t.Fatalf("unmarshal = %v (err %v), want 08:30:00", dt, err)
			}
		})
		t.Run("When given a malformed string", func(t *testing.T) {
			var dt DayTime
			if err := json.Unmarshal([]byte(`"not-a-time"`), &dt); err == nil {
				t.Fatal("expected an error for a malformed DayTime")
			}
		})
		t.Run("When given a non-string token", func(t *testing.T) {
			var dt DayTime
			if err := json.Unmarshal([]byte(`123`), &dt); err == nil {
				t.Fatal("expected an error for a non-string DayTime")
			}
		})
	})
}

func TestDateJSON(t *testing.T) {
	t.Run("Given Date values", func(t *testing.T) {
		t.Run("When marshalled and unmarshalled", func(t *testing.T) {
			raw, _ := json.Marshal(On(2026, time.December, 25))
			if string(raw) != `"2026-12-25"` {
				t.Fatalf("marshal = %s, want \"2026-12-25\"", raw)
			}
			var d Date
			if err := json.Unmarshal([]byte(`"2025-03-09"`), &d); err != nil || d != On(2025, 3, 9) {
				t.Fatalf("unmarshal = %v (err %v), want 2025-03-09", d, err)
			}
		})
		t.Run("When given a malformed string", func(t *testing.T) {
			var d Date
			if err := json.Unmarshal([]byte(`"2025/03/09"`), &d); err == nil {
				t.Fatal("expected an error for a malformed Date")
			}
		})
		t.Run("When given a non-string token", func(t *testing.T) {
			var d Date
			if err := json.Unmarshal([]byte(`true`), &d); err == nil {
				t.Fatal("expected an error for a non-string Date")
			}
		})
	})
}

func TestScheduleJSONBadZone(t *testing.T) {
	t.Run("Given JSON naming an unknown zone", func(t *testing.T) {
		t.Run("When unmarshalled", func(t *testing.T) {
			var s Schedule
			err := json.Unmarshal([]byte(`{"loc":"Mars/Olympus","week":[[],[],[],[],[],[],[]]}`), &s)
			t.Run("Then it returns ErrBadSchedule", func(t *testing.T) {
				if !errors.Is(err, ErrBadSchedule) {
					t.Fatalf("unmarshal = %v, want ErrBadSchedule", err)
				}
			})
		})
	})
	t.Run("Given JSON with an empty zone name", func(t *testing.T) {
		t.Run("When unmarshalled", func(t *testing.T) {
			var s Schedule
			if err := json.Unmarshal([]byte(`{"loc":"","week":[[],[],[],[],[],[],[]]}`), &s); err != nil {
				t.Fatalf("unmarshal = %v, want nil (nil Loc, validated later)", err)
			}
			t.Run("Then Loc is nil and Validate rejects it", func(t *testing.T) {
				if s.Loc != nil {
					t.Fatalf("Loc = %v, want nil", s.Loc)
				}
				if !errors.Is(s.Validate(), ErrBadSchedule) {
					t.Fatal("Validate should reject a nil Loc")
				}
			})
		})
	})
	t.Run("Given JSON that is a complete value of the wrong shape", func(t *testing.T) {
		t.Run("When unmarshalled into a Schedule", func(t *testing.T) {
			var s Schedule
			if err := json.Unmarshal([]byte(`123`), &s); err == nil {
				t.Fatal("expected a type-mismatch error")
			}
		})
	})
}
