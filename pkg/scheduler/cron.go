package scheduler

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// cronSpec is a parsed 5-field cron expression: minute, hour, day-of-month,
// month, day-of-week. The grammar supports '*', single integers, and
// comma-separated lists. Step/range syntax is deliberately out of scope for
// alpha — see scheduler.go for the rationale.
type cronSpec struct {
	min, hour, dom, mon, dow fieldSet
}

type fieldSet struct {
	any    bool
	values map[int]struct{}
}

func (f fieldSet) has(v int) bool {
	if f.any {
		return true
	}
	_, ok := f.values[v]
	return ok
}

func parseField(raw string, lo, hi int) (fieldSet, error) {
	raw = strings.TrimSpace(raw)
	if raw == "*" {
		return fieldSet{any: true}, nil
	}
	if raw == "" {
		return fieldSet{}, errors.New("scheduler: empty cron field")
	}
	values := map[int]struct{}{}
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(p)
		n, err := strconv.Atoi(p)
		if err != nil {
			return fieldSet{}, errors.New("scheduler: cron field is not an integer or '*': " + p)
		}
		if n < lo || n > hi {
			return fieldSet{}, errors.New("scheduler: cron field out of range: " + p)
		}
		values[n] = struct{}{}
	}
	return fieldSet{values: values}, nil
}

func parseCron(expr string) (cronSpec, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return cronSpec{}, errors.New("scheduler: cron expression needs 5 fields (m h dom mon dow)")
	}
	var s cronSpec
	var err error
	if s.min, err = parseField(fields[0], 0, 59); err != nil {
		return s, err
	}
	if s.hour, err = parseField(fields[1], 0, 23); err != nil {
		return s, err
	}
	if s.dom, err = parseField(fields[2], 1, 31); err != nil {
		return s, err
	}
	if s.mon, err = parseField(fields[3], 1, 12); err != nil {
		return s, err
	}
	if s.dow, err = parseField(fields[4], 0, 6); err != nil {
		return s, err
	}
	return s, nil
}

func (s cronSpec) matches(t time.Time) bool {
	return s.min.has(t.Minute()) &&
		s.hour.has(t.Hour()) &&
		s.dom.has(t.Day()) &&
		s.mon.has(int(t.Month())) &&
		s.dow.has(int(t.Weekday())) &&
		t.Second() == 0
}
