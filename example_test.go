package sundial_test

import (
	"fmt"
	"time"

	"github.com/zkrebbekx/sundial"
)

// mustLoc loads a zone for the examples, falling back to UTC so the examples run
// deterministically even without a zone database. The business schedule is the
// same civil 09:00–17:00 either way.
func mustLoc() *time.Location {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		return time.UTC
	}
	return loc
}

// Example measures elapsed working time and the due instant for an 8-working-hour
// SLA that starts late on a Friday afternoon and spills over the weekend.
func Example() {
	loc := mustLoc()
	sched := sundial.Schedule{
		Loc:  loc,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}

	start := time.Date(2026, 1, 9, 16, 0, 0, 0, loc) // Friday 16:00
	t, _ := sundial.Start(sundial.Config{Schedule: sched, Budget: 8 * time.Hour}, start)

	// 1h Friday + 7h Monday = due Monday 16:00; the weekend does not count.
	fmt.Println("due:", t.DueAt().Format("Mon 15:04"))
	fmt.Println("remaining on Friday 16:30:", t.Remaining(start.Add(30*time.Minute)))
	// Output:
	// due: Mon 16:00
	// remaining on Friday 16:30: 7h30m0s
}

// Example_pause shows that pausing while waiting on the customer excludes that
// working time, pushing the due instant later.
func Example_pause() {
	loc := mustLoc()
	sched := sundial.Schedule{
		Loc:  loc,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}

	start := time.Date(2026, 1, 5, 9, 0, 0, 0, loc) // Monday 09:00
	t, _ := sundial.Start(sundial.Config{Schedule: sched, Budget: 8 * time.Hour}, start)
	fmt.Println("due before pause:", t.DueAt().Format("Mon 15:04"))

	_ = t.Pause(time.Date(2026, 1, 5, 11, 0, 0, 0, loc)) // waiting on customer 11:00–14:00
	_ = t.Resume(time.Date(2026, 1, 5, 14, 0, 0, 0, loc))
	fmt.Println("due after 3h pause:", t.DueAt().Format("Mon 15:04"))
	// Output:
	// due before pause: Mon 17:00
	// due after 3h pause: Tue 12:00
}

// Example_escalation shows write-once escalation firing: each level fires once,
// with a stable instant, so polling drives escalations without double-paging.
func Example_escalation() {
	loc := mustLoc()
	sched := sundial.Schedule{
		Loc:  loc,
		Week: sundial.Weekdays(sundial.Window{Open: sundial.At(9, 0, 0), Close: sundial.At(17, 0, 0)}),
	}

	start := time.Date(2026, 1, 5, 9, 0, 0, 0, loc) // Monday 09:00
	t, _ := sundial.Start(sundial.Config{
		Schedule: sched,
		Budget:   8 * time.Hour,
		Levels:   []sundial.Level{{Name: "warn", At: 4 * time.Hour}, {Name: "breach", At: 8 * time.Hour}},
	}, start)

	for _, f := range t.Fired(time.Date(2026, 1, 6, 10, 0, 0, 0, loc)) { // 9 working hours in
		fmt.Printf("%s fired at %s\n", f.Level.Name, f.At.Format("Mon 15:04"))
	}
	// Output:
	// warn fired at Mon 13:00
	// breach fired at Mon 17:00
}
