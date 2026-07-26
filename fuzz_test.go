package sundial

import (
	"math/rand"
	"testing"
	"time"
)

// randSchedule builds a valid, non-empty schedule from a PRNG: each of Monday
// through Friday gets either one window or a lunch-split pair, plus a couple of
// holidays. It always has positive weekly working time, so Add terminates.
func randSchedule(loc *time.Location, r *rand.Rand) Schedule {
	var wk [7][]Window
	for wd := time.Monday; wd <= time.Friday; wd++ {
		openH := 6 + r.Intn(4)   // 06..09
		closeH := 15 + r.Intn(5) // 15..19
		if r.Intn(2) == 0 {
			wk[wd] = []Window{{Open: At(openH, 0, 0), Close: At(closeH, 0, 0)}}
		} else {
			// A lunch split: [open,12) and [13,close).
			wk[wd] = []Window{
				{Open: At(openH, 0, 0), Close: At(12, 0, 0)},
				{Open: At(13, 0, 0), Close: At(closeH, 0, 0)},
			}
		}
	}
	s := Schedule{
		Loc:  loc,
		Week: wk,
		Holidays: Holidays(
			On(2025, 3, 10), // the spring-forward Monday
			On(2025, 11, 3), // the Monday after fall-back
			On(2025, 7, 4),  // a summer Friday
		),
	}
	return s
}

func TestPropertyRoundTripAndFired(t *testing.T) {
	loc := nyLoc(t)
	t.Run("Given random valid schedules and timers around a DST transition", func(t *testing.T) {
		// Start on the Friday before spring-forward so samples straddle it.
		base := time.Date(2025, 3, 7, 5, 0, 0, 0, loc)
		t.Run("When exercised over many seeds", func(t *testing.T) {
			t.Run("Then round-trip holds, Fired is stable and monotone, and nothing panics", func(t *testing.T) {
				for seed := int64(0); seed < 200; seed++ {
					r := rand.New(rand.NewSource(seed))
					s := randSchedule(loc, r)
					if err := s.Validate(); err != nil {
						t.Fatalf("seed %d: generated schedule invalid: %v", seed, err)
					}

					// Round-trip property.
					start := base.Add(time.Duration(r.Intn(45*24*60)) * time.Minute)
					d := time.Duration(r.Int63n(int64(120 * time.Hour)))
					if rt := s.Working(start, s.Add(start, d)); rt != d {
						t.Fatalf("seed %d: round-trip broke: d=%v got=%v", seed, d, rt)
					}

					// Fired stability + monotonicity under a random poll sequence
					// with random pauses.
					budget := time.Duration(1+r.Intn(40)) * time.Hour
					levels := []Level{
						{"warn", budget / 2},
						{"breach", budget},
						{"page", budget + budget/2},
					}
					tm, err := Start(Config{Schedule: s, Budget: budget, Levels: levels}, start)
					if err != nil {
						t.Fatalf("seed %d: Start: %v", seed, err)
					}

					firedAt := map[string]time.Time{}
					prevCount := 0
					now := start
					for step := 0; step < 40; step++ {
						now = now.Add(time.Duration(r.Intn(6*60)) * time.Minute)
						switch r.Intn(6) {
						case 0:
							_ = tm.Pause(now) // may error if already paused; must not panic
						case 1:
							_ = tm.Resume(now) // may error if not paused; must not panic
						}
						_ = tm.Remaining(now)
						_ = tm.Breached(now)
						fs := tm.Fired(now)
						if len(fs) < prevCount {
							t.Fatalf("seed %d: Fired shrank from %d to %d", seed, prevCount, len(fs))
						}
						prevCount = len(fs)
						for _, f := range fs {
							if was, ok := firedAt[f.Level.Name]; ok {
								if !was.Equal(f.At) {
									t.Fatalf("seed %d: %s fired-at rewritten: %v then %v", seed, f.Level.Name, was, f.At)
								}
							} else {
								firedAt[f.Level.Name] = f.At
							}
						}
					}
				}
			})
		})
	})
}

// FuzzRoundTrip drives arbitrary bytes into a schedule, start and budget and
// asserts the core invariant Working(start, Add(start,d)) == d, that Add never
// runs backward for a positive budget, and that nothing panics.
func FuzzRoundTrip(f *testing.F) {
	f.Add([]byte{0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88})
	f.Add([]byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01})
	f.Add([]byte{0xff, 0xee, 0xdd, 0xcc, 0xbb, 0xaa, 0x99, 0x88})

	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		f.Skipf("America/New_York unavailable: %v", err)
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) < 8 {
			t.Skip()
		}
		r := rand.New(rand.NewSource(int64(data[0])<<24 | int64(data[1])<<16 | int64(data[2])<<8 | int64(data[3])))
		s := randSchedule(loc, r)

		base := time.Date(2025, 3, 7, 5, 0, 0, 0, loc)
		startMin := int(data[4])<<8 | int(data[5]) // 0..65535 minutes ≈ 45 days
		start := base.Add(time.Duration(startMin) * time.Minute)
		d := time.Duration(int(data[6])<<8|int(data[7])) * time.Minute // 0..~45h

		landed := s.Add(start, d)
		if d > 0 && landed.Before(start) {
			t.Fatalf("Add ran backward: start=%v d=%v landed=%v", start, d, landed)
		}
		if rt := s.Working(start, landed); rt != d {
			t.Fatalf("round-trip broke: start=%v d=%v landed=%v Working=%v", start, d, landed, rt)
		}
	})
}
