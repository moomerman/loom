package jobs

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed cron expression: five fields, minute hour
// day-of-month month day-of-week, evaluated in UTC.
//
//	"0 3 * * *"     every day at 03:00
//	"*/15 * * * *"  every 15 minutes
//	"0 9 * * 1-5"   weekdays at 09:00 (0 or 7 is Sunday)
//
// Each field is *, a number, a range a-b, a list a,b,c, or any of those
// with a step /n. When both day fields are restricted, a day matches if
// either does, as in standard cron.
type Schedule struct {
	expr                          string
	minute, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar              bool
}

// String returns the expression the schedule was parsed from.
func (s *Schedule) String() string { return s.expr }

type cronField struct {
	name     string
	min, max int
}

var cronFields = []cronField{
	{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7},
}

// ParseCron parses a cron expression. Errors are JB504.
func ParseCron(expr string) (*Schedule, error) {
	bad := func(msg string) error {
		return &Error{
			Code: JB504,
			Msg:  fmt.Sprintf("invalid cron expression %q: %s", expr, msg),
			Fix:  `use five fields, minute hour day-of-month month day-of-week, with numbers, *, ranges a-b, lists a,b and steps /n, e.g. "0 3 * * *" for 03:00 UTC daily`,
		}
	}
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, bad(fmt.Sprintf("it has %d fields, not 5", len(fields)))
	}
	s := &Schedule{expr: expr}
	sets := []*uint64{&s.minute, &s.hour, &s.dom, &s.month, &s.dow}
	for i, f := range fields {
		set, err := parseCronField(f, cronFields[i])
		if err != nil {
			return nil, bad(err.Error())
		}
		*sets[i] = set
	}
	if s.dow&(1<<7) != 0 { // 7 is Sunday too
		s.dow |= 1
	}
	s.domStar, s.dowStar = fields[2] == "*", fields[4] == "*"
	return s, nil
}

func parseCronField(f string, cf cronField) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(f, ",") {
		rng, stepStr, hasStep := strings.Cut(part, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepStr)
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("the %s step %q is not a positive number", cf.name, stepStr)
			}
			step = n
		}
		lo, hi := cf.min, cf.max
		switch {
		case rng == "*":
		case strings.Contains(rng, "-"):
			a, b, _ := strings.Cut(rng, "-")
			var err error
			if lo, err = cronNumber(a, cf); err != nil {
				return 0, err
			}
			if hi, err = cronNumber(b, cf); err != nil {
				return 0, err
			}
			if lo > hi {
				return 0, fmt.Errorf("the %s range %q runs backwards", cf.name, rng)
			}
		default:
			n, err := cronNumber(rng, cf)
			if err != nil {
				return 0, err
			}
			lo = n
			if !hasStep {
				hi = n
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func cronNumber(s string, cf cronField) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("the %s %q is not a number (names like MON or JAN aren't supported)", cf.name, s)
	}
	if n < cf.min || n > cf.max {
		return 0, fmt.Errorf("the %s %d is outside %d-%d", cf.name, n, cf.min, cf.max)
	}
	return n, nil
}

func (s *Schedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<t.Day()) != 0
	dow := s.dow&(1<<int(t.Weekday())) != 0
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dow
	case s.dowStar:
		return dom
	}
	return dom || dow
}

// Next returns the first time the schedule fires strictly after t, in
// UTC, or the zero time if it never does (e.g. "0 0 31 2 *").
func (s *Schedule) Next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		switch {
		case s.month&(1<<int(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
		case s.hour&(1<<t.Hour()) == 0:
			t = t.Truncate(time.Hour).Add(time.Hour)
		case s.minute&(1<<t.Minute()) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

// latest returns the last time the schedule fired in (from, to], or the
// zero time.
func (s *Schedule) latest(from, to time.Time) time.Time {
	var last time.Time
	for t := s.Next(from); !t.IsZero() && !t.After(to); t = s.Next(t) {
		last = t
	}
	return last
}
