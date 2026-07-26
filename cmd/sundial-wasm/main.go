//go:build js && wasm

// Command sundial-wasm is the browser playground's engine: the sundial SLA
// clock compiled to WebAssembly, exposing a small JSON API on the global
// "sundial" object. It has no backend — every call runs in the visitor's tab
// over the pure library, and nothing is stored.
//
// The bridge is stateless. The page holds the whole timer configuration — the
// schedule (zone, per-weekday windows, holidays), the budget, the escalation
// levels, the start instant, the pause intervals and the current "now" — and
// sends all of it on every call. The engine rebuilds a fresh Timer, reads the
// state at now, and returns the computed result (elapsed, due, remaining,
// breach, per-level firings). Because a fresh Timer computes each firing
// instant as a pure function of the schedule and pauses, the write-once
// escalation semantics hold across calls for free: dragging "now" back and
// forth never moves a fired-at instant.
//
// Instants cross the boundary as civil-time strings in the schedule's zone —
// "2006-01-02T15:04:05", with no offset and no Z. The page treats the timeline
// as civil time; the engine resolves each string in the schedule's *time.Location
// with time.ParseInLocation, so a 09:00–17:00 window is eight working hours
// whether or not the clocks changed that day. Durations cross as whole seconds.
//
// The zone database is embedded (import _ "time/tzdata") because a browser tab
// has no zoneinfo files; without it time.LoadLocation("America/New_York") would
// fail and the DST demonstration could not run.
//
// This file carries the js && wasm build constraint so the native toolchain —
// go build ./..., go vet ./..., go test ./... — never compiles it (syscall/js
// exists only under GOOS=js). The root module stays zero-dependency: syscall/js
// and time/tzdata are stdlib and add nothing to go.sum, and this command imports
// only the sundial library beside it.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
	"time"
	_ "time/tzdata"

	"github.com/zkrebbekx/sundial"
)

func main() {
	obj := map[string]any{}
	register(obj, "compute", handleCompute)
	register(obj, "loadScenario", handleLoadScenario)
	obj["scenarios"] = toJS(scenarioList())

	js.Global().Set("sundial", js.ValueOf(obj))
	js.Global().Set("__sundialReady", js.ValueOf(true))
	if cb := js.Global().Get("__sundialOnReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}

	// Keep the Go runtime alive for the lifetime of the page.
	select {}
}

// register wraps a handler so every call recovers from a panic into a returned
// {error} object rather than tearing down the wasm instance, and marshals the
// result to a real JS object the page can read without parsing.
func register(obj map[string]any, name string, fn func([]byte) (any, error)) {
	obj[name] = js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
		defer func() {
			if r := recover(); r != nil {
				result = toJS(map[string]any{"error": fmt.Sprintf("panic: %v", r), "code": "panic"})
			}
		}()
		out, err := fn(input(args))
		if err != nil {
			return toJS(errObj(err))
		}
		return toJS(out)
	})
}

// input normalises the first argument to JSON bytes. A JS object is
// stringified; a bare string is passed through verbatim (loadScenario accepts
// one); anything absent becomes an empty object.
func input(args []js.Value) []byte {
	if len(args) == 0 {
		return []byte("{}")
	}
	a := args[0]
	switch a.Type() {
	case js.TypeString:
		return []byte(a.String())
	case js.TypeUndefined, js.TypeNull:
		return []byte("{}")
	default:
		return []byte(js.Global().Get("JSON").Call("stringify", a).String())
	}
}

// toJS marshals a Go value and parses it back into a native JS object so the
// page receives structured data rather than a string to parse itself.
func toJS(v any) js.Value {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]any{"error": err.Error(), "code": "marshal"})
	}
	return js.Global().Get("JSON").Call("parse", string(b))
}

// --- error contract ---------------------------------------------------------

// errObj renders an error as the page's {error, code} shape.
func errObj(err error) map[string]any {
	return map[string]any{"error": err.Error(), "code": errCode(err)}
}

// argErr is a bridge-minted validation error (a malformed time string, an
// unparseable date) distinct from a library sentinel. It carries a stable code.
type argErr struct {
	code string
	msg  string
}

func (e *argErr) Error() string { return e.msg }

func badArg(code, msg string) error { return &argErr{code: code, msg: msg} }

// errCode maps an error to a stable string code: a bridge argErr keeps its own
// code, and every sundial sentinel is mirrored onto its name. An unrecognised
// error is "error".
func errCode(err error) string {
	var a *argErr
	if errors.As(err, &a) {
		return a.code
	}
	switch {
	case errors.Is(err, sundial.ErrBadSchedule):
		return "bad_schedule"
	case errors.Is(err, sundial.ErrBadWindow):
		return "bad_window"
	case errors.Is(err, sundial.ErrBadLevels):
		return "bad_levels"
	case errors.Is(err, sundial.ErrBadBudget):
		return "bad_budget"
	case errors.Is(err, sundial.ErrAlreadyPaused):
		return "already_paused"
	case errors.Is(err, sundial.ErrNotPaused):
		return "not_paused"
	case errors.Is(err, sundial.ErrBadSnapshot):
		return "bad_snapshot"
	default:
		return "error"
	}
}

// --- input bounds -----------------------------------------------------------

// These caps stop a hostile or accidental input from forcing an astronomical
// amount of work in the tab. They are far larger than any real SLA needs.
const (
	maxWindowsPerDay = 8
	maxHolidays      = 512
	maxPauses        = 64
	maxLevels        = 32
	// maxDurSeconds bounds a budget or level threshold. Schedule.Add walks
	// working windows day by day, so an unbounded duration could spin the tab;
	// ~100000 working hours (over a decade) is the ceiling.
	maxDurSeconds = 100000 * 3600
)

// civilLayout is the wire format for every instant: a civil date-time with no
// zone, resolved in the schedule's Loc on the way in and formatted the same way
// on the way out.
const civilLayout = "2006-01-02T15:04:05"

// --- request / response DTOs ------------------------------------------------

type windowDTO struct {
	Open  string `json:"open"`
	Close string `json:"close"`
}

type scheduleDTO struct {
	Tz       string         `json:"tz"`
	Week     [7][]windowDTO `json:"week"` // index 0=Sunday … 6=Saturday (time.Weekday)
	Holidays []string       `json:"holidays"`
}

type levelDTO struct {
	Name      string `json:"name"`
	AtSeconds int64  `json:"atSeconds"`
}

type pauseDTO struct {
	Start string `json:"start"`
	End   string `json:"end"` // empty = still open (no resume yet)
}

type computeReq struct {
	Schedule      scheduleDTO `json:"schedule"`
	BudgetSeconds int64       `json:"budgetSeconds"`
	Levels        []levelDTO  `json:"levels"`
	Start         string      `json:"start"`
	Now           string      `json:"now"`
	Pauses        []pauseDTO  `json:"pauses"`
}

type levelResp struct {
	Name       string `json:"name"`
	AtSeconds  int64  `json:"atSeconds"`
	CrossingAt string `json:"crossingAt"` // civil instant elapsed reaches AtSeconds
	Reached    bool   `json:"reached"`    // has now passed the crossing?
}

type pauseResp struct {
	Start          string `json:"start"`
	End            string `json:"end"`
	Open           bool   `json:"open"`
	WorkingSeconds int64  `json:"workingSeconds"` // working time inside the pause (the DueAt shift)
}

type computeResp struct {
	Start            string      `json:"start"`
	Now              string      `json:"now"`
	DueAt            string      `json:"dueAt"`
	BudgetSeconds    int64       `json:"budgetSeconds"`
	ElapsedSeconds   int64       `json:"elapsedSeconds"`
	RemainingSeconds int64       `json:"remainingSeconds"`
	Breached         bool        `json:"breached"`
	BreachedAt       string      `json:"breachedAt"`      // civil instant, "" if not breached as of now
	Paused           bool        `json:"paused"`          // an open pause is freezing the clock
	WallSpanSeconds  int64       `json:"wallSpanSeconds"` // real span DueAt − Start (DST teaching)
	Levels           []levelResp `json:"levels"`
	Pauses           []pauseResp `json:"pauses"`
}

// --- parsing helpers --------------------------------------------------------

func loadLoc(tz string) (*time.Location, error) {
	if tz == "" {
		tz = "UTC"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		return nil, badArg("bad_schedule", fmt.Sprintf("unknown time zone %q — use an IANA name like \"America/New_York\"", tz))
	}
	return loc, nil
}

// parseCivil resolves a civil-time string in loc. It accepts the full
// "2006-01-02T15:04:05" layout and the seconds-less "2006-01-02T15:04".
func parseCivil(field, value string, loc *time.Location) (time.Time, error) {
	for _, layout := range []string{civilLayout, "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, value, loc); err == nil {
			return t, nil
		}
	}
	return time.Time{}, badArg("invalid_argument", fmt.Sprintf(
		"%s must be a civil date-time such as 2026-01-09T16:00:00, got %q", field, value))
}

// parseDayTime reuses sundial.DayTime's own decoder so the accepted forms
// ("HH:MM" and "HH:MM:SS", plus the 24:00:00 end-of-day bound) stay identical
// to the library's.
func parseDayTime(s string) (sundial.DayTime, error) {
	var dt sundial.DayTime
	if err := dt.UnmarshalJSON([]byte(strconv.Quote(s))); err != nil {
		return dt, badArg("bad_window", fmt.Sprintf("invalid time-of-day %q (want HH:MM or HH:MM:SS)", s))
	}
	return dt, nil
}

func parseDate(s string) (sundial.Date, error) {
	tt, err := time.Parse("2006-01-02", s)
	if err != nil {
		return sundial.Date{}, badArg("bad_schedule", fmt.Sprintf("invalid holiday date %q (want YYYY-MM-DD)", s))
	}
	return sundial.On(tt.Year(), tt.Month(), tt.Day()), nil
}

// buildSchedule turns the wire schedule into a validated sundial.Schedule and
// returns its zone for resolving the other instants in the request.
func buildSchedule(dto scheduleDTO) (sundial.Schedule, *time.Location, error) {
	loc, err := loadLoc(dto.Tz)
	if err != nil {
		return sundial.Schedule{}, nil, err
	}
	var week [7][]sundial.Window
	for d := 0; d < 7; d++ {
		if len(dto.Week[d]) > maxWindowsPerDay {
			return sundial.Schedule{}, nil, badArg("bad_window", fmt.Sprintf("a day may hold at most %d windows", maxWindowsPerDay))
		}
		for _, w := range dto.Week[d] {
			open, err := parseDayTime(w.Open)
			if err != nil {
				return sundial.Schedule{}, nil, err
			}
			cl, err := parseDayTime(w.Close)
			if err != nil {
				return sundial.Schedule{}, nil, err
			}
			week[d] = append(week[d], sundial.Window{Open: open, Close: cl})
		}
	}
	if len(dto.Holidays) > maxHolidays {
		return sundial.Schedule{}, nil, badArg("bad_schedule", fmt.Sprintf("at most %d holidays", maxHolidays))
	}
	var hol map[sundial.Date]struct{}
	if len(dto.Holidays) > 0 {
		hol = make(map[sundial.Date]struct{}, len(dto.Holidays))
		for _, h := range dto.Holidays {
			d, err := parseDate(h)
			if err != nil {
				return sundial.Schedule{}, nil, err
			}
			hol[d] = struct{}{}
		}
	}
	s := sundial.Schedule{Loc: loc, Week: week, Holidays: hol}
	if err := s.Validate(); err != nil {
		return sundial.Schedule{}, nil, err
	}
	return s, loc, nil
}

// applyPauses replays the request's pause intervals onto a timer in order. An
// interval with an empty End is left open (no resume), freezing the clock.
func applyPauses(t *sundial.Timer, pauses []pauseDTO, loc *time.Location) error {
	for i, p := range pauses {
		ps, err := parseCivil(fmt.Sprintf("pauses[%d].start", i), p.Start, loc)
		if err != nil {
			return err
		}
		if err := t.Pause(ps); err != nil {
			return err
		}
		if strings.TrimSpace(p.End) != "" {
			pe, err := parseCivil(fmt.Sprintf("pauses[%d].end", i), p.End, loc)
			if err != nil {
				return err
			}
			if err := t.Resume(pe); err != nil {
				return err
			}
		}
	}
	return nil
}

// buildLevels converts the wire levels into sundial.Level values, capping the
// count and each threshold. Ascending order is enforced by Start (ErrBadLevels).
func buildLevels(dtos []levelDTO) ([]sundial.Level, time.Duration, error) {
	if len(dtos) > maxLevels {
		return nil, 0, badArg("bad_levels", fmt.Sprintf("at most %d levels", maxLevels))
	}
	levels := make([]sundial.Level, 0, len(dtos))
	var maxAt time.Duration
	for _, d := range dtos {
		if d.AtSeconds < 0 || d.AtSeconds > maxDurSeconds {
			return nil, 0, badArg("bad_levels", fmt.Sprintf("a level threshold must be between 0 and %d seconds", maxDurSeconds))
		}
		at := time.Duration(d.AtSeconds) * time.Second
		if at > maxAt {
			maxAt = at
		}
		name := d.Name
		if name == "" {
			name = "level"
		}
		levels = append(levels, sundial.Level{Name: name, At: at})
	}
	return levels, maxAt, nil
}

// farInstant returns a now that is guaranteed to lie past every level's
// crossing, so a single Fired call yields all crossing instants. It projects a
// scratch timer whose budget covers the largest threshold and pads two days of
// wall clock beyond its due instant.
func farInstant(s sundial.Schedule, start time.Time, pauses []pauseDTO, loc *time.Location, budget time.Duration) time.Time {
	st, err := sundial.Start(sundial.Config{Schedule: s, Budget: budget}, start)
	if err != nil {
		return start.AddDate(1, 0, 0)
	}
	_ = applyPauses(st, pauses, loc)
	return st.DueAt().Add(48 * time.Hour)
}

// --- compute ----------------------------------------------------------------

func handleCompute(in []byte) (any, error) {
	var req computeReq
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	if len(req.Pauses) > maxPauses {
		return nil, badArg("invalid_argument", fmt.Sprintf("at most %d pauses", maxPauses))
	}
	if req.BudgetSeconds < 0 || req.BudgetSeconds > maxDurSeconds {
		return nil, badArg("bad_budget", fmt.Sprintf("budget must be between 0 and %d seconds", maxDurSeconds))
	}

	sched, loc, err := buildSchedule(req.Schedule)
	if err != nil {
		return nil, err
	}
	levels, maxLevelAt, err := buildLevels(req.Levels)
	if err != nil {
		return nil, err
	}
	start, err := parseCivil("start", req.Start, loc)
	if err != nil {
		return nil, err
	}
	now, err := parseCivil("now", req.Now, loc)
	if err != nil {
		return nil, err
	}
	budget := time.Duration(req.BudgetSeconds) * time.Second

	t, err := sundial.Start(sundial.Config{Schedule: sched, Budget: budget, Levels: levels}, start)
	if err != nil {
		return nil, err
	}
	if err := applyPauses(t, req.Pauses, loc); err != nil {
		return nil, err
	}

	due := t.DueAt()
	bAt, breached := t.BreachedAt()
	// BreachedAt only records once Breached is observed true; ask at now.
	nowBreached := t.Breached(now)
	if nowBreached {
		bAt, breached = t.BreachedAt()
	}

	resp := computeResp{
		Start:            start.Format(civilLayout),
		Now:              now.Format(civilLayout),
		DueAt:            due.Format(civilLayout),
		BudgetSeconds:    req.BudgetSeconds,
		ElapsedSeconds:   int64(t.Elapsed(now) / time.Second),
		RemainingSeconds: int64(t.Remaining(now) / time.Second),
		Breached:         nowBreached,
		Paused:           t.Paused(),
		WallSpanSeconds:  int64(due.Sub(start) / time.Second),
	}
	if breached {
		resp.BreachedAt = bAt.Format(civilLayout)
	}

	// Per-level firings. reached = the ascending prefix of levels whose
	// threshold now has passed; crossing instants come from a far-future poll so
	// even pending levels report where they will fire.
	if len(levels) > 0 {
		reached := len(t.Fired(now))
		farBudget := budget
		if maxLevelAt > farBudget {
			farBudget = maxLevelAt
		}
		far := farInstant(sched, start, req.Pauses, loc, farBudget)
		all := t.Fired(far)
		resp.Levels = make([]levelResp, 0, len(levels))
		for i, lv := range levels {
			lr := levelResp{
				Name:      lv.Name,
				AtSeconds: int64(lv.At / time.Second),
				Reached:   i < reached,
			}
			if i < len(all) {
				lr.CrossingAt = all[i].At.Format(civilLayout)
			}
			resp.Levels = append(resp.Levels, lr)
		}
	}

	// Echo the pauses with the working time each consumes — the amount each
	// completed pause shifts DueAt later. An open pause shifts nothing yet.
	resp.Pauses = make([]pauseResp, 0, len(req.Pauses))
	for i, p := range req.Pauses {
		ps, err := parseCivil(fmt.Sprintf("pauses[%d].start", i), p.Start, loc)
		if err != nil {
			return nil, err
		}
		pr := pauseResp{Start: ps.Format(civilLayout)}
		if strings.TrimSpace(p.End) != "" {
			pe, err := parseCivil(fmt.Sprintf("pauses[%d].end", i), p.End, loc)
			if err != nil {
				return nil, err
			}
			pr.End = pe.Format(civilLayout)
			pr.WorkingSeconds = int64(sched.Working(ps, pe) / time.Second)
		} else {
			pr.Open = true
		}
		resp.Pauses = append(resp.Pauses, pr)
	}

	return resp, nil
}
