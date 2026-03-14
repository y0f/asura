package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Expr struct {
	minutes []int
	hours   []int
	doms    []int
	months  []int
	dows    []int
}

func Parse(expr string) (*Expr, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron expression must have 5 fields (minute hour dom month dow)")
	}
	e := &Expr{}
	var err error
	if e.minutes, err = parseField(fields[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if e.hours, err = parseField(fields[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if e.doms, err = parseField(fields[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day-of-month: %w", err)
	}
	if e.months, err = parseField(fields[3], 1, 12); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if e.dows, err = parseField(fields[4], 0, 6); err != nil {
		return nil, fmt.Errorf("day-of-week: %w", err)
	}
	return e, nil
}

func (e *Expr) Matches(t time.Time) bool {
	return contains(e.minutes, t.Minute()) &&
		contains(e.hours, t.Hour()) &&
		contains(e.doms, t.Day()) &&
		contains(e.months, int(t.Month())) &&
		contains(e.dows, int(t.Weekday()))
}

func parseField(field string, min, max int) ([]int, error) {
	if field == "*" {
		return makeRange(min, max), nil
	}
	var result []int
	for _, part := range strings.Split(field, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "/") {
			vals, err := parseStep(part, min, max)
			if err != nil {
				return nil, err
			}
			result = append(result, vals...)
		} else if strings.Contains(part, "-") {
			vals, err := parseRange(part, min, max)
			if err != nil {
				return nil, err
			}
			result = append(result, vals...)
		} else {
			v, err := strconv.Atoi(part)
			if err != nil || v < min || v > max {
				return nil, fmt.Errorf("invalid value %q (must be %d-%d)", part, min, max)
			}
			result = append(result, v)
		}
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("empty field")
	}
	return result, nil
}

func parseRange(s string, min, max int) ([]int, error) {
	parts := strings.SplitN(s, "-", 2)
	lo, err := strconv.Atoi(parts[0])
	if err != nil || lo < min {
		return nil, fmt.Errorf("invalid range start %q", parts[0])
	}
	hi, err := strconv.Atoi(parts[1])
	if err != nil || hi > max || hi < lo {
		return nil, fmt.Errorf("invalid range end %q", parts[1])
	}
	return makeRange(lo, hi), nil
}

func parseStep(s string, min, max int) ([]int, error) {
	parts := strings.SplitN(s, "/", 2)
	step, err := strconv.Atoi(parts[1])
	if err != nil || step <= 0 {
		return nil, fmt.Errorf("invalid step %q", parts[1])
	}
	start := min
	if parts[0] != "*" {
		start, err = strconv.Atoi(parts[0])
		if err != nil || start < min {
			return nil, fmt.Errorf("invalid step start %q", parts[0])
		}
	}
	var result []int
	for v := start; v <= max; v += step {
		result = append(result, v)
	}
	return result, nil
}

func makeRange(lo, hi int) []int {
	r := make([]int, hi-lo+1)
	for i := range r {
		r[i] = lo + i
	}
	return r
}

func contains(vals []int, v int) bool {
	for _, val := range vals {
		if val == v {
			return true
		}
	}
	return false
}
