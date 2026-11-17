package automation

import (
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

// Retained only for pre-activation owner requests, original NULL-method plans,
// and immutable Runs whose managed-service snapshot predates schedule-next.
// Remove when none of those historical consumers remain; managed paths must
// never call this interpreter after service activation.
// HistoricalNext is strictly after after. Cron uses local wall minutes: nonexistent DST
// minutes are skipped; both distinct UTC occurrences of a repeated minute fire.
// DOM and DOW use ordinary cron OR semantics when both are restricted.
func HistoricalNext(kind, spec, zone string, after time.Time) (time.Time, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return time.Time{}, ErrInvalid
	}
	if kind == "at" {
		t, e := time.Parse(time.RFC3339Nano, spec)
		if e != nil {
			return time.Time{}, ErrInvalid
		}
		return t.UTC(), nil
	}
	if kind != "cron" {
		return time.Time{}, ErrInvalid
	}
	fields := strings.Fields(spec)
	if len(fields) != 5 {
		return time.Time{}, ErrInvalid
	}
	bounds := [][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	masks := make([]map[int]bool, 5)
	last := -1
	for i, s := range fields {
		if i == 4 && len(s) == 2 && s[1] == 'L' {
			last, err = strconv.Atoi(s[:1])
			if err != nil || last > 6 {
				return time.Time{}, ErrInvalid
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
	return time.Time{}, ErrInvalid
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
				return nil, ErrInvalid
			}
		}
		lo, hi := min, max
		if base != "*" {
			a, b, span := strings.Cut(base, "-")
			lo, err = strconv.Atoi(a)
			if err != nil {
				return nil, ErrInvalid
			}
			hi = lo
			if span {
				hi, err = strconv.Atoi(b)
			} else if hasStep {
				hi = max
			}
			if err != nil || lo < min || hi > max || hi < lo {
				return nil, ErrInvalid
			}
		}
		for n := lo; n <= hi; n += step {
			out[n] = true
		}
	}
	return out, nil
}

// HistoricalLocalTime serves only owner requests before schedule activation.
func HistoricalLocalTime(date, clock, zone string) (string, error) {
	loc, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" {
		return "", ErrInvalid
	}
	wall, err := time.Parse("2006-01-02T15:04", date+"T"+clock)
	if err != nil {
		return "", ErrInvalid
	}
	offsets := map[int]bool{}
	for h := -36; h <= 36; h++ {
		_, offset := wall.Add(time.Duration(h) * time.Hour).In(loc).Zone()
		offsets[offset] = true
	}
	var matches []time.Time
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		if candidate.In(loc).Format("2006-01-02T15:04") == date+"T"+clock {
			matches = append(matches, candidate)
		}
	}
	if len(matches) != 1 {
		return "", ErrInvalid
	}
	return matches[0].UTC().Format(time.RFC3339), nil
}

// HistoricalFrequency serves pre-frequency inputs, old receipt authentication,
// and unchanged stops of plans with no accepted raw frequency facts.
func HistoricalFrequency(frequency, clock, weekday, day string) (string, error) {
	at, err := time.Parse("15:04", clock)
	if err != nil {
		return "", ErrInvalid
	}
	dom, dow := "*", "*"
	switch frequency {
	case "daily":
	case "weekdays":
		dow = "1-5"
	case "weekly":
		n, err := strconv.Atoi(weekday)
		if err != nil || n < 0 || n > 6 {
			return "", ErrInvalid
		}
		dow = strconv.Itoa(n)
	case "monthly":
		n, err := strconv.Atoi(day)
		if err != nil || n < 1 || n > 31 {
			return "", ErrInvalid
		}
		dom = strconv.Itoa(n)
	default:
		return "", ErrInvalid
	}
	return strconv.Itoa(at.Minute()) + " " + strconv.Itoa(at.Hour()) + " " + dom + " * " + dow, nil
}

// HistoricalLocalMatch is restricted to old NULL-local-input configurations
// and old receipt authentication; managed conversion uses the owning method.
func HistoricalLocalMatch(spec, zone, date, clock string) bool {
	at, err := time.Parse(time.RFC3339Nano, spec)
	loc, zoneErr := time.LoadLocation(zone)
	return err == nil && zoneErr == nil && at.In(loc).Format("2006-01-02") == date && at.In(loc).Format("15:04") == clock
}
