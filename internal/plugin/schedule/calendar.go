package schedule

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var errInvalid = errors.New("invalid schedule")

// next preserves the existing calendar policy, including DST and cron OR rules. Cron uses local wall minutes: nonexistent DST
// minutes are skipped; both distinct UTC occurrences of a repeated minute fire.
// DOM and DOW use ordinary cron OR semantics when both are restricted.
func next(kind, spec, zone string, after time.Time) (time.Time, error) {
	loc, err := calendarLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return time.Time{}, errInvalid
	}
	if kind == "at" {
		t, e := time.Parse(time.RFC3339Nano, spec)
		if e != nil {
			return time.Time{}, errInvalid
		}
		return t.UTC(), nil
	}
	if kind != "cron" {
		return time.Time{}, errInvalid
	}
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return time.Time{}, errInvalid
	}
	bounds := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	masks := make([]map[int]bool, 5)
	last := -1
	for i, s := range fields {
		if i == 4 && len(s) == 2 && s[1] == 'L' {
			last, err = strconv.Atoi(s[:1])
			if err != nil || last > 6 {
				return time.Time{}, errInvalid
			}
			continue
		}
		masks[i], err = field(s, bounds[i][0], bounds[i][1])
		if err != nil {
			return time.Time{}, err
		}
	}
	// A Gregorian calendar repeats within 400 years; eight years suffice for
	// supported day/month patterns including leap day. Invalid combinations fail.
	limit := after.AddDate(8, 0, 0)
	for t := after.UTC().Truncate(time.Minute).Add(time.Minute); !t.After(limit); t = t.Add(time.Minute) {
		local := t.In(loc)
		if !masks[3][int(local.Month())] {
			continue
		}
		dom := masks[2][local.Day()]
		dow := false
		if last >= 0 {
			dow = int(local.Weekday()) == last && local.AddDate(0, 0, 7).Month() != local.Month()
		} else {
			dow = masks[4][int(local.Weekday())]
		}
		day := dom && dow
		if !strings.Contains(fields[2], "*") && !strings.Contains(fields[4], "*") {
			day = dom || dow
		}
		if day && masks[1][local.Hour()] && masks[0][local.Minute()] {
			return t, nil
		}
	}
	return time.Time{}, errInvalid
}
func field(s string, min, max int) (map[int]bool, error) {
	out := map[int]bool{}
	for _, part := range strings.Split(s, ",") {
		base, stepText, hasStep := strings.Cut(part, "/")
		step := 1
		var err error
		if hasStep {
			step, err = strconv.Atoi(stepText)
			if err != nil || step < 1 || step > max-min+1 {
				return nil, errInvalid
			}
		}
		lo, hi := min, max
		if base != "*" {
			a, b, span := strings.Cut(base, "-")
			lo, err = strconv.Atoi(a)
			if err != nil {
				return nil, errInvalid
			}
			hi = lo
			if span {
				hi, err = strconv.Atoi(b)
			} else if hasStep {
				hi = max
			}
			if err != nil || lo < min || hi > max || hi < lo {
				return nil, errInvalid
			}
		}
		for n := lo; n <= hi; n += step {
			out[n] = true
		}
	}
	return out, nil
}
